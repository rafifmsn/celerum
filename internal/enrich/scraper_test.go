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

	// 3. Firecrawl scraper
	fcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-fc-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"# Firecrawl Title\n\nFull article markdown body."}}`))
	}))
	defer fcServer.Close()

	fcScraper := &FirecrawlScraper{
		client:  fcServer.Client(),
		apiKey:  "test-fc-key",
		baseURL: fcServer.URL,
	}
	contentFC, err := fcScraper.FetchContent(context.Background(), "https://example.com/news")
	if err != nil {
		t.Fatalf("firecrawl scraper failed: %v", err)
	}
	if contentFC != "# Firecrawl Title\n\nFull article markdown body." {
		t.Errorf("firecrawl scraper output mismatch: %q", contentFC)
	}

	// Verify NewScraper constructs FirecrawlScraper
	cfgFC := &config.Config{Enrichment: config.EnrichmentConfig{Scraper: "firecrawl", FirecrawlAPIKey: "test-fc-key"}}
	scraperFactory := NewScraper(cfgFC)
	if _, ok := scraperFactory.(*FirecrawlScraper); !ok {
		t.Errorf("expected *FirecrawlScraper from NewScraper, got %T", scraperFactory)
	}
}

