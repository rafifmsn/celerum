package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete system configuration.
type Config struct {
	Version    string           `yaml:"version" json:"version"`
	Engine     EngineConfig     `yaml:"engine" json:"engine"`
	Feeds      []FeedConfig     `yaml:"feeds" json:"feeds"`
	Keywords   KeywordsConfig   `yaml:"keywords" json:"keywords"`
	Enrichment EnrichmentConfig `yaml:"enrichment" json:"enrichment"`
	LLM        LLMConfig        `yaml:"llm" json:"llm"`
	Dispatch   DispatchConfig   `yaml:"dispatch" json:"dispatch"`
	Database   DatabaseConfig   `yaml:"database" json:"database"`
}

// DatabaseConfig specifies the embedded SQLite database location.
type DatabaseConfig struct {
	Path string `yaml:"path" json:"path"`
}

// EngineConfig governs polling loops and algorithmic clustering parameters.
type EngineConfig struct {
	PollInterval        string  `yaml:"poll_interval" json:"poll_interval"`
	WindowDuration      string  `yaml:"window_duration" json:"window_duration"`
	FlushInterval       string  `yaml:"flush_interval" json:"flush_interval"`
	MaxArticlesPerFeed  int     `yaml:"max_articles_per_feed" json:"max_articles_per_feed"`
	SimilarityThreshold float64 `yaml:"similarity_threshold" json:"similarity_threshold"`
	BreakingThreshold   float64 `yaml:"breaking_threshold" json:"breaking_threshold"`
	TopK                int     `yaml:"top_k" json:"top_k"`

	// Parsed durations
	PollDuration time.Duration `yaml:"-" json:"-"`
	WindowDur    time.Duration `yaml:"-" json:"-"`
	FlushDur     time.Duration `yaml:"-" json:"-"`
}

// FeedConfig defines an RSS or Atom source.
type FeedConfig struct {
	Name     string `yaml:"name" json:"name"`
	URL      string `yaml:"url" json:"url"`
	Category string `yaml:"category,omitempty" json:"category"`
	Tier     int    `yaml:"tier" json:"tier"`
	Enabled  *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// IsEnabled reports whether the feed is active (defaults to true if unset).
func (f FeedConfig) IsEnabled() bool {
	return f.Enabled == nil || *f.Enabled
}

// KeywordsConfig contains boost and blocklist terms.
type KeywordsConfig struct {
	Boost     []string `yaml:"boost" json:"boost"`
	Blocklist []string `yaml:"blocklist" json:"blocklist"`
}

// EnrichmentConfig controls optional scraping.
type EnrichmentConfig struct {
	Scraper         string `yaml:"scraper" json:"scraper"`
	JinaAPIKey      string `yaml:"jina_api_key" json:"jina_api_key"`
	FirecrawlAPIKey string `yaml:"firecrawl_api_key" json:"firecrawl_api_key"`
}

// LLMConfig controls AI summarization.
type LLMConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	Provider     string `yaml:"provider" json:"provider"`
	BaseURL      string `yaml:"base_url" json:"base_url"`
	Model        string `yaml:"model" json:"model"`
	APIKey       string `yaml:"api_key" json:"api_key"`
	Language     string `yaml:"language" json:"language"`
	SystemPrompt string `yaml:"system_prompt" json:"system_prompt"`
}

// DispatchConfig holds webhook destinations.
type DispatchConfig struct {
	Telegram TelegramConfig `yaml:"telegram" json:"telegram"`
	Webhook  WebhookConfig  `yaml:"webhook" json:"webhook"`
}

// TelegramConfig defines Telegram Bot API target parameters.
type TelegramConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	BotToken string `yaml:"bot_token" json:"bot_token"`
	ChatID   string `yaml:"chat_id" json:"chat_id"`
}

// WebhookConfig defines generic HTTP POST target parameters.
type WebhookConfig struct {
	Enabled bool              `yaml:"enabled" json:"enabled"`
	URL     string            `yaml:"url" json:"url"`
	Headers map[string]string `yaml:"headers" json:"headers"`
}

var envVarPattern = regexp.MustCompile(`\$\{([a-zA-Z_0-9]+)(?::-([^}]*))?\}`)

// expandEnv replaces ${VAR} or ${VAR:-default} occurrences.
func expandEnv(raw []byte) []byte {
	return envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		submatches := envVarPattern.FindSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		varName := string(submatches[1])
		val, found := os.LookupEnv(varName)
		if found && val != "" {
			return []byte(val)
		}
		if len(submatches) > 2 && len(submatches[2]) > 0 {
			return submatches[2]
		}
		return []byte(val)
	})
}

var dotEnvPattern = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_0-9]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^#\s]*))`)

// LoadDotEnv parses and sets environment variables from a .env file if present.
func LoadDotEnv(dir string) {
	paths := []string{
		filepath.Join(dir, ".env"),
		".env",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			matches := dotEnvPattern.FindStringSubmatch(line)
			if len(matches) > 1 {
				key := matches[1]
				val := ""
				if matches[2] != "" {
					val = matches[2]
				} else if len(matches) > 3 && matches[3] != "" {
					val = matches[3]
				} else if len(matches) > 4 {
					val = matches[4]
				}

				if _, exists := os.LookupEnv(key); !exists {
					_ = os.Setenv(key, val)
				}
			}
		}
		break
	}
}

// Load reads and parses a YAML configuration file from path with environment expansion.
func Load(path string) (*Config, error) {
	LoadDotEnv(filepath.Dir(path))

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	expanded := expandEnv(raw)

	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML config: %w", err)
	}

	if err := cfg.ValidateAndSetDefaults(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// ParseYAML parses a YAML config document with environment variable expansion.
func ParseYAML(raw []byte) (*Config, error) {
	expanded := expandEnv(raw)
	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML config: %w", err)
	}
	if err := cfg.ValidateAndSetDefaults(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return &cfg, nil
}

// ExportYAML marshals a Config into YAML bytes.
func ExportYAML(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cannot export nil config")
	}
	if cfg.Version == "" {
		cfg.Version = "1"
	}
	return yaml.Marshal(cfg)
}

// DefaultConfig returns a sensible default runtime configuration when no YAML file is present.
func DefaultConfig() *Config {
	cfg := &Config{
		Version:  "1",
		Database: DatabaseConfig{Path: "data/celerum.db"},
		Engine: EngineConfig{
			PollInterval:        "5m",
			WindowDuration:      "3h",
			FlushInterval:       "1h",
			MaxArticlesPerFeed:  20,
			SimilarityThreshold: 0.28,
			BreakingThreshold:   12.0,
			TopK:                5,
		},
		Keywords: KeywordsConfig{
			Boost:     []string{"etf", "sec", "fed", "ath", "acquisition"},
			Blocklist: []string{"/sponsored/", "/press-releases/"},
		},
		Enrichment: EnrichmentConfig{Scraper: "none"},
		LLM: LLMConfig{
			Enabled:  false,
			Provider: "openrouter",
			Model:    "deepseek/deepseek-chat",
			Language: "en",
		},
	}
	_ = cfg.ValidateAndSetDefaults()
	return cfg
}

// ValidateAndSetDefaults verifies configuration rules and establishes sane defaults.
func (c *Config) ValidateAndSetDefaults() error {
	if c.Database.Path == "" {
		c.Database.Path = "data/celerum.db"
	}

	if c.Engine.PollInterval == "" {
		c.Engine.PollInterval = "5m"
	}
	pollDur, err := time.ParseDuration(c.Engine.PollInterval)
	if err != nil {
		return fmt.Errorf("invalid engine.poll_interval: %w", err)
	}
	c.Engine.PollDuration = pollDur

	if c.Engine.WindowDuration == "" {
		c.Engine.WindowDuration = "3h"
	}
	winDur, err := time.ParseDuration(c.Engine.WindowDuration)
	if err != nil {
		return fmt.Errorf("invalid engine.window_duration: %w", err)
	}
	c.Engine.WindowDur = winDur

	if c.Engine.FlushInterval == "" {
		c.Engine.FlushInterval = "1h"
	}
	flushDur, err := time.ParseDuration(c.Engine.FlushInterval)
	if err != nil {
		return fmt.Errorf("invalid engine.flush_interval: %w", err)
	}
	c.Engine.FlushDur = flushDur

	if c.Engine.MaxArticlesPerFeed <= 0 {
		c.Engine.MaxArticlesPerFeed = 20
	}
	if c.Engine.SimilarityThreshold <= 0 {
		c.Engine.SimilarityThreshold = 0.28
	}
	if c.Engine.BreakingThreshold <= 0 {
		c.Engine.BreakingThreshold = 12.0
	}
	if c.Engine.TopK <= 0 {
		c.Engine.TopK = 5
	}

	for i := range c.Feeds {
		if c.Feeds[i].URL == "" {
			return fmt.Errorf("feed entry %d is missing url", i)
		}
		if c.Feeds[i].Tier <= 0 {
			c.Feeds[i].Tier = 1
		}
		if c.Feeds[i].Name == "" {
			c.Feeds[i].Name = c.Feeds[i].URL
		}
		if strings.TrimSpace(c.Feeds[i].Category) == "" {
			c.Feeds[i].Category = "General"
		}
	}

	if c.LLM.Enabled {
		if c.LLM.Provider == "" {
			c.LLM.Provider = "openai"
		}
		if c.LLM.Model == "" {
			c.LLM.Model = "gpt-4o-mini"
		}
		if c.LLM.Language == "" {
			c.LLM.Language = "en"
		}
		provider := strings.ToLower(strings.TrimSpace(c.LLM.Provider))
		if provider != "ollama" && strings.TrimSpace(c.LLM.APIKey) == "" {
			return fmt.Errorf("llm is enabled with provider %q but api_key is missing", c.LLM.Provider)
		}
	}

	scraper := strings.ToLower(strings.TrimSpace(c.Enrichment.Scraper))
	if scraper == "firecrawl" && strings.TrimSpace(c.Enrichment.FirecrawlAPIKey) == "" {
		return fmt.Errorf("enrichment scraper is set to \"firecrawl\" but firecrawl_api_key is missing")
	}
	if scraper == "jina" && strings.TrimSpace(c.Enrichment.JinaAPIKey) == "" {
		return fmt.Errorf("enrichment scraper is set to \"jina\" but jina_api_key is missing")
	}

	if c.Dispatch.Telegram.Enabled {
		if c.Dispatch.Telegram.BotToken == "" || c.Dispatch.Telegram.ChatID == "" {
			return fmt.Errorf("telegram dispatch enabled but bot_token or chat_id is missing")
		}
	}

	if c.Dispatch.Webhook.Enabled {
		if c.Dispatch.Webhook.URL == "" {
			return fmt.Errorf("generic webhook dispatch enabled but url is missing")
		}
	}

	return nil
}

// DefaultTemplate returns standard configuration content for `celerum init`.
func DefaultTemplate() string {
	return `# Celerum Configuration
version: "1"

database:
  path: "data/celerum.db"

engine:
  poll_interval: "5m"
  window_duration: "3h"
  flush_interval: "1h"
  max_articles_per_feed: 20
  similarity_threshold: 0.28
  breaking_threshold: 12.0
  top_k: 5

feeds:
  - name: "CoinDesk"
    url: "https://www.coindesk.com/arc/outboundfeeds/rss/"
    tier: 1
  - name: "Cointelegraph"
    url: "https://cointelegraph.com/rss"
    tier: 2

keywords:
  boost:
    - "etf"
    - "sec"
    - "fed"
    - "ath"
    - "acquisition"
  blocklist:
    - "/sponsored/"
    - "/press-releases/"

enrichment:
  scraper: "none" # none | direct | jina | firecrawl
  jina_api_key: "${JINA_API_KEY}"
  firecrawl_api_key: "${FIRECRAWL_API_KEY}"

llm:
  enabled: false
  provider: "openrouter" # openrouter | deepseek | openai | groq | ollama
  base_url: "" # optional, defaults to provider API endpoint
  model: "deepseek/deepseek-chat"
  api_key: "${LLM_API_KEY}"
  language: "en"

dispatch:
  telegram:
    enabled: false
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    chat_id: "${TELEGRAM_CHAT_ID}"
  webhook:
    enabled: false
    url: "https://example.com/api/news"
    headers:
      Authorization: "Bearer ${WEBHOOK_SECRET}"
`
}

