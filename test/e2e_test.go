package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"celerum/internal/config"
	"celerum/internal/engine"
	"celerum/internal/store"
	"celerum/pkg/model"
)

func TestEndToEndPipeline(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "e2e_celerum.db")

	recentTime := time.Now().Format(time.RFC1123Z)

	// 1. Mock Feed 1: Bloomberg/Tier 1 RSS
	feed1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Bloomberg Markets</title>
    <item>
      <title>Federal Reserve cuts benchmark interest rate by 50 basis points in emergency shift</title>
      <link>https://bloomberg.com/news/fed-rate-cut-50bps</link>
      <description>The Federal Reserve lowered borrowing costs by half a percentage point today in response to labor market shifts.</description>
      <pubDate>%s</pubDate>
    </item>
  </channel>
</rss>`, recentTime)
	}))
	defer feed1.Close()

	// 2. Mock Feed 2: Reuters/Tier 1 RSS reporting the same event with different wording
	feed2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Reuters Financial Wire</title>
    <item>
      <title>Fed cuts interest rates by 50 basis points to support cooling labor market</title>
      <link>https://reuters.com/markets/us-fed-rate-reduction-50bps</link>
      <description>US central bankers voted to reduce interest rates by 50 bps, signaling the start of an easing cycle.</description>
      <pubDate>%s</pubDate>
    </item>
  </channel>
</rss>`, recentTime)
	}))
	defer feed2.Close()

	// 3. Mock Feed 3: Secondary blog with unrelated low-velocity news
	feed3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Local Tech Digest</title>
    <item>
      <title>Local hardware club announces upcoming weekend robotics exhibition</title>
      <link>https://localtech.example.com/robotics-exhibition</link>
      <description>Join us this Saturday for interactive robotics presentations.</description>
      <pubDate>%s</pubDate>
    </item>
  </channel>
</rss>`, recentTime)
	}))
	defer feed3.Close()

	// 4. Mock Webhook Receiver
	var receivedPayloads []model.Payload
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p model.Payload
		if err := json.Unmarshal(body, &p); err == nil {
			receivedPayloads = append(receivedPayloads, p)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	// 5. Mock LLM Server
	var llmCallCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&llmCallCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{
			"choices": [
				{
					"message": {
						"content": "{\"title\": \"The Fed Memangkas Suku Bunga 50 Bps\", \"summary\": \"Bank sentral AS memangkas suku bunga acuan sebesar 50 basis poin guna menjaga stabilitas pasar tenaga kerja.\", \"takeaways\": [\"Pemangkasan 50 bps dimulai\", \"Siklus pelonggaran moneter aktif\"], \"sentiment\": \"bullish\"}"
					}
				}
			]
		}`)
	}))
	defer llmServer.Close()

	// Construct test configuration
	cfg := &config.Config{
		Database: config.DatabaseConfig{Path: dbPath},
		Engine: config.EngineConfig{
			PollInterval:        "5m",
			WindowDuration:      "3h",
			FlushInterval:       "1h",
			MaxArticlesPerFeed:  10,
			SimilarityThreshold: 0.20,
			BreakingThreshold:   9.0, // Multi-source Fed rate cut will cross this
			TopK:                3,
			PollDuration:        5 * time.Minute,
			WindowDur:           3 * time.Hour,
			FlushDur:            1 * time.Hour,
		},
		Feeds: []config.FeedConfig{
			{Name: "Bloomberg", URL: feed1.URL, Tier: 1},
			{Name: "Reuters", URL: feed2.URL, Tier: 1},
			{Name: "LocalTech", URL: feed3.URL, Tier: 3},
		},
		Keywords: config.KeywordsConfig{
			Boost: []string{"fed", "rate cut", "basis points"},
		},
		Enrichment: config.EnrichmentConfig{
			Scraper: "none",
		},
		LLM: config.LLMConfig{
			Enabled:  true,
			BaseURL:  llmServer.URL,
			Model:    "gpt-4o-mini",
			Language: "id",
		},
		Dispatch: config.DispatchConfig{
			Webhook: config.WebhookConfig{
				Enabled: true,
				URL:     webhookServer.URL,
			},
		},
	}

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer st.Close()

	eng := engine.New(cfg, st)
	ctx := context.Background()

	// Execute RunOnce via Check first to verify dry-run isolation
	dryRunPayloads, err := eng.Check(ctx)
	if err != nil {
		t.Fatalf("Check() failed: %v", err)
	}
	if len(dryRunPayloads) == 0 {
		t.Fatalf("expected dry-run to discover clusters")
	}
	if len(receivedPayloads) != 0 {
		t.Errorf("expected 0 webhooks during dry-run, got %d", len(receivedPayloads))
	}

	// Now run live execution cycle
	now := time.Now()
	if err := eng.PollFeeds(ctx); err != nil {
		t.Fatalf("PollFeeds failed: %v", err)
	}

	dispatched, err := eng.EvaluateAndDispatch(ctx, now, false)
	if err != nil {
		t.Fatalf("EvaluateAndDispatch failed: %v", err)
	}

	// Breaking story should be dispatched immediately
	if len(dispatched) != 1 {
		t.Fatalf("expected exactly 1 breaking cluster dispatched, got %d", len(dispatched))
	}

	p := dispatched[0]
	if p.ClusterSize != 2 {
		t.Errorf("expected cluster size 2 for Bloomberg + Reuters, got %d", p.ClusterSize)
	}
	if !p.Enriched {
		t.Errorf("expected payload to be enriched by LLM")
	}
	if p.Title != "The Fed Memangkas Suku Bunga 50 Bps" {
		t.Errorf("expected Indonesian LLM title, got: %s", p.Title)
	}
	if len(p.Takeaways) != 2 {
		t.Errorf("expected 2 takeaways, got %d", len(p.Takeaways))
	}
	if p.Sentiment == nil || *p.Sentiment != "bullish" {
		t.Errorf("expected sentiment 'bullish'")
	}
	if len(p.Sources) != 2 {
		t.Errorf("expected 2 source entries in payload, got %d", len(p.Sources))
	}

	// Verify webhook received the payload
	if len(receivedPayloads) != 1 {
		t.Fatalf("expected 1 payload received at webhook endpoint, got %d", len(receivedPayloads))
	}
	if receivedPayloads[0].ID != p.ID {
		t.Errorf("webhook payload ID mismatch: got %s, want %s", receivedPayloads[0].ID, p.ID)
	}

	// Run evaluation again: verify deduplication prevents duplicate alerting
	dispatchedSecond, err := eng.EvaluateAndDispatch(ctx, now, false)
	if err != nil {
		t.Fatalf("second EvaluateAndDispatch failed: %v", err)
	}
	if len(dispatchedSecond) != 0 {
		t.Errorf("expected 0 duplicate dispatches, got %d", len(dispatchedSecond))
	}
	if len(receivedPayloads) != 1 {
		t.Errorf("webhook payload count should remain 1, got %d", len(receivedPayloads))
	}

	_ = os.Remove(dbPath)
}
