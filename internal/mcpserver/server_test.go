package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"celerum/internal/config"
	"celerum/internal/doctor"
	"celerum/internal/engine"
	"celerum/internal/store"
	"celerum/pkg/model"
)

func connectInMemory(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	tServer, tClient := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, tServer, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, tClient, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func seedTestStoreAndEngine(t *testing.T, feedURL string) (*store.Store, *engine.Engine, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "mcp_test.db")
	st, err := store.New(dbPath)
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
			TopK:                5,
		},
		Feeds: []config.FeedConfig{
			{Name: "CoinDesk", URL: feedURL, Category: "Crypto", Tier: 1},
		},
		Keywords: config.KeywordsConfig{Boost: []string{"etf", "fed"}},
	}
	cfg, err := st.BootstrapConfig(seed)
	if err != nil {
		t.Fatalf("BootstrapConfig: %v", err)
	}

	now := time.Now().UTC()
	if _, err := st.UpsertArticles([]model.Article{
		{
			ID:          "art-1",
			FeedURL:     feedURL,
			FeedName:    "CoinDesk",
			FeedTier:    1,
			Category:    "Crypto",
			Title:       "SEC approves spot Bitcoin ETF",
			Description: "Major milestone for institutional BTC access.",
			URL:         "https://coindesk.com/etf",
			PublishedAt: now.Add(-5 * time.Minute),
			IngestedAt:  now,
		},
		{
			ID:          "art-2",
			FeedURL:     feedURL,
			FeedName:    "CoinDesk",
			FeedTier:    2,
			Category:    "Macro",
			Title:       "Federal Reserve holds rates steady",
			Description: "FOMC keeps target range unchanged.",
			URL:         "https://coindesk.com/fed",
			PublishedAt: now.Add(-2 * time.Minute),
			IngestedAt:  now,
		},
	}); err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}

	if err := st.UpsertActiveClusters([]store.ClusterRecord{
		{
			ClusterHash:      "c-breaking",
			RepresentativeID: "art-1",
			Title:            "SEC approves spot Bitcoin ETF",
			Summary:          "Spot ETF greenlit.",
			Score:            15.5,
			Tier:             1,
			SourceCount:      2,
			Category:         "Crypto",
			LeadSource:       "CoinDesk",
			Sources: []model.SourceInfo{
				{Name: "CoinDesk", URL: "https://coindesk.com/etf", Title: "SEC approves spot Bitcoin ETF"},
			},
			Status:    "active",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ClusterHash:      "c-normal",
			RepresentativeID: "art-2",
			Title:            "Federal Reserve holds rates steady",
			Score:            9.0,
			Tier:             2,
			SourceCount:      1,
			Category:         "Macro",
			LeadSource:       "CoinDesk",
			Sources: []model.SourceInfo{
				{Name: "CoinDesk", URL: "https://coindesk.com/fed", Title: "Federal Reserve holds rates steady"},
			},
			Status:    "active",
			CreatedAt: now.Add(-time.Minute),
			UpdatedAt: now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpsertActiveClusters: %v", err)
	}

	eng := engine.New(cfg, st)
	return st, eng, dbPath
}

func TestMCPServerToolsAndResources(t *testing.T) {
	rssSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>Test Headline</title><link>https://example.com/1</link><description>Body</description></item></channel></rss>`)
	}))
	defer rssSrv.Close()

	st, eng, _ := seedTestStoreAndEngine(t, rssSrv.URL)
	srv := New(eng, st, Options{
		DoctorOpts: &doctor.Options{
			Send:       true, // Must be overridden to false internally.
			FeedClient: rssSrv.Client(),
		},
	})
	cs := connectInMemory(t, srv)
	ctx := context.Background()

	toolsRes, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	toolNames := map[string]bool{}
	for _, tl := range toolsRes.Tools {
		toolNames[tl.Name] = true
	}
	for _, want := range []string{"get_top_clusters", "get_raw_feed", "search_articles", "manage_sources", "run_doctor"} {
		if !toolNames[want] {
			t.Fatalf("expected tool %q in ListTools, got %v", want, toolNames)
		}
	}

	resList, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resList.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resList.Resources))
	}

	// 1. Read resources
	feedsRes, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "celerum://feeds"})
	if err != nil {
		t.Fatalf("ReadResource feeds: %v", err)
	}
	if len(feedsRes.Contents) != 1 || !strings.Contains(feedsRes.Contents[0].Text, "CoinDesk") {
		t.Fatalf("unexpected celerum://feeds contents: %+v", feedsRes.Contents)
	}

	clustersRes, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "celerum://clusters/latest"})
	if err != nil {
		t.Fatalf("ReadResource clusters: %v", err)
	}
	if len(clustersRes.Contents) != 1 || !strings.Contains(clustersRes.Contents[0].Text, "SEC approves spot Bitcoin ETF") {
		t.Fatalf("unexpected celerum://clusters/latest contents: %+v", clustersRes.Contents)
	}

	// 2. get_top_clusters with filter and clamping
	cCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_top_clusters",
		Arguments: map[string]any{"filter": "breaking", "limit": 500},
	})
	if err != nil || cCall.IsError {
		t.Fatalf("get_top_clusters failed: err=%v res=%+v", err, cCall)
	}
	var cOut ClustersOutput
	if err := json.Unmarshal([]byte(cCall.Content[0].(*mcp.TextContent).Text), &cOut); err != nil {
		t.Fatalf("unmarshal clusters output: %v", err)
	}
	if cOut.ReturnedCount != 1 || cOut.Clusters[0].ClusterHash != "c-breaking" {
		t.Fatalf("expected only breaking cluster, got %+v", cOut)
	}

	// Invalid filter yields tool error
	badFilter, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_top_clusters",
		Arguments: map[string]any{"filter": "invalid"},
	})
	if err != nil || !badFilter.IsError {
		t.Fatalf("expected tool error for invalid filter, got err=%v isError=%v", err, badFilter.IsError)
	}

	// 3. get_raw_feed with category filter and limit clamp
	rawCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_raw_feed",
		Arguments: map[string]any{"category": "Macro", "limit": 500},
	})
	if err != nil || rawCall.IsError {
		t.Fatalf("get_raw_feed failed: err=%v res=%+v", err, rawCall)
	}
	var rawOut ArticlesOutput
	if err := json.Unmarshal([]byte(rawCall.Content[0].(*mcp.TextContent).Text), &rawOut); err != nil {
		t.Fatalf("unmarshal raw output: %v", err)
	}
	if rawOut.TotalCount != 1 || rawOut.Limit != maxPageLimit || rawOut.Articles[0].ID != "art-2" {
		t.Fatalf("unexpected get_raw_feed output: %+v", rawOut)
	}

	// 4. search_articles
	searchCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_articles",
		Arguments: map[string]any{"query": "Bitcoin"},
	})
	if err != nil || searchCall.IsError {
		t.Fatalf("search_articles failed: err=%v res=%+v", err, searchCall)
	}
	var searchOut ArticlesOutput
	if err := json.Unmarshal([]byte(searchCall.Content[0].(*mcp.TextContent).Text), &searchOut); err != nil {
		t.Fatalf("unmarshal search output: %v", err)
	}
	if searchOut.TotalCount != 1 || searchOut.Articles[0].ID != "art-1" {
		t.Fatalf("unexpected search_articles output: %+v", searchOut)
	}

	emptySearch, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_articles",
		Arguments: map[string]any{"query": "   "},
	})
	if err != nil || !emptySearch.IsError {
		t.Fatalf("expected tool error for empty search query")
	}

	// 5. manage_sources CRUD + URL guard + config_revision bump
	badURLCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "manage_sources",
		Arguments: map[string]any{"action": "create", "name": "Bad", "url": "file:///etc/passwd"},
	})
	if err != nil || !badURLCall.IsError {
		t.Fatalf("expected tool error for file:// URL")
	}

	createCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "manage_sources",
		Arguments: map[string]any{
			"action":   "create",
			"name":     "Reuters",
			"url":      "https://reuters.com/rss",
			"category": "Macro",
			"tier":     1,
		},
	})
	if err != nil || createCall.IsError {
		t.Fatalf("manage_sources create failed: err=%v res=%+v", err, createCall)
	}
	var createOut SourcesOutput
	if err := json.Unmarshal([]byte(createCall.Content[0].(*mcp.TextContent).Text), &createOut); err != nil {
		t.Fatalf("unmarshal create output: %v", err)
	}
	if createOut.Source == nil || createOut.Source.ID <= 0 || !createOut.Source.Enabled {
		t.Fatalf("unexpected created source: %+v", createOut)
	}

	// Partial update toggling enabled=false
	updCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "manage_sources",
		Arguments: map[string]any{"action": "update", "id": createOut.Source.ID, "enabled": false},
	})
	if err != nil || updCall.IsError {
		t.Fatalf("manage_sources update failed: err=%v res=%+v", err, updCall)
	}
	var updOut SourcesOutput
	if err := json.Unmarshal([]byte(updCall.Content[0].(*mcp.TextContent).Text), &updOut); err != nil {
		t.Fatalf("unmarshal update output: %v", err)
	}
	if updOut.Source.Enabled || updOut.Source.Name != "Reuters" {
		t.Fatalf("expected partial update to preserve name and disable source, got %+v", updOut.Source)
	}

	delCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "manage_sources",
		Arguments: map[string]any{"action": "delete", "id": createOut.Source.ID},
	})
	if err != nil || delCall.IsError {
		t.Fatalf("manage_sources delete failed: err=%v res=%+v", err, delCall)
	}

	// 6. run_doctor (non-destructive)
	docCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "run_doctor",
		Arguments: map[string]any{},
	})
	if err != nil || docCall.IsError {
		t.Fatalf("run_doctor failed: err=%v res=%+v", err, docCall)
	}
	var docOut doctor.Report
	if err := json.Unmarshal([]byte(docCall.Content[0].(*mcp.TextContent).Text), &docOut); err != nil {
		t.Fatalf("unmarshal doctor output: %v", err)
	}
	if len(docOut.Stages) != 6 {
		t.Fatalf("expected 6 doctor stages, got %d", len(docOut.Stages))
	}
	if strings.Contains(docOut.Stages[5].Summary, "live verification alert") {
		t.Fatalf("expected run_doctor to force Send=false, got stage 6 summary: %s", docOut.Stages[5].Summary)
	}
}

func TestMCPServerReadOnlyMode(t *testing.T) {
	_, _, dbPath := seedTestStoreAndEngine(t, "https://example.com/rss")

	roStore, err := store.NewReadOnly(dbPath)
	if err != nil {
		t.Fatalf("store.NewReadOnly: %v", err)
	}
	defer roStore.Close()

	cfg, err := roStore.ExportToConfig()
	if err != nil {
		t.Fatalf("ExportToConfig: %v", err)
	}
	eng := engine.New(cfg, roStore)
	srv := New(eng, roStore, Options{ReadOnly: true})
	cs := connectInMemory(t, srv)
	ctx := context.Background()

	toolsRes, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range toolsRes.Tools {
		if tl.Name == "manage_sources" {
			t.Fatalf("manage_sources must not be registered in ReadOnly mode")
		}
	}
	if len(toolsRes.Tools) != 4 {
		t.Fatalf("expected 4 tools in ReadOnly mode, got %d", len(toolsRes.Tools))
	}

	rawCall, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_raw_feed",
		Arguments: map[string]any{},
	})
	if err != nil || rawCall.IsError {
		t.Fatalf("get_raw_feed in ReadOnly mode failed: err=%v res=%+v", err, rawCall)
	}
}
