package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"celerum/internal/config"
	"celerum/internal/engine"
	"celerum/internal/store"
	"celerum/pkg/model"
)

func setupTestServer(t *testing.T) (*Server, *store.Store, *engine.Engine) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "server_test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seed := &config.Config{
		Version: "1",
		Engine: config.EngineConfig{
			PollInterval:        "5m",
			WindowDuration:      "3h",
			FlushInterval:       "1h",
			MaxArticlesPerFeed:  20,
			SimilarityThreshold: 0.25,
			BreakingThreshold:   12.0,
			TopK:                3,
		},
		Feeds: []config.FeedConfig{
			{Name: "CoinDesk", URL: "https://coindesk.com/rss", Category: "Crypto", Tier: 1},
		},
		Keywords: config.KeywordsConfig{Boost: []string{"etf", "sec"}},
	}
	cfg, err := st.BootstrapConfig(seed)
	if err != nil {
		t.Fatalf("BootstrapConfig: %v", err)
	}

	eng := engine.New(cfg, st)
	srv := New(eng, st)
	return srv, st, eng
}

func TestServerAPIRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	h := srv.Handler()

	// Seed an article
	now := time.Now().UTC()
	_, err := st.UpsertArticles([]model.Article{{
		ID:          "art-1",
		FeedURL:     "https://coindesk.com/rss",
		FeedName:    "CoinDesk",
		FeedTier:    1,
		Category:    "Crypto",
		Title:       "SEC approves spot Bitcoin ETF in-kind creations",
		Description: "Authorized participants may settle in BTC directly.",
		URL:         "https://coindesk.com/etf",
		ImageURL:    "https://coindesk.com/etf.jpg",
		PublishedAt: now,
		IngestedAt:  now,
	}})
	if err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}

	// 1. Overview
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/overview expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var ov map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("invalid overview JSON: %v", err)
	}
	if ov["raw_fetched_count"].(float64) != 1 {
		t.Errorf("expected raw_fetched_count=1, got %v", ov["raw_fetched_count"])
	}

	// 2. Articles
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/articles?category=Crypto", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/articles expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SEC approves spot Bitcoin ETF") {
		t.Errorf("expected seeded article in /api/articles response")
	}

	// 3. Sources CRUD
	createBody := `{"name":"Reuters","url":"https://reuters.com/rss","category":"Macro","tier":1,"enabled":true}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/sources", strings.NewReader(createBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/sources expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Settings get & strict validation
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var settingsMap map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &settingsMap); err != nil {
		t.Fatalf("GET /api/settings invalid JSON: %v", err)
	}
	if _, ok := settingsMap["engine"]["poll_interval"]; !ok {
		t.Fatalf("expected snake_case poll_interval in GET /api/settings, got keys: %#v", settingsMap["engine"])
	}
	if _, ok := settingsMap["keywords"]["boost"]; !ok {
		t.Fatalf("expected snake_case boost array in GET /api/settings, got: %#v", settingsMap["keywords"])
	}

	badSettings := `{"engine":{"poll_interval":"5m","window_duration":"3h","flush_interval":"1h"},"enrichment":{"scraper":"jina","jina_api_key":""}}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(badSettings)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings expected 400 when jina missing key, got %d", rec.Code)
	}

	// 5. YAML export & import
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/yaml", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://reuters.com/rss") {
		t.Fatalf("GET /api/config/yaml unexpected: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/yaml", bytes.NewReader(rec.Body.Bytes())))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config/yaml expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// 6. Dispatch, Doctor preview & SPA root
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/doctor", strings.NewReader(`{"send":false,"settings":{"engine":{"poll_interval":"5m","window_duration":"24h","flush_interval":"1h"},"enrichment":{"scraper":"jina","jina_api_key":""},"llm":{"enabled":false},"dispatch":{}}}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "jina_api_key") {
		t.Fatalf("POST /api/doctor preview failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dispatch?page=2&limit=10", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"history_total"`) || !strings.Contains(rec.Body.String(), `"history_page":2`) {
		t.Fatalf("GET /api/dispatch expected paginated history metadata, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Celerum") {
		t.Fatalf("GET / SPA root failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/intelligence", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "html") {
		t.Fatalf("GET /intelligence SPA fallback failed: %d", rec.Code)
	}
}

func TestServerMCPEndpoint(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	now := time.Now().UTC()
	if _, err := st.UpsertArticles([]model.Article{{
		ID:          "mcp-art-1",
		FeedURL:     "https://coindesk.com/rss",
		FeedName:    "CoinDesk",
		FeedTier:    1,
		Category:    "Crypto",
		Title:       "MCP streamable test article",
		URL:         "https://coindesk.com/mcp-test",
		PublishedAt: now,
		IngestedAt:  now,
	}}); err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Streamable HTTP connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 5 {
		t.Fatalf("expected 5 MCP tools on /mcp, got %d", len(tools.Tools))
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_raw_feed",
		Arguments: map[string]any{"category": "Crypto"},
	})
	if err != nil || res.IsError {
		t.Fatalf("get_raw_feed over /mcp failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "MCP streamable test article") {
		t.Fatalf("expected seeded article in /mcp get_raw_feed response")
	}
}

func TestServerMCPReadOnlyOption(t *testing.T) {
	_, st, eng := setupTestServer(t)
	roSrv := NewWithOptions(eng, st, Options{MCPReadOnly: true})
	ts := httptest.NewServer(roSrv.Handler())
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "http-ro-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Streamable HTTP connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "manage_sources" {
			t.Fatalf("expected manage_sources disabled when MCPReadOnly=true")
		}
	}
	if len(tools.Tools) != 4 {
		t.Fatalf("expected 4 tools with MCPReadOnly=true, got %d", len(tools.Tools))
	}
}
