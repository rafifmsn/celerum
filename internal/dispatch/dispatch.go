package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"celerum/internal/config"
	"celerum/pkg/model"
)

// Dispatcher defines the interface for delivering structured news payloads.
type Dispatcher interface {
	Name() string
	Dispatch(ctx context.Context, p model.Payload) error
}

// TelegramDispatcher delivers formatted news alerts via Telegram Bot API.
type TelegramDispatcher struct {
	client   *http.Client
	botToken string
	chatID   string
}

// NewTelegramDispatcher creates a TelegramDispatcher.
func NewTelegramDispatcher(botToken, chatID string) *TelegramDispatcher {
	return &TelegramDispatcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		botToken: botToken,
		chatID:   chatID,
	}
}

func (t *TelegramDispatcher) Name() string {
	return "telegram"
}

// FormatTelegramMessage formats the payload into safe HTML with size bounding.
func FormatTelegramMessage(p model.Payload) string {
	var sb strings.Builder

	if p.Enriched && strings.TrimSpace(p.Content) != "" {
		seen := make(map[string]bool)
		var sites []string
		for _, s := range p.Sources {
			if s.Name != "" && !seen[s.Name] {
				seen[s.Name] = true
				sites = append(sites, s.Name)
			}
		}
		if len(sites) == 0 && p.FeedName != "" {
			sites = append(sites, p.FeedName)
		}

		sb.WriteString("<b>AI Summary - ")
		sb.WriteString(html.EscapeString(strings.Join(sites, ", ")))
		sb.WriteString("</b>\n\n")

		sb.WriteString(strings.TrimSpace(p.Content))
		sb.WriteString("\n\n")
	}

	// Coverage
	if len(p.Sources) > 0 {
		sb.WriteString("<b>Coverage:</b>\n")
		for _, s := range p.Sources {
			if s.PublishedAt > 0 {
				t := time.Unix(s.PublishedAt, 0).UTC()
				dateStr := strings.TrimPrefix(t.Format("02 Jan 15:04 UTC"), "0")
				sb.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>: %s (%s)\n",
					html.EscapeString(s.URL),
					html.EscapeString(s.Name),
					html.EscapeString(s.Title),
					dateStr,
				))
			} else {
				sb.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>: %s\n",
					html.EscapeString(s.URL),
					html.EscapeString(s.Name),
					html.EscapeString(s.Title),
				))
			}
		}
	}

	msg := sb.String()
	// Defensively respect Telegram 4096 character limit
	if len(msg) > 4000 {
		msg = msg[:3990] + "..."
	}
	return msg
}

func (t *TelegramDispatcher) Dispatch(ctx context.Context, p model.Payload) error {
	msgHTML := FormatTelegramMessage(p)

	payloadObj := map[string]interface{}{
		"chat_id":                  t.chatID,
		"text":                     msgHTML,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	}

	jsonBytes, err := json.Marshal(payloadObj)
	if err != nil {
		return fmt.Errorf("marshaling telegram payload: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return fmt.Errorf("creating telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("dispatching to telegram: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GenericWebhookDispatcher delivers JSON payloads to a custom HTTP endpoint.
type GenericWebhookDispatcher struct {
	client  *http.Client
	url     string
	headers map[string]string
}

// NewGenericWebhookDispatcher creates a GenericWebhookDispatcher.
func NewGenericWebhookDispatcher(url string, headers map[string]string) *GenericWebhookDispatcher {
	return &GenericWebhookDispatcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		url:     url,
		headers: headers,
	}
}

func (w *GenericWebhookDispatcher) Name() string {
	return "webhook"
}

func (w *GenericWebhookDispatcher) Dispatch(ctx context.Context, p model.Payload) error {
	jsonBytes, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshaling webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(jsonBytes))
	if err != nil {
		return fmt.Errorf("creating webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("dispatching to webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// BuildDispatchers instantiates all enabled dispatchers from configuration.
func BuildDispatchers(cfg *config.Config) []Dispatcher {
	var dispatchers []Dispatcher

	if cfg.Dispatch.Telegram.Enabled {
		dispatchers = append(dispatchers, NewTelegramDispatcher(
			cfg.Dispatch.Telegram.BotToken,
			cfg.Dispatch.Telegram.ChatID,
		))
	}

	if cfg.Dispatch.Webhook.Enabled {
		dispatchers = append(dispatchers, NewGenericWebhookDispatcher(
			cfg.Dispatch.Webhook.URL,
			cfg.Dispatch.Webhook.Headers,
		))
	}

	return dispatchers
}

