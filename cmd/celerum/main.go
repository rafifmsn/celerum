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

	"celerum/internal/ai"
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

		// 1. Fallback Alert (without LLM)
		fmt.Printf("1/2: Sending fallback alert (without LLM) to Telegram chat %s...\n", chatID)
		fallbackPayload := model.Payload{
			ID:       "test-fallback",
			FeedName: "Cointelegraph",
			Title:    "Bitwise to put down Dogecoin ETF less than a year after launch",
			Enriched: false,
			Sources: []model.SourceInfo{
				{
					Name:  "Cointelegraph",
					URL:   "https://cointelegraph.com/news/bitwise-put-down-dogecoin-etf-year-launch?utm_source=rss_feed&utm_medium=rss&utm_campaign=rss_partner_inbound",
					Title: "Bitwise to put down Dogecoin ETF less than a year after launch",
				},
			},
			ClusterSize: 1,
			Score:       7.5,
			Timestamp:   time.Now().Unix(),
		}

		ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
		if err := disp.Dispatch(ctx1, fallbackPayload); err != nil {
			cancel1()
			fmt.Fprintf(os.Stderr, "failed to deliver fallback Telegram alert: %v\n", err)
			os.Exit(1)
		}
		cancel1()
		fmt.Println("Success: Fallback alert delivered.")

		// 2. Enriched Alert (with live LLM synthesis)
		fmt.Printf("\n2/2: Generating and sending enriched alert (with LLM) to Telegram chat %s...\n", chatID)
		var enrichedContent string
		if cfg.LLM.Enabled {
			summarizer := ai.NewSummarizer(cfg)
			llmCtx, cancelLLM := context.WithTimeout(context.Background(), 30*time.Second)
			sampleTitle := "Longsys begins Hong Kong trading after HK$7.08 billion share sale"
			sampleContent := "Longsys began trading in Hong Kong on September 8 after raising gross proceeds of HK$7.0775 billion, or about $903 million, in an IPO, according to Hong Kong Exchanges and Clearing. Shares opened roughly flat and were last reported at HK$235.8, marginally below the HK$236 offer price. That restrained debut came as Longsys reported an earnings surge, with net profit of RMB10.7 billion for the first half of 2026, more than 260 times the level a year earlier. Revenue rose 136.3% year over year to RMB24.1 billion. The company said it would direct most of its net IPO proceeds to research and development in chip design and advanced memory products."
			fmt.Printf("Invoking LLM model %s via %s...\n", cfg.LLM.Model, cfg.LLM.Provider)
			synth, err := summarizer.Summarize(llmCtx, sampleTitle, sampleContent, cfg.LLM.Language)
			cancelLLM()
			if err != nil {
				fmt.Printf("[warn] LLM synthesis failed: %v (falling back to sample content)\n", err)
				enrichedContent = "Longsys began trading in Hong Kong on September 8 after raising gross proceeds of HK$7.0775 billion ($903 million) in an IPO.\n\nShares opened roughly flat at HK$235.8 against the HK$236 offer price. The restrained debut came as Longsys reported 1H 2026 net profit surging to RMB10.7 billion on strong AI memory demand."
			} else {
				enrichedContent = synth
				fmt.Println("LLM synthesis generated successfully.")
			}
		}

		enrichedPayload := model.Payload{
			ID:       "test-enriched",
			FeedName: "Cointelegraph",
			Title:    "Longsys begins Hong Kong trading after HK$7.08 billion share sale",
			Content:  enrichedContent,
			Enriched: true,
			Sources: []model.SourceInfo{
				{
					Name:  "Cointelegraph",
					URL:   "https://cointelegraph.com/news/bitwise-put-down-dogecoin-etf-year-launch",
					Title: "Longsys begins Hong Kong trading after HK$7.08 billion share sale",
				},
				{
					Name:  "CoinDesk",
					URL:   "https://coindesk.com/markets/2026/09/08/longsys-ipo-debut-hong-kong",
					Title: "AI Memory Maker Longsys Raises $903M in Muted Debut",
				},
			},
			ClusterSize: 2,
			Score:       12.5,
			Timestamp:   time.Now().Unix(),
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		if err := disp.Dispatch(ctx2, enrichedPayload); err != nil {
			cancel2()
			fmt.Fprintf(os.Stderr, "failed to deliver enriched Telegram alert: %v\n", err)
			os.Exit(1)
		}
		cancel2()
		fmt.Println("Success: Enriched alert delivered.")
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

