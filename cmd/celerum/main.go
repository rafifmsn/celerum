package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"celerum/internal/ai"
	"celerum/internal/config"
	"celerum/internal/dispatch"
	"celerum/internal/doctor"
	"celerum/internal/engine"
	"celerum/internal/server"
	"celerum/internal/store"
	"celerum/pkg/model"
)

const usage = `Celerum - Algorithmic RSS Intelligence Engine

Usage:
  celerum init [-c config.yaml]               Create default configuration file
  celerum import [-c config.yaml]             Overwrite SQLite settings and sources from YAML
  celerum check [-c config.yaml]              Dry-run feed clustering and scoring to stdout
  celerum doctor [-c config.yaml] [--send]    Run 6-stage non-destructive diagnostics
  celerum test telegram [-c config.yaml]      Send test alert to verify Telegram credentials
  celerum run [-c config.yaml] [-addr :8080]  Run adaptive polling daemon and web dashboard

Flags:
  -c string      Path to configuration file (default "celerum.yaml")
  -addr string   Dashboard HTTP listen address (default ":8080" or CELERUM_ADDR)
  --send         Send a live verification alert in Stage 6 of 'doctor'
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
	case "import":
		runImport(subArgs)
	case "check":
		runCheck(subArgs)
	case "doctor":
		runDoctor(subArgs)
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

func importConfigFile(configPath string) (int, string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return 0, "", fmt.Errorf("error loading config: %w", err)
	}

	st, err := store.New(cfg.Database.Path)
	if err != nil {
		return 0, "", fmt.Errorf("error initializing store: %w", err)
	}
	defer st.Close()

	if err := st.ImportConfig(cfg, true); err != nil {
		return 0, "", fmt.Errorf("error importing config: %w", err)
	}

	return len(cfg.Feeds), cfg.Database.Path, nil
}

func runImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	configPath := fs.String("c", "celerum.yaml", "Path to config file")
	_ = fs.Parse(args)

	feedCount, dbPath, err := importConfigFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("Imported %d feed(s) and runtime settings from %s into %s\n", feedCount, *configPath, dbPath)
	fmt.Println("Note: if 'celerum run' is currently running, restart it to reload the updated SQLite configuration.")
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

func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := fs.String("c", "celerum.yaml", "Path to config file")
	send := fs.Bool("send", false, "Send live verification alerts to enabled dispatchers")
	_ = fs.Parse(args)

	var cfg *config.Config
	var st *store.Store

	if _, statErr := os.Stat(*configPath); statErr == nil {
		loaded, err := config.Load(*configPath)
		if err != nil {
			// Still attempt to run doctor Stage 1 so user gets structured diagnostic output
			fmt.Fprintf(os.Stderr, "[FAIL] Stage 1 - Config & Environment: %v\n", err)
			os.Exit(1)
		}
		cfg = loaded
	} else {
		cfg = config.DefaultConfig()
	}

	st, err := store.New(cfg.Database.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if runtimeCfg, err := st.BootstrapConfig(cfg); err == nil {
		cfg = runtimeCfg
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	report := doctor.Run(ctx, cfg, st, doctor.Options{Send: *send})
	fmt.Println("Celerum System Doctor (6-Stage Non-Destructive Diagnostics)")
	fmt.Println("------------------------------------------------------------")
	for _, s := range report.Stages {
		fmt.Printf("[%s] Stage %d: %s (%dms)\n", s.Status, s.Stage, s.Name, s.LatencyMs)
		fmt.Printf("       %s\n", s.Summary)
		if s.Detail != "" {
			fmt.Printf("       Detail: %s\n", s.Detail)
		}
	}
	fmt.Println("------------------------------------------------------------")
	if !report.OK {
		fmt.Println("Doctor result: FAIL")
		os.Exit(1)
	}
	fmt.Println("Doctor result: PASS")
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

		fmt.Printf("1/2: Sending fallback alert (without LLM) to Telegram chat %s...\n", chatID)
		fallbackPayload := model.Payload{
			ID:       "test-fallback",
			FeedName: "Cointelegraph",
			Title:    "Bitwise to put down Dogecoin ETF less than a year after launch",
			Enriched: false,
			Sources: []model.SourceInfo{
				{
					Name:  "Cointelegraph",
					URL:   "https://cointelegraph.com/news/bitwise-put-down-dogecoin-etf-year-launch",
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

		fmt.Printf("\n2/2: Generating and sending enriched alert (with LLM) to Telegram chat %s...\n", chatID)
		var enrichedContent string
		if cfg.LLM.Enabled {
			summarizer := ai.NewSummarizer(cfg)
			llmCtx, cancelLLM := context.WithTimeout(context.Background(), 30*time.Second)
			sampleTitle := "Longsys begins Hong Kong trading after HK$7.08 billion share sale"
			sampleContent := "Longsys began trading in Hong Kong on September 8 after raising gross proceeds of HK$7.0775 billion, or about $903 million, in an IPO, according to Hong Kong Exchanges and Clearing."
			fmt.Printf("Invoking LLM model %s via %s...\n", cfg.LLM.Model, cfg.LLM.Provider)
			synth, err := summarizer.Summarize(llmCtx, sampleTitle, sampleContent, cfg.LLM.Language)
			cancelLLM()
			if err != nil {
				fmt.Fprintf(os.Stderr, "LLM synthesis failed: %v\n", err)
				os.Exit(1)
			}
			enrichedContent = synth
			fmt.Println("LLM synthesis generated successfully.")
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
	defaultAddr := os.Getenv("CELERUM_ADDR")
	if defaultAddr == "" {
		defaultAddr = ":8080"
	}
	addr := fs.String("addr", defaultAddr, "HTTP dashboard listen address")
	_ = fs.Parse(args)

	var yamlCfg *config.Config
	if _, err := os.Stat(*configPath); err == nil {
		loaded, loadErr := config.Load(*configPath)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "error loading config: %v\n", loadErr)
			os.Exit(1)
		}
		yamlCfg = loaded
	} else {
		config.LoadDotEnv(".")
	}

	dbPath := "data/celerum.db"
	if yamlCfg != nil && yamlCfg.Database.Path != "" {
		dbPath = yamlCfg.Database.Path
	}

	st, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cfg, err := st.BootstrapConfig(yamlCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error bootstrapping sqlite config: %v\n", err)
		os.Exit(1)
	}

	eng := engine.New(cfg, st)
	srv := server.New(eng, st)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[info] celerum dashboard listening on %s", *addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[error] http server error: %v", err)
			stop()
		}
	}()

	err = eng.Run(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)

	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("engine error: %v", err)
	}
}
