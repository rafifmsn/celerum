package enrich

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"celerum/internal/config"
)

func TestScrapers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body><h1>Major Regulatory Decision</h1><p>The regulator approved the ETF filing.</p></body></html>"))
	}))
	defer server.Close()

	// 1. None scraper
	cfgNone := &config.Config{Enrichment: config.EnrichmentConfig{Scraper: "none"}}
	scraperNone := NewScraper(cfgNone)
	contentNone, err := scraperNone.FetchContent(context.Background(), server.URL)
	if err != nil || contentNone != "" {
		t.Errorf("expected empty content from NoneScraper, got %q, err: %v", contentNone, err)
	}

	// 2. Direct scraper
	cfgDirect := &config.Config{Enrichment: config.EnrichmentConfig{Scraper: "direct"}}
	scraperDirect := NewScraper(cfgDirect)
	contentDirect, err := scraperDirect.FetchContent(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("direct scraper failed: %v", err)
	}
	if contentDirect != "Major Regulatory Decision The regulator approved the ETF filing." {
		t.Errorf("direct scraper output mismatch: %q", contentDirect)
	}
}

