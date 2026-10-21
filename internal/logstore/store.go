// Package logstore persists the request and attempt log in SQLite so the
// history survives restarts. Configuration stays in config.json; this database
// is append-only from the request path and pruned by the retention policy.
package logstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Request is one completed client inference request.
type Request struct {
	ID          int64     `json:"id"`
	RequestID   string    `json:"request_id"`
	At          time.Time `json:"time"`
	Session     string    `json:"session,omitempty"`
	ServerKeyID string    `json:"server_key_id,omitempty"`
	Model       string    `json:"model"`
	Tier        string    `json:"tier,omitempty"`
	KeyID       string    `json:"key_id,omitempty"`
	Channel     string    `json:"channel,omitempty"`
	Anonymous   bool      `json:"anonymous"`
	Proxy       string    `json:"proxy_node,omitempty"`
	ExitNode    string    `json:"exit_node,omitempty"`
	ExitID      string    `json:"exit_id,omitempty"`
	Attempts    int       `json:"attempts"`
	Status      int       `json:"status"`
	DurationMS  int64     `json:"duration_ms"`
	Success     bool      `json:"success"`
	Outcome     string    `json:"outcome,omitempty"`
}

// Attempt is one upstream try inside one request.
type Attempt struct {
	ID         int64     `json:"id"`
	RequestID  string    `json:"request_id"`
	At         time.Time `json:"time"`
	Attempt    int       `json:"attempt"`
	Model      string    `json:"model,omitempty"`
	Tier       string    `json:"tier,omitempty"`
	KeyID      string    `json:"key_id,omitempty"`
	Channel    string    `json:"channel,omitempty"`
	Anonymous  bool      `json:"anonymous"`
	Proxy      string    `json:"proxy_node,omitempty"`
	ExitNode   string    `json:"exit_node,omitempty"`
	ExitID     string    `json:"exit_id,omitempty"`
	Status     int       `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Success    bool      `json:"success"`
	Outcome    string    `json:"outcome,omitempty"`
}

// Query filters the stored request log. Zero values mean "no filter".
type Query struct {
	Limit       int
	BeforeID    int64
	Model       string
	Channel     string
	Tier        string
	KeyID       string
	ServerKeyID string
	ExitID      string
	RequestID   string
	Status      int
	Success     *bool
	Anonymous   *bool
	Since       time.Time
	Until       time.Time
	Keyword     string
	FailedOnly  bool
}

// RequestPage is one page of the request log, newest first.
type RequestPage struct {
	Requests   []Request `json:"requests"`
	NextCursor int64     `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

// Stats describes the stored log for the management API.
type Stats struct {
	Enabled       bool      `json:"enabled"`
	Path          string    `json:"path"`
	Requests      int64     `json:"requests"`
	Attempts      int64     `json:"attempts"`
	Oldest        time.Time `json:"oldest,omitempty"`
	Newest        time.Time `json:"newest,omitempty"`
	SizeBytes     int64     `json:"size_bytes"`
	Dropped       int64     `json:"dropped"`
	RetentionDays int       `json:"retention_days"`
	MaxRows       int       `json:"max_rows"`
}

// Store owns the SQLite handle. Writes go through Writer so the request path
// never waits on disk.
type Store struct {
	db   *sql.DB
	path string
}

// OpenStore creates the database, runs migrations and returns a usable store.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("logstore: empty path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("logstore: resolve path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("logstore: create directory: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(absolute) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("logstore: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("logstore: ping: %w", err)
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: absolute}, nil
}

func runMigrations(db *sql.DB) error {
	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("logstore: load migrations: %w", err)
	}
	driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
	if err != nil {
		return fmt.Errorf("logstore: migration driver: %w", err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "logstore", driver)
	if err != nil {
		return fmt.Errorf("logstore: migration runner: %w", err)
	}
	// Close is intentionally not called: the sqlite driver closes the shared
	// *sql.DB, and the store keeps using it after migration.
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("logstore: migrate: %w", err)
	}
	return nil
}

// Path returns the absolute database path.
func (s *Store) Path() string { return s.path }

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// InsertRequests stores completed requests. A repeated request id replaces the
// earlier row, so the recorded outcome is always the final one.
func (s *Store) InsertRequests(ctx context.Context, rows []Request) error {
	return s.insertBatch(ctx, rows, nil)
}

// InsertAttempts stores upstream attempts.
func (s *Store) InsertAttempts(ctx context.Context, rows []Attempt) error {
	return s.insertBatch(ctx, nil, rows)
}

// insertBatch writes both tables inside one transaction.
func (s *Store) insertBatch(ctx context.Context, requests []Request, attempts []Attempt) error {
	if len(requests) == 0 && len(attempts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("logstore: begin batch: %w", err)
	}
	if err := insertRequests(ctx, tx, requests); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := insertAttempts(ctx, tx, attempts); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("logstore: commit batch: %w", err)
	}
	return nil
}

func insertRequests(ctx context.Context, tx *sql.Tx, rows []Request) error {
	if len(rows) == 0 {
		return nil
	}
	statement, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO request_logs (
		request_id, at, session, server_key_id, model, tier, key_id, channel, anonymous,
		proxy_node, exit_node, exit_id, attempts, status, duration_ms, success, outcome
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("logstore: prepare request insert: %w", err)
	}
	defer statement.Close()
	for _, row := range rows {
		if _, err := statement.ExecContext(ctx,
			row.RequestID, row.At.UnixNano(), row.Session, row.ServerKeyID, row.Model, row.Tier,
			row.KeyID, row.Channel, boolInt(row.Anonymous), row.Proxy, row.ExitNode, row.ExitID,
			row.Attempts, row.Status, row.DurationMS, boolInt(row.Success), row.Outcome,
		); err != nil {
			return fmt.Errorf("logstore: insert request: %w", err)
		}
	}
	return nil
}

func insertAttempts(ctx context.Context, tx *sql.Tx, rows []Attempt) error {
	if len(rows) == 0 {
		return nil
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO request_attempts (
		request_id, at, attempt, model, tier, key_id, channel, anonymous,
		proxy_node, exit_node, exit_id, status, duration_ms, success, outcome
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("logstore: prepare attempt insert: %w", err)
	}
	defer statement.Close()
	for _, row := range rows {
		if _, err := statement.ExecContext(ctx,
			row.RequestID, row.At.UnixNano(), row.Attempt, row.Model, row.Tier, row.KeyID,
			row.Channel, boolInt(row.Anonymous), row.Proxy, row.ExitNode, row.ExitID,
			row.Status, row.DurationMS, boolInt(row.Success), row.Outcome,
		); err != nil {
			return fmt.Errorf("logstore: insert attempt: %w", err)
		}
	}
	return nil
}

// Requests returns one page of the log, newest first.
func (s *Store) Requests(ctx context.Context, query Query) (RequestPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	where, args := query.where()
	page := RequestPage{Requests: []Request{}}
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, request_id, at, session, server_key_id, model, tier, key_id, channel, anonymous,
		proxy_node, exit_node, exit_id, attempts, status, duration_ms, success, outcome
		FROM request_logs `+where+` ORDER BY id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return page, fmt.Errorf("logstore: query requests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item Request
		var at, anonymous, success int64
		if err := rows.Scan(&item.ID, &item.RequestID, &at, &item.Session, &item.ServerKeyID,
			&item.Model, &item.Tier, &item.KeyID, &item.Channel, &anonymous, &item.Proxy,
			&item.ExitNode, &item.ExitID, &item.Attempts, &item.Status, &item.DurationMS,
			&success, &item.Outcome); err != nil {
			return page, fmt.Errorf("logstore: scan request: %w", err)
		}
		item.At = time.Unix(0, at).UTC()
		item.Anonymous = anonymous != 0
		item.Success = success != 0
		page.Requests = append(page.Requests, item)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("logstore: read requests: %w", err)
	}
	if len(page.Requests) > limit {
		page.Requests = page.Requests[:limit]
		page.HasMore = true
		page.NextCursor = page.Requests[limit-1].ID
	}
	return page, nil
}

// AttemptsOf returns every stored attempt of one request, oldest first.
func (s *Store) AttemptsOf(ctx context.Context, requestID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, request_id, at, attempt, model, tier, key_id, channel, anonymous,
		proxy_node, exit_node, exit_id, status, duration_ms, success, outcome
		FROM request_attempts WHERE request_id = ? ORDER BY attempt ASC, id ASC`, requestID)
	if err != nil {
		return nil, fmt.Errorf("logstore: query attempts: %w", err)
	}
	defer rows.Close()
	result := []Attempt{}
	for rows.Next() {
		var item Attempt
		var at, anonymous, success int64
		if err := rows.Scan(&item.ID, &item.RequestID, &at, &item.Attempt, &item.Model, &item.Tier,
			&item.KeyID, &item.Channel, &anonymous, &item.Proxy, &item.ExitNode, &item.ExitID,
			&item.Status, &item.DurationMS, &success, &item.Outcome); err != nil {
			return nil, fmt.Errorf("logstore: scan attempt: %w", err)
		}
		item.At = time.Unix(0, at).UTC()
		item.Anonymous = anonymous != 0
		item.Success = success != 0
		result = append(result, item)
	}
	return result, rows.Err()
}

// RequestByExternalID returns the stored row for one request id.
func (s *Store) RequestByExternalID(ctx context.Context, requestID string) (Request, bool, error) {
	page, err := s.Requests(ctx, Query{RequestID: requestID, Limit: 1})
	if err != nil || len(page.Requests) == 0 {
		return Request{}, false, err
	}
	return page.Requests[0], true, nil
}

// facetColumns is a compile-time allowlist: the query below interpolates only
// these names, never a value that came from a request.
var facetColumns = []string{"model", "channel", "tier", "exit_id"}

// Facets lists the distinct filter values so the UI can offer them.
func (s *Store) Facets(ctx context.Context) (map[string][]string, error) {
	result := map[string][]string{}
	for _, column := range facetColumns {
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT `+column+` FROM request_logs
			WHERE `+column+` <> '' ORDER BY `+column+` ASC LIMIT 200`)
		if err != nil {
			return nil, fmt.Errorf("logstore: facets: %w", err)
		}
		values := []string{}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return nil, fmt.Errorf("logstore: scan facet: %w", err)
			}
			values = append(values, value)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("logstore: read facets: %w", err)
		}
		result[column] = values
	}
	return result, nil
}

// Prune drops rows older than the retention window and trims the table back to
// the row cap, oldest first. It returns how many request rows were removed.
func (s *Store) Prune(ctx context.Context, olderThan time.Time, maxRows int) (int, error) {
	removed := int64(0)
	if !olderThan.IsZero() {
		result, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE at < ?`, olderThan.UnixNano())
		if err != nil {
			return 0, fmt.Errorf("logstore: prune by age: %w", err)
		}
		removed, _ = result.RowsAffected()
	}
	if maxRows > 0 {
		cutoff, err := s.cutoffID(ctx, maxRows)
		if err != nil {
			return int(removed), err
		}
		if cutoff > 0 {
			result, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE id <= ?`, cutoff)
			if err != nil {
				return int(removed), fmt.Errorf("logstore: prune by rows: %w", err)
			}
			extra, _ := result.RowsAffected()
			removed += extra
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM request_attempts WHERE request_id NOT IN (SELECT request_id FROM request_logs)`); err != nil {
		return int(removed), fmt.Errorf("logstore: prune attempts: %w", err)
	}
	return int(removed), nil
}

// cutoffID returns the highest id that must go to keep the newest maxRows rows.
func (s *Store) cutoffID(ctx context.Context, maxRows int) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_logs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("logstore: count requests: %w", err)
	}
	if count <= int64(maxRows) {
		return 0, nil
	}
	var cutoff int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM request_logs ORDER BY id DESC LIMIT 1 OFFSET ?`, maxRows).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("logstore: cutoff id: %w", err)
	}
	return cutoff, nil
}

// Stats reports the size and freshness of the stored log.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{Enabled: true, Path: s.path}
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(at), MAX(at) FROM request_logs`)
	var oldest, newest sql.NullInt64
	if err := row.Scan(&stats.Requests, &oldest, &newest); err != nil {
		return stats, fmt.Errorf("logstore: request stats: %w", err)
	}
	if oldest.Valid {
		stats.Oldest = time.Unix(0, oldest.Int64).UTC()
	}
	if newest.Valid {
		stats.Newest = time.Unix(0, newest.Int64).UTC()
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_attempts`).Scan(&stats.Attempts); err != nil {
		return stats, fmt.Errorf("logstore: attempt stats: %w", err)
	}
	if info, err := os.Stat(s.path); err == nil {
		stats.SizeBytes = info.Size()
	}
	return stats, nil
}

// Purge empties the log; the operator asked for it from the management API.
func (s *Store) Purge(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_logs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("logstore: count before purge: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM request_attempts`); err != nil {
		return 0, fmt.Errorf("logstore: purge attempts: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM request_logs`); err != nil {
		return 0, fmt.Errorf("logstore: purge requests: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return count, fmt.Errorf("logstore: vacuum: %w", err)
	}
	return count, nil
}

func (q Query) where() (string, []any) {
	conditions := []string{}
	args := []any{}
	add := func(clause string, value any) {
		conditions = append(conditions, clause)
		args = append(args, value)
	}
	if q.BeforeID > 0 {
		add("id < ?", q.BeforeID)
	}
	addEqual(&conditions, &args, "model", q.Model)
	addEqual(&conditions, &args, "channel", q.Channel)
	addEqual(&conditions, &args, "tier", q.Tier)
	addEqual(&conditions, &args, "key_id", q.KeyID)
	addEqual(&conditions, &args, "server_key_id", q.ServerKeyID)
	addEqual(&conditions, &args, "exit_id", q.ExitID)
	addEqual(&conditions, &args, "request_id", q.RequestID)
	if q.Status > 0 {
		add("status = ?", q.Status)
	}
	if q.Success != nil {
		add("success = ?", boolInt(*q.Success))
	}
	if q.Anonymous != nil {
		add("anonymous = ?", boolInt(*q.Anonymous))
	}
	if q.FailedOnly {
		conditions = append(conditions, "success = 0")
	}
	if !q.Since.IsZero() {
		add("at >= ?", q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		add("at <= ?", q.Until.UnixNano())
	}
	if keyword := strings.ToLower(strings.TrimSpace(q.Keyword)); keyword != "" {
		like := "%" + keyword + "%"
		conditions = append(conditions, `(lower(request_id) LIKE ? OR lower(model) LIKE ? OR lower(channel) LIKE ?
			OR lower(tier) LIKE ? OR lower(key_id) LIKE ? OR lower(server_key_id) LIKE ?
			OR lower(exit_node) LIKE ? OR lower(outcome) LIKE ?)`)
		args = append(args, like, like, like, like, like, like, like, like)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

func addEqual(conditions *[]string, args *[]any, column, value string) {
	if value == "" {
		return
	}
	*conditions = append(*conditions, column+" = ?")
	*args = append(*args, value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
