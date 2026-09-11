package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// NewSummarizer creates an LLM summarizer based on config.
func NewSummarizer(cfg *config.Config) Summarizer {
	baseURL := strings.TrimRight(cfg.LLM.BaseURL, "/")
	if baseURL == "" {
		switch strings.ToLower(cfg.LLM.Provider) {
		case "openrouter":
			baseURL = "https://openrouter.ai/api/v1"
		case "deepseek":
			baseURL = "https://api.deepseek.com/v1"
		case "groq":
			baseURL = "https://api.groq.com/openai/v1"
		case "ollama":
			baseURL = "http://localhost:11434/v1"
		default:
			baseURL = "https://api.openai.com/v1"
		}
	}

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
