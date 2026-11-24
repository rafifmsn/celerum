package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"celerum/internal/config"
	"celerum/internal/doctor"
	"celerum/internal/engine"
	"celerum/internal/store"
	"celerum/pkg/model"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

// Options configures MCP server capabilities and testing overrides.
type Options struct {
	ReadOnly   bool
	DoctorOpts *doctor.Options
}

// GetClustersInput defines query parameters for get_top_clusters.
type GetClustersInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Cluster filter: all, breaking, or tier1"`
	Tier   string `json:"tier,omitempty" jsonschema:"Source tier filter: all, 1, 2, or 3"`
	Sort   string `json:"sort,omitempty" jsonschema:"Sort order: score or recent"`
	Query  string `json:"query,omitempty" jsonschema:"Optional case-insensitive text filter"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum clusters to return (default 20, max 100)"`
}

// ClustersOutput is the structured response for get_top_clusters and celerum://clusters/latest.
type ClustersOutput struct {
	Clusters            []store.ClusterRecord      `json:"clusters"`
	TotalWindowClusters int                        `json:"total_window_clusters"`
	ReturnedCount       int                        `json:"returned_count"`
	BreakingCount       int                        `json:"breaking_count"`
	HeartbeatCount      int                        `json:"heartbeat_count"`
	BelowThresholdCount int                        `json:"below_threshold_count"`
	TierCounts          map[string]int             `json:"tier_counts"`
	BoostKeywords       []engine.KeywordMatchCount `json:"boost_keywords"`
	BreakingThreshold   float64                    `json:"breaking_threshold"`
	WindowDuration      string                     `json:"window_duration"`
}

// GetRawFeedInput defines pagination and filter parameters for get_raw_feed.
type GetRawFeedInput struct {
	Category string `json:"category,omitempty" jsonschema:"Optional category filter"`
	Source   string `json:"source,omitempty" jsonschema:"Optional feed name or feed URL filter"`
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number (default 1)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Page size (default 20, max 100)"`
}

// SearchArticlesInput defines keyword search parameters for search_articles.
type SearchArticlesInput struct {
	Query    string `json:"query" jsonschema:"Required search query matched against title, description, or source name"`
	Category string `json:"category,omitempty" jsonschema:"Optional category filter"`
	Source   string `json:"source,omitempty" jsonschema:"Optional feed name or feed URL filter"`
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number (default 1)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Page size (default 20, max 100)"`
}

// ArticlesOutput is the structured response for article listing and search tools.
type ArticlesOutput struct {
	Articles   []model.Article `json:"articles"`
	TotalCount int             `json:"total_count"`
	Page       int             `json:"page"`
	Limit      int             `json:"limit"`
	Categories []string        `json:"categories"`
	Sources    []string        `json:"sources"`
}

// ManageSourcesInput defines scoped CRUD operations for manage_sources.
type ManageSourcesInput struct {
	Action   string `json:"action" jsonschema:"Operation to perform: list, create, update, or delete"`
	ID       int64  `json:"id,omitempty" jsonschema:"Source ID (required for update and delete)"`
	Name     string `json:"name,omitempty" jsonschema:"Display name of the feed source"`
	URL      string `json:"url,omitempty" jsonschema:"HTTP/HTTPS URL of the RSS or Atom feed"`
	Category string `json:"category,omitempty" jsonschema:"Feed category (default General)"`
	Tier     int    `json:"tier,omitempty" jsonschema:"Authority tier (1=highest, default 1)"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"Whether polling is enabled for this source"`
	Search   string `json:"search,omitempty" jsonschema:"Optional filter string when action is list"`
}

// SourcesOutput is the structured response for manage_sources.
type SourcesOutput struct {
	Action     string               `json:"action"`
	Sources    []store.SourceRecord `json:"sources,omitempty"`
	Source     *store.SourceRecord  `json:"source,omitempty"`
	Deleted    bool                 `json:"deleted,omitempty"`
	TotalCount int                  `json:"total_count"`
}

// RunDoctorInput defines the empty input object for run_doctor.
type RunDoctorInput struct{}

// New builds an MCP server exposing Celerum tools and resources backed by Engine and Store.
func New(eng *engine.Engine, st *store.Store, opts Options) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "celerum",
		Title:   "Celerum RSS Intelligence",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_top_clusters",
		Title:       "Get Top Clusters",
		Description: "Return ranked news clusters in the active sliding window, filterable by breaking status, tier, sort order, and search text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetClustersInput) (*mcp.CallToolResult, ClustersOutput, error) {
		out, err := buildClustersOutput(eng, in)
		if err != nil {
			return nil, ClustersOutput{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_raw_feed",
		Title:       "Get Raw Feed",
		Description: "Browse persisted raw RSS/Atom articles ordered by recency with optional category and source filters.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetRawFeedInput) (*mcp.CallToolResult, ArticlesOutput, error) {
		out, err := queryArticlesOutput(st, in.Category, in.Source, "", in.Page, in.Limit)
		if err != nil {
			return nil, ArticlesOutput{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_articles",
		Title:       "Search Articles",
		Description: "Search persisted articles by keyword across title, description, and feed name with optional category/source filters.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchArticlesInput) (*mcp.CallToolResult, ArticlesOutput, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, ArticlesOutput{}, errors.New("query is required")
		}
		out, err := queryArticlesOutput(st, in.Category, in.Source, in.Query, in.Page, in.Limit)
		if err != nil {
			return nil, ArticlesOutput{}, err
		}
		return nil, out, nil
	})

	if !opts.ReadOnly {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "manage_sources",
			Title:       "Manage Sources",
			Description: "List, create, update, or delete persisted RSS/Atom feed sources and reload engine schedules.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageSourcesInput) (*mcp.CallToolResult, SourcesOutput, error) {
			out, err := handleManageSources(eng, st, in)
			if err != nil {
				return nil, SourcesOutput{}, err
			}
			return nil, out, nil
		})
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_doctor",
		Title:       "Run System Doctor",
		Description: "Run non-destructive 6-stage Celerum pipeline diagnostics without sending live alerts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ RunDoctorInput) (*mcp.CallToolResult, doctor.Report, error) {
		rep, err := runReadOnlyDoctor(ctx, st, opts.DoctorOpts)
		if err != nil {
			return nil, doctor.Report{}, err
		}
		return nil, rep, nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         "celerum://feeds",
		Name:        "feeds",
		Title:       "Configured RSS/Atom Feeds",
		Description: "Persisted feed sources with conditional ETag/Last-Modified and polling telemetry.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if st == nil {
			return nil, errors.New("store is unavailable")
		}
		sources, err := st.ListSources("")
		if err != nil {
			return nil, err
		}
		if sources == nil {
			sources = []store.SourceRecord{}
		}
		return jsonResourceResult(req.Params.URI, map[string]any{
			"sources":     sources,
			"total_count": len(sources),
		})
	})

	srv.AddResource(&mcp.Resource{
		URI:         "celerum://clusters/latest",
		Name:        "latest_clusters",
		Title:       "Latest Ranked Clusters",
		Description: "Latest ranked clusters in the active sliding window (capped at 100 entries).",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		out, err := buildClustersOutput(eng, GetClustersInput{Limit: maxPageLimit})
		if err != nil {
			return nil, err
		}
		return jsonResourceResult(req.Params.URI, out)
	})

	return srv
}

// NewHTTPHandler returns a Streamable HTTP handler ready to mount on /mcp.
func NewHTTPHandler(eng *engine.Engine, st *store.Store, opts Options) http.Handler {
	srv := New(eng, st, opts)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: 30 * time.Minute,
	})
}

func buildClustersOutput(eng *engine.Engine, in GetClustersInput) (ClustersOutput, error) {
	if eng == nil {
		return ClustersOutput{}, errors.New("engine is unavailable")
	}

	filter := strings.ToLower(strings.TrimSpace(in.Filter))
	if filter == "" {
		filter = "all"
	}
	switch filter {
	case "all", "breaking", "tier1":
	default:
		return ClustersOutput{}, fmt.Errorf("invalid filter %q: expected all, breaking, or tier1", in.Filter)
	}

	tier := strings.ToLower(strings.TrimSpace(in.Tier))
	if tier == "" {
		tier = "all"
	}
	switch tier {
	case "all", "1", "2", "3":
	default:
		return ClustersOutput{}, fmt.Errorf("invalid tier %q: expected all, 1, 2, or 3", in.Tier)
	}

	sortBy := strings.ToLower(strings.TrimSpace(in.Sort))
	if sortBy == "" {
		sortBy = "score"
	}
	switch sortBy {
	case "score", "recent":
	default:
		return ClustersOutput{}, fmt.Errorf("invalid sort %q: expected score or recent", in.Sort)
	}

	resp, err := eng.GetClustersView(filter, tier, sortBy, in.Query)
	if err != nil {
		return ClustersOutput{}, err
	}

	limit := clampLimit(in.Limit, defaultPageLimit, maxPageLimit)
	clusters := resp.Clusters
	if clusters == nil {
		clusters = []store.ClusterRecord{}
	}
	if len(clusters) > limit {
		clusters = clusters[:limit]
	}

	boostKws := resp.BoostKeywords
	if boostKws == nil {
		boostKws = []engine.KeywordMatchCount{}
	}
	tierCounts := resp.TierCounts
	if tierCounts == nil {
		tierCounts = map[string]int{}
	}

	return ClustersOutput{
		Clusters:            clusters,
		TotalWindowClusters: resp.TotalWindowClusters,
		ReturnedCount:       len(clusters),
		BreakingCount:       resp.BreakingCount,
		HeartbeatCount:      resp.HeartbeatCount,
		BelowThresholdCount: resp.BelowThresholdCount,
		TierCounts:          tierCounts,
		BoostKeywords:       boostKws,
		BreakingThreshold:   resp.BreakingThreshold,
		WindowDuration:      resp.WindowDuration,
	}, nil
}

func queryArticlesOutput(st *store.Store, category, source, search string, page, limit int) (ArticlesOutput, error) {
	if st == nil {
		return ArticlesOutput{}, errors.New("store is unavailable")
	}
	if page <= 0 {
		page = 1
	}
	clampedLimit := clampLimit(limit, defaultPageLimit, maxPageLimit)

	items, total, categories, sources, err := st.QueryArticles(category, source, search, page, clampedLimit)
	if err != nil {
		return ArticlesOutput{}, err
	}
	if items == nil {
		items = []model.Article{}
	}
	if categories == nil {
		categories = []string{}
	}
	if sources == nil {
		sources = []string{}
	}

	return ArticlesOutput{
		Articles:   items,
		TotalCount: total,
		Page:       page,
		Limit:      clampedLimit,
		Categories: categories,
		Sources:    sources,
	}, nil
}

func handleManageSources(eng *engine.Engine, st *store.Store, in ManageSourcesInput) (SourcesOutput, error) {
	if st == nil {
		return SourcesOutput{}, errors.New("store is unavailable")
	}
	if st.IsReadOnly() {
		return SourcesOutput{}, errors.New("store is opened in read-only mode")
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "list":
		sources, err := st.ListSources(in.Search)
		if err != nil {
			return SourcesOutput{}, err
		}
		if sources == nil {
			sources = []store.SourceRecord{}
		}
		return SourcesOutput{
			Action:     "list",
			Sources:    sources,
			TotalCount: len(sources),
		}, nil

	case "create":
		if err := validateFeedURL(in.URL); err != nil {
			return SourcesOutput{}, err
		}
		enabled := true
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		created, err := st.CreateSource(store.SourceRecord{
			Name:     strings.TrimSpace(in.Name),
			URL:      strings.TrimSpace(in.URL),
			Category: strings.TrimSpace(in.Category),
			Tier:     in.Tier,
			Enabled:  enabled,
		})
		if err != nil {
			return SourcesOutput{}, err
		}
		reloadEngine(eng, st)
		return SourcesOutput{
			Action:     "create",
			Source:     &created,
			TotalCount: 1,
		}, nil

	case "update":
		if in.ID <= 0 {
			return SourcesOutput{}, errors.New("id must be a positive integer")
		}
		existing, err := findSourceByID(st, in.ID)
		if err != nil {
			return SourcesOutput{}, err
		}

		merged := existing
		if name := strings.TrimSpace(in.Name); name != "" {
			merged.Name = name
		}
		if rawURL := strings.TrimSpace(in.URL); rawURL != "" {
			if err := validateFeedURL(rawURL); err != nil {
				return SourcesOutput{}, err
			}
			merged.URL = rawURL
		}
		if cat := strings.TrimSpace(in.Category); cat != "" {
			merged.Category = cat
		}
		if in.Tier > 0 {
			merged.Tier = in.Tier
		}
		if in.Enabled != nil {
			merged.Enabled = *in.Enabled
		}

		updated, err := st.UpdateSource(merged)
		if err != nil {
			return SourcesOutput{}, err
		}
		reloadEngine(eng, st)
		return SourcesOutput{
			Action:     "update",
			Source:     &updated,
			TotalCount: 1,
		}, nil

	case "delete":
		if in.ID <= 0 {
			return SourcesOutput{}, errors.New("id must be a positive integer")
		}
		if err := st.DeleteSource(in.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return SourcesOutput{}, fmt.Errorf("source %d not found", in.ID)
			}
			return SourcesOutput{}, err
		}
		reloadEngine(eng, st)
		return SourcesOutput{
			Action:  "delete",
			Deleted: true,
		}, nil

	default:
		return SourcesOutput{}, fmt.Errorf("unsupported action %q: expected list, create, update, or delete", in.Action)
	}
}

func findSourceByID(st *store.Store, id int64) (store.SourceRecord, error) {
	all, err := st.ListSources("")
	if err != nil {
		return store.SourceRecord{}, err
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return store.SourceRecord{}, fmt.Errorf("source %d not found", id)
}

func validateFeedURL(raw string) error {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return errors.New("url is required")
	}
	parsed, err := url.Parse(clean)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Host == "" {
		return errors.New("url must be a valid http or https URL")
	}
	return nil
}

func reloadEngine(eng *engine.Engine, st *store.Store) {
	if eng == nil || st == nil {
		return
	}
	if cfg, err := st.ExportToConfig(); err == nil {
		eng.ReloadConfig(cfg)
	}
}

func runReadOnlyDoctor(ctx context.Context, st *store.Store, override *doctor.Options) (doctor.Report, error) {
	if st == nil {
		return doctor.Report{}, errors.New("store is unavailable")
	}
	cfg, err := st.ExportToConfig()
	if err != nil || cfg == nil {
		cfg = config.DefaultConfig()
	}
	var opts doctor.Options
	if override != nil {
		opts = *override
	}
	opts.Send = false
	return doctor.Run(ctx, cfg, st, opts), nil
}

func jsonResourceResult(uri string, payload any) (*mcp.ReadResourceResult, error) {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func clampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
