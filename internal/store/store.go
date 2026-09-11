package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store provides persistence using embedded SQLite.
type Store struct {
	db *sql.DB
}

// WebhookRetry represents a queued webhook delivery attempt.
type WebhookRetry struct {
	ID          int64
	TargetType  string
	TargetURL   string
	Payload     string
	Attempts    int
	NextRetryAt time.Time
	CreatedAt   time.Time
}

// New initializes an embedded SQLite database and creates the schema.
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

	// Performance pragmatic PRAGMAs for embedded SQLite
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
		next_retry_at INTEGER,
		created_at INTEGER
	);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing sqlite schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// GetFeedState retrieves HTTP conditional caching headers for a given feed URL.
func (s *Store) GetFeedState(feedURL string) (etag, lastModified string, lastPolledAt time.Time, err error) {
	query := `SELECT etag, last_modified, last_polled_at FROM feed_state WHERE feed_url = ?`
	var polledUnix int64
	row := s.db.QueryRow(query, feedURL)
	err = row.Scan(&etag, &lastModified, &polledUnix)
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
	_, err := s.db.Exec(query, feedURL, etag, lastModified, lastPolledAt.Unix())
	if err != nil {
		return fmt.Errorf("updating feed_state for %s: %w", feedURL, err)
	}
	return nil
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
func (s *Store) QueueWebhookRetry(targetType, targetURL, payload string, nextRetryAt time.Time) error {
	query := `
	INSERT INTO webhook_retries (target_type, target_url, payload, attempts, next_retry_at, created_at)
	VALUES (?, ?, ?, 0, ?, ?);
	`
	now := time.Now().Unix()
	_, err := s.db.Exec(query, targetType, targetURL, payload, nextRetryAt.Unix(), now)
	if err != nil {
		return fmt.Errorf("queueing webhook retry: %w", err)
	}
	return nil
}

// GetPendingWebhookRetries fetches webhooks ready for retry.
func (s *Store) GetPendingWebhookRetries(limit int, now time.Time) ([]WebhookRetry, error) {
	query := `
	SELECT id, target_type, target_url, payload, attempts, next_retry_at, created_at
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
		if err := rows.Scan(&item.ID, &item.TargetType, &item.TargetURL, &item.Payload, &item.Attempts, &nextUnix, &createdUnix); err != nil {
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
func (s *Store) UpdateWebhookRetry(id int64, attempts int, nextRetryAt time.Time) error {
	query := `UPDATE webhook_retries SET attempts = ?, next_retry_at = ? WHERE id = ?;`
	_, err := s.db.Exec(query, attempts, nextRetryAt.Unix(), id)
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

// PruneOldRecords prunes dispatched cluster hashes and dead retry entries older than retention.
func (s *Store) PruneOldRecords(retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()

	_, err1 := s.db.Exec(`DELETE FROM dispatched_clusters WHERE dispatched_at < ?;`, cutoff)
	if err1 != nil {
		return fmt.Errorf("pruning dispatched_clusters: %w", err1)
	}

	_, err2 := s.db.Exec(`DELETE FROM webhook_retries WHERE attempts >= 5 OR created_at < ?;`, cutoff)
	if err2 != nil {
		return fmt.Errorf("pruning webhook_retries: %w", err2)
	}

	return nil
}

