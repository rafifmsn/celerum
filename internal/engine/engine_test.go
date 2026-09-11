package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"celerum/internal/config"
	"celerum/internal/store"
	"celerum/pkg/model"
)

func TestEngineHybridDispatchAndDeduplication(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "engine_test.db")

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	defer st.Close()

	var dispatchedCount int
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatchedCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	cfg := &config.Config{
		Engine: config.EngineConfig{
			SimilarityThreshold: 0.20,
			BreakingThreshold:   10.0,
			TopK:                3,
			FlushDur:            1 * time.Hour,
			WindowDur:           3 * time.Hour,
			MaxArticlesPerFeed:  10,
		},
		Keywords: config.KeywordsConfig{
			Boost: []string{"etf", "sec"},
		},
		Dispatch: config.DispatchConfig{
			Webhook: config.WebhookConfig{
				Enabled: true,
				URL:     webhookServer.URL,
			},
		},
	}

	eng := New(cfg, st)

	now := time.Now()

	// Inject 2 articles reporting the same breaking SEC ETF news into feed buffer
	eng.feedBuffers["https://coindesk.com/rss"] = []model.Article{
		{
			ID:          "cd-1",
			FeedURL:     "https://coindesk.com/rss",
			FeedTier:    1,
			Title:       "SEC officially approves first spot Bitcoin ETF",
			Description: "Historic milestone reached as regulator signs off on spot Bitcoin ETF.",
			URL:         "https://coindesk.com/article-1",
			PublishedAt: now.Add(-5 * time.Minute),
		},
	}
	eng.feedBuffers["https://cointelegraph.com/rss"] = []model.Article{
		{
			ID:          "ct-1",
			FeedURL:     "https://cointelegraph.com/rss",
			FeedTier:    2,
			Title:       "First spot Bitcoin ETF approved by SEC regulators",
			Description: "Bitcoin ETF officially approved by the Securities and Exchange Commission.",
			URL:         "https://cointelegraph.com/article-1",
			PublishedAt: now.Add(-4 * time.Minute),
		},
	}

	// First evaluation: breaking news threshold should trigger immediate dispatch
	payloads, err := eng.EvaluateAndDispatch(context.Background(), now, false)
	if err != nil {
		t.Fatalf("EvaluateAndDispatch failed: %v", err)
	}

	if len(payloads) != 1 {
		t.Fatalf("expected 1 cluster dispatched, got %d", len(payloads))
	}
	if payloads[0].ClusterSize != 2 {
		t.Errorf("expected cluster size 2, got %d", payloads[0].ClusterSize)
	}
	if dispatchedCount != 1 {
		t.Errorf("expected 1 webhook delivery, got %d", dispatchedCount)
	}

	// Second evaluation immediately: should be deduplicated (already dispatched in SQLite)
	payloadsSecond, err := eng.EvaluateAndDispatch(context.Background(), now, false)
	if err != nil {
		t.Fatalf("second EvaluateAndDispatch failed: %v", err)
	}
	if len(payloadsSecond) != 0 {
		t.Errorf("expected 0 duplicate payloads dispatched, got %d", len(payloadsSecond))
	}
	if dispatchedCount != 1 {
		t.Errorf("expected webhook count to remain 1, got %d", dispatchedCount)
	}
}

func TestEngineCheck(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "check_test.db")
	st, _ := store.New(dbPath)
	defer st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><item><title>Federal Reserve interest rate announcement live today</title><link>https://example.com/fed</link><description>Rates held constant.</description></item></channel></rss>`))
	}))
	defer server.Close()

	cfg := &config.Config{
		Engine: config.EngineConfig{
			SimilarityThreshold: 0.40,
			TopK:                3,
			MaxArticlesPerFeed:  10,
		},
		Feeds: []config.FeedConfig{
			{URL: server.URL, Tier: 1},
		},
	}

	eng := New(cfg, st)
	payloads, err := eng.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() failed: %v", err)
	}

	if len(payloads) != 1 {
		t.Fatalf("expected 1 payload from Check, got %d", len(payloads))
	}
	if payloads[0].Title != "Federal Reserve interest rate announcement live today" {
		t.Errorf("title mismatch: %s", payloads[0].Title)
	}
}

