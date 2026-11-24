package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

type testScraper struct {
	data map[string]string
}

func (s *testScraper) FetchContent(ctx context.Context, targetURL string) (string, error) {
	return s.data[targetURL], nil
}

type testSummarizer struct {
	capturedContent string
}

func (s *testSummarizer) Summarize(ctx context.Context, title, content, language string) (string, error) {
	s.capturedContent = content
	return "Multi-source consolidated briefing", nil
}

func TestBuildPayloadMultiSourceScraping(t *testing.T) {
	cfg := &config.Config{
		Enrichment: config.EnrichmentConfig{
			Scraper: "direct",
		},
		LLM: config.LLMConfig{
			Enabled:  true,
			Language: "en",
		},
		Feeds: []config.FeedConfig{
			{Name: "CoinDesk", URL: "https://coindesk.com/rss", Tier: 1},
			{Name: "Cointelegraph", URL: "https://cointelegraph.com/rss", Tier: 2},
		},
	}

	mockScraper := &testScraper{
		data: map[string]string{
			"https://coindesk.com/article-1":      "CoinDesk reporting full article details on $900M IPO.",
			"https://cointelegraph.com/article-2": "Cointelegraph covering the same HK listing with 15% upsize option.",
		},
	}
	mockSum := &testSummarizer{}

	eng := &Engine{
		cfg:        cfg,
		scraper:    mockScraper,
		summarizer: mockSum,
	}

	articles := []model.Article{
		{
			FeedURL:     "https://coindesk.com/rss",
			Title:       "Longsys debuts in Hong Kong",
			Description: "Short summary 1",
			URL:         "https://coindesk.com/article-1",
		},
		{
			FeedURL:     "https://cointelegraph.com/rss",
			Title:       "Longsys starts HK trading",
			Description: "Short summary 2",
			URL:         "https://cointelegraph.com/article-2",
		},
	}

	cluster := model.Cluster{
		ID:             "cluster-1",
		Articles:       articles,
		Representative: articles[0],
		Score:          15.0,
	}

	payload := eng.BuildPayload(context.Background(), cluster, "hash-test")

	if !payload.Enriched {
		t.Errorf("expected payload to be enriched")
	}
	if payload.Content != "Multi-source consolidated briefing" {
		t.Errorf("expected consolidated content, got: %s", payload.Content)
	}

	if !strings.Contains(mockSum.capturedContent, "[Source 1: CoinDesk - Longsys debuts in Hong Kong]") {
		t.Errorf("expected Source 1 in LLM content, got: %s", mockSum.capturedContent)
	}
	if !strings.Contains(mockSum.capturedContent, "CoinDesk reporting full article details on $900M IPO.") {
		t.Errorf("expected Source 1 scraped text in LLM content, got: %s", mockSum.capturedContent)
	}
	if !strings.Contains(mockSum.capturedContent, "[Source 2: Cointelegraph - Longsys starts HK trading]") {
		t.Errorf("expected Source 2 in LLM content, got: %s", mockSum.capturedContent)
	}
	if !strings.Contains(mockSum.capturedContent, "Cointelegraph covering the same HK listing with 15% upsize option.") {
		t.Errorf("expected Source 2 scraped text in LLM content, got: %s", mockSum.capturedContent)
	}
}

func TestPollFeedsAlwaysLoadsPersistedETag(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.New(filepath.Join(tempDir, "etag_test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	var sawIfNoneMatch string
	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawIfNoneMatch = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer feedServer.Close()

	// Seed ETag in SQLite while feedBuffers is completely empty (simulating fresh restart)
	if err := st.UpdateFeedState(feedServer.URL, `"persisted-v9"`, "Mon, 02 Jan 2006 15:04:05 MST", time.Now()); err != nil {
		t.Fatalf("UpdateFeedState: %v", err)
	}

	cfg := &config.Config{
		Engine: config.EngineConfig{
			PollDuration:       5 * time.Minute,
			WindowDur:          3 * time.Hour,
			FlushDur:           1 * time.Hour,
			MaxArticlesPerFeed: 10,
		},
		Feeds: []config.FeedConfig{
			{Name: "TestFeed", URL: feedServer.URL, Tier: 1},
		},
	}

	eng := New(cfg, st)
	if len(eng.feedBuffers[feedServer.URL]) != 0 {
		t.Fatalf("expected empty feedBuffers initially")
	}

	if err := eng.PollFeeds(context.Background()); err != nil {
		t.Fatalf("PollFeeds: %v", err)
	}
	if sawIfNoneMatch != `"persisted-v9"` {
		t.Fatalf("expected If-None-Match header %q even with empty buffer, got %q", `"persisted-v9"`, sawIfNoneMatch)
	}
}

func TestComputeAdaptiveDelay(t *testing.T) {
	base := 5 * time.Minute

	d1, c1 := computeAdaptiveDelay(base, 0, 0, false)
	if c1 != 1 || d1 < 6*time.Minute || d1 > 9*time.Minute {
		t.Fatalf("expected ~7.5m backoff on first 304, got %v (count=%d)", d1, c1)
	}

	d2, c2 := computeAdaptiveDelay(base, 5, 0, false)
	if c2 != 6 || d2 > 34*time.Minute {
		t.Fatalf("expected capped ~30m backoff, got %v (count=%d)", d2, c2)
	}

	dReset, cReset := computeAdaptiveDelay(base, 4, 0, true)
	if cReset != 0 || dReset < 4*time.Minute || dReset > 6*time.Minute {
		t.Fatalf("expected reset to ~5m when new articles arrive, got %v (count=%d)", dReset, cReset)
	}
}

func TestEngineSyncConfigRevision(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sync_rev.db")
	stDaemon, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New daemon: %v", err)
	}
	defer stDaemon.Close()

	seed := &config.Config{
		Version: "1",
		Engine: config.EngineConfig{
			PollInterval:   "5m",
			WindowDuration: "3h",
			FlushInterval:  "1h",
			TopK:           3,
		},
		Feeds: []config.FeedConfig{
			{Name: "Feed A", URL: "https://example.com/a.xml", Tier: 1},
		},
	}
	cfg, err := stDaemon.BootstrapConfig(seed)
	if err != nil {
		t.Fatalf("BootstrapConfig: %v", err)
	}

	eng := New(cfg, stDaemon)
	if eng.SyncConfigRevision() {
		t.Fatalf("expected no reload when revision has not changed")
	}

	// Simulate an external writer process (such as 'celerum mcp' or 'celerum import').
	stExternal, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New external: %v", err)
	}
	defer stExternal.Close()

	if _, err := stExternal.CreateSource(store.SourceRecord{
		Name:     "Feed B",
		URL:      "https://example.com/b.xml",
		Category: "Tech",
		Tier:     1,
		Enabled:  true,
	}); err != nil {
		t.Fatalf("external CreateSource: %v", err)
	}

	if !eng.SyncConfigRevision() {
		t.Fatalf("expected SyncConfigRevision to detect external source mutation")
	}

	eng.mu.Lock()
	feedCount := len(eng.cfg.Feeds)
	_, hasSchedule := eng.schedules["https://example.com/b.xml"]
	eng.mu.Unlock()

	if feedCount != 2 || !hasSchedule {
		t.Fatalf("expected 2 feeds and schedule for Feed B after sync, got count=%d hasSchedule=%v", feedCount, hasSchedule)
	}
	if eng.SyncConfigRevision() {
		t.Fatalf("expected subsequent SyncConfigRevision to be a no-op")
	}
}


