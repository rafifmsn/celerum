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

func TestMCPServerArticleCursors(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "mcp_cursor.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	base := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	seed := []model.Article{
		{ID: "a1", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Alpha Bitcoin", URL: "https://ex.com/1", PublishedAt: base.Add(-4 * time.Minute), IngestedAt: base},
		{ID: "a2", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Beta Bitcoin", URL: "https://ex.com/2", PublishedAt: base.Add(-3 * time.Minute), IngestedAt: base},
		{ID: "a3", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Gamma Macro", URL: "https://ex.com/3", PublishedAt: base.Add(-2 * time.Minute), IngestedAt: base},
		// a4 and a5 share identical timestamps to verify deterministic id DESC tie-breaking.
		{ID: "a4", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Delta Bitcoin", URL: "https://ex.com/4", PublishedAt: base.Add(-1 * time.Minute), IngestedAt: base},
		{ID: "a5", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Epsilon Bitcoin", URL: "https://ex.com/5", PublishedAt: base.Add(-1 * time.Minute), IngestedAt: base},
	}
	if _, err := st.UpsertArticles(seed); err != nil {
		t.Fatalf("UpsertArticles seed: %v", err)
	}

	eng := engine.New(config.DefaultConfig(), st)
	srv := New(eng, st, Options{})
	cs := connectInMemory(t, srv)
	ctx := context.Background()

	callArticles := func(tool string, args map[string]any) (ArticlesOutput, *mcp.CallToolResult) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s transport error: %v", tool, err)
		}
		if res.IsError {
			return ArticlesOutput{}, res
		}
		var out ArticlesOutput
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatalf("unmarshal %s output: %v", tool, err)
		}
		return out, res
	}

	// 1. Initial bounded slice (limit=2) returns a5, a4 and both cursors.
	p1, res := callArticles("get_raw_feed", map[string]any{"category": "Crypto", "limit": 2})
	if res.IsError {
		t.Fatalf("page 1 error: %+v", res)
	}
	if len(p1.Articles) != 2 || p1.Articles[0].ID != "a5" || p1.Articles[1].ID != "a4" {
		t.Fatalf("unexpected page 1 articles: %+v", p1.Articles)
	}
	if !p1.HasMore || p1.NextCursor == "" || p1.HeadCursor == "" || p1.TotalCount != 5 || p1.Page != 1 {
		t.Fatalf("unexpected page 1 metadata: %+v", p1)
	}

	// 2. Concurrent events arrive while agent inspects page 1 (one fresh, one backdated).
	if _, err := st.UpsertArticles([]model.Article{
		{ID: "a6-new", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Zeta Bitcoin", URL: "https://ex.com/6", PublishedAt: base.Add(1 * time.Minute), IngestedAt: base.Add(2 * time.Minute)},
		{ID: "a7-backdated", FeedURL: "https://ex.com/rss", FeedName: "ExFeed", Category: "Crypto", Title: "Backdated Bitcoin", URL: "https://ex.com/7", PublishedAt: base.Add(-10 * time.Minute), IngestedAt: base.Add(2 * time.Minute)},
	}); err != nil {
		t.Fatalf("UpsertArticles concurrent: %v", err)
	}

	// 3. Finish historical slice with next_cursor: no duplicates or skips from a6-new; head_cursor omitted.
	p2, res := callArticles("get_raw_feed", map[string]any{"category": "Crypto", "cursor": p1.NextCursor, "limit": 2})
	if res.IsError {
		t.Fatalf("page 2 error: %+v", res)
	}
	if len(p2.Articles) != 2 || p2.Articles[0].ID != "a3" || p2.Articles[1].ID != "a2" {
		t.Fatalf("unexpected page 2 articles: %+v", p2.Articles)
	}
	if !p2.HasMore || p2.NextCursor == "" || p2.HeadCursor != "" || p2.TotalCount != 0 || p2.Page != 0 {
		t.Fatalf("unexpected page 2 metadata: %+v", p2)
	}

	p3, res := callArticles("get_raw_feed", map[string]any{"category": "Crypto", "cursor": p2.NextCursor, "limit": 2})
	if res.IsError {
		t.Fatalf("page 3 error: %+v", res)
	}
	if len(p3.Articles) != 2 || p3.Articles[0].ID != "a1" || p3.Articles[1].ID != "a7-backdated" || p3.HasMore || p3.NextCursor != "" {
		t.Fatalf("unexpected page 3 output: %+v", p3)
	}

	// 4. Pick up newly arrived events using head_cursor from page 1 (limit=1 to test forward paging).
	t1, res := callArticles("get_raw_feed", map[string]any{"category": "Crypto", "after_cursor": p1.HeadCursor, "limit": 1})
	if res.IsError {
		t.Fatalf("tail 1 error: %+v", res)
	}
	if len(t1.Articles) != 1 || t1.Articles[0].ID != "a6-new" || !t1.HasMore || t1.NextCursor != "" || t1.HeadCursor == "" {
		t.Fatalf("unexpected tail 1 output: %+v", t1)
	}

	t2, res := callArticles("get_raw_feed", map[string]any{"category": "Crypto", "after_cursor": t1.HeadCursor, "limit": 5})
	if res.IsError {
		t.Fatalf("tail 2 error: %+v", res)
	}
	if len(t2.Articles) != 1 || t2.Articles[0].ID != "a7-backdated" || t2.HasMore || t2.HeadCursor == "" {
		t.Fatalf("unexpected tail 2 output: %+v", t2)
	}

	// Polling again with no new arrivals echoes head_cursor unchanged.
	t3, res := callArticles("get_raw_feed", map[string]any{"category": "Crypto", "after_cursor": t2.HeadCursor, "limit": 5})
	if res.IsError {
		t.Fatalf("tail 3 error: %+v", res)
	}
	if len(t3.Articles) != 0 || t3.HasMore || t3.HeadCursor != t2.HeadCursor {
		t.Fatalf("unexpected tail 3 output: %+v", t3)
	}

	// 5. search_articles supports cursor + after_cursor identically.
	s1, res := callArticles("search_articles", map[string]any{"query": "Bitcoin", "limit": 2})
	if res.IsError || len(s1.Articles) != 2 || !s1.HasMore || s1.NextCursor == "" || s1.HeadCursor == "" {
		t.Fatalf("unexpected search page 1: %+v res=%+v", s1, res)
	}
	s2, res := callArticles("search_articles", map[string]any{"query": "Bitcoin", "cursor": s1.NextCursor, "limit": 2})
	if res.IsError || len(s2.Articles) != 2 || !s2.HasMore {
		t.Fatalf("unexpected search page 2: %+v res=%+v", s2, res)
	}
	if s2.Articles[0].ID == s1.Articles[0].ID || s2.Articles[0].ID == s1.Articles[1].ID {
		t.Fatalf("duplicate search results across cursor hop")
	}

	// 6. Validation errors: conflicting args, filter mismatch, malformed cursor, wrong cursor kind.
	invalidCases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"both_cursors", "get_raw_feed", map[string]any{"category": "Crypto", "cursor": p1.NextCursor, "after_cursor": p1.HeadCursor}},
		{"page_with_cursor", "get_raw_feed", map[string]any{"category": "Crypto", "cursor": p1.NextCursor, "page": 2}},
		{"filter_mismatch_category", "get_raw_feed", map[string]any{"category": "Macro", "cursor": p1.NextCursor}},
		{"filter_mismatch_query", "search_articles", map[string]any{"query": "Ethereum", "cursor": s1.NextCursor}},
		{"malformed_cursor", "get_raw_feed", map[string]any{"cursor": "not-valid-base64!!!"}},
		{"wrong_cursor_kind_before", "get_raw_feed", map[string]any{"category": "Crypto", "cursor": p1.HeadCursor}},
		{"wrong_cursor_kind_after", "get_raw_feed", map[string]any{"category": "Crypto", "after_cursor": p1.NextCursor}},
	}
	for _, tc := range invalidCases {
		_, errRes := callArticles(tc.tool, tc.args)
		if !errRes.IsError {
			t.Fatalf("case %s: expected tool error, got success", tc.name)
		}
	}
}
