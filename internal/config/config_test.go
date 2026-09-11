package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigLoadAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "celerum.yaml")

	content := `
version: "1"
feeds:
  - name: "TestFeed"
    url: "https://example.com/rss"
    tier: 1
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Engine.PollDuration != 5*time.Minute {
		t.Errorf("expected 5m poll duration, got %v", cfg.Engine.PollDuration)
	}
	if cfg.Engine.FlushDur != 1*time.Hour {
		t.Errorf("expected 1h flush duration, got %v", cfg.Engine.FlushDur)
	}
	if cfg.Engine.SimilarityThreshold != 0.40 {
		t.Errorf("expected 0.40 similarity threshold, got %v", cfg.Engine.SimilarityThreshold)
	}
	if cfg.Database.Path != "data/celerum.db" {
		t.Errorf("expected data/celerum.db path, got %v", cfg.Database.Path)
	}
}

func TestEnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_TELEGRAM_TOKEN", "12345:token")
	t.Setenv("TEST_CHAT_ID", "987654")

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "celerum.yaml")

	content := `
version: "1"
feeds:
  - url: "https://example.com/rss"
dispatch:
  telegram:
    enabled: true
    bot_token: "${TEST_TELEGRAM_TOKEN}"
    chat_id: "${TEST_CHAT_ID}"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Dispatch.Telegram.BotToken != "12345:token" {
		t.Errorf("expected token expanded, got %s", cfg.Dispatch.Telegram.BotToken)
	}
	if cfg.Dispatch.Telegram.ChatID != "987654" {
		t.Errorf("expected chat id expanded, got %s", cfg.Dispatch.Telegram.ChatID)
	}
}

