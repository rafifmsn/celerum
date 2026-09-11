package score

import (
	"testing"
	"time"

	"celerum/internal/config"
	"celerum/pkg/model"
)

func TestScorerAndBuildClusters(t *testing.T) {
	cfg := &config.Config{
		Engine: config.EngineConfig{
			TopK: 2,
		},
		Keywords: config.KeywordsConfig{
			Boost: []string{"etf", "sec", "fed"},
		},
	}
	scorer := NewScorer(cfg)

	now := time.Now()

	group1 := []model.Article{
		{
			ID:          "1",
			FeedTier:    1,
			Title:       "SEC approves spot Ethereum ETF filings",
			Description: "Major regulatory shift as SEC greenlights filings.",
			PublishedAt: now.Add(-10 * time.Minute),
		},
		{
			ID:          "2",
			FeedTier:    2,
			Title:       "Ethereum ETF approved by SEC in landmark decision",
			Description: "Trading expected to begin soon.",
			PublishedAt: now.Add(-5 * time.Minute),
		},
	}

	group2 := []model.Article{
		{
			ID:          "3",
			FeedTier:    3,
			Title:       "Community member launches meme token on Solana",
			Description: "Fun experiment launched on decentralized exchange.",
			PublishedAt: now.Add(-20 * time.Minute),
		},
	}

	clusters := scorer.BuildClusters([][]model.Article{group1, group2}, now)

	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}

	// Group 1 should score significantly higher than group 2
	if clusters[0].Representative.ID != "1" {
		t.Errorf("expected group 1 (SEC Ethereum ETF) to be first, got ID %s", clusters[0].Representative.ID)
	}

	if clusters[0].Score <= clusters[1].Score {
		t.Errorf("expected cluster 0 score (%f) > cluster 1 score (%f)", clusters[0].Score, clusters[1].Score)
	}
}

func TestPickRepresentative(t *testing.T) {
	articles := []model.Article{
		{ID: "blog", FeedTier: 3, Title: "Blog post", Description: "Short summary"},
		{ID: "wire", FeedTier: 1, Title: "Wire release", Description: "Official complete press statement"},
		{ID: "aggregator", FeedTier: 2, Title: "News summary", Description: "Aggregated excerpt"},
	}

	rep := PickRepresentative(articles)
	if rep.ID != "wire" {
		t.Errorf("expected tier 1 wire to be picked as representative, got %s", rep.ID)
	}
}

