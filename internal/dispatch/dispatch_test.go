package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"celerum/pkg/model"
)

func TestFormatTelegramMessage_Enriched(t *testing.T) {
	p := model.Payload{
		ID:       "test-enriched",
		FeedName: "CoinDesk",
		Title:    "Bitcoin <All-Time High>",
		Content:  "BTC crossed $100,000 following $2.4B ETF inflows.\n\n• Inflow record\n• Bullish momentum",
		Enriched: true,
		Sources: []model.SourceInfo{
			{
				Name:        "CoinDesk",
				Title:       "Original Title",
				URL:         "https://example.com/coindesk",
				PublishedAt: 1789139040, // 2026-09-11 15:04:00 UTC
			},
			{
				Name:        "Cointelegraph",
				Title:       "Alternative Title",
				URL:         "https://example.com/cointelegraph",
				PublishedAt: 1789140120, // 2026-09-11 15:22:00 UTC
			},
		},
	}

	formatted := FormatTelegramMessage(p)

	if !strings.Contains(formatted, "<b>AI Summary - CoinDesk, Cointelegraph</b>") {
		t.Errorf("expected AI Summary header with unique sites, got %s", formatted)
	}
	if !strings.Contains(formatted, "BTC crossed $100,000 following $2.4B ETF inflows.") {
		t.Errorf("expected content in message, got %s", formatted)
	}
	if !strings.Contains(formatted, "<b>Coverage:</b>\n") {
		t.Errorf("expected Coverage section, got %s", formatted)
	}
	if !strings.Contains(formatted, `<a href="https://example.com/coindesk">CoinDesk</a>: Original Title (11 Sep 15:04 UTC)`) {
		t.Errorf("expected formatted source link with timestamp in message, got %s", formatted)
	}
	if !strings.Contains(formatted, `<a href="https://example.com/cointelegraph">Cointelegraph</a>: Alternative Title (11 Sep 15:22 UTC)`) {
		t.Errorf("expected second formatted source link with timestamp in message, got %s", formatted)
	}
	if strings.Contains(formatted, "• <a href") {
		t.Errorf("expected no bullets in coverage section, got %s", formatted)
	}
}

func TestFormatTelegramMessage_Fallback(t *testing.T) {
	p := model.Payload{
		ID:       "test-fallback",
		FeedName: "CoinDesk",
		Title:    "Bitcoin <All-Time High>",
		Enriched: false,
		Sources: []model.SourceInfo{
			{Name: "CoinDesk", Title: "Original Title", URL: "https://example.com/coindesk"},
		},
	}

	formatted := FormatTelegramMessage(p)

	if strings.Contains(formatted, "AI Summary") {
		t.Errorf("expected no AI Summary header in fallback mode, got %s", formatted)
	}
	if !strings.Contains(formatted, "<b>Coverage:</b>\n") {
		t.Errorf("expected Coverage header in fallback mode, got %s", formatted)
	}
	if !strings.Contains(formatted, `<a href="https://example.com/coindesk">CoinDesk</a>: Original Title`) {
		t.Errorf("expected clean source link in message, got %s", formatted)
	}
	if strings.Contains(formatted, "• <a href") {
		t.Errorf("expected no bullets in coverage section, got %s", formatted)
	}
}

func TestGenericWebhookDispatcher(t *testing.T) {
	var receivedHeader string
	var receivedBody model.Payload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	headers := map[string]string{
		"Authorization": "Bearer test-secret",
	}
	dispatcher := NewGenericWebhookDispatcher(server.URL, headers)

	payload := model.Payload{
		ID:          "test-1",
		Title:       "Fed leaves rates unchanged",
		ClusterSize: 3,
		Score:       15.5,
	}

	err := dispatcher.Dispatch(context.Background(), payload)
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	if receivedHeader != "Bearer test-secret" {
		t.Errorf("expected header 'Bearer test-secret', got %q", receivedHeader)
	}
	if receivedBody.Title != "Fed leaves rates unchanged" {
		t.Errorf("expected payload title match, got %q", receivedBody.Title)
	}
}

