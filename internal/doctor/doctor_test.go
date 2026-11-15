package doctor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"celerum/internal/config"
	"celerum/internal/store"
)

func TestDoctorAllStagesPassAndNonDestructive(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.New(filepath.Join(tempDir, "doctor.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	articlePage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><p>Full article content for doctor scraping test.</p></body></html>"))
	}))
	defer articlePage.Close()

	rssServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"doc-v1"`)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>Federal Reserve holds benchmark rate</title><link>%s</link><description>Official update.</description></item></channel></rss>`, articlePage.URL)
	}))
	defer rssServer.Close()

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Fed held rates steady."}}]}`))
	}))
	defer llmServer.Close()

	tgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bottok/getMe" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"celerum_bot"}}`))
			return
		}
		if r.URL.Path == "/bottok/getChat" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-1001}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer tgServer.Close()

	cfg := &config.Config{
		Database: config.DatabaseConfig{Path: filepath.Join(tempDir, "doctor.db")},
		Engine: config.EngineConfig{
			PollInterval:       "5m",
			WindowDuration:     "3h",
			FlushInterval:      "1h",
			MaxArticlesPerFeed: 10,
		},
		Feeds: []config.FeedConfig{
			{Name: "TestWire", URL: rssServer.URL, Tier: 1},
		},
		Enrichment: config.EnrichmentConfig{Scraper: "direct"},
		LLM: config.LLMConfig{
			Enabled:  true,
			Provider: "openai",
			BaseURL:  llmServer.URL,
			APIKey:   "test-key",
			Model:    "gpt-4o-mini",
		},
		Dispatch: config.DispatchConfig{
			Telegram: config.TelegramConfig{
				Enabled:  true,
				BotToken: "tok",
				ChatID:   "-1001",
			},
		},
	}

	report := Run(context.Background(), cfg, st, Options{
		Send:            false,
		TelegramBaseURL: tgServer.URL,
	})

	if !report.OK {
		t.Fatalf("expected all 6 stages PASS, got report: %+v", report)
	}
	if len(report.Stages) != 6 {
		t.Fatalf("expected 6 stages, got %d", len(report.Stages))
	}
	for _, s := range report.Stages {
		if s.Status != "PASS" {
			t.Errorf("stage %d (%s) expected PASS, got %s: %s (%s)", s.Stage, s.Name, s.Status, s.Summary, s.Detail)
		}
	}

	// Verify doctor did not mutate dispatched_clusters or clusters
	clusters, err := st.QueryClusters("all", "all", "", time.Time{}, "score")
	if err != nil {
		t.Fatalf("QueryClusters: %v", err)
	}
	if len(clusters) != 0 {
		t.Errorf("expected doctor to be non-destructive, found %d clusters", len(clusters))
	}
}

func TestDoctorStrictFailureWithoutFallback(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.New(filepath.Join(tempDir, "doctor_fail.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Database:   config.DatabaseConfig{Path: filepath.Join(tempDir, "doctor_fail.db")},
		Feeds:      []config.FeedConfig{{Name: "Dead", URL: "http://127.0.0.1:1/dead.xml", Tier: 1}},
		Enrichment: config.EnrichmentConfig{Scraper: "jina"},
		LLM:        config.LLMConfig{Enabled: true, Provider: "openrouter"},
	}

	report := Run(context.Background(), cfg, st, Options{})
	if report.OK {
		t.Fatalf("expected report.OK=false on broken config/feeds")
	}
	if report.Stages[0].Status != "FAIL" {
		t.Errorf("expected Stage 1 FAIL on missing jina/LLM keys, got %s", report.Stages[0].Status)
	}
}
