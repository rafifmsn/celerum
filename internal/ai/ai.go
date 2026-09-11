package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"celerum/internal/config"
)

// SummaryResult represents structured synthesis from the LLM.
type SummaryResult struct {
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Takeaways []string `json:"takeaways"`
	Sentiment string   `json:"sentiment"`
}

// Summarizer produces structured summaries from article content.
type Summarizer interface {
	Summarize(ctx context.Context, title, content, language string) (*SummaryResult, error)
}

// Client implements OpenAI-compatible chat completion calls.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
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
			Timeout: 10 * time.Second,
		},
		baseURL: baseURL,
		apiKey:  cfg.LLM.APIKey,
		model:   modelName,
	}
}

type chatRequest struct {
	Model          string           `json:"model"`
	Messages       []chatMessage    `json:"messages"`
	ResponseFormat *responseFormat  `json:"response_format,omitempty"`
	Temperature    float64          `json:"temperature"`
}

type responseFormat struct {
	Type string `json:"type"`
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

var codeFenceRegex = regexp.MustCompile(`(?s)^` + "```" + `(?:json)?\s*(.*?)\s*` + "```" + `$`)

// Summarize dispatches a prompt to the LLM with retry and JSON unmarshaling.
func (c *Client) Summarize(ctx context.Context, title, content, language string) (*SummaryResult, error) {
	if language == "" {
		language = "id"
	}

	systemPrompt := fmt.Sprintf(
		"You are a financial and market news summarizer. Output strictly valid JSON with no conversational text or markdown formatting.\n"+
			"Target language: %s.\n"+
			"Schema: {\"title\": \"Headline in %s\", \"summary\": \"2-3 sentence overview\", \"takeaways\": [\"bullet 1\", \"bullet 2\"], \"sentiment\": \"bullish|bearish|neutral\"}",
		language, language,
	)

	userPrompt := fmt.Sprintf("Title: %s\n\nContent:\n%s", title, content)

	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0.2,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling chat request: %w", err)
	}

	url := c.baseURL + "/chat/completions"

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
		if err != nil {
			return nil, fmt.Errorf("creating http request: %w", err)
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
		if match := codeFenceRegex.FindStringSubmatch(rawContent); len(match) > 1 {
			rawContent = strings.TrimSpace(match[1])
		}

		var result SummaryResult
		if err := json.Unmarshal([]byte(rawContent), &result); err != nil {
			lastErr = fmt.Errorf("parsing summary JSON: %w (raw: %s)", err, rawContent)
			continue
		}

		return &result, nil
	}

	return nil, fmt.Errorf("llm summarization failed after retries: %w", lastErr)
}
