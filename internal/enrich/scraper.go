package enrich

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"celerum/internal/config"
)

// Scraper defines the contract for fetching full article body context.
type Scraper interface {
	FetchContent(ctx context.Context, targetURL string) (string, error)
}

// NoneScraper returns empty content without making network requests.
type NoneScraper struct{}

func (s *NoneScraper) FetchContent(ctx context.Context, targetURL string) (string, error) {
	return "", nil
}

// JinaScraper proxies target URLs via Jina Reader.
type JinaScraper struct {
	client *http.Client
	apiKey string
}

func (s *JinaScraper) FetchContent(ctx context.Context, targetURL string) (string, error) {
	readerURL := fmt.Sprintf("https://r.jina.ai/%s", targetURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readerURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating jina request: %w", err)
	}

	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting jina reader: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jina reader returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 50000))
	if err != nil {
		return "", fmt.Errorf("reading jina body: %w", err)
	}

	return strings.TrimSpace(string(body)), nil
}

var (
	htmlTagRegex = regexp.MustCompile(`<[^>]*>`)
	spaceRegex   = regexp.MustCompile(`\s+`)
)

// DirectScraper performs direct HTTP GET and strips HTML tags.
type DirectScraper struct {
	client *http.Client
}

func (s *DirectScraper) FetchContent(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating direct request: %w", err)
	}
	req.Header.Set("User-Agent", "Celerum/1.0 (+https://github.com/rafifmsn/celerum)")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("direct request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("direct scraper returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 50000))
	if err != nil {
		return "", fmt.Errorf("reading direct body: %w", err)
	}

	stripped := htmlTagRegex.ReplaceAllString(string(body), " ")
	cleaned := strings.TrimSpace(spaceRegex.ReplaceAllString(stripped, " "))

	// Limit to reasonable character count
	if len(cleaned) > 2000 {
		cleaned = cleaned[:2000]
	}

	return cleaned, nil
}

// NewScraper returns a configured Scraper implementation based on config.
func NewScraper(cfg *config.Config) Scraper {
	provider := strings.ToLower(strings.TrimSpace(cfg.Enrichment.Scraper))
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	switch provider {
	case "jina":
		return &JinaScraper{
			client: client,
			apiKey: cfg.Enrichment.JinaAPIKey,
		}
	case "direct":
		return &DirectScraper{
			client: client,
		}
	default:
		return &NoneScraper{}
	}
}

