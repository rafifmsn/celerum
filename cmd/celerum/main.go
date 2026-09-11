package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"celerum/internal/config"
	"celerum/internal/dispatch"
	"celerum/internal/engine"
	"celerum/internal/store"
	"celerum/pkg/model"
)

const usage = `Celerum - Algorithmic RSS Intelligence Engine

Usage:
  celerum init [-c config.yaml]          Create default configuration file
  celerum check [-c config.yaml]         Dry-run feed clustering and scoring to stdout
  celerum test telegram [-c config.yaml] Send test alert to verify Telegram credentials
  celerum run [-c config.yaml] [--once]  Run continuous background polling daemon

Flags:
  -c string   Path to configuration file (default "celerum.yaml")
  --once      Run a single execution cycle and exit (for 'run' subcommand)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	subcommand := os.Args[1]
	subArgs := os.Args[2:]

	switch subcommand {
	case "init":
		runInit(subArgs)
	case "check":
		runCheck(subArgs)
	case "test":
		runTest(subArgs)
	case "run":
		runDaemon(subArgs)
	case "help", "-h", "--help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n%s", subcommand, usage)
		os.Exit(1)
	}
}

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("c", "celerum.yaml", "Path to config file")
	_ = fs.Parse(args)

	if _, err := os.Stat(*configPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists, aborting to prevent overwrite\n", *configPath)
		os.Exit(1)
	}

	content := config.DefaultTemplate()
	if err := os.WriteFile(*configPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing configuration template: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created default configuration template at %s\n", *configPath)
}

func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	configPath := fs.String("c", "celerum.yaml", "Path to config file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	st, err := store.New(cfg.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	eng := engine.New(cfg, st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println("Running Celerum check (evaluating feeds and clusters)...")
	payloads, err := eng.Check(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check failed: %v\n", err)
		os.Exit(1)
	}

	if len(payloads) == 0 {
		fmt.Println("No articles or clusters found across configured feeds.")
		return
	}

	fmt.Printf("\nDiscovered %d Top-K Clusters:\n\n", len(payloads))
	for i, p := range payloads {
		fmt.Printf("[%d] Score: %.2f | Sources: %d\n", i+1, p.Score, p.ClusterSize)
		fmt.Printf("    Title: %s\n", p.Title)
		if len(p.Summary) > 120 {
			fmt.Printf("    Summary: %s...\n", p.Summary[:120])
		} else if p.Summary != "" {
			fmt.Printf("    Summary: %s\n", p.Summary)
		}
		for _, s := range p.Sources {
			fmt.Printf("    • %s: %s\n", s.Name, s.URL)
		}
		fmt.Println()
	}
}

func runTest(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: celerum test telegram [-c config.yaml]")
		os.Exit(1)
	}

	target := args[0]
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	configPath := fs.String("c", "celerum.yaml", "Path to config file")
	_ = fs.Parse(args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	switch target {
	case "telegram":
		token := cfg.Dispatch.Telegram.BotToken
		chatID := cfg.Dispatch.Telegram.ChatID
		if token == "" || chatID == "" {
			fmt.Fprintln(os.Stderr, "error: TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID is missing in .env or celerum.yaml")
			os.Exit(1)
		}

		disp := dispatch.NewTelegramDispatcher(token, chatID)
		testPayload := model.Payload{
			ID:    "test-verification",
			Title: "Celerum Telegram Connection Verified",
			Summary: "Your Telegram Bot API credentials and channel permissions have been successfully verified.",
			Takeaways: []string{
				"Bot Token authenticated",
				"Chat ID write permissions verified",
				"HTML payload formatting active",
			},
			Sources: []model.SourceInfo{
				{Name: "Celerum Engine", URL: "https://github.com/rafifmsn/celerum", Title: "System Verification"},
			},
			ClusterSize: 1,
			Score:       10.0,
			Timestamp:   time.Now().Unix(),
		}

		fmt.Printf("Sending verification alert to Telegram chat %s...\n", chatID)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := disp.Dispatch(ctx, testPayload); err != nil {
			fmt.Fprintf(os.Stderr, "failed to deliver Telegram alert: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Success: Telegram verification alert delivered.")
	default:
		fmt.Fprintf(os.Stderr, "unknown test target: %s (supported: telegram)\n", target)
		os.Exit(1)
	}
}

func runDaemon(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("c", "celerum.yaml", "Path to config file")
	once := fs.Bool("once", false, "Run single cycle and exit")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	st, err := store.New(cfg.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	eng := engine.New(cfg, st)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := eng.Run(ctx, *once); err != nil && err != context.Canceled {
		log.Fatalf("engine error: %v", err)
	}
}

