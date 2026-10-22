package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
)

// HealthRecord is one persisted health key.
type HealthRecord struct {
	NodeHash string
	Stats    health.KeyStats
}

// HealthLoadStats reports how a health-key table was restored.
type HealthLoadStats struct {
	Loaded  int
	Payload int
	Columns int
	Skipped int
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

// AccuracySample is one ACTIVE-cohort first countable request.
type AccuracySample struct {
	NodeHash    string
	Key         health.HealthKey
	ActiveEpoch uint32
	RequestID   string
	Success     bool
	At          time.Time
}

// RecordAccuracySample keeps the first countable request per ACTIVE epoch.
func (s *Store) RecordAccuracySample(ctx context.Context, sample AccuracySample) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO egress_accuracy_samples
			(node_hash, target, model, protocol, mode, active_epoch, request_id, success, recorded_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_hash, target, model, protocol, mode, active_epoch) DO NOTHING`,
		sample.NodeHash, sample.Key.Target, sample.Key.Model, sample.Key.Protocol, sample.Key.Mode,
		sample.ActiveEpoch, sample.RequestID, boolInt(sample.Success), sample.At.UnixNano())
	if err != nil {
		return fmt.Errorf("egress state: record accuracy sample: %w", err)
	}
	return nil
}
