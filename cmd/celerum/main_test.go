package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

func TestMCPStdioSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "celerum-test-bin")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}

	dbPath := filepath.Join(tmpDir, "stdio_mcp.db")
	cfgPath := filepath.Join(tmpDir, "celerum.yaml")
	cfgContent := `database:
  path: "` + dbPath + `"
feeds:
  - name: "CoinDesk"
    url: "https://coindesk.com/rss"
    tier: 1
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Verify read-write stdio mode creates/seeds DB and serves all 5 tools cleanly over stdout.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{
		Command: exec.Command(binPath, "mcp", "-c", cfgPath),
	}, nil)
	if err != nil {
		t.Fatalf("stdio CommandTransport connect (rw): %v", err)
	}
	toolsRW, err := cs.ListTools(ctx, nil)
	if err != nil {
		_ = cs.Close()
		t.Fatalf("ListTools (rw): %v", err)
	}
	if len(toolsRW.Tools) != 5 {
		_ = cs.Close()
		t.Fatalf("expected 5 tools in rw stdio mode, got %d", len(toolsRW.Tools))
	}
	_ = cs.Close()

	// Verify --read-only stdio mode opens existing DB read-only and omits manage_sources.
	csRO, err := client.Connect(ctx, &mcp.CommandTransport{
		Command: exec.Command(binPath, "mcp", "-c", cfgPath, "--read-only"),
	}, nil)
	if err != nil {
		t.Fatalf("stdio CommandTransport connect (ro): %v", err)
	}
	defer csRO.Close()

	toolsRO, err := csRO.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools (ro): %v", err)
	}
	if len(toolsRO.Tools) != 4 {
		t.Fatalf("expected 4 tools in --read-only stdio mode, got %d", len(toolsRO.Tools))
	}
}

func TestResolveStoreInputs_AnchorsRelativeDBAndRespectsOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "nested")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfgPath := filepath.Join(subDir, "celerum.yaml")
	cfgContent := "database:\n  path: \"data/celerum.db\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, gotDB, err := resolveStoreInputs(storeInputOptions{
		configPath:      cfgPath,
		configExplicit:  true,
		requireExisting: true,
	})
	if err != nil {
		t.Fatalf("resolveStoreInputs with config: %v", err)
	}
	wantDB := filepath.Join(subDir, "data", "celerum.db")
	if gotDB != wantDB {
		t.Fatalf("expected config-relative db %s, got %s", wantDB, gotDB)
	}

	envDB := filepath.Join(tmpDir, "env.db")
	t.Setenv("CELERUM_DB", envDB)
	_, gotEnvDB, err := resolveStoreInputs(storeInputOptions{
		configPath:      cfgPath,
		configExplicit:  true,
		requireExisting: true,
	})
	if err != nil {
		t.Fatalf("resolveStoreInputs with CELERUM_DB: %v", err)
	}
	if gotEnvDB != envDB {
		t.Fatalf("expected CELERUM_DB %s, got %s", envDB, gotEnvDB)
	}

	flagDB := filepath.Join(tmpDir, "flag.db")
	_, gotFlagDB, err := resolveStoreInputs(storeInputOptions{
		configPath:      cfgPath,
		configExplicit:  true,
		dbFlag:          flagDB,
		requireExisting: true,
	})
	if err != nil {
		t.Fatalf("resolveStoreInputs with --db: %v", err)
	}
	if gotFlagDB != flagDB {
		t.Fatalf("expected --db %s, got %s", flagDB, gotFlagDB)
	}
}

func TestResolveStoreInputs_ExplicitMissingConfigErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if _, _, err := resolveStoreInputs(storeInputOptions{
		configPath:     missing,
		configExplicit: true,
	}); err == nil {
		t.Fatal("expected error for explicit missing config")
	}
}

func TestMCPStdio_FailsFastInEmptyDirectoryWithoutCreatingDB(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "celerum-test-bin")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}

	emptyCwd := filepath.Join(tmpDir, "random-agent-cwd")
	if err := os.MkdirAll(emptyCwd, 0755); err != nil {
		t.Fatalf("mkdir emptyCwd: %v", err)
	}

	cmd := exec.Command(binPath, "mcp")
	cmd.Dir = emptyCwd
	cmd.Env = append(os.Environ(), "CELERUM_DB=")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected celerum mcp in empty directory to exit non-zero")
	}

	if _, err := os.Stat(filepath.Join(emptyCwd, "data")); !os.IsNotExist(err) {
		t.Fatalf("expected no data/ directory created in %s, stat err=%v", emptyCwd, err)
	}
}
