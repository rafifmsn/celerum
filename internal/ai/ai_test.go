package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"celerum/internal/config"
)

func TestSummarizerSuccess(t *testing.T) {
	expectedContent := "Bitcoin broke the $100,000 level following $2.4B in institutional ETF inflows.\n\n• Daily trading volume jumped 45%\n• Institutional accumulation continues"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{
			"choices": [
				{
					"message": {
						"content": %q
					}
				}
			]
		}`, expectedContent)
	}))
	defer server.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			BaseURL: server.URL,
			Model:   "test-model",
			APIKey:  "test-key",
		},
	}
	summarizer := NewSummarizer(cfg)

	res, err := summarizer.Summarize(context.Background(), "Bitcoin record", "Content body", "en")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	if res != expectedContent {
		t.Errorf("content mismatch: got %q, want %q", res, expectedContent)
	}
}

func TestSummarizerRetryOnFailure(t *testing.T) {
	attempts := 0
	expectedContent := "<b>Recovered Briefing</b>\n\nOperation succeeded on retry attempt."

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error": {"message": "overloaded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{
			"choices": [
				{
					"message": {
						"content": %q
					}
				}
			]
		}`, expectedContent)
	}))
	defer server.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			BaseURL: server.URL,
			Model:   "test-model",
		},
	}
	summarizer := NewSummarizer(cfg)

	res, err := summarizer.Summarize(context.Background(), "Test", "Content", "en")
	if err != nil {
		t.Fatalf("expected success after retry, got err: %v", err)
	}
	if res != expectedContent {
		t.Errorf("expected %q, got %q", expectedContent, res)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestSummarizerPermanentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error": "down"}`)
	}))
	defer server.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			BaseURL: server.URL,
		},
	}
	summarizer := NewSummarizer(cfg)

	_, err := summarizer.Summarize(context.Background(), "Test", "Content", "en")
	if err == nil {
		t.Fatalf("expected error on 500 responses")
	}
}

func TestPromptConstruction(t *testing.T) {
	sys := buildSystemPrompt("", "")
	if sys == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if !strings.Contains(sys, "Target language for all text: en.") {
		t.Errorf("expected default language en in system prompt: %s", sys)
	}
	for _, expectedKeyword := range []string{"QUANTITATIVE DATA", "PRECISE ENTITIES", "REPHRASED HIGH-DENSITY NARRATIVE", "Do NOT generate a headline"} {
		if !strings.Contains(sys, expectedKeyword) {
			t.Errorf("system prompt missing expected directive: %s", expectedKeyword)
		}
	}

	custom := "Custom prompt in {{language}} with specific tone."
	customBuilt := buildSystemPrompt(custom, "id")
	if customBuilt != "Custom prompt in id with specific tone." {
		t.Errorf("custom prompt interpolation failed, got: %s", customBuilt)
	}

	user := buildUserPrompt("Metaplanet Equity Backlash", "Shareholders seething over 20% pool", "")
	if !strings.Contains(user, "briefing in en") {
		t.Errorf("expected user prompt to default to en: %s", user)
	}
	if !strings.Contains(user, "Do NOT include a headline") {
		t.Errorf("expected user prompt to direct no headline: %s", user)
	}
	if !strings.Contains(user, "Metaplanet Equity Backlash") || !strings.Contains(user, "20% pool") {
		t.Errorf("user prompt does not contain source data: %s", user)
	}
}





