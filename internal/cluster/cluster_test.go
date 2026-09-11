package cluster

import (
	"testing"
	"time"

	"celerum/pkg/model"
)

func TestCleanTextAndTokenize(t *testing.T) {
	raw := "<p>Bitcoin hits a new <b>All-Time High</b> of $100k! See https://example.com for more.</p>"
	tokens := Tokenize(raw)

	expectedContains := []string{"bitcoin", "hit", "new", "time", "high", "100k"}
	tokenMap := make(map[string]bool)
	for _, tok := range tokens {
		tokenMap[tok] = true
	}

	for _, exp := range expectedContains {
		if !tokenMap[exp] {
			t.Errorf("expected token %q to be present in %v", exp, tokens)
		}
	}

	if tokenMap["all"] || tokenMap["for"] {
		t.Errorf("expected stop-words 'all' and 'for' to be removed")
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := []uint64{10, 20, 30, 40}
	b := []uint64{20, 30, 40, 50}

	sim := Jaccard(a, b)
	// Intersection: {20, 30, 40} (3), Union: {10, 20, 30, 40, 50} (5) -> 3/5 = 0.6
	if sim < 0.59 || sim > 0.61 {
		t.Errorf("expected Jaccard 0.60, got %f", sim)
	}

	c := []uint64{100, 200}
	simZero := Jaccard(a, c)
	if simZero != 0.0 {
		t.Errorf("expected Jaccard 0.0, got %f", simZero)
	}
}

func TestDisjointSet(t *testing.T) {
	ds := NewDisjointSet(5)
	ds.Union(0, 1)
	ds.Union(1, 2)
	ds.Union(3, 4)

	if ds.Find(0) != ds.Find(2) {
		t.Errorf("elements 0 and 2 should be in the same set")
	}
	if ds.Find(0) == ds.Find(3) {
		t.Errorf("elements 0 and 3 should be in different sets")
	}
}

func TestClusterArticles(t *testing.T) {
	articles := []model.Article{
		{
			ID:          "1",
			Title:       "BlackRock Bitcoin ETF records $1 billion daily inflows as crypto markets surge",
			Description: "Institutional investors pour money into BlackRock spot Bitcoin ETF product.",
			PublishedAt: time.Now(),
		},
		{
			ID:          "2",
			Title:       "Spot Bitcoin ETF from BlackRock sees record $1B inflows amid crypto market rally",
			Description: "BlackRock ETF registers over one billion dollars in inflows as market surges.",
			PublishedAt: time.Now(),
		},
		{
			ID:          "3",
			Title:       "Federal Reserve leaves interest rates unchanged at 5.25 percent benchmark",
			Description: "Fed Chair Jerome Powell announced that monetary policy remains restrictive.",
			PublishedAt: time.Now(),
		},
	}

	clusters := ClusterArticles(articles, 0.20)

	if len(clusters) != 2 {
		t.Fatalf("expected 2 distinct clusters, got %d", len(clusters))
	}

	// Verify that articles 1 and 2 are in the same cluster
	foundMerged := false
	for _, c := range clusters {
		if len(c) == 2 {
			foundMerged = true
			ids := map[string]bool{c[0].ID: true, c[1].ID: true}
			if !ids["1"] || !ids["2"] {
				t.Errorf("expected cluster with articles 1 and 2, got %v", ids)
			}
		}
	}

	if !foundMerged {
		t.Errorf("expected a merged cluster of size 2 for articles 1 and 2")
	}
}
