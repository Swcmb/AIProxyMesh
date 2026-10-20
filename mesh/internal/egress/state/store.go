// Package state persists the egress pool in SQLite so node identity, manual
// switches, cooldowns, latency history and attributed upstream statistics
// survive restarts. Business configuration stays in config.json; this database
// is updated independently.
package state

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// LatencyRecord is one persisted per-host observation.
type LatencyRecord struct {
	EWMA      time.Duration
	UpdatedAt time.Time
}

// UpstreamRecord is the persisted attribution counter for one target.
type UpstreamRecord struct {
	Success uint64
	Failure uint64
}

// NodeRecord is the durable part of one node.
type NodeRecord struct {
	Hash          string
	Raw           []byte
	Enabled       bool
	Failures      int32
	CooldownUntil time.Time
	CreatedAt     time.Time
	Subscriptions []string
	Latency       map[string]LatencyRecord
	Upstream      map[string]UpstreamRecord
}

// Snapshot is the full persisted pool state.
type Snapshot struct {
	Nodes []NodeRecord
}

// Event is one exit-selection or failover record.
type Event struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	RequestID string    `json:"request_id,omitempty"`
	NodeHash  string    `json:"node_id,omitempty"`
	NodeName  string    `json:"node_name,omitempty"`
	Target    string    `json:"target,omitempty"`
	Kind      string    `json:"kind"`
	Status    int       `json:"status,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// Store owns the SQLite handle.
type Store struct {
	db   *sql.DB
	path string
}

// Open creates the database, runs migrations and returns a usable store.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("egress state: empty path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("egress state: resolve path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("egress state: create directory: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(absolute) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("egress state: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("egress state: ping: %w", err)
	}
	if err := migrateDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: absolute}, nil
}

func migrateDB(db *sql.DB) error {
	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("egress state: load migrations: %w", err)
	}
	driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
	if err != nil {
		return fmt.Errorf("egress state: migration driver: %w", err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "egress", driver)
	if err != nil {
		return fmt.Errorf("egress state: migration runner: %w", err)
	}
	// Close is intentionally not called: the sqlite driver closes the shared
	// *sql.DB, and the store keeps using it after migration.
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("egress state: migrate: %w", err)
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

// Save replaces the persisted pool with the supplied snapshot in one
// transaction.
func (s *Store) Save(ctx context.Context, snapshot Snapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("egress state: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	keep := make(map[string]struct{}, len(snapshot.Nodes))
	for _, record := range snapshot.Nodes {
		keep[record.Hash] = struct{}{}
		cooldown := int64(0)
		if !record.CooldownUntil.IsZero() {
			cooldown = record.CooldownUntil.UnixNano()
		}
		created := record.CreatedAt
		if created.IsZero() {
			created = time.Now()
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO egress_nodes (hash, raw, enabled, failures, cooldown_until, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(hash) DO UPDATE SET
				raw = excluded.raw,
				enabled = excluded.enabled,
				failures = excluded.failures,
				cooldown_until = excluded.cooldown_until,
				created_at = excluded.created_at`,
			record.Hash, record.Raw, boolInt(record.Enabled), record.Failures, cooldown, created.UnixNano())
		if err != nil {
			return fmt.Errorf("egress state: upsert node: %w", err)
		}
		for _, table := range []string{"egress_node_subscriptions", "egress_latency", "egress_upstream_stats"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE node_hash = ?", record.Hash); err != nil {
				return fmt.Errorf("egress state: clear %s: %w", table, err)
			}
		}
		for _, subID := range record.Subscriptions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO egress_node_subscriptions (node_hash, subscription_id) VALUES (?, ?)`, record.Hash, subID); err != nil {
				return fmt.Errorf("egress state: insert subscription ref: %w", err)
			}
		}
		for host, latency := range record.Latency {
			if _, err := tx.ExecContext(ctx, `INSERT INTO egress_latency (node_hash, host, ewma_ns, updated_at) VALUES (?, ?, ?, ?)`,
				record.Hash, host, latency.EWMA.Nanoseconds(), latency.UpdatedAt.UnixNano()); err != nil {
				return fmt.Errorf("egress state: insert latency: %w", err)
			}
		}
		for target, stats := range record.Upstream {
			if _, err := tx.ExecContext(ctx, `INSERT INTO egress_upstream_stats (node_hash, target, successes, failures) VALUES (?, ?, ?, ?)`,
				record.Hash, target, stats.Success, stats.Failure); err != nil {
				return fmt.Errorf("egress state: insert upstream stats: %w", err)
			}
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT hash FROM egress_nodes`)
	if err != nil {
		return fmt.Errorf("egress state: list nodes: %w", err)
	}
	var stale []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			_ = rows.Close()
			return fmt.Errorf("egress state: scan node: %w", err)
		}
		if _, ok := keep[hash]; !ok {
			stale = append(stale, hash)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("egress state: close node rows: %w", err)
	}
	for _, hash := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM egress_nodes WHERE hash = ?`, hash); err != nil {
			return fmt.Errorf("egress state: delete node: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("egress state: commit: %w", err)
	}
	return nil
}

// Load reads the persisted pool snapshot.
func (s *Store) Load(ctx context.Context) (Snapshot, error) {
	snapshot := Snapshot{}
	rows, err := s.db.QueryContext(ctx, `SELECT hash, raw, enabled, failures, cooldown_until, created_at FROM egress_nodes`)
	if err != nil {
		return snapshot, fmt.Errorf("egress state: load nodes: %w", err)
	}
	defer rows.Close()
	byHash := make(map[string]*NodeRecord)
	for rows.Next() {
		record := &NodeRecord{}
		var enabled int
		var cooldown, created int64
		if err := rows.Scan(&record.Hash, &record.Raw, &enabled, &record.Failures, &cooldown, &created); err != nil {
			return snapshot, fmt.Errorf("egress state: scan node: %w", err)
		}
		record.Enabled = enabled != 0
		if cooldown != 0 {
			record.CooldownUntil = time.Unix(0, cooldown).UTC()
		}
		record.CreatedAt = time.Unix(0, created).UTC()
		record.Latency = make(map[string]LatencyRecord)
		record.Upstream = make(map[string]UpstreamRecord)
		byHash[record.Hash] = record
	}
	if err := rows.Err(); err != nil {
		return snapshot, fmt.Errorf("egress state: iterate nodes: %w", err)
	}

	subRows, err := s.db.QueryContext(ctx, `SELECT node_hash, subscription_id FROM egress_node_subscriptions`)
	if err != nil {
		return snapshot, fmt.Errorf("egress state: load subscriptions: %w", err)
	}
	for subRows.Next() {
		var hash, subID string
		if err := subRows.Scan(&hash, &subID); err != nil {
			_ = subRows.Close()
			return snapshot, fmt.Errorf("egress state: scan subscription: %w", err)
		}
		if record := byHash[hash]; record != nil {
			record.Subscriptions = append(record.Subscriptions, subID)
		}
	}
	if err := subRows.Close(); err != nil {
		return snapshot, err
	}

	latencyRows, err := s.db.QueryContext(ctx, `SELECT node_hash, host, ewma_ns, updated_at FROM egress_latency`)
	if err != nil {
		return snapshot, fmt.Errorf("egress state: load latency: %w", err)
	}
	for latencyRows.Next() {
		var hash, host string
		var ewma, updated int64
		if err := latencyRows.Scan(&hash, &host, &ewma, &updated); err != nil {
			_ = latencyRows.Close()
			return snapshot, fmt.Errorf("egress state: scan latency: %w", err)
		}
		if record := byHash[hash]; record != nil {
			record.Latency[host] = LatencyRecord{EWMA: time.Duration(ewma), UpdatedAt: time.Unix(0, updated).UTC()}
		}
	}
	if err := latencyRows.Close(); err != nil {
		return snapshot, err
	}

	upstreamRows, err := s.db.QueryContext(ctx, `SELECT node_hash, target, successes, failures FROM egress_upstream_stats`)
	if err != nil {
		return snapshot, fmt.Errorf("egress state: load upstream stats: %w", err)
	}
	for upstreamRows.Next() {
		var hash, target string
		var success, failure uint64
		if err := upstreamRows.Scan(&hash, &target, &success, &failure); err != nil {
			_ = upstreamRows.Close()
			return snapshot, fmt.Errorf("egress state: scan upstream stats: %w", err)
		}
		if record := byHash[hash]; record != nil {
			record.Upstream[target] = UpstreamRecord{Success: success, Failure: failure}
		}
	}
	if err := upstreamRows.Close(); err != nil {
		return snapshot, err
	}
	for _, record := range byHash {
		snapshot.Nodes = append(snapshot.Nodes, *record)
	}
	return snapshot, nil
}

// AppendEvent records one selection/failover event.
func (s *Store) AppendEvent(ctx context.Context, event Event) error {
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO egress_events (at, request_id, node_hash, node_name, target, kind, status, attempt, reason, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		at.UnixNano(), event.RequestID, event.NodeHash, event.NodeName, event.Target,
		event.Kind, event.Status, event.Attempt, event.Reason, event.Detail)
	if err != nil {
		return fmt.Errorf("egress state: append event: %w", err)
	}
	return nil
}

// Events returns the newest events, oldest first.
func (s *Store) Events(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, request_id, node_hash, node_name, target, kind, status, attempt, reason, detail
		FROM egress_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("egress state: list events: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0, limit)
	for rows.Next() {
		var event Event
		var at int64
		if err := rows.Scan(&event.ID, &at, &event.RequestID, &event.NodeHash, &event.NodeName,
			&event.Target, &event.Kind, &event.Status, &event.Attempt, &event.Reason, &event.Detail); err != nil {
			return nil, fmt.Errorf("egress state: scan event: %w", err)
		}
		event.At = time.Unix(0, at).UTC()
		events = append(events, event)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, rows.Err()
}

// PruneEvents keeps only the newest keep rows.
func (s *Store) PruneEvents(ctx context.Context, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM egress_events WHERE id NOT IN (
			SELECT id FROM egress_events ORDER BY id DESC LIMIT ?
		)`, keep)
	if err != nil {
		return fmt.Errorf("egress state: prune events: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
