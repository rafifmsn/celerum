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

func TestFormatTelegramMessage(t *testing.T) {
	p := model.Payload{
		Title:     "Bitcoin <All-Time High>",
		Summary:   "BTC crossed $100,000.",
		Takeaways: []string{"Inflow record", "Bullish momentum"},
		Sources: []model.SourceInfo{
			{Name: "CoinDesk", Title: "Original Title", URL: "https://example.com/coindesk"},
		},
	}

	formatted := FormatTelegramMessage(p)

	if !strings.Contains(formatted, "Bitcoin &lt;All-Time High&gt;") {
		t.Errorf("expected HTML escaping for title, got %s", formatted)
	}
	if !strings.Contains(formatted, "BTC crossed $100,000.") {
		t.Errorf("expected summary in message")
	}
	if !strings.Contains(formatted, "Inflow record") {
		t.Errorf("expected takeaway in message")
	}
	if !strings.Contains(formatted, `href="https://example.com/coindesk"`) {
		t.Errorf("expected source link in message")
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

