package main

import (
	"os"
	"path/filepath"
	"testing"

	"celerum/internal/store"
)

func TestImportConfigFile_OverwritesExistingSQLiteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "celerum.db")

	initialYAML := filepath.Join(tmpDir, "initial.yaml")
	initialContent := `database:
  path: "` + dbPath + `"
engine:
  top_k: 3
  breaking_threshold: 8.0
keywords:
  boost: ["bitcoin"]
feeds:
  - name: "Initial Feed"
    url: "https://example.com/rss1.xml"
    tier: 1
`
	if err := os.WriteFile(initialYAML, []byte(initialContent), 0644); err != nil {
		t.Fatalf("write initial yaml: %v", err)
	}

	count, gotDB, err := importConfigFile(initialYAML)
	if err != nil {
		t.Fatalf("initial import failed: %v", err)
	}
	if count != 1 || gotDB != dbPath {
		t.Fatalf("unexpected initial result: count=%d db=%s", count, gotDB)
	}

	updatedYAML := filepath.Join(tmpDir, "updated.yaml")
	updatedContent := `database:
  path: "` + dbPath + `"
engine:
  top_k: 7
  breaking_threshold: 9.0
keywords:
  boost: ["ethereum", "solana"]
feeds:
  - name: "Feed A"
    url: "https://example.com/a.xml"
    tier: 1
  - name: "Feed B"
    url: "https://example.com/b.xml"
    tier: 2
`
	if err := os.WriteFile(updatedYAML, []byte(updatedContent), 0644); err != nil {
		t.Fatalf("write updated yaml: %v", err)
	}

	count, _, err = importConfigFile(updatedYAML)
	if err != nil {
		t.Fatalf("updated import failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 imported feeds, got %d", count)
	}

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()

	cfg, err := st.ExportToConfig()
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if cfg.Engine.TopK != 7 {
		t.Fatalf("expected engine.top_k=7, got %d", cfg.Engine.TopK)
	}
	if len(cfg.Keywords.Boost) != 2 || cfg.Keywords.Boost[0] != "ethereum" {
		t.Fatalf("expected updated boost keywords, got %v", cfg.Keywords.Boost)
	}
	if len(cfg.Feeds) != 2 || cfg.Feeds[0].Name != "Feed A" || cfg.Feeds[1].Name != "Feed B" {
		t.Fatalf("expected replaced feeds, got %+v", cfg.Feeds)
	}
}

func TestImportConfigFile_MissingFileReturnsError(t *testing.T) {
	if _, _, err := importConfigFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected error for missing yaml file")
	}
}
