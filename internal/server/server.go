package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"celerum/internal/ai"
	"celerum/internal/config"
	"celerum/internal/doctor"
	"celerum/internal/engine"
	"celerum/internal/store"
	"celerum/pkg/model"
	"celerum/web"
)

// Server exposes Celerum's REST API, SSE stream, and embedded React SPA.
type Server struct {
	eng   *engine.Engine
	store *store.Store
	mux   *http.ServeMux
}

// New constructs a configured HTTP Server.
func New(eng *engine.Engine, st *store.Store) *Server {
	s := &Server{
		eng:   eng,
		store: st,
		mux:   http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/overview", s.handleOverview)
	s.mux.HandleFunc("GET /api/clusters", s.handleClusters)
	s.mux.HandleFunc("GET /api/articles", s.handleArticles)

	s.mux.HandleFunc("GET /api/sources", s.handleListSources)
	s.mux.HandleFunc("POST /api/sources", s.handleCreateSource)
	s.mux.HandleFunc("PUT /api/sources/{id}", s.handleUpdateSource)
	s.mux.HandleFunc("DELETE /api/sources/{id}", s.handleDeleteSource)

	s.mux.HandleFunc("GET /api/config/yaml", s.handleExportYAML)
	s.mux.HandleFunc("POST /api/config/yaml", s.handleImportYAML)

	s.mux.HandleFunc("GET /api/dispatch", s.handleGetDispatch)
	s.mux.HandleFunc("POST /api/dispatch/flush", s.handleFlushDispatch)
	s.mux.HandleFunc("POST /api/dispatch/retries/{id}/retry", s.handleRetryWebhook)
	s.mux.HandleFunc("DELETE /api/dispatch/retries/{id}", s.handleDeleteRetry)

	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	s.mux.HandleFunc("POST /api/models/discover", s.handleDiscoverModels)
	s.mux.HandleFunc("POST /api/doctor", s.handleDoctor)

	s.mux.HandleFunc("GET /api/events", s.handleSSE)

	// Embedded SPA fallback
	distFS, err := fs.Sub(web.DistFS, "dist")
	if err == nil {
		s.mux.Handle("/", spaHandler(distFS))
	}
}

func spaHandler(distFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(distFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "endpoint not found", "")
			return
		}

		cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}

		if info, err := fs.Stat(distFS, cleanPath); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		indexBytes, err := fs.ReadFile(distFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexBytes)
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	tier := r.URL.Query().Get("tier")
	metrics := s.eng.GetOverviewTelemetry(tier)
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := q.Get("filter")
	tier := q.Get("tier")
	sortBy := q.Get("sort")
	search := q.Get("q")

	resp, err := s.eng.GetClustersView(filter, tier, sortBy, search)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleArticles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	category := q.Get("category")
	source := q.Get("source")
	search := q.Get("q")
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}

	items, total, categories, sources, err := s.store.QueryArticles(category, source, search, page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
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

	writeJSON(w, http.StatusOK, map[string]any{
		"articles":    items,
		"total_count": total,
		"page":        page,
		"limit":       limit,
		"categories":  categories,
		"sources":     sources,
	})
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("q")
	allSources, err := s.store.ListSources("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}

	var t1, t2, t3, cached304, updated200 int
	for _, src := range allSources {
		switch src.Tier {
		case 1:
			t1++
		case 2:
			t2++
		default:
			t3++
		}
		if src.LastStatus == 304 {
			cached304++
		} else if src.LastStatus == 200 {
			updated200++
		}
	}

	filtered := allSources
	if strings.TrimSpace(search) != "" {
		filtered, err = s.store.ListSources(search)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "")
			return
		}
	}
	if filtered == nil {
		filtered = []store.SourceRecord{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sources":           filtered,
		"total_count":       len(allSources),
		"tier1_count":       t1,
		"tier2_count":       t2,
		"tier3_count":       t3,
		"etag_cached_count": cached304,
		"updated_200_count": updated200,
	})
}

func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	var req store.SourceRecord
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload", "")
		return
	}
	created, err := s.store.CreateSource(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	if cfg, err := s.store.ExportToConfig(); err == nil {
		s.eng.ReloadConfig(cfg)
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid source id", "")
		return
	}
	var req store.SourceRecord
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload", "")
		return
	}
	req.ID = id
	updated, err := s.store.UpdateSource(req)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "source not found", "")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	if cfg, err := s.store.ExportToConfig(); err == nil {
		s.eng.ReloadConfig(cfg)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid source id", "")
		return
	}
	if err := s.store.DeleteSource(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "source not found", "")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if cfg, err := s.store.ExportToConfig(); err == nil {
		s.eng.ReloadConfig(cfg)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleExportYAML(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.ExportToConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	yamlBytes, err := config.ExportYAML(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="celerum.yaml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(yamlBytes)
}

func (s *Server) handleImportYAML(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed reading request body", "")
		return
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var wrapper struct {
			YAML string `json:"yaml"`
		}
		if err := json.Unmarshal(raw, &wrapper); err == nil && strings.TrimSpace(wrapper.YAML) != "" {
			raw = []byte(wrapper.YAML)
		}
	}

	cfg, err := config.ParseYAML(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	if err := s.store.ImportConfig(cfg, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	reloaded, err := s.store.ExportToConfig()
	if err == nil {
		s.eng.ReloadConfig(reloaded)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imported":      true,
		"sources_count": len(cfg.Feeds),
	})
}

type retryView struct {
	ID          int64     `json:"id"`
	TargetType  string    `json:"target_type"`
	TargetURL   string    `json:"target_url"`
	Headline    string    `json:"headline"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error"`
	NextRetryAt time.Time `json:"next_retry_at"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) handleGetDispatch(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("q")
	cfg, err := s.store.ExportToConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}

	rawRetries, err := s.store.ListAllWebhookRetries(search)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}

	retries := make([]retryView, 0, len(rawRetries))
	for _, rt := range rawRetries {
		var p model.Payload
		_ = json.Unmarshal([]byte(rt.Payload), &p)
		headline := p.Title
		if headline == "" {
			headline = "Queued payload #" + strconv.FormatInt(rt.ID, 10)
		}
		retries = append(retries, retryView{
			ID:          rt.ID,
			TargetType:  rt.TargetType,
			TargetURL:   rt.TargetURL,
			Headline:    headline,
			Attempts:    rt.Attempts,
			LastError:   rt.LastError,
			NextRetryAt: rt.NextRetryAt,
			CreatedAt:   rt.CreatedAt,
		})
	}

	winDur := cfg.Engine.WindowDur
	if winDur <= 0 {
		winDur = 3 * time.Hour
	}
	buffered, err := s.store.QueryClusters("buffered", "all", search, time.Now().UTC().Add(-winDur), "score")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if buffered == nil {
		buffered = []store.ClusterRecord{}
	}

	history, err := s.store.QueryClusters("dispatched", "all", search, time.Time{}, "recent")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if history == nil {
		history = []store.ClusterRecord{}
	}

	var activeTargets []string
	if cfg.Dispatch.Telegram.Enabled {
		activeTargets = append(activeTargets, "Telegram")
	}
	if cfg.Dispatch.Webhook.Enabled {
		activeTargets = append(activeTargets, "Webhook")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"next_flush_in_seconds": s.eng.NextFlushInSeconds(),
		"top_k":                 cfg.Engine.TopK,
		"active_targets":        strings.Join(activeTargets, " + "),
		"retries":               retries,
		"buffered":              buffered,
		"history":               history,
	})
}

func (s *Server) handleFlushDispatch(w http.ResponseWriter, r *http.Request) {
	payloads, err := s.eng.FlushNow(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"flushed":          true,
		"dispatched_count": len(payloads),
	})
}

func (s *Server) handleRetryWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid retry id", "")
		return
	}
	if err := s.eng.RetryNow(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "retry entry not found", "")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"retried": true})
}

func (s *Server) handleDeleteRetry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid retry id", "")
		return
	}
	if err := s.store.DeleteWebhookRetry(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.ExportToConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, store.RuntimeSettings{
		Engine:     cfg.Engine,
		Keywords:   cfg.Keywords,
		Enrichment: cfg.Enrichment,
		LLM:        cfg.LLM,
		Dispatch:   cfg.Dispatch,
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var rt store.RuntimeSettings
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&rt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON settings payload", "")
		return
	}

	candidate := &config.Config{
		Version:    "1",
		Database:   config.DatabaseConfig{Path: s.store.DBPath()},
		Engine:     rt.Engine,
		Keywords:   rt.Keywords,
		Enrichment: rt.Enrichment,
		LLM:        rt.LLM,
		Dispatch:   rt.Dispatch,
	}

	if err := candidate.ValidateAndSetDefaults(); err != nil {
		field := inferValidationField(err.Error())
		writeError(w, http.StatusBadRequest, err.Error(), field)
		return
	}

	rt.Engine = candidate.Engine
	rt.Keywords = candidate.Keywords
	rt.Enrichment = candidate.Enrichment
	rt.LLM = candidate.LLM
	rt.Dispatch = candidate.Dispatch

	if err := s.store.SaveRuntimeSettings(rt); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if fullCfg, err := s.store.ExportToConfig(); err == nil {
		s.eng.ReloadConfig(fullCfg)
	}
	writeJSON(w, http.StatusOK, rt)
}

func inferValidationField(msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "jina_api_key"):
		return "enrichment.jina_api_key"
	case strings.Contains(low, "firecrawl_api_key"):
		return "enrichment.firecrawl_api_key"
	case strings.Contains(low, "llm") && strings.Contains(low, "api_key"):
		return "llm.api_key"
	case strings.Contains(low, "telegram"):
		return "dispatch.telegram"
	case strings.Contains(low, "webhook"):
		return "dispatch.webhook"
	default:
		return ""
	}
}

func (s *Server) handleDiscoverModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload", "")
		return
	}

	models, err := ai.ListModels(r.Context(), req.Provider, req.BaseURL, req.APIKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "llm.api_key")
		return
	}
	if models == nil {
		models = []string{}
	}

	endpoint := "/v1/models"
	if strings.EqualFold(strings.TrimSpace(req.Provider), "ollama") {
		endpoint = "/api/tags"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"models":   models,
		"endpoint": endpoint,
	})
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Send bool `json:"send"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req)
	}

	cfg, err := s.store.ExportToConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}

	report := doctor.Run(r.Context(), cfg, s.store, doctor.Options{Send: req.Send})
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported", "")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.eng.SubscribeEvents()
	defer unsubscribe()

	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"live\"}\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, string(b))
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg, field string) {
	payload := map[string]any{"error": msg}
	if field != "" {
		payload["field"] = field
	}
	writeJSON(w, status, payload)
}
