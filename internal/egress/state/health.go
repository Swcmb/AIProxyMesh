package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
)

// HealthRecord is one persisted health key.
type HealthRecord struct {
	NodeHash string
	Stats    health.KeyStats
}

// SaveHealthKeys upserts the given records in one transaction. The core
// columns stay queryable; payload_json preserves the full statistical state so
// a restart resumes from the same evidence.
func (s *Store) SaveHealthKeys(ctx context.Context, records []HealthRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("egress state: begin health tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO egress_health_keys (
			node_hash, target, model, protocol, mode, valid_required, state,
			http_success, http_failure, valid_success, valid_failure,
			primary_success, primary_failure, last_success_at, last_failure_at,
			last_reachable_at, active_epoch, environment_epoch, pending_reverify,
			reverify_success, next_probe_at, state_version, payload_json, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_hash, target, model, protocol, mode) DO UPDATE SET
			valid_required = excluded.valid_required,
			state = excluded.state,
			http_success = excluded.http_success,
			http_failure = excluded.http_failure,
			valid_success = excluded.valid_success,
			valid_failure = excluded.valid_failure,
			primary_success = excluded.primary_success,
			primary_failure = excluded.primary_failure,
			last_success_at = excluded.last_success_at,
			last_failure_at = excluded.last_failure_at,
			last_reachable_at = excluded.last_reachable_at,
			active_epoch = excluded.active_epoch,
			environment_epoch = excluded.environment_epoch,
			pending_reverify = excluded.pending_reverify,
			reverify_success = excluded.reverify_success,
			next_probe_at = excluded.next_probe_at,
			state_version = excluded.state_version,
			payload_json = excluded.payload_json,
			updated_at = excluded.updated_at
		WHERE excluded.state_version >= egress_health_keys.state_version`)
	if err != nil {
		return fmt.Errorf("egress state: prepare health upsert: %w", err)
	}
	defer stmt.Close()
	now := time.Now()
	for _, record := range records {
		snapshot := record.Stats
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("egress state: encode health key: %w", err)
		}
		key := snapshot.Key
		_, err = stmt.ExecContext(ctx, record.NodeHash, key.Target, key.Model, key.Protocol, key.Mode,
			boolInt(snapshot.ValidRequired), string(snapshot.State),
			snapshot.HTTP200Success, snapshot.HTTP200Failure, snapshot.ValidSuccess, snapshot.ValidFailure,
			snapshot.PrimarySuccess, snapshot.PrimaryFailure,
			timeNanos(snapshot.LastSuccessAt), timeNanos(snapshot.LastFailureAt),
			timeNanos(snapshot.LastReachableAt), snapshot.ActiveEpoch, snapshot.EnvironmentEpoch,
			boolInt(snapshot.PendingReverify), snapshot.ReverifySuccess, timeNanos(snapshot.NextProbeAt),
			snapshot.StateVersion, string(payload), now.UnixNano())
		if err != nil {
			return fmt.Errorf("egress state: upsert health key: %w", err)
		}
	}
	return tx.Commit()
}

// LoadHealthKeys restores every persisted health key.
func (s *Store) LoadHealthKeys(ctx context.Context) ([]HealthRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT node_hash, target, model, protocol, mode, valid_required, state, payload_json
		FROM egress_health_keys`)
	if err != nil {
		return nil, fmt.Errorf("egress state: load health keys: %w", err)
	}
	defer rows.Close()
	var out []HealthRecord
	for rows.Next() {
		var (
			nodeHash, target, model, protocol, mode, state string
			validRequired                                  int
			payload                                        string
		)
		if err := rows.Scan(&nodeHash, &target, &model, &protocol, &mode, &validRequired, &state, &payload); err != nil {
			return nil, fmt.Errorf("egress state: scan health key: %w", err)
		}
		record := HealthRecord{NodeHash: nodeHash}
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &record.Stats); err != nil {
				return nil, fmt.Errorf("egress state: decode health key: %w", err)
			}
		}
		if record.Stats.Key.Target == "" {
			record.Stats.Key = health.HealthKey{Target: target, Model: model, Protocol: protocol, Mode: mode}
			record.Stats.ValidRequired = validRequired != 0
			record.Stats.State = health.State(state)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// SaveEnvironment persists the single environment row.
func (s *Store) SaveEnvironment(ctx context.Context, snapshot health.EnvironmentSnapshot) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO egress_environment (id, epoch, state, last_probe_at, probe_failures,
			bootstrap_failures, environment_failures, bootstrap_latency_ms, updated_at)
		VALUES (1,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			epoch = excluded.epoch,
			state = excluded.state,
			last_probe_at = excluded.last_probe_at,
			probe_failures = excluded.probe_failures,
			bootstrap_failures = excluded.bootstrap_failures,
			environment_failures = excluded.environment_failures,
			bootstrap_latency_ms = excluded.bootstrap_latency_ms,
			updated_at = excluded.updated_at`,
		snapshot.Epoch, string(snapshot.State), timeNanos(snapshot.LastProbeAt), snapshot.ProbeFailures,
		snapshot.BootstrapFailures, snapshot.EnvironmentFailures, snapshot.BootstrapLatency.Milliseconds(),
		timeNanos(snapshot.UpdatedAt))
	if err != nil {
		return fmt.Errorf("egress state: save environment: %w", err)
	}
	return nil
}

// LoadEnvironment restores the environment row when present.
func (s *Store) LoadEnvironment(ctx context.Context) (health.EnvironmentSnapshot, bool, error) {
	var (
		snapshot           health.EnvironmentSnapshot
		state              string
		lastProbe, updated int64
		latencyMS          int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT epoch, state, last_probe_at, probe_failures, bootstrap_failures,
			environment_failures, bootstrap_latency_ms, updated_at
		FROM egress_environment WHERE id = 1`).Scan(
		&snapshot.Epoch, &state, &lastProbe, &snapshot.ProbeFailures,
		&snapshot.BootstrapFailures, &snapshot.EnvironmentFailures, &latencyMS, &updated)
	if err == sql.ErrNoRows {
		return snapshot, false, nil
	}
	if err != nil {
		return snapshot, false, fmt.Errorf("egress state: load environment: %w", err)
	}
	snapshot.State = health.EnvironmentState(state)
	snapshot.LastProbeAt = unixNano(lastProbe)
	snapshot.BootstrapLatency = time.Duration(latencyMS) * time.Millisecond
	snapshot.UpdatedAt = unixNano(updated)
	return snapshot, true, nil
}

// SaveMeta stores one scheduler/meta value.
func (s *Store) SaveMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO egress_meta (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("egress state: save meta: %w", err)
	}
	return nil
}

// LoadMeta reads one scheduler/meta value.
func (s *Store) LoadMeta(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM egress_meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("egress state: load meta: %w", err)
	}
	return value, true, nil
}

// DeleteHealthKeys removes every key of a node (used when a node is evicted).
func (s *Store) DeleteHealthKeys(ctx context.Context, nodeHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM egress_health_keys WHERE node_hash = ?`, nodeHash)
	if err != nil {
		return fmt.Errorf("egress state: delete health keys: %w", err)
	}
	return nil
}

func timeNanos(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}

func unixNano(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

// RegionBlockRecord is one persisted hard region block.
type RegionBlockRecord struct {
	Target  string
	Country string
	Until   time.Time
}

// SaveRegionBlocks replaces the block table with the live snapshot.
func (s *Store) SaveRegionBlocks(ctx context.Context, blocks []RegionBlockRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("egress state: begin region tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM egress_region_blocks`); err != nil {
		return fmt.Errorf("egress state: clear region blocks: %w", err)
	}
	for _, block := range blocks {
		if block.Target == "" || block.Country == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO egress_region_blocks (target, country, until) VALUES (?,?,?)
			ON CONFLICT(target, country) DO UPDATE SET until = excluded.until`,
			block.Target, block.Country, block.Until.UnixNano()); err != nil {
			return fmt.Errorf("egress state: save region block: %w", err)
		}
	}
	return tx.Commit()
}

// LoadRegionBlocks returns unexpired blocks.
func (s *Store) LoadRegionBlocks(ctx context.Context, now time.Time) ([]RegionBlockRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target, country, until FROM egress_region_blocks WHERE until > ?`, now.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("egress state: load region blocks: %w", err)
	}
	defer rows.Close()
	var out []RegionBlockRecord
	for rows.Next() {
		var target, country string
		var until int64
		if err := rows.Scan(&target, &country, &until); err != nil {
			return nil, err
		}
		out = append(out, RegionBlockRecord{Target: target, Country: country, Until: unixNano(until)})
	}
	return out, rows.Err()
}
