package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"celerum/internal/config"
	"celerum/pkg/model"
)

// Ingester handles conditional HTTP fetching, XML parsing, and quota filtering.
type Ingester struct {
	client    *http.Client
	blocklist []string
	maxPerFeed int
}

// NewIngester creates a configured feed Ingester.
func NewIngester(cfg *config.Config) *Ingester {
	var blocklist []string
	for _, b := range cfg.Keywords.Blocklist {
		trimmed := strings.ToLower(strings.TrimSpace(b))
		if trimmed != "" {
			blocklist = append(blocklist, trimmed)
		}
	}
	maxPerFeed := cfg.Engine.MaxArticlesPerFeed
	if maxPerFeed <= 0 {
		maxPerFeed = 20
	}

	return &Ingester{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		blocklist:  blocklist,
		maxPerFeed: maxPerFeed,
	}
}

// FetchResult captures articles and updated cache headers.
type FetchResult struct {
	Articles     []model.Article
	ETag         string
	LastModified string
	NotModified  bool
}

// FetchFeed conditionally queries a feed URL with ETag and Last-Modified headers.
func (ing *Ingester) FetchFeed(ctx context.Context, feedCfg config.FeedConfig, etag, lastModified string) (*FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedCfg.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", feedCfg.URL, err)
	}

	req.Header.Set("User-Agent", "Celerum/1.0 (+https://github.com/rafifmsn/celerum)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := ing.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting feed %s: %w", feedCfg.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &FetchResult{
			NotModified:  true,
			ETag:         etag,
			LastModified: lastModified,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, feedCfg.URL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading feed body from %s: %w", feedCfg.URL, err)
	}

	newETag := resp.Header.Get("ETag")
	if newETag == "" {
		newETag = etag
	}
	newLastMod := resp.Header.Get("Last-Modified")
	if newLastMod == "" {
		newLastMod = lastModified
	}

	parsedArticles, err := ing.Parse(body, feedCfg)
	if err != nil {
		return nil, fmt.Errorf("parsing feed XML from %s: %w", feedCfg.URL, err)
	}

	filtered := ing.FilterAndCap(parsedArticles)

	return &FetchResult{
		Articles:     filtered,
		ETag:         newETag,
		LastModified: newLastMod,
		NotModified:  false,
	}, nil
}

// Raw RSS 2.0 XML schema
type rssRoot struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	DCDate      string `xml:"date"`
	GUID        string `xml:"guid"`
}

// Raw Atom XML schema
type atomRoot struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	ID        string     `xml:"id"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// Parse converts XML data into normalized Article structs.
func (ing *Ingester) Parse(data []byte, feedCfg config.FeedConfig) ([]model.Article, error) {
	// Attempt RSS 2.0 parse
	var rss rssRoot
	if err := xml.Unmarshal(data, &rss); err == nil && len(rss.Channel.Items) > 0 {
		articles := make([]model.Article, 0, len(rss.Channel.Items))
		for _, item := range rss.Channel.Items {
			title := strings.TrimSpace(item.Title)
			link := strings.TrimSpace(item.Link)
			desc := strings.TrimSpace(item.Description)

			pubTime := parseDate(item.PubDate, item.DCDate)
			artID := computeID(link, title)

			articles = append(articles, model.Article{
				ID:          artID,
				FeedURL:     feedCfg.URL,
				FeedTier:    feedCfg.Tier,
				Title:       title,
				Description: desc,
				URL:         link,
				PublishedAt: pubTime,
			})
		}
		return articles, nil
	}

	// Attempt Atom parse
	var atom atomRoot
	if err := xml.Unmarshal(data, &atom); err == nil && len(atom.Entries) > 0 {
		articles := make([]model.Article, 0, len(atom.Entries))
		for _, entry := range atom.Entries {
			title := strings.TrimSpace(entry.Title)
			link := ""
			for _, l := range entry.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			desc := strings.TrimSpace(entry.Summary)
			if desc == "" {
				desc = strings.TrimSpace(entry.Content)
			}

			pubTime := parseDate(entry.Published, entry.Updated)
			artID := computeID(link, title)

			articles = append(articles, model.Article{
				ID:          artID,
				FeedURL:     feedCfg.URL,
				FeedTier:    feedCfg.Tier,
				Title:       title,
				Description: desc,
				URL:         link,
				PublishedAt: pubTime,
			})
		}
		return articles, nil
	}

	return nil, fmt.Errorf("unable to parse XML as RSS 2.0 or Atom")
}

// FilterAndCap enforces low-information pruning and per-feed quotas.
func (ing *Ingester) FilterAndCap(articles []model.Article) []model.Article {
	filtered := make([]model.Article, 0, len(articles))

	for _, art := range articles {
		// Drop titles under 25 chars
		if len(art.Title) < 25 {
			continue
		}

		// Drop identical title and description
		if art.Title == art.Description {
			continue
		}

		// Drop URLs matching blocklist
		lowerURL := strings.ToLower(art.URL)
		blocked := false
		for _, b := range ing.blocklist {
			if strings.Contains(lowerURL, b) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}

		filtered = append(filtered, art)
	}

	// Tail-dropping: keep at most maxPerFeed items
	if len(filtered) > ing.maxPerFeed {
		filtered = filtered[:ing.maxPerFeed]
	}

	return filtered
}

func computeID(url, title string) string {
	h := sha256.New()
	h.Write([]byte(url))
	h.Write([]byte(title))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

var dateFormats = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseDate(dates ...string) time.Time {
	for _, d := range dates {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		for _, layout := range dateFormats {
			if t, err := time.Parse(layout, d); err == nil {
				return t
			}
		}
	}
	return time.Now()
}

