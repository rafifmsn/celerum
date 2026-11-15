package feed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"celerum/internal/config"
	"celerum/pkg/model"
)

func TestParseRSS(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/">
  <channel>
    <title>Crypto News</title>
    <item>
      <title>Bitcoin hits brand new &amp; high above $100k</title>
      <link>https://example.com/bitcoin-ath</link>
      <description>&lt;p&gt;Bitcoin reached &lt;b&gt;six figures&lt;/b&gt; today.&lt;/p&gt;</description>
      <category>Markets</category>
      <media:content url="https://cdn.example.com/btc.jpg" medium="image" />
      <pubDate>Mon, 02 Jan 2006 15:04:05 MST</pubDate>
    </item>
    <item>
      <title>FLASH: SEC approves ETF</title>
      <link>https://example.com/sec-flash</link>
      <description>FLASH: SEC approves ETF</description>
      <media:thumbnail url="https://cdn.example.com/thumb.png" />
    </item>
    <item>
      <title>Fed holds rates steady</title>
      <link>https://example.com/fed-rates</link>
      <description><![CDATA[<div><img src="https://cdn.example.com/fed.webp" alt="Fed"/><p>Rates unchanged at 5.25%.</p></div>]]></description>
      <enclosure url="https://cdn.example.com/audio.mp3" type="audio/mpeg" />
    </item>
    <item>
      <title>Enclosure image test</title>
      <link>https://example.com/enc-img</link>
      <description>Plain summary</description>
      <enclosure url="https://cdn.example.com/enc.jpg" type="image/jpeg" />
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
		Name: "CoinDesk",
		URL:  "https://example.com/feed.xml",
		Tier: 1,
	}

	articles, err := ingester.Parse([]byte(xmlData), feedCfg)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(articles) != 4 {
		t.Fatalf("expected 4 articles, got %d", len(articles))
	}
	if articles[0].Title != "Bitcoin hits brand new & high above $100k" {
		t.Errorf("title mismatch: %q", articles[0].Title)
	}
	if articles[0].Description != "Bitcoin reached six figures today." {
		t.Errorf("sanitized description mismatch: %q", articles[0].Description)
	}
	if articles[0].ImageURL != "https://cdn.example.com/btc.jpg" {
		t.Errorf("media:content image mismatch: %q", articles[0].ImageURL)
	}
	if articles[0].Category != "Markets" {
		t.Errorf("category mismatch: %q", articles[0].Category)
	}
	if articles[0].FeedName != "CoinDesk" {
		t.Errorf("feed name mismatch: %q", articles[0].FeedName)
	}
	if articles[0].FeedTier != 1 {
		t.Errorf("expected tier 1, got %d", articles[0].FeedTier)
	}

	// Title == Description should become empty Description
	if articles[1].Description != "" {
		t.Errorf("expected empty description when identical to title, got %q", articles[1].Description)
	}
	if articles[1].ImageURL != "https://cdn.example.com/thumb.png" {
		t.Errorf("media:thumbnail image mismatch: %q", articles[1].ImageURL)
	}

	// HTML <img> extraction fallback (skipping non-image enclosure)
	if articles[2].ImageURL != "https://cdn.example.com/fed.webp" {
		t.Errorf("img src fallback mismatch: %q", articles[2].ImageURL)
	}
	if articles[2].Description != "Rates unchanged at 5.25%." {
		t.Errorf("description mismatch: %q", articles[2].Description)
	}

	// Image enclosure
	if articles[3].ImageURL != "https://cdn.example.com/enc.jpg" {
		t.Errorf("enclosure image mismatch: %q", articles[3].ImageURL)
	}
}

func TestParseAtom(t *testing.T) {
	atomData := `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Tech Blog</title>
  <entry>
    <title>SEC approves innovative decentralized settlement network</title>
    <link href="https://example.com/sec-decision" rel="alternate"/>
    <summary>&lt;b&gt;Regulators&lt;/b&gt; have formally approved the new protocol today.</summary>
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
	if articles[0].Description != "Regulators have formally approved the new protocol today." {
		t.Errorf("sanitized atom summary mismatch: %q", articles[0].Description)
	}
	if articles[0].FeedName != "Tech Blog" {
		t.Errorf("expected fallback feed name from channel title, got %q", articles[0].FeedName)
	}
}

func TestFilterAndCap(t *testing.T) {
	cfg := &config.Config{
		Engine: config.EngineConfig{
			MaxArticlesPerFeed: 3,
		},
		Keywords: config.KeywordsConfig{
			Blocklist: []string{"/sponsored/", "/ad/"},
		},
	}
	ingester := NewIngester(cfg)

	articles := []model.Article{
		{Title: "FLASH: BTC spikes", URL: "https://example.com/1", Description: ""},
		{Title: "This is a valid long article title with plenty of length", URL: "https://example.com/sponsored/deal", Description: "Sponsored post"},
		{Title: "Valid article 1 that covers breaking news in decentralized finance", URL: "https://example.com/valid1", Description: "Good desc 1"},
		{Title: "Valid article 2 with substantial headline information included", URL: "https://example.com/valid2", Description: "Good desc 2"},
		{Title: "Valid article 3 that should be tail-dropped due to max limit", URL: "https://example.com/valid3", Description: "Good desc 3"},
	}

	filtered := ingester.FilterAndCap(articles)

	if len(filtered) != 3 {
		t.Fatalf("expected 3 articles retained (short flash kept, sponsored dropped, capped at 3), got %d", len(filtered))
	}
	if filtered[0].URL != "https://example.com/1" {
		t.Errorf("expected short breaking headline to be preserved for raw feed, got %s", filtered[0].URL)
	}
	if filtered[1].URL != "https://example.com/valid1" || filtered[2].URL != "https://example.com/valid2" {
		t.Errorf("unexpected filtered order: %+v", filtered)
	}
}

func TestConditionalFetchingAndCacheHeaders(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/ratelimit" {
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.Header().Set("Cache-Control", "public, max-age=180")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Cache-Control", "max-age=120")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><item><title>Breaking market headlines are here right now</title><link>https://example.com/item</link><description>Full report.</description></item></channel></rss>`))
	}))
	defer server.Close()

	cfg := &config.Config{Engine: config.EngineConfig{MaxArticlesPerFeed: 10}}
	ingester := NewIngester(cfg)
	feedCfg := config.FeedConfig{URL: server.URL, Tier: 1}

	res1, err := ingester.FetchFeed(context.Background(), feedCfg, "", "")
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if res1.NotModified || res1.StatusCode != 200 || res1.NextPollAfter != 120*time.Second {
		t.Errorf("unexpected first result: %+v", res1)
	}

	res2, err := ingester.FetchFeed(context.Background(), feedCfg, res1.ETag, "")
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if !res2.NotModified || res2.StatusCode != 304 || res2.NextPollAfter != 180*time.Second {
		t.Errorf("expected 304 with 180s max-age, got %+v", res2)
	}

	_, err = ingester.FetchFeed(context.Background(), config.FeedConfig{URL: server.URL + "/ratelimit", Tier: 1}, "", "")
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) {
		t.Fatalf("expected *FetchError on 429, got %v", err)
	}
	if fetchErr.StatusCode != 429 || fetchErr.RetryAfter != 45*time.Second {
		t.Errorf("expected 429 with 45s Retry-After, got status=%d retry=%v", fetchErr.StatusCode, fetchErr.RetryAfter)
	}
}

func TestUTLSTLSServerRoundTrip(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><item><title>TLS Feed Item</title><link>https://example.com/tls</link></item></channel></rss>`))
	}))
	defer ts.Close()

	cfg := &config.Config{Engine: config.EngineConfig{MaxArticlesPerFeed: 5}}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: newUTLSRoundTripper(true),
	}
	ing := NewIngesterWithClient(cfg, client)
	res, err := ing.FetchFeed(context.Background(), config.FeedConfig{URL: ts.URL, Tier: 1}, "", "")
	if err != nil {
		t.Fatalf("uTLS fetch against TLS server failed: %v", err)
	}
	if len(res.Articles) != 1 || res.Articles[0].Title != "TLS Feed Item" {
		t.Fatalf("unexpected articles from uTLS fetch: %+v", res.Articles)
	}
}
