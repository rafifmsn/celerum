package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"celerum/internal/config"
	"celerum/pkg/model"
)

const (
	DefaultMaxArticlesPerFeed = 500
	DefaultMaxArticlesGlobal  = 10000
)

// Store provides persistence using embedded SQLite.
type Store struct {
	db     *sql.DB
	dbPath string
}

// WebhookRetry represents a queued webhook delivery attempt.
type WebhookRetry struct {
	ID          int64     `json:"id"`
	TargetType  string    `json:"target_type"`
	TargetURL   string    `json:"target_url"`
	Payload     string    `json:"payload"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error"`
	NextRetryAt time.Time `json:"next_retry_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// SourceRecord represents a persisted RSS/Atom feed source and its scheduler telemetry.
type SourceRecord struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	Category       string    `json:"category"`
	Tier           int       `json:"tier"`
	Enabled        bool      `json:"enabled"`
	ETag           string    `json:"etag"`
	LastModified   string    `json:"last_modified"`
	LastPolledAt   time.Time `json:"last_polled_at"`
	LastStatus     int       `json:"last_status"`
	LastLatencyMs  int64     `json:"last_latency_ms"`
	NextPollAt     time.Time `json:"next_poll_at"`
	Consecutive304 int       `json:"consecutive_304"`
	LastError      string    `json:"last_error"`
}

// ClusterRecord represents a persisted cluster in active, buffered, or dispatched state.
type ClusterRecord struct {
	ClusterHash     string             `json:"cluster_hash"`
	RepresentativeID string            `json:"representative_id"`
	Title           string             `json:"title"`
	Summary         string             `json:"summary"`
	Enriched        bool               `json:"enriched"`
	Score           float64            `json:"score"`
	Tier            int                `json:"tier"`
	SourceCount     int                `json:"source_count"`
	Category        string             `json:"category"`
	LeadSource      string             `json:"lead_source"`
	Sources         []model.SourceInfo `json:"sources"`
	Status          string             `json:"status"`
	DispatchMode    string             `json:"dispatch_mode"`
	DispatchTargets string             `json:"dispatch_targets"`
	ScraperUsed     string             `json:"scraper_used"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	DispatchedAt    time.Time          `json:"dispatched_at"`
}

// RuntimeSettings represents the JSON-serializable runtime settings stored in SQLite.
type RuntimeSettings struct {
	Engine     config.EngineConfig     `json:"engine"`
	Keywords   config.KeywordsConfig   `json:"keywords"`
	Enrichment config.EnrichmentConfig `json:"enrichment"`
	LLM        config.LLMConfig        `json:"llm"`
	Dispatch   config.DispatchConfig   `json:"dispatch"`
}

// New initializes an embedded SQLite database and creates/migrates the schema.
func New(dbPath string) (*Store, error) {
	if dbPath == "" {
		dbPath = "celerum.db"
	}

	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating database directory %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database at %s: %w", dbPath, err)
	}

	// Single-writer safety for SQLite
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("executing pragma %s: %w", p, err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS feed_state (
		feed_url TEXT PRIMARY KEY,
		etag TEXT,
		last_modified TEXT,
		last_polled_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS dispatched_clusters (
		cluster_hash TEXT PRIMARY KEY,
		dispatched_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS webhook_retries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target_type TEXT,
		target_url TEXT,
		payload TEXT,
		attempts INTEGER DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		next_retry_at INTEGER,
		created_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		url TEXT NOT NULL UNIQUE,
		category TEXT NOT NULL DEFAULT 'General',
		tier INTEGER NOT NULL DEFAULT 1,
		enabled INTEGER NOT NULL DEFAULT 1,
		etag TEXT NOT NULL DEFAULT '',
		last_modified TEXT NOT NULL DEFAULT '',
		last_polled_at INTEGER NOT NULL DEFAULT 0,
		last_status INTEGER NOT NULL DEFAULT 0,
		last_latency_ms INTEGER NOT NULL DEFAULT 0,
		next_poll_at INTEGER NOT NULL DEFAULT 0,
		consecutive_304 INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value_json TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS articles (
		id TEXT PRIMARY KEY,
		feed_url TEXT NOT NULL,
		feed_name TEXT NOT NULL DEFAULT '',
		feed_tier INTEGER NOT NULL DEFAULT 1,
		category TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL,
		image_url TEXT NOT NULL DEFAULT '',
		published_at INTEGER NOT NULL,
		ingested_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_articles_feed_published ON articles(feed_url, published_at DESC);
	CREATE INDEX IF NOT EXISTS idx_articles_published ON articles(published_at DESC);

	CREATE TABLE IF NOT EXISTS clusters (
		cluster_hash TEXT PRIMARY KEY,
		representative_id TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		enriched INTEGER NOT NULL DEFAULT 0,
		score REAL NOT NULL DEFAULT 0,
		tier INTEGER NOT NULL DEFAULT 1,
		source_count INTEGER NOT NULL DEFAULT 1,
		category TEXT NOT NULL DEFAULT '',
		lead_source TEXT NOT NULL DEFAULT '',
		sources_json TEXT NOT NULL DEFAULT '[]',
		status TEXT NOT NULL DEFAULT 'active',
		dispatch_mode TEXT NOT NULL DEFAULT '',
		dispatch_targets TEXT NOT NULL DEFAULT '',
		scraper_used TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		dispatched_at INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_clusters_status_updated ON clusters(status, updated_at DESC);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing sqlite schema: %w", err)
	}

	st := &Store{db: db, dbPath: dbPath}
	if err := st.migrateColumns(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func (s *Store) migrateColumns() error {
	migrations := []struct {
		table  string
		column string
		ddl    string
	}{
		{"webhook_retries", "last_error", "ALTER TABLE webhook_retries ADD COLUMN last_error TEXT NOT NULL DEFAULT '';"},
		{"clusters", "scraper_used", "ALTER TABLE clusters ADD COLUMN scraper_used TEXT NOT NULL DEFAULT '';"},
		{"clusters", "dispatch_mode", "ALTER TABLE clusters ADD COLUMN dispatch_mode TEXT NOT NULL DEFAULT '';"},
		{"clusters", "dispatch_targets", "ALTER TABLE clusters ADD COLUMN dispatch_targets TEXT NOT NULL DEFAULT '';"},
	}
	for _, m := range migrations {
		has, err := s.hasColumn(m.table, m.column)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.Exec(m.ddl); err != nil {
				return fmt.Errorf("migrating %s.%s: %w", m.table, m.column, err)
			}
		}
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s);", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// DBPath returns the SQLite database file path.
func (s *Store) DBPath() string {
	return s.dbPath
}

// CheckHealth verifies WAL mode, expected tables, and read/write transaction integrity.
func (s *Store) CheckHealth() (journalMode string, sizeBytes int64, err error) {
	if err := s.db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		return "", 0, fmt.Errorf("checking journal_mode: %w", err)
	}
	required := []string{"sources", "settings", "articles", "clusters", "webhook_retries", "dispatched_clusters"}
	for _, tbl := range required {
		var name string
		if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&name); err != nil {
			return journalMode, 0, fmt.Errorf("missing required table %q: %w", tbl, err)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return journalMode, 0, fmt.Errorf("starting health check tx: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO settings (key, value_json, updated_at) VALUES ('__doctor_probe', '1', ?);`, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		return journalMode, 0, fmt.Errorf("write probe failed: %w", err)
	}
	_ = tx.Rollback()

	if info, statErr := os.Stat(s.dbPath); statErr == nil {
		sizeBytes = info.Size()
	}
	return journalMode, sizeBytes, nil
}

// GetFeedState retrieves HTTP conditional caching headers for a given feed URL.
func (s *Store) GetFeedState(feedURL string) (etag, lastModified string, lastPolledAt time.Time, err error) {
	var polledUnix int64
	err = s.db.QueryRow(`SELECT etag, last_modified, last_polled_at FROM sources WHERE url = ?`, feedURL).
		Scan(&etag, &lastModified, &polledUnix)
	if err == nil {
		if polledUnix > 0 {
			lastPolledAt = time.Unix(polledUnix, 0)
		}
		return etag, lastModified, lastPolledAt, nil
	}
	if err != sql.ErrNoRows {
		return "", "", time.Time{}, fmt.Errorf("querying sources state for %s: %w", feedURL, err)
	}

	err = s.db.QueryRow(`SELECT etag, last_modified, last_polled_at FROM feed_state WHERE feed_url = ?`, feedURL).
		Scan(&etag, &lastModified, &polledUnix)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", time.Time{}, nil
		}
		return "", "", time.Time{}, fmt.Errorf("querying feed_state for %s: %w", feedURL, err)
	}
	if polledUnix > 0 {
		lastPolledAt = time.Unix(polledUnix, 0)
	}
	return etag, lastModified, lastPolledAt, nil
}

// UpdateFeedState persists updated HTTP conditional caching headers.
func (s *Store) UpdateFeedState(feedURL, etag, lastModified string, lastPolledAt time.Time) error {
	query := `
	INSERT INTO feed_state (feed_url, etag, last_modified, last_polled_at)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(feed_url) DO UPDATE SET
		etag = excluded.etag,
		last_modified = excluded.last_modified,
		last_polled_at = excluded.last_polled_at;
	`
	if _, err := s.db.Exec(query, feedURL, etag, lastModified, lastPolledAt.Unix()); err != nil {
		return fmt.Errorf("updating feed_state for %s: %w", feedURL, err)
	}
	_, _ = s.db.Exec(`
		UPDATE sources SET etag = ?, last_modified = ?, last_polled_at = ? WHERE url = ?;
	`, etag, lastModified, lastPolledAt.Unix(), feedURL)
	return nil
}

// UpdateSourcePollTelemetry persists rich per-source poll state and adaptive schedule cursor.
func (s *Store) UpdateSourcePollTelemetry(feedURL, etag, lastModified string, polledAt time.Time, status int, latencyMs int64, nextPollAt time.Time, consecutive304 int, lastError string) error {
	if status == 200 || status == 304 {
		_ = s.UpdateFeedState(feedURL, etag, lastModified, polledAt)
	}
	_, err := s.db.Exec(`
		UPDATE sources
		SET etag = CASE WHEN ? != '' THEN ? ELSE etag END,
		    last_modified = CASE WHEN ? != '' THEN ? ELSE last_modified END,
		    last_polled_at = ?,
		    last_status = ?,
		    last_latency_ms = ?,
		    next_poll_at = ?,
		    consecutive_304 = ?,
		    last_error = ?
		WHERE url = ?;
	`, etag, etag, lastModified, lastModified, polledAt.Unix(), status, latencyMs, nextPollAt.Unix(), consecutive304, lastError, feedURL)
	return err
}

// ListSources returns all configured feed sources ordered by tier and name.
func (s *Store) ListSources(search string) ([]SourceRecord, error) {
	query := `
		SELECT id, name, url, category, tier, enabled, etag, last_modified,
		       last_polled_at, last_status, last_latency_ms, next_poll_at, consecutive_304, last_error
		FROM sources
	`
	var args []any
	if q := strings.TrimSpace(search); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		query += ` WHERE LOWER(name) LIKE ? OR LOWER(url) LIKE ? OR LOWER(category) LIKE ? `
		args = append(args, like, like, like)
	}
	query += ` ORDER BY tier ASC, name ASC;`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing sources: %w", err)
	}
	defer rows.Close()

	var out []SourceRecord
	for rows.Next() {
		var rec SourceRecord
		var enabledInt int
		var polledUnix, nextUnix int64
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.URL, &rec.Category, &rec.Tier, &enabledInt,
			&rec.ETag, &rec.LastModified, &polledUnix, &rec.LastStatus,
			&rec.LastLatencyMs, &nextUnix, &rec.Consecutive304, &rec.LastError,
		); err != nil {
			return nil, fmt.Errorf("scanning source: %w", err)
		}
		rec.Enabled = enabledInt != 0
		if polledUnix > 0 {
			rec.LastPolledAt = time.Unix(polledUnix, 0).UTC()
		}
		if nextUnix > 0 {
			rec.NextPollAt = time.Unix(nextUnix, 0).UTC()
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CreateSource inserts a new RSS/Atom source.
func (s *Store) CreateSource(src SourceRecord) (SourceRecord, error) {
	if strings.TrimSpace(src.URL) == "" {
		return SourceRecord{}, fmt.Errorf("source url is required")
	}
	if strings.TrimSpace(src.Name) == "" {
		src.Name = src.URL
	}
	if strings.TrimSpace(src.Category) == "" {
		src.Category = "General"
	}
	if src.Tier <= 0 {
		src.Tier = 1
	}
	enabledInt := 1
	if !src.Enabled {
		enabledInt = 0
	}

	res, err := s.db.Exec(`
		INSERT INTO sources (name, url, category, tier, enabled)
		VALUES (?, ?, ?, ?, ?);
	`, src.Name, src.URL, src.Category, src.Tier, enabledInt)
	if err != nil {
		return SourceRecord{}, fmt.Errorf("inserting source: %w", err)
	}
	id, _ := res.LastInsertId()
	src.ID = id
	return src, nil
}

// UpdateSource updates an existing source by ID.
func (s *Store) UpdateSource(src SourceRecord) (SourceRecord, error) {
	if src.ID <= 0 {
		return SourceRecord{}, fmt.Errorf("invalid source id")
	}
	if strings.TrimSpace(src.URL) == "" {
		return SourceRecord{}, fmt.Errorf("source url is required")
	}
	if strings.TrimSpace(src.Name) == "" {
		src.Name = src.URL
	}
	if strings.TrimSpace(src.Category) == "" {
		src.Category = "General"
	}
	if src.Tier <= 0 {
		src.Tier = 1
	}
	enabledInt := 0
	if src.Enabled {
		enabledInt = 1
	}

	res, err := s.db.Exec(`
		UPDATE sources SET name = ?, url = ?, category = ?, tier = ?, enabled = ? WHERE id = ?;
	`, src.Name, src.URL, src.Category, src.Tier, enabledInt, src.ID)
	if err != nil {
		return SourceRecord{}, fmt.Errorf("updating source %d: %w", src.ID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return SourceRecord{}, sql.ErrNoRows
	}
	return src, nil
}

// DeleteSource removes a source and its cached ETag state.
func (s *Store) DeleteSource(id int64) error {
	var feedURL string
	err := s.db.QueryRow(`SELECT url FROM sources WHERE id = ?;`, id).Scan(&feedURL)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM sources WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("deleting source %d: %w", id, err)
	}
	_, _ = s.db.Exec(`DELETE FROM feed_state WHERE feed_url = ?;`, feedURL)
	return nil
}

// SaveRuntimeSettings persists engine, keyword, enrichment, LLM, and dispatch settings as JSON.
func (s *Store) SaveRuntimeSettings(rt RuntimeSettings) error {
	raw, err := json.Marshal(rt)
	if err != nil {
		return fmt.Errorf("marshaling runtime settings: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO settings (key, value_json, updated_at)
		VALUES ('runtime_config', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at;
	`, string(raw), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("saving runtime settings: %w", err)
	}
	return nil
}

// LoadRuntimeSettings reads persisted runtime settings, returning nil if not yet seeded.
func (s *Store) LoadRuntimeSettings() (*RuntimeSettings, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value_json FROM settings WHERE key = 'runtime_config';`).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("loading runtime settings: %w", err)
	}
	var rt RuntimeSettings
	if err := json.Unmarshal([]byte(raw), &rt); err != nil {
		return nil, fmt.Errorf("decoding runtime settings: %w", err)
	}
	return &rt, nil
}

// BootstrapConfig seeds SQLite from yamlCfg if sources & settings are empty, then loads the SQLite runtime Config.
func (s *Store) BootstrapConfig(yamlCfg *config.Config) (*config.Config, error) {
	var srcCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sources;`).Scan(&srcCount); err != nil {
		return nil, err
	}
	rt, err := s.LoadRuntimeSettings()
	if err != nil {
		return nil, err
	}

	if srcCount == 0 && rt == nil {
		seed := yamlCfg
		if seed == nil {
			seed = config.DefaultConfig()
		}
		if err := s.ImportConfig(seed, true); err != nil {
			return nil, fmt.Errorf("seeding sqlite config: %w", err)
		}
	}

	return s.ExportToConfig()
}

// ImportConfig writes a Config struct into SQLite settings and sources.
func (s *Store) ImportConfig(cfg *config.Config, replaceSources bool) error {
	if cfg == nil {
		return fmt.Errorf("cannot import nil config")
	}
	rt := RuntimeSettings{
		Engine:     cfg.Engine,
		Keywords:   cfg.Keywords,
		Enrichment: cfg.Enrichment,
		LLM:        cfg.LLM,
		Dispatch:   cfg.Dispatch,
	}
	if err := s.SaveRuntimeSettings(rt); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if replaceSources {
		if _, err := tx.Exec(`DELETE FROM sources;`); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, f := range cfg.Feeds {
		enabledInt := 1
		if !f.IsEnabled() {
			enabledInt = 0
		}
		cat := strings.TrimSpace(f.Category)
		if cat == "" {
			cat = "General"
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = f.URL
		}
		tier := f.Tier
		if tier <= 0 {
			tier = 1
		}
		_, err := tx.Exec(`
			INSERT INTO sources (name, url, category, tier, enabled)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(url) DO UPDATE SET
				name = excluded.name,
				category = excluded.category,
				tier = excluded.tier,
				enabled = excluded.enabled;
		`, name, f.URL, cat, tier, enabledInt)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("upserting source %s: %w", f.URL, err)
		}
	}
	return tx.Commit()
}

// ExportToConfig reconstructs a complete config.Config from SQLite as the single source of truth.
func (s *Store) ExportToConfig() (*config.Config, error) {
	rt, err := s.LoadRuntimeSettings()
	if err != nil {
		return nil, err
	}
	if rt == nil {
		def := config.DefaultConfig()
		rt = &RuntimeSettings{
			Engine:     def.Engine,
			Keywords:   def.Keywords,
			Enrichment: def.Enrichment,
			LLM:        def.LLM,
			Dispatch:   def.Dispatch,
		}
	}

	sources, err := s.ListSources("")
	if err != nil {
		return nil, err
	}

	feeds := make([]config.FeedConfig, 0, len(sources))
	for _, src := range sources {
		en := src.Enabled
		feeds = append(feeds, config.FeedConfig{
			Name:     src.Name,
			URL:      src.URL,
			Category: src.Category,
			Tier:     src.Tier,
			Enabled:  &en,
		})
	}

	cfg := &config.Config{
		Version:    "1",
		Database:   config.DatabaseConfig{Path: s.dbPath},
		Engine:     rt.Engine,
		Feeds:      feeds,
		Keywords:   rt.Keywords,
		Enrichment: rt.Enrichment,
		LLM:        rt.LLM,
		Dispatch:   rt.Dispatch,
	}
	// Set parsed durations without failing runtime load if user saved partial keys while editing
	_ = parseEngineDurationsOnly(cfg)
	return cfg, nil
}

func parseEngineDurationsOnly(c *config.Config) error {
	if c.Engine.PollInterval == "" {
		c.Engine.PollInterval = "5m"
	}
	if d, err := time.ParseDuration(c.Engine.PollInterval); err == nil {
		c.Engine.PollDuration = d
	} else {
		c.Engine.PollDuration = 5 * time.Minute
	}
	if c.Engine.WindowDuration == "" {
		c.Engine.WindowDuration = "3h"
	}
	if d, err := time.ParseDuration(c.Engine.WindowDuration); err == nil {
		c.Engine.WindowDur = d
	} else {
		c.Engine.WindowDur = 3 * time.Hour
	}
	if c.Engine.FlushInterval == "" {
		c.Engine.FlushInterval = "1h"
	}
	if d, err := time.ParseDuration(c.Engine.FlushInterval); err == nil {
		c.Engine.FlushDur = d
	} else {
		c.Engine.FlushDur = 1 * time.Hour
	}
	if c.Engine.MaxArticlesPerFeed <= 0 {
		c.Engine.MaxArticlesPerFeed = 20
	}
	if c.Engine.SimilarityThreshold <= 0 {
		c.Engine.SimilarityThreshold = 0.28
	}
	if c.Engine.BreakingThreshold <= 0 {
		c.Engine.BreakingThreshold = 12.0
	}
	if c.Engine.TopK <= 0 {
		c.Engine.TopK = 5
	}
	return nil
}

// UpsertArticles persists raw ingested articles and returns the number of newly inserted rows.
func (s *Store) UpsertArticles(articles []model.Article) (int, error) {
	if len(articles) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO articles (
			id, feed_url, feed_name, feed_tier, category, title, description, url, image_url, published_at, ingested_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	nowUnix := time.Now().Unix()
	for _, a := range articles {
		ingUnix := a.IngestedAt.Unix()
		if ingUnix <= 0 {
			ingUnix = nowUnix
		}
		pubUnix := a.PublishedAt.Unix()
		if pubUnix <= 0 {
			pubUnix = ingUnix
		}
		res, err := stmt.Exec(
			a.ID, a.FeedURL, a.FeedName, a.FeedTier, a.Category,
			a.Title, a.Description, a.URL, a.ImageURL, pubUnix, ingUnix,
		)
		if err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("inserting article %s: %w", a.ID, err)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// QueryArticles returns paginated raw articles alongside distinct categories and sources for filters.
func (s *Store) QueryArticles(category, source, search string, page, limit int) (items []model.Article, total int, categories []string, sources []string, err error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}

	var where []string
	var args []any

	if c := strings.TrimSpace(category); c != "" && !strings.EqualFold(c, "all") {
		where = append(where, "LOWER(category) = ?")
		args = append(args, strings.ToLower(c))
	}
	if src := strings.TrimSpace(source); src != "" && !strings.EqualFold(src, "all") {
		where = append(where, "(LOWER(feed_name) = ? OR LOWER(feed_url) = ?)")
		args = append(args, strings.ToLower(src), strings.ToLower(src))
	}
	if q := strings.TrimSpace(search); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, "(LOWER(title) LIKE ? OR LOWER(description) LIKE ? OR LOWER(feed_name) LIKE ?)")
		args = append(args, like, like, like)
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	if err = s.db.QueryRow(`SELECT COUNT(*) FROM articles`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, nil, nil, fmt.Errorf("counting articles: %w", err)
	}

	offset := (page - 1) * limit
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.Query(`
		SELECT id, feed_url, feed_name, feed_tier, category, title, description, url, image_url, published_at, ingested_at
		FROM articles`+whereSQL+`
		ORDER BY published_at DESC, ingested_at DESC
		LIMIT ? OFFSET ?;
	`, queryArgs...)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("querying articles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a model.Article
		var pubUnix, ingUnix int64
		if err := rows.Scan(&a.ID, &a.FeedURL, &a.FeedName, &a.FeedTier, &a.Category, &a.Title, &a.Description, &a.URL, &a.ImageURL, &pubUnix, &ingUnix); err != nil {
			return nil, 0, nil, nil, err
		}
		a.PublishedAt = time.Unix(pubUnix, 0).UTC()
		a.IngestedAt = time.Unix(ingUnix, 0).UTC()
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, nil, err
	}

	catRows, err := s.db.Query(`SELECT DISTINCT category FROM articles WHERE category != '' ORDER BY category ASC;`)
	if err == nil {
		defer catRows.Close()
		for catRows.Next() {
			var c string
			if err := catRows.Scan(&c); err == nil {
				categories = append(categories, c)
			}
		}
	}

	srcRows, err := s.db.Query(`SELECT DISTINCT feed_name FROM articles WHERE feed_name != '' ORDER BY feed_name ASC;`)
	if err == nil {
		defer srcRows.Close()
		for srcRows.Next() {
			var src string
			if err := srcRows.Scan(&src); err == nil {
				sources = append(sources, src)
			}
		}
	}

	return items, total, categories, sources, nil
}

// GetRecentArticles returns articles published after cutoff for window clustering and telemetry.
func (s *Store) GetRecentArticles(cutoff time.Time) ([]model.Article, error) {
	rows, err := s.db.Query(`
		SELECT id, feed_url, feed_name, feed_tier, category, title, description, url, image_url, published_at, ingested_at
		FROM articles
		WHERE published_at >= ?
		ORDER BY published_at DESC;
	`, cutoff.Unix())
	if err != nil {
		return nil, fmt.Errorf("querying recent articles: %w", err)
	}
	defer rows.Close()

	var out []model.Article
	for rows.Next() {
		var a model.Article
		var pubUnix, ingUnix int64
		if err := rows.Scan(&a.ID, &a.FeedURL, &a.FeedName, &a.FeedTier, &a.Category, &a.Title, &a.Description, &a.URL, &a.ImageURL, &pubUnix, &ingUnix); err != nil {
			return nil, err
		}
		a.PublishedAt = time.Unix(pubUnix, 0).UTC()
		a.IngestedAt = time.Unix(ingUnix, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertActiveClusters persists evaluated clusters while preserving already-dispatched cluster state.
func (s *Store) UpsertActiveClusters(records []ClusterRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO clusters (
			cluster_hash, representative_id, title, summary, enriched, score, tier,
			source_count, category, lead_source, sources_json, status, dispatch_mode,
			dispatch_targets, scraper_used, created_at, updated_at, dispatched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cluster_hash) DO UPDATE SET
			representative_id = excluded.representative_id,
			title = excluded.title,
			summary = CASE WHEN clusters.enriched = 1 THEN clusters.summary ELSE excluded.summary END,
			score = excluded.score,
			tier = excluded.tier,
			source_count = excluded.source_count,
			category = excluded.category,
			lead_source = excluded.lead_source,
			sources_json = excluded.sources_json,
			status = CASE WHEN clusters.status = 'dispatched' THEN 'dispatched' ELSE excluded.status END,
			updated_at = excluded.updated_at;
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, r := range records {
		srcBytes, _ := json.Marshal(r.Sources)
		enrichedInt := 0
		if r.Enriched {
			enrichedInt = 1
		}
		var dispUnix int64
		if !r.DispatchedAt.IsZero() {
			dispUnix = r.DispatchedAt.Unix()
		}
		_, err := stmt.Exec(
			r.ClusterHash, r.RepresentativeID, r.Title, r.Summary, enrichedInt,
			r.Score, r.Tier, r.SourceCount, r.Category, r.LeadSource,
			string(srcBytes), r.Status, r.DispatchMode, r.DispatchTargets, r.ScraperUsed,
			r.CreatedAt.Unix(), r.UpdatedAt.Unix(), dispUnix,
		)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("upserting cluster %s: %w", r.ClusterHash, err)
		}
	}
	return tx.Commit()
}

// MarkClusterDispatchedFull updates both dispatched_clusters and clusters with summary and delivery metadata.
func (s *Store) MarkClusterDispatchedFull(hash, summary string, enriched bool, mode, targets, scraperUsed string, dispatchedAt time.Time) error {
	if err := s.MarkClusterDispatched(hash, dispatchedAt); err != nil {
		return err
	}
	enrichedInt := 0
	if enriched {
		enrichedInt = 1
	}
	_, err := s.db.Exec(`
		UPDATE clusters
		SET status = 'dispatched',
		    summary = CASE WHEN ? != '' THEN ? ELSE summary END,
		    enriched = CASE WHEN ? = 1 THEN 1 ELSE enriched END,
		    dispatch_mode = ?,
		    dispatch_targets = ?,
		    scraper_used = ?,
		    dispatched_at = ?,
		    updated_at = ?
		WHERE cluster_hash = ?;
	`, summary, summary, enrichedInt, mode, targets, scraperUsed, dispatchedAt.Unix(), dispatchedAt.Unix(), hash)
	return err
}

// QueryClusters returns persisted clusters matching optional filters ordered by score or recency.
func (s *Store) QueryClusters(statusFilter, tierFilter, search string, since time.Time, sortBy string) ([]ClusterRecord, error) {
	var where []string
	var args []any

	if !since.IsZero() {
		where = append(where, "updated_at >= ?")
		args = append(args, since.Unix())
	}
	if sf := strings.TrimSpace(statusFilter); sf != "" && sf != "all" {
		where = append(where, "status = ?")
		args = append(args, sf)
	}
	if tf := strings.TrimSpace(tierFilter); tf != "" && tf != "all" {
		where = append(where, "tier = ?")
		args = append(args, tf)
	}
	if q := strings.TrimSpace(search); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, "(LOWER(title) LIKE ? OR LOWER(summary) LIKE ? OR LOWER(lead_source) LIKE ? OR LOWER(sources_json) LIKE ?)")
		args = append(args, like, like, like, like)
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	orderSQL := " ORDER BY score DESC, updated_at DESC"
	if sortBy == "recent" {
		orderSQL = " ORDER BY updated_at DESC, score DESC"
	}

	rows, err := s.db.Query(`
		SELECT cluster_hash, representative_id, title, summary, enriched, score, tier,
		       source_count, category, lead_source, sources_json, status, dispatch_mode,
		       dispatch_targets, scraper_used, created_at, updated_at, dispatched_at
		FROM clusters`+whereSQL+orderSQL+`;`, args...)
	if err != nil {
		return nil, fmt.Errorf("querying clusters: %w", err)
	}
	defer rows.Close()

	var out []ClusterRecord
	for rows.Next() {
		var r ClusterRecord
		var enrichedInt int
		var srcJSON string
		var createdUnix, updatedUnix, dispUnix int64
		if err := rows.Scan(
			&r.ClusterHash, &r.RepresentativeID, &r.Title, &r.Summary, &enrichedInt,
			&r.Score, &r.Tier, &r.SourceCount, &r.Category, &r.LeadSource,
			&srcJSON, &r.Status, &r.DispatchMode, &r.DispatchTargets, &r.ScraperUsed,
			&createdUnix, &updatedUnix, &dispUnix,
		); err != nil {
			return nil, err
		}
		r.Enriched = enrichedInt != 0
		_ = json.Unmarshal([]byte(srcJSON), &r.Sources)
		r.CreatedAt = time.Unix(createdUnix, 0).UTC()
		r.UpdatedAt = time.Unix(updatedUnix, 0).UTC()
		if dispUnix > 0 {
			r.DispatchedAt = time.Unix(dispUnix, 0).UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAllWebhookRetries returns all pending/retrying rows for the Dispatch Queue UI.
func (s *Store) ListAllWebhookRetries(search string) ([]WebhookRetry, error) {
	query := `
		SELECT id, target_type, target_url, payload, attempts, last_error, next_retry_at, created_at
		FROM webhook_retries
		WHERE attempts < 5
	`
	var args []any
	if q := strings.TrimSpace(search); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		query += ` AND (LOWER(target_type) LIKE ? OR LOWER(payload) LIKE ? OR LOWER(last_error) LIKE ?) `
		args = append(args, like, like, like)
	}
	query += ` ORDER BY next_retry_at ASC;`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing webhook retries: %w", err)
	}
	defer rows.Close()

	var retries []WebhookRetry
	for rows.Next() {
		var item WebhookRetry
		var nextUnix, createdUnix int64
		if err := rows.Scan(&item.ID, &item.TargetType, &item.TargetURL, &item.Payload, &item.Attempts, &item.LastError, &nextUnix, &createdUnix); err != nil {
			return nil, err
		}
		item.NextRetryAt = time.Unix(nextUnix, 0).UTC()
		item.CreatedAt = time.Unix(createdUnix, 0).UTC()
		retries = append(retries, item)
	}
	return retries, rows.Err()
}

// GetWebhookRetryByID fetches a single webhook retry record.
func (s *Store) GetWebhookRetryByID(id int64) (*WebhookRetry, error) {
	var item WebhookRetry
	var nextUnix, createdUnix int64
	err := s.db.QueryRow(`
		SELECT id, target_type, target_url, payload, attempts, last_error, next_retry_at, created_at
		FROM webhook_retries WHERE id = ?;
	`, id).Scan(&item.ID, &item.TargetType, &item.TargetURL, &item.Payload, &item.Attempts, &item.LastError, &nextUnix, &createdUnix)
	if err != nil {
		return nil, err
	}
	item.NextRetryAt = time.Unix(nextUnix, 0).UTC()
	item.CreatedAt = time.Unix(createdUnix, 0).UTC()
	return &item, nil
}

// IsClusterDispatched returns true if the cluster hash has already been dispatched.
func (s *Store) IsClusterDispatched(clusterHash string) (bool, error) {
	query := `SELECT 1 FROM dispatched_clusters WHERE cluster_hash = ?`
	var dummy int
	err := s.db.QueryRow(query, clusterHash).Scan(&dummy)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("checking dispatched cluster %s: %w", clusterHash, err)
	}
	return true, nil
}

// MarkClusterDispatched records that a cluster has been dispatched.
func (s *Store) MarkClusterDispatched(clusterHash string, dispatchedAt time.Time) error {
	query := `
	INSERT INTO dispatched_clusters (cluster_hash, dispatched_at)
	VALUES (?, ?)
	ON CONFLICT(cluster_hash) DO UPDATE SET
		dispatched_at = excluded.dispatched_at;
	`
	_, err := s.db.Exec(query, clusterHash, dispatchedAt.Unix())
	if err != nil {
		return fmt.Errorf("marking cluster dispatched %s: %w", clusterHash, err)
	}
	return nil
}

// QueueWebhookRetry persists a failed webhook delivery payload for future attempts.
func (s *Store) QueueWebhookRetry(targetType, targetURL, payload string, nextRetryAt time.Time, lastErr ...string) error {
	errMsg := ""
	if len(lastErr) > 0 {
		errMsg = lastErr[0]
	}
	query := `
	INSERT INTO webhook_retries (target_type, target_url, payload, attempts, last_error, next_retry_at, created_at)
	VALUES (?, ?, ?, 0, ?, ?, ?);
	`
	now := time.Now().Unix()
	_, err := s.db.Exec(query, targetType, targetURL, payload, errMsg, nextRetryAt.Unix(), now)
	if err != nil {
		return fmt.Errorf("queueing webhook retry: %w", err)
	}
	return nil
}

// GetPendingWebhookRetries fetches webhooks ready for retry.
func (s *Store) GetPendingWebhookRetries(limit int, now time.Time) ([]WebhookRetry, error) {
	query := `
	SELECT id, target_type, target_url, payload, attempts, last_error, next_retry_at, created_at
	FROM webhook_retries
	WHERE next_retry_at <= ? AND attempts < 5
	ORDER BY next_retry_at ASC
	LIMIT ?;
	`
	rows, err := s.db.Query(query, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("querying pending webhook retries: %w", err)
	}
	defer rows.Close()

	var retries []WebhookRetry
	for rows.Next() {
		var item WebhookRetry
		var nextUnix, createdUnix int64
		if err := rows.Scan(&item.ID, &item.TargetType, &item.TargetURL, &item.Payload, &item.Attempts, &item.LastError, &nextUnix, &createdUnix); err != nil {
			return nil, fmt.Errorf("scanning webhook retry: %w", err)
		}
		item.NextRetryAt = time.Unix(nextUnix, 0)
		item.CreatedAt = time.Unix(createdUnix, 0)
		retries = append(retries, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook retries: %w", err)
	}
	return retries, nil
}

// UpdateWebhookRetry records a failed retry attempt and updates next retry time.
func (s *Store) UpdateWebhookRetry(id int64, attempts int, nextRetryAt time.Time, lastErr ...string) error {
	if len(lastErr) > 0 && lastErr[0] != "" {
		_, err := s.db.Exec(`UPDATE webhook_retries SET attempts = ?, next_retry_at = ?, last_error = ? WHERE id = ?;`,
			attempts, nextRetryAt.Unix(), lastErr[0], id)
		if err != nil {
			return fmt.Errorf("updating webhook retry %d: %w", id, err)
		}
		return nil
	}
	_, err := s.db.Exec(`UPDATE webhook_retries SET attempts = ?, next_retry_at = ? WHERE id = ?;`,
		attempts, nextRetryAt.Unix(), id)
	if err != nil {
		return fmt.Errorf("updating webhook retry %d: %w", id, err)
	}
	return nil
}

// DeleteWebhookRetry removes a successfully dispatched webhook from the retry queue.
func (s *Store) DeleteWebhookRetry(id int64) error {
	query := `DELETE FROM webhook_retries WHERE id = ?;`
	_, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("deleting webhook retry %d: %w", id, err)
	}
	return nil
}

// PruneOldRecords enforces dual time + volume retention caps across all tables.
func (s *Store) PruneOldRecords(retentionDays int) error {
	return s.PruneWithLimits(retentionDays, DefaultMaxArticlesPerFeed, DefaultMaxArticlesGlobal)
}

// PruneWithLimits prunes records older than retentionDays and caps articles per feed and globally.
func (s *Store) PruneWithLimits(retentionDays, maxPerFeed, maxGlobal int) error {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	if maxPerFeed <= 0 {
		maxPerFeed = DefaultMaxArticlesPerFeed
	}
	if maxGlobal <= 0 {
		maxGlobal = DefaultMaxArticlesGlobal
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()

	stmts := []struct {
		name string
		sql  string
		args []any
	}{
		{"dispatched_clusters", `DELETE FROM dispatched_clusters WHERE dispatched_at < ?;`, []any{cutoff}},
		{"webhook_retries", `DELETE FROM webhook_retries WHERE attempts >= 5 OR created_at < ?;`, []any{cutoff}},
		{"clusters_age", `DELETE FROM clusters WHERE updated_at < ?;`, []any{cutoff}},
		{"articles_age", `DELETE FROM articles WHERE ingested_at < ? AND published_at < ?;`, []any{cutoff, cutoff}},
		{"articles_per_feed", `
			DELETE FROM articles WHERE id IN (
				SELECT id FROM (
					SELECT id, ROW_NUMBER() OVER (PARTITION BY feed_url ORDER BY published_at DESC, ingested_at DESC) AS rn
					FROM articles
				) WHERE rn > ?
			);`, []any{maxPerFeed}},
		{"articles_global", `
			DELETE FROM articles WHERE id IN (
				SELECT id FROM articles ORDER BY published_at DESC, ingested_at DESC LIMIT -1 OFFSET ?
			);`, []any{maxGlobal}},
	}

	for _, st := range stmts {
		if _, err := s.db.Exec(st.sql, st.args...); err != nil {
			return fmt.Errorf("pruning %s: %w", st.name, err)
		}
	}
	return nil
}
