package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"celerum/internal/config"
	"celerum/pkg/model"
)

func TestParseRSS(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Crypto News</title>
    <item>
      <title>Bitcoin hits brand new all-time high above $100k</title>
      <link>https://example.com/bitcoin-ath</link>
      <description>Bitcoin reached six figures today amid surging institutional demand.</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 MST</pubDate>
    </item>
  </channel>
</rss>`

	cfg := &config.Config{
		Engine: config.EngineConfig{
			MaxArticlesPerFeed: 10,
		},
	}
	ingester := NewIngester(cfg)
	feedCfg := config.FeedConfig{
		URL:  "https://example.com/feed.xml",
		Tier: 1,
	}

	articles, err := ingester.Parse([]byte(xmlData), feedCfg)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].Title != "Bitcoin hits brand new all-time high above $100k" {
		t.Errorf("title mismatch: %s", articles[0].Title)
	}
	if articles[0].FeedTier != 1 {
		t.Errorf("expected tier 1, got %d", articles[0].FeedTier)
	}
}

func TestParseAtom(t *testing.T) {
	atomData := `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Tech Blog</title>
  <entry>
    <title>SEC approves innovative decentralized settlement network</title>
    <link href="https://example.com/sec-decision" rel="alternate"/>
    <summary>Regulators have formally approved the new protocol today.</summary>
    <published>2026-09-10T12:00:00Z</published>
  </entry>
</feed>`

	cfg := &config.Config{
		Engine: config.EngineConfig{MaxArticlesPerFeed: 10},
	}
	ingester := NewIngester(cfg)
	feedCfg := config.FeedConfig{URL: "https://example.com/atom.xml", Tier: 2}

	articles, err := ingester.Parse([]byte(atomData), feedCfg)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].URL != "https://example.com/sec-decision" {
		t.Errorf("URL mismatch: %s", articles[0].URL)
	}
}

func TestFilterAndCap(t *testing.T) {
	cfg := &config.Config{
		Engine: config.EngineConfig{
			MaxArticlesPerFeed: 2,
		},
		Keywords: config.KeywordsConfig{
			Blocklist: []string{"/sponsored/", "/ad/"},
		},
	}
	ingester := NewIngester(cfg)

	articles := []model.Article{
		{Title: "Short", URL: "https://example.com/1", Description: "Short title dropped"},
		{Title: "This is a valid long article title with plenty of length", URL: "https://example.com/sponsored/deal", Description: "Sponsored post"},
		{Title: "Valid article 1 that covers breaking news in decentralized finance", URL: "https://example.com/valid1", Description: "Good desc 1"},
		{Title: "Valid article 2 with substantial headline information included", URL: "https://example.com/valid2", Description: "Good desc 2"},
		{Title: "Valid article 3 that should be tail-dropped due to max limit", URL: "https://example.com/valid3", Description: "Good desc 3"},
	}

	filtered := ingester.FilterAndCap(articles)

	if len(filtered) != 2 {
		t.Fatalf("expected 2 articles retained after filter and cap, got %d", len(filtered))
	}
	if filtered[0].URL != "https://example.com/valid1" {
		t.Errorf("expected valid1, got %s", filtered[0].URL)
	}
	if filtered[1].URL != "https://example.com/valid2" {
		t.Errorf("expected valid2, got %s", filtered[1].URL)
	}
}

func TestConditionalFetching(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><item><title>Breaking market headlines are here right now</title><link>https://example.com/item</link><description>Full report.</description></item></channel></rss>`))
	}))
	defer server.Close()

	cfg := &config.Config{Engine: config.EngineConfig{MaxArticlesPerFeed: 10}}
	ingester := NewIngester(cfg)
	feedCfg := config.FeedConfig{URL: server.URL, Tier: 1}

	// First request: 200 OK
	res1, err := ingester.FetchFeed(context.Background(), feedCfg, "", "")
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if res1.NotModified {
		t.Errorf("expected NotModified=false")
	}
	if res1.ETag != `"v1"` {
		t.Errorf("expected etag v1, got %s", res1.ETag)
	}

	// Second request with ETag: 304 Not Modified
	res2, err := ingester.FetchFeed(context.Background(), feedCfg, res1.ETag, "")
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if !res2.NotModified {
		t.Errorf("expected NotModified=true on conditional request")
	}
	if len(res2.Articles) != 0 {
		t.Errorf("expected 0 articles on 304 response")
	}
}

