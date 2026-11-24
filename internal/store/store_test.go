package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"celerum/internal/config"
	"celerum/pkg/model"
)

func TestStoreFeedStateAndDispatchedClusters(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer st.Close()

	feedURL := "https://example.com/rss"
	etag := `"abc123etag"`
	lastMod := "Mon, 02 Jan 2006 15:04:05 MST"
	now := time.Now().Truncate(time.Second)

	// Feed state update & query
	if err := st.UpdateFeedState(feedURL, etag, lastMod, now); err != nil {
		t.Fatalf("UpdateFeedState failed: %v", err)
	}

	gotEtag, gotMod, gotPolled, err := st.GetFeedState(feedURL)
	if err != nil {
		t.Fatalf("GetFeedState failed: %v", err)
	}
	if gotEtag != etag || gotMod != lastMod || !gotPolled.Equal(now) {
		t.Errorf("feed state mismatch: got (%s, %s, %v), want (%s, %s, %v)", gotEtag, gotMod, gotPolled, etag, lastMod, now)
	}

	// Cluster dispatched deduplication
	clusterHash := "sha256-hash-cluster-1"
	dispatched, err := st.IsClusterDispatched(clusterHash)
	if err != nil {
		t.Fatalf("IsClusterDispatched failed: %v", err)
	}
	if dispatched {
		t.Errorf("expected cluster not dispatched initially")
	}

	if err := st.MarkClusterDispatched(clusterHash, now); err != nil {
		t.Fatalf("MarkClusterDispatched failed: %v", err)
	}

	dispatched, err = st.IsClusterDispatched(clusterHash)
	if err != nil {
		t.Fatalf("IsClusterDispatched failed: %v", err)
	}
	if !dispatched {
		t.Errorf("expected cluster to be marked dispatched")
	}
}

func TestStoreWebhookRetries(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_retry.db")

	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer st.Close()

	payload := `{"id":"1","title":"Test"}`
	targetURL := "https://example.com/webhook"
	now := time.Now().Truncate(time.Second)

	if err := st.QueueWebhookRetry("webhook", targetURL, payload, now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("QueueWebhookRetry failed: %v", err)
	}

	retries, err := st.GetPendingWebhookRetries(10, now)
	if err != nil {
		t.Fatalf("GetPendingWebhookRetries failed: %v", err)
	}
	if len(retries) != 1 {
		t.Fatalf("expected 1 retry, got %d", len(retries))
	}

	item := retries[0]
	if item.Payload != payload || item.TargetURL != targetURL {
		t.Errorf("retry mismatch: got %+v", item)
	}

	// Delete on success
	if err := st.DeleteWebhookRetry(item.ID); err != nil {
		t.Fatalf("DeleteWebhookRetry failed: %v", err)
	}

	retries, err = st.GetPendingWebhookRetries(10, now)
	if err != nil {
		t.Fatalf("GetPendingWebhookRetries failed: %v", err)
	}
	if len(retries) != 0 {
		t.Errorf("expected 0 retries after delete, got %d", len(retries))
	}
}

func TestStoreBootstrapSourcesArticlesClustersAndDualPrune(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "full.db")

	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Version: "1",
		Engine: config.EngineConfig{
			PollInterval:   "5m",
			WindowDuration: "3h",
			FlushInterval:  "1h",
			TopK:           3,
		},
		Feeds: []config.FeedConfig{
			{Name: "CoinDesk", URL: "https://coindesk.com/rss", Category: "Crypto", Tier: 1},
			{Name: "Reuters", URL: "https://reuters.com/rss", Category: "Macro", Tier: 1},
		},
		Keywords: config.KeywordsConfig{Boost: []string{"etf", "fed"}},
	}

	bootstrapped, err := st.BootstrapConfig(cfg)
	if err != nil {
		t.Fatalf("BootstrapConfig failed: %v", err)
	}
	if len(bootstrapped.Feeds) != 2 {
		t.Fatalf("expected 2 seeded sources, got %d", len(bootstrapped.Feeds))
	}

	now := time.Now().UTC()
	var articles []model.Article
	for i := 0; i < 12; i++ {
		articles = append(articles, model.Article{
			ID:          "art-" + string(rune('a'+i)),
			FeedURL:     "https://coindesk.com/rss",
			FeedName:    "CoinDesk",
			FeedTier:    1,
			Category:    "Crypto",
			Title:       "Article headline " + string(rune('A'+i)),
			Description: "Body",
			URL:         "https://coindesk.com/" + string(rune('a'+i)),
			ImageURL:    "https://coindesk.com/img.jpg",
			PublishedAt: now.Add(-time.Duration(i) * time.Minute),
			IngestedAt:  now,
		})
	}
	inserted, err := st.UpsertArticles(articles)
	if err != nil {
		t.Fatalf("UpsertArticles failed: %v", err)
	}
	if inserted != 12 {
		t.Fatalf("expected 12 inserted articles, got %d", inserted)
	}

	items, total, cats, srcs, err := st.QueryArticles("Crypto", "CoinDesk", "", 1, 5)
	if err != nil {
		t.Fatalf("QueryArticles failed: %v", err)
	}
	if total != 12 || len(items) != 5 || len(cats) != 1 || len(srcs) != 1 {
		t.Fatalf("unexpected query result: total=%d len=%d cats=%v srcs=%v", total, len(items), cats, srcs)
	}

	// Test dual volume cap pruning (max 5 per feed, max 10 global)
	if err := st.PruneWithLimits(7, 5, 10); err != nil {
		t.Fatalf("PruneWithLimits failed: %v", err)
	}
	_, totalAfter, _, _, err := st.QueryArticles("", "", "", 1, 20)
	if err != nil {
		t.Fatalf("QueryArticles after prune failed: %v", err)
	}
	if totalAfter != 5 {
		t.Fatalf("expected 5 articles remaining after per-feed cap 5, got %d", totalAfter)
	}
}

func TestLegacyRuntimeSettingsMigration(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	legacyJSON := `{"engine":{"WindowDuration":"24h"},"enrichment":{"Scraper":"firecrawl","FirecrawlAPIKey":"fc-legacy"},"llm":{"Enabled":true,"Provider":"openrouter","APIKey":"sk-legacy"},"dispatch":{"Telegram":{"Enabled":true,"BotToken":"bot-legacy","ChatID":"123"}}}`
	if _, err := st.db.Exec(`INSERT INTO settings (key, value_json, updated_at) VALUES ('runtime_config', ?, 1);`, legacyJSON); err != nil {
		t.Fatalf("seed legacy settings: %v", err)
	}

	rt, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("LoadRuntimeSettings: %v", err)
	}
	if rt.Engine.WindowDuration != "24h" || rt.Enrichment.FirecrawlAPIKey != "fc-legacy" || rt.LLM.APIKey != "sk-legacy" || rt.Dispatch.Telegram.BotToken != "bot-legacy" {
		t.Fatalf("expected legacy keys preserved, got %+v", rt)
	}
}

func TestDispatchedClustersPagination(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "dispatch_page.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	var recs []ClusterRecord
	for i := 0; i < 5; i++ {
		recs = append(recs, ClusterRecord{
			ClusterHash:      fmt.Sprintf("hash-%d", i),
			RepresentativeID: fmt.Sprintf("rep-%d", i),
			Title:            fmt.Sprintf("Dispatched headline %d", i),
			Score:            10.0 + float64(i),
			Tier:             1,
			SourceCount:      2,
			LeadSource:       "CoinDesk",
			Status:           "dispatched",
			DispatchMode:     "breaking",
			CreatedAt:        now,
			UpdatedAt:        now.Add(time.Duration(i) * time.Minute),
			DispatchedAt:     now.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := st.UpsertActiveClusters(recs); err != nil {
		t.Fatalf("UpsertActiveClusters: %v", err)
	}
	for _, r := range recs {
		if err := st.MarkClusterDispatchedFull(r.ClusterHash, r.Summary, r.Enriched, r.DispatchMode, "Telegram", r.ScraperUsed, r.DispatchedAt); err != nil {
			t.Fatalf("MarkClusterDispatchedFull: %v", err)
		}
	}

	page1, total, err := st.QueryDispatchedClustersPage("", 1, 2)
	if err != nil {
		t.Fatalf("QueryDispatchedClustersPage page 1: %v", err)
	}
	if total != 5 || len(page1) != 2 {
		t.Fatalf("expected total=5 len=2 on page 1, got total=%d len=%d", total, len(page1))
	}

	page3, _, err := st.QueryDispatchedClustersPage("", 3, 2)
	if err != nil {
		t.Fatalf("QueryDispatchedClustersPage page 3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("expected 1 row on page 3, got %d", len(page3))
	}
}

func TestStoreReadOnlyAndConfigRevision(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rev_ro.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	r0, err := st.GetConfigRevision()
	if err != nil || r0 != 0 {
		t.Fatalf("expected initial revision 0, got %d (err=%v)", r0, err)
	}

	created, err := st.CreateSource(SourceRecord{
		Name:    "CoinDesk",
		URL:     "https://coindesk.com/rss",
		Tier:    1,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	r1, _ := st.GetConfigRevision()
	if r1 <= r0 {
		t.Fatalf("expected revision increment after CreateSource: r0=%d r1=%d", r0, r1)
	}

	created.Tier = 2
	if _, err := st.UpdateSource(created); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}
	r2, _ := st.GetConfigRevision()
	if r2 <= r1 {
		t.Fatalf("expected revision increment after UpdateSource: r1=%d r2=%d", r1, r2)
	}

	if err := st.SaveRuntimeSettings(RuntimeSettings{Engine: config.DefaultConfig().Engine}); err != nil {
		t.Fatalf("SaveRuntimeSettings: %v", err)
	}
	r3, _ := st.GetConfigRevision()
	if r3 <= r2 {
		t.Fatalf("expected revision increment after SaveRuntimeSettings: r2=%d r3=%d", r2, r3)
	}

	if err := st.DeleteSource(created.ID); err != nil {
		t.Fatalf("DeleteSource: %v", err)
	}
	r4, _ := st.GetConfigRevision()
	if r4 <= r3 {
		t.Fatalf("expected revision increment after DeleteSource: r3=%d r4=%d", r3, r4)
	}

	ro, err := NewReadOnly(dbPath)
	if err != nil {
		t.Fatalf("NewReadOnly: %v", err)
	}
	defer ro.Close()

	if !ro.IsReadOnly() {
		t.Fatalf("expected IsReadOnly() == true")
	}
	if _, err := ro.GetConfigRevision(); err != nil {
		t.Fatalf("read-only GetConfigRevision failed: %v", err)
	}
	if _, err := ro.CreateSource(SourceRecord{Name: "Blocked", URL: "https://example.com/rss", Enabled: true}); err == nil {
		t.Fatalf("expected write via read-only store to fail")
	}
}


