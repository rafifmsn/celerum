package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"celerum/internal/config"
)

// Summarizer produces structured briefings from article content.
type Summarizer interface {
	Summarize(ctx context.Context, title, content, language string) (string, error)
}

// Client implements OpenAI-compatible chat completion calls.
type Client struct {
	httpClient   *http.Client
	baseURL      string
	apiKey       string
	model        string
	customPrompt string
}

// ResolveBaseURL returns the effective OpenAI-compatible API base URL for a provider.
func ResolveBaseURL(provider, baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed != "" {
		return trimmed
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

// ListModels queries the provider's model catalog (/v1/models or Ollama /api/tags).
func ListModels(ctx context.Context, provider, baseURL, apiKey string) ([]string, error) {
	prov := strings.ToLower(strings.TrimSpace(provider))
	if prov == "" {
		prov = "openai"
	}
	if prov != "ollama" && strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("api_key is required to fetch models for provider %q", prov)
	}

	resolved := ResolveBaseURL(prov, baseURL)
	client := &http.Client{Timeout: 15 * time.Second}

	if prov == "ollama" {
		ollamaRoot := strings.TrimSuffix(resolved, "/v1")
		endpoint := ollamaRoot + "/api/tags"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("creating ollama tags request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying ollama %s: %w", endpoint, err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var parsed struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("decoding ollama tags: %w", err)
		}
		models := make([]string, 0, len(parsed.Models))
		for _, m := range parsed.Models {
			if strings.TrimSpace(m.Name) != "" {
				models = append(models, m.Name)
			}
		}
		sort.Strings(models)
		return models, nil
	}

	endpoint := resolved + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}

	models := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if strings.TrimSpace(m.ID) != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// NewSummarizer creates an LLM summarizer based on config.
func NewSummarizer(cfg *config.Config) Summarizer {
	baseURL := ResolveBaseURL(cfg.LLM.Provider, cfg.LLM.BaseURL)

	modelName := cfg.LLM.Model
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:      baseURL,
		apiKey:       cfg.LLM.APIKey,
		model:        modelName,
		customPrompt: cfg.LLM.SystemPrompt,
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func buildSystemPrompt(customPrompt, language string) string {
	if language == "" {
		language = "en"
	}
	if strings.TrimSpace(customPrompt) != "" {
		return strings.ReplaceAll(customPrompt, "{{language}}", language)
	}

	return fmt.Sprintf(`You are an elite financial news analyst and market intelligence editor.
Your task is to rephrase and synthesize the provided source into a high-density, authoritative news briefing.
Do NOT generate a headline, title, or sources section; the headline and sources are handled programmatically by the system.
Output clean formatting suitable for Telegram: use dense paragraphs and bullet points (•) where appropriate for distinct metrics.
Do NOT use markdown headers (# or ##), code blocks, or conversational commentary.

Target language for all text: %s.

CORE EDITORIAL DIRECTIVES:
1. RETAIN ALL QUANTITATIVE DATA (NON-NEGOTIABLE):
   - Never omit, round off, or convert hard figures into generic descriptions.
   - Retain every number, percentage, dollar amount, multiple, token price, share volume, valuation, and date present in the source (e.g. "$680M", "HK$7.08B", "RMB10.7B", "20%%", "2x", "50 bps").
   - Quantify every development whenever figures exist in the source.

2. PRECISE ENTITIES, MECHANISMS, AND CATALYSTS:
   - Identify specific companies, protocols, tickers, stock codes, executives, and regulators by name.
   - State the exact financial or legal mechanism (e.g. "IPO proceeds allocation", "convertible debt notes", "treasury reserve accumulation") rather than vague descriptions.

3. REPHRASED HIGH-DENSITY NARRATIVE:
   - Rephrase and organize into well-structured, fluent paragraphs rather than raw copy-pasting.
   - Eliminate filler words, platitudes, and empty commentary.
   - Flexible length: for brief sources, deliver a tight factual paragraph; for rich in-depth articles, deliver 2-3 dense narrative paragraphs.`,
		language,
	)
}

func buildUserPrompt(title, content, language string) string {
	if language == "" {
		language = "en"
	}
	return fmt.Sprintf(
		"Source Title: %s\n\nSource Content:\n%s\n\nTask: Deliver a high-density executive briefing in %s adhering strictly to all editorial directives. Do NOT include a headline. Retain all quantitative metrics, names, and mechanisms without conversational filler.",
		title, content, language,
	)
}

// Summarize dispatches a prompt to the LLM with retry and returns clean formatted text.
func (c *Client) Summarize(ctx context.Context, title, content, language string) (string, error) {
	if language == "" {
		language = "en"
	}

	systemPrompt := buildSystemPrompt(c.customPrompt, language)
	userPrompt := buildUserPrompt(title, content, language)

	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.2,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling chat request: %w", err)
	}

	url := c.baseURL + "/chat/completions"

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
		if err != nil {
			return "", fmt.Errorf("creating http request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
			continue
		}

		var chatResp chatResponse
		if err := json.Unmarshal(body, &chatResp); err != nil {
			lastErr = fmt.Errorf("unmarshaling chat response: %w", err)
			continue
		}

		if len(chatResp.Choices) == 0 {
			lastErr = fmt.Errorf("no choices in response")
			continue
		}

		rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
		return rawContent, nil
	}

	return "", fmt.Errorf("llm summarization failed after retries: %w", lastErr)
}
