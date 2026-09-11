package store

import (
	"path/filepath"
	"testing"
	"time"
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

