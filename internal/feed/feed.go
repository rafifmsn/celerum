package feed

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

	"celerum/internal/config"
	"celerum/pkg/model"
)

var (
	htmlTagRegex        = regexp.MustCompile(`<[^>]*>`)
	whitespaceRegex     = regexp.MustCompile(`\s+`)
	imgSrcRegex         = regexp.MustCompile(`(?is)<img[^>]+src\s*=\s*["']([^"']+)["']`)
	mediaContentRegex   = regexp.MustCompile(`(?is)<media:content[^>]+url\s*=\s*["']([^"']+)["']`)
	mediaThumbnailRegex = regexp.MustCompile(`(?is)<media:thumbnail[^>]+url\s*=\s*["']([^"']+)["']`)
	maxAgeRegex         = regexp.MustCompile(`(?i)\bmax-age\s*=\s*(\d+)`)
)

// FetchError represents a non-200/304 HTTP or transport error from a feed poll.
type FetchError struct {
	URL        string
	StatusCode int
	RetryAfter time.Duration
	LatencyMs  int64
	Err        error
}

func (e *FetchError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("feed %s returned HTTP %d: %v", e.URL, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("feed %s error: %v", e.URL, e.Err)
}

func (e *FetchError) Unwrap() error {
	return e.Err
}

// utlsRoundTripper mimics a modern Chrome TLS ClientHello and negotiates HTTP/2 or HTTP/1.1 via ALPN.
type utlsRoundTripper struct {
	insecureSkipVerify bool
	plainTransport     *http.Transport
}

func newUTLSRoundTripper(insecureSkipVerify bool) *utlsRoundTripper {
	return &utlsRoundTripper{
		insecureSkipVerify: insecureSkipVerify,
		plainTransport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        64,
			IdleConnTimeout:     60 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

type closingReadCloser struct {
	io.ReadCloser
	closeFn func()
}

func (c *closingReadCloser) Close() error {
	err := c.ReadCloser.Close()
	if c.closeFn != nil {
		c.closeFn()
	}
	return err
}

func (rt *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return rt.plainTransport.RoundTrip(req)
	}

	host := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(req.Context(), "tcp", addr)
	if err != nil {
		return nil, err
	}

	uconn := utls.UClient(rawConn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: rt.insecureSkipVerify,
	}, utls.HelloChrome_Auto)

	if err := uconn.HandshakeContext(req.Context()); err != nil {
		_ = uconn.Close()
		return nil, err
	}

	alpn := uconn.ConnectionState().NegotiatedProtocol
	if alpn == "h2" {
		h2t := &http2.Transport{}
		cc, err := h2t.NewClientConn(uconn)
		if err != nil {
			_ = uconn.Close()
			return nil, err
		}
		resp, err := cc.RoundTrip(req)
		if err != nil {
			_ = cc.Close()
			_ = uconn.Close()
			return nil, err
		}
		resp.Body = &closingReadCloser{
			ReadCloser: resp.Body,
			closeFn: func() {
				_ = cc.Close()
				_ = uconn.Close()
			},
		}
		return resp, nil
	}

	// HTTP/1.1 over the already-handshaken uTLS connection.
	var used bool
	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSNextProto:      make(map[string]func(string, *tls.Conn) http.RoundTripper),
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if used {
				return nil, errors.New("one-shot uTLS connection already consumed")
			}
			used = true
			return uconn, nil
		},
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		_ = uconn.Close()
		return nil, err
	}
	origBody := resp.Body
	resp.Body = &closingReadCloser{
		ReadCloser: origBody,
		closeFn: func() {
			tr.CloseIdleConnections()
		},
	}
	return resp, nil
}

// Ingester handles conditional HTTP fetching, XML parsing, and quota filtering.
type Ingester struct {
	mu         sync.RWMutex
	client     *http.Client
	blocklist  []string
	maxPerFeed int
}

// NewIngester creates a configured feed Ingester backed by uTLS Chrome transport.
func NewIngester(cfg *config.Config) *Ingester {
	ing := &Ingester{
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: newUTLSRoundTripper(false),
		},
	}
	ing.UpdateConfig(cfg)
	return ing
}

// NewIngesterWithClient creates an Ingester with a custom HTTP client (useful for testing).
func NewIngesterWithClient(cfg *config.Config, client *http.Client) *Ingester {
	ing := &Ingester{
		client: client,
	}
	ing.UpdateConfig(cfg)
	return ing
}

// UpdateConfig refreshes blocklist and per-feed caps dynamically.
func (ing *Ingester) UpdateConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
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

	ing.mu.Lock()
	ing.blocklist = blocklist
	ing.maxPerFeed = maxPerFeed
	ing.mu.Unlock()
}

// FetchResult captures articles, updated cache headers, and adaptive poll hints.
type FetchResult struct {
	Articles      []model.Article
	ETag          string
	LastModified  string
	NotModified   bool
	StatusCode    int
	LatencyMs     int64
	NextPollAfter time.Duration
}

// ParseRetryAndCacheHeaders extracts Retry-After and Cache-Control: max-age hints.
func ParseRetryAndCacheHeaders(headers http.Header, now time.Time) time.Duration {
	var best time.Duration

	if ra := strings.TrimSpace(headers.Get("Retry-After")); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			d := time.Duration(secs) * time.Second
			if d > best {
				best = d
			}
		} else if t, err := http.ParseTime(ra); err == nil {
			d := t.Sub(now)
			if d > best {
				best = d
			}
		}
	}

	if cc := headers.Get("Cache-Control"); cc != "" {
		if matches := maxAgeRegex.FindStringSubmatch(cc); len(matches) == 2 {
			if secs, err := strconv.Atoi(matches[1]); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > best {
					best = d
				}
			}
		}
	}

	return best
}

// FetchFeed conditionally queries a feed URL with ETag and Last-Modified headers.
func (ing *Ingester) FetchFeed(ctx context.Context, feedCfg config.FeedConfig, etag, lastModified string) (*FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedCfg.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", feedCfg.URL, err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	start := time.Now()
	resp, err := ing.client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, &FetchError{
			URL:       feedCfg.URL,
			LatencyMs: latencyMs,
			Err:       err,
		}
	}
	defer resp.Body.Close()

	now := time.Now()
	nextAfter := ParseRetryAndCacheHeaders(resp.Header, now)

	if resp.StatusCode == http.StatusNotModified {
		newETag := resp.Header.Get("ETag")
		if newETag == "" {
			newETag = etag
		}
		newLastMod := resp.Header.Get("Last-Modified")
		if newLastMod == "" {
			newLastMod = lastModified
		}
		return &FetchResult{
			NotModified:   true,
			ETag:          newETag,
			LastModified:  newLastMod,
			StatusCode:    resp.StatusCode,
			LatencyMs:     latencyMs,
			NextPollAfter: nextAfter,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &FetchError{
			URL:        feedCfg.URL,
			StatusCode: resp.StatusCode,
			RetryAfter: nextAfter,
			LatencyMs:  latencyMs,
			Err:        fmt.Errorf("unexpected status code %d", resp.StatusCode),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, &FetchError{
			URL:       feedCfg.URL,
			LatencyMs: latencyMs,
			Err:       fmt.Errorf("reading feed body: %w", err),
		}
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
		return nil, &FetchError{
			URL:        feedCfg.URL,
			StatusCode: resp.StatusCode,
			LatencyMs:  latencyMs,
			Err:        fmt.Errorf("parsing feed XML: %w", err),
		}
	}

	filtered := ing.FilterAndCap(parsedArticles)

	return &FetchResult{
		Articles:      filtered,
		ETag:          newETag,
		LastModified:  newLastMod,
		NotModified:   false,
		StatusCode:    resp.StatusCode,
		LatencyMs:     latencyMs,
		NextPollAfter: nextAfter,
	}, nil
}

type mediaTag struct {
	URL    string `xml:"url,attr"`
	Medium string `xml:"medium,attr"`
	Type   string `xml:"type,attr"`
}

type enclosureTag struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// Raw RSS 2.0 XML schema
type rssRoot struct {
	Channel struct {
		Title string    `xml:"title"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title          string         `xml:"title"`
	Link           string         `xml:"link"`
	Description    string         `xml:"description"`
	ContentEncoded string         `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	PubDate        string         `xml:"pubDate"`
	DCDate         string         `xml:"date"`
	GUID           string         `xml:"guid"`
	Categories     []string       `xml:"category"`
	MediaContents  []mediaTag     `xml:"http://search.yahoo.com/mrss/ content"`
	MediaThumbs    []mediaTag     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	Enclosures     []enclosureTag `xml:"enclosure"`
	InnerXML       string         `xml:",innerxml"`
}

// Raw Atom XML schema
type atomRoot struct {
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title         string         `xml:"title"`
	Links         []atomLink     `xml:"link"`
	Summary       string         `xml:"summary"`
	Content       string         `xml:"content"`
	Published     string         `xml:"published"`
	Updated       string         `xml:"updated"`
	ID            string         `xml:"id"`
	Categories    []atomCategory `xml:"category"`
	MediaContents []mediaTag     `xml:"http://search.yahoo.com/mrss/ content"`
	MediaThumbs   []mediaTag     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	InnerXML      string         `xml:",innerxml"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

func sanitizeHTMLText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	step1 := htmlTagRegex.ReplaceAllString(raw, " ")
	unescaped := html.UnescapeString(step1)
	step2 := htmlTagRegex.ReplaceAllString(unescaped, " ")
	unescaped2 := html.UnescapeString(step2)
	collapsed := whitespaceRegex.ReplaceAllString(unescaped2, " ")
	return strings.TrimSpace(collapsed)
}

func extractImageFromHTML(rawHTML ...string) string {
	for _, chunk := range rawHTML {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		unescaped := html.UnescapeString(chunk)
		if m := imgSrcRegex.FindStringSubmatch(unescaped); len(m) == 2 {
			src := strings.TrimSpace(m[1])
			if src != "" && !strings.HasPrefix(strings.ToLower(src), "data:") {
				return src
			}
		}
	}
	return ""
}

func extractRSSImage(item rssItem) string {
	for _, mc := range item.MediaContents {
		url := strings.TrimSpace(mc.URL)
		if url == "" {
			continue
		}
		if mc.Medium == "" || strings.EqualFold(mc.Medium, "image") || strings.HasPrefix(strings.ToLower(mc.Type), "image/") {
			return url
		}
	}
	if m := mediaContentRegex.FindStringSubmatch(item.InnerXML); len(m) == 2 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}

	for _, mt := range item.MediaThumbs {
		if url := strings.TrimSpace(mt.URL); url != "" {
			return url
		}
	}
	if m := mediaThumbnailRegex.FindStringSubmatch(item.InnerXML); len(m) == 2 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}

	for _, enc := range item.Enclosures {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(enc.Type)), "image/") && strings.TrimSpace(enc.URL) != "" {
			return strings.TrimSpace(enc.URL)
		}
	}

	return extractImageFromHTML(item.Description, item.ContentEncoded)
}

func extractAtomImage(entry atomEntry) string {
	for _, mc := range entry.MediaContents {
		url := strings.TrimSpace(mc.URL)
		if url == "" {
			continue
		}
		if mc.Medium == "" || strings.EqualFold(mc.Medium, "image") || strings.HasPrefix(strings.ToLower(mc.Type), "image/") {
			return url
		}
	}
	if m := mediaContentRegex.FindStringSubmatch(entry.InnerXML); len(m) == 2 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}

	for _, mt := range entry.MediaThumbs {
		if url := strings.TrimSpace(mt.URL); url != "" {
			return url
		}
	}
	if m := mediaThumbnailRegex.FindStringSubmatch(entry.InnerXML); len(m) == 2 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}

	for _, l := range entry.Links {
		if l.Rel == "enclosure" && strings.HasPrefix(strings.ToLower(l.Type), "image/") && strings.TrimSpace(l.Href) != "" {
			return strings.TrimSpace(l.Href)
		}
	}

	return extractImageFromHTML(entry.Summary, entry.Content)
}

func resolveCategory(feedCfg config.FeedConfig, itemCats []string) string {
	if strings.TrimSpace(feedCfg.Category) != "" && !strings.EqualFold(strings.TrimSpace(feedCfg.Category), "General") {
		return strings.TrimSpace(feedCfg.Category)
	}
	for _, c := range itemCats {
		c = sanitizeHTMLText(c)
		if c != "" {
			return c
		}
	}
	if strings.TrimSpace(feedCfg.Category) != "" {
		return strings.TrimSpace(feedCfg.Category)
	}
	return "General"
}

func resolveFeedName(feedCfg config.FeedConfig, channelTitle string) string {
	if strings.TrimSpace(feedCfg.Name) != "" && feedCfg.Name != feedCfg.URL {
		return strings.TrimSpace(feedCfg.Name)
	}
	if clean := sanitizeHTMLText(channelTitle); clean != "" {
		return clean
	}
	if strings.TrimSpace(feedCfg.Name) != "" {
		return strings.TrimSpace(feedCfg.Name)
	}
	return feedCfg.URL
}

// Parse converts XML data into normalized Article structs.
func (ing *Ingester) Parse(data []byte, feedCfg config.FeedConfig) ([]model.Article, error) {
	now := time.Now().UTC()

	// Attempt RSS 2.0 parse
	var rss rssRoot
	if err := xml.Unmarshal(data, &rss); err == nil && len(rss.Channel.Items) > 0 {
		feedName := resolveFeedName(feedCfg, rss.Channel.Title)
		articles := make([]model.Article, 0, len(rss.Channel.Items))
		for _, item := range rss.Channel.Items {
			title := sanitizeHTMLText(item.Title)
			link := strings.TrimSpace(item.Link)

			imageURL := extractRSSImage(item)

			rawDesc := item.Description
			if strings.TrimSpace(rawDesc) == "" {
				rawDesc = item.ContentEncoded
			}
			desc := sanitizeHTMLText(rawDesc)
			if strings.EqualFold(desc, title) {
				desc = ""
			}

			pubTime := parseDate(item.PubDate, item.DCDate)
			artID := computeID(link, title)

			articles = append(articles, model.Article{
				ID:          artID,
				FeedURL:     feedCfg.URL,
				FeedName:    feedName,
				FeedTier:    feedCfg.Tier,
				Category:    resolveCategory(feedCfg, item.Categories),
				Title:       title,
				Description: desc,
				URL:         link,
				ImageURL:    imageURL,
				PublishedAt: pubTime,
				IngestedAt:  now,
			})
		}
		return articles, nil
	}

	// Attempt Atom parse
	var atom atomRoot
	if err := xml.Unmarshal(data, &atom); err == nil && len(atom.Entries) > 0 {
		feedName := resolveFeedName(feedCfg, atom.Title)
		articles := make([]model.Article, 0, len(atom.Entries))
		for _, entry := range atom.Entries {
			title := sanitizeHTMLText(entry.Title)
			link := ""
			for _, l := range entry.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = strings.TrimSpace(l.Href)
					break
				}
			}

			imageURL := extractAtomImage(entry)

			rawDesc := entry.Summary
			if strings.TrimSpace(rawDesc) == "" {
				rawDesc = entry.Content
			}
			desc := sanitizeHTMLText(rawDesc)
			if strings.EqualFold(desc, title) {
				desc = ""
			}

			var cats []string
			for _, c := range entry.Categories {
				if c.Label != "" {
					cats = append(cats, c.Label)
				} else if c.Term != "" {
					cats = append(cats, c.Term)
				}
			}

			pubTime := parseDate(entry.Published, entry.Updated)
			artID := computeID(link, title)

			articles = append(articles, model.Article{
				ID:          artID,
				FeedURL:     feedCfg.URL,
				FeedName:    feedName,
				FeedTier:    feedCfg.Tier,
				Category:    resolveCategory(feedCfg, cats),
				Title:       title,
				Description: desc,
				URL:         link,
				ImageURL:    imageURL,
				PublishedAt: pubTime,
				IngestedAt:  now,
			})
		}
		return articles, nil
	}

	return nil, fmt.Errorf("unable to parse XML as RSS 2.0 or Atom")
}

// FilterAndCap removes blocklisted URLs and enforces per-feed quotas while preserving
// short breaking headlines and title-only entries for raw feed storage.
func (ing *Ingester) FilterAndCap(articles []model.Article) []model.Article {
	ing.mu.RLock()
	blocklist := ing.blocklist
	maxPerFeed := ing.maxPerFeed
	ing.mu.RUnlock()

	filtered := make([]model.Article, 0, len(articles))

	for _, art := range articles {
		if strings.TrimSpace(art.Title) == "" {
			continue
		}

		lowerURL := strings.ToLower(art.URL)
		blocked := false
		for _, b := range blocklist {
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

	if maxPerFeed > 0 && len(filtered) > maxPerFeed {
		filtered = filtered[:maxPerFeed]
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
