package store

import (
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


