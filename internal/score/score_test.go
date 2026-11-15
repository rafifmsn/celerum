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

func TestTierWeightInversion(t *testing.T) {
	cfg := &config.Config{Engine: config.EngineConfig{TopK: 5}}
	scorer := NewScorer(cfg)
	now := time.Now()

	t1 := scorer.ScoreCluster([]model.Article{{ID: "1", FeedURL: "https://t1.com/rss", FeedTier: 1, Title: "Market update", PublishedAt: now}}, now)
	t2 := scorer.ScoreCluster([]model.Article{{ID: "2", FeedURL: "https://t2.com/rss", FeedTier: 2, Title: "Market update", PublishedAt: now}}, now)
	t3 := scorer.ScoreCluster([]model.Article{{ID: "3", FeedURL: "https://t3.com/rss", FeedTier: 3, Title: "Market update", PublishedAt: now}}, now)

	if !(t1 > t2 && t2 > t3) {
		t.Fatalf("expected Tier 1 (%f) > Tier 2 (%f) > Tier 3 (%f)", t1, t2, t3)
	}
}

func TestWordBoundaryKeywordMatching(t *testing.T) {
	cfg := &config.Config{
		Engine:   config.EngineConfig{TopK: 5},
		Keywords: config.KeywordsConfig{Boost: []string{"sec", "basis points"}},
	}
	scorer := NewScorer(cfg)
	now := time.Now()

	falsePositive := scorer.ScoreCluster([]model.Article{{
		ID:          "fp",
		FeedURL:     "https://example.com/rss",
		FeedTier:    1,
		Title:       "Second quarter technology sector report released",
		Description: "Secondary market activity grows.",
		PublishedAt: now,
	}}, now)

	exactMatch := scorer.ScoreCluster([]model.Article{{
		ID:          "ok",
		FeedURL:     "https://example.com/rss",
		FeedTier:    1,
		Title:       "SEC cuts review time by 50 basis points",
		Description: "Official filing.",
		PublishedAt: now,
	}}, now)

	if exactMatch <= falsePositive {
		t.Fatalf("expected exact token match (%f) > false positive (%f)", exactMatch, falsePositive)
	}
	if falsePositive != 3.0+4.5 {
		t.Fatalf("expected no keyword bonus on false positive, got %f", falsePositive)
	}
	if exactMatch != 3.0+4.5+4.0 {
		t.Fatalf("expected +4.0 keyword bonus for 'sec' and 'basis points', got %f", exactMatch)
	}
}

func TestUniqueSourceCountAndDecay(t *testing.T) {
	cfg := &config.Config{Engine: config.EngineConfig{TopK: 5}}
	scorer := NewScorer(cfg)
	now := time.Now()

	noisySingleFeed := []model.Article{
		{ID: "1", FeedURL: "https://noisy.com/rss", FeedTier: 1, Title: "Update one", PublishedAt: now},
		{ID: "2", FeedURL: "https://noisy.com/rss", FeedTier: 1, Title: "Update two", PublishedAt: now},
		{ID: "3", FeedURL: "https://noisy.com/rss", FeedTier: 1, Title: "Update three", PublishedAt: now},
		{ID: "4", FeedURL: "https://noisy.com/rss", FeedTier: 1, Title: "Update four", PublishedAt: now},
		{ID: "5", FeedURL: "https://noisy.com/rss", FeedTier: 1, Title: "Update five", PublishedAt: now},
	}

	singleArticle := []model.Article{
		{ID: "a", FeedURL: "https://noisy.com/rss", FeedTier: 1, Title: "Update one", PublishedAt: now},
	}

	sNoisy := scorer.ScoreCluster(noisySingleFeed, now)
	sSingle := scorer.ScoreCluster(singleArticle, now)
	if sNoisy != sSingle {
		t.Fatalf("expected 5 articles from same FeedURL (%f) to score equal to 1 article (%f)", sNoisy, sSingle)
	}

	stale := scorer.ScoreCluster([]model.Article{
		{ID: "old", FeedURL: "https://a.com/rss", FeedTier: 1, Title: "Old update", PublishedAt: now.Add(-3 * time.Hour)},
	}, now)
	if stale >= sSingle*0.5 {
		t.Fatalf("expected 3h-old cluster (%f) to decay below 50%% of fresh cluster (%f)", stale, sSingle)
	}
}

func BenchmarkScoreCluster(b *testing.B) {
	cfg := &config.Config{
		Engine:   config.EngineConfig{TopK: 5},
		Keywords: config.KeywordsConfig{Boost: []string{"etf", "sec", "fed", "ath", "acquisition"}},
	}
	scorer := NewScorer(cfg)
	now := time.Now()
	articles := []model.Article{
		{ID: "1", FeedURL: "https://coindesk.com/rss", FeedTier: 1, Title: "SEC approves spot Ethereum ETF filings", Description: "Regulators greenlight spot ETF listings across major exchanges.", PublishedAt: now.Add(-12 * time.Minute)},
		{ID: "2", FeedURL: "https://bloomberg.com/rss", FeedTier: 1, Title: "US SEC clears spot Ethereum ETFs for trading", Description: "Issuers finalize fee disclosures ahead of market open.", PublishedAt: now.Add(-10 * time.Minute)},
		{ID: "3", FeedURL: "https://cointelegraph.com/rss", FeedTier: 2, Title: "Ethereum ETF approval triggers institutional inflows", Description: "Market analysts expect billions in new allocations.", PublishedAt: now.Add(-8 * time.Minute)},
		{ID: "4", FeedURL: "https://theblock.co/rss", FeedTier: 2, Title: "Spot Ether ETFs receive final SEC sign-off", Description: "Trading begins following S-1 effectiveness.", PublishedAt: now.Add(-5 * time.Minute)},
		{ID: "5", FeedURL: "https://decrypt.co/rss", FeedTier: 3, Title: "Ether climbs as SEC approves ETF applications", Description: "Crypto markets rally after regulatory milestone.", PublishedAt: now.Add(-3 * time.Minute)},
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = scorer.ScoreCluster(articles, now)
	}
}


