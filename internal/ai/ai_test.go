package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"celerum/internal/config"
)

func TestSummarizerSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [
				{
					"message": {
						"content": "{\"title\": \"Rekor Baru Bitcoin\", \"summary\": \"Bitcoin menembus rekor baru.\", \"takeaways\": [\"Institusi membeli\", \"Sentimen positif\"], \"sentiment\": \"bullish\"}"
					}
				}
			]
		}`))
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

	res, err := summarizer.Summarize(context.Background(), "Bitcoin record", "Content body", "id")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	if res.Title != "Rekor Baru Bitcoin" {
		t.Errorf("title mismatch: %s", res.Title)
	}
	if res.Sentiment != "bullish" {
		t.Errorf("sentiment mismatch: %s", res.Sentiment)
	}
	if len(res.Takeaways) != 2 {
		t.Errorf("expected 2 takeaways, got %d", len(res.Takeaways))
	}
}

func TestSummarizerRetryOnFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error": {"message": "overloaded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [
				{
					"message": {
						"content": "{\"title\": \"Pulih\", \"summary\": \"Berhasil setelah percobaan kedua.\", \"takeaways\": [], \"sentiment\": \"neutral\"}"
					}
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			BaseURL: server.URL,
			Model:   "test-model",
		},
	}
	summarizer := NewSummarizer(cfg)

	res, err := summarizer.Summarize(context.Background(), "Test", "Content", "id")
	if err != nil {
		t.Fatalf("expected success after retry, got err: %v", err)
	}
	if res.Title != "Pulih" {
		t.Errorf("expected Pulih, got %s", res.Title)
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

	_, err := summarizer.Summarize(context.Background(), "Test", "Content", "id")
	if err == nil {
		t.Fatalf("expected error on 500 responses")
	}
}

