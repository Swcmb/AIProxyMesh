package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
)

// The column order is shared by the writer and the scalar JSON fallback.
var healthColumns = []string{
	"node_hash", "target", "model", "protocol", "mode", "valid_required", "state",
	"http_success", "http_failure", "valid_success", "valid_failure", "primary_success", "primary_failure",
	"stream_success", "stream_failure", "consecutive_stream_fails", "unknown_streak", "last_unknown_at", "rate_limited",
	"admission_http_success", "admission_http_failure", "admission_valid_success", "admission_valid_failure",
	"admission_http_buckets", "admission_valid_buckets", "bucket_start", "bucket_http_seen", "bucket_valid_seen",
	"bucket_http_reservoir", "bucket_valid_reservoir",
	"alpha200", "beta200", "alpha_valid", "beta_valid", "decayed_at",
	"ttfb_ewma_ns", "total_ewma_ns", "latency_hist", "ttfb_samples", "last_sample_at",
	"consecutive_fails", "consecutive_success", "open_count", "open_until", "last_success_at", "last_failure_at",
	"last_probe_at", "last_reachable_at", "window_buckets", "window_index", "active_epoch", "environment_epoch",
	"pending_reverify", "reverify_success", "last_check_samples", "last_check_valid_samples", "http_check_round",
	"valid_check_round", "next_probe_at", "recovery_start", "epoch_success", "epoch_failure", "prev_state",
	"last_status", "not_found_count", "last_not_found_at", "half_open_success", "last_error", "state_version",
	"failure_pending_until", "payload_json", "updated_at",
}

var healthBlobColumns = map[string]struct{}{
	"bucket_http_reservoir": {}, "bucket_valid_reservoir": {}, "window_buckets": {}, "latency_hist": {},
}

var healthInsertSQL = buildHealthInsertSQL()
var healthSelectSQL = buildHealthSelectSQL()

func buildHealthInsertSQL() string {
	placeholders := make([]string, len(healthColumns))
	for i := range healthColumns {
		placeholders[i] = "?"
	}
	updates := make([]string, 0, len(healthColumns)-5)
	for _, column := range healthColumns[5:] {
		updates = append(updates, column+" = excluded."+column)
	}
	return fmt.Sprintf(`INSERT INTO egress_health_keys (%s) VALUES (%s)
		ON CONFLICT(node_hash, target, model, protocol, mode) DO UPDATE SET %s
		WHERE excluded.state_version >= egress_health_keys.state_version`,
		strings.Join(healthColumns, ", "), strings.Join(placeholders, ","), strings.Join(updates, ", "))
}

func buildHealthSelectSQL() string {
	pairs := make([]string, 0, len(healthColumns)*2)
	for _, column := range healthColumns {
		if column == "node_hash" || column == "payload_json" || column == "updated_at" || column == "failure_pending_until" {
			continue
		}
		if _, blob := healthBlobColumns[column]; blob {
			continue
		}
		pairs = append(pairs, "'"+column+"', "+column)
	}
	return fmt.Sprintf(`SELECT node_hash, payload_json, json_object(%s) AS scalars,
		hex(bucket_http_reservoir), hex(bucket_valid_reservoir), hex(window_buckets), hex(latency_hist)
		FROM egress_health_keys`, strings.Join(pairs, ", "))
}

// SaveHealthKeys upserts the given records in one transaction. Every scalar
// column and the fixed BLOB encodings are written; payload_json stays for
// readers from the earlier hybrid format.
func (s *Store) SaveHealthKeys(ctx context.Context, records []HealthRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("egress state: begin health tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, healthInsertSQL)
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
		if _, err := stmt.ExecContext(ctx, healthArgs(record.NodeHash, snapshot, payload, now)...); err != nil {
			return fmt.Errorf("egress state: upsert health key: %w", err)
		}
	}
	return tx.Commit()
}

func healthArgs(nodeHash string, snapshot health.KeyStats, payload []byte, now time.Time) []any {
	key := snapshot.Key
	return []any{
		nodeHash, key.Target, key.Model, key.Protocol, key.Mode,
		boolInt(snapshot.ValidRequired), string(snapshot.State),
		int64(snapshot.HTTP200Success), int64(snapshot.HTTP200Failure),
		int64(snapshot.ValidSuccess), int64(snapshot.ValidFailure),
		int64(snapshot.PrimarySuccess), int64(snapshot.PrimaryFailure),
		int64(snapshot.StreamSuccess), int64(snapshot.StreamFailure),
		int64(snapshot.ConsecutiveStreamFails), int64(snapshot.UnknownStreak),
		timeNanos(snapshot.LastUnknownAt), int64(snapshot.RateLimited),
		int64(snapshot.AdmissionHTTP200Success), int64(snapshot.AdmissionHTTP200Failure),
		int64(snapshot.AdmissionValidSuccess), int64(snapshot.AdmissionValidFailure),
		int64(snapshot.AdmissionHTTPBuckets), int64(snapshot.AdmissionValidBuckets),
		timeNanos(snapshot.BucketStart), int64(snapshot.HTTPSeen), int64(snapshot.ValidSeen),
		snapshot.HTTPReservoir.Encode(), snapshot.ValidReservoir.Encode(),
		snapshot.Beta200.Alpha, snapshot.Beta200.Beta, snapshot.BetaValid.Alpha, snapshot.BetaValid.Beta,
		timeNanos(snapshot.Beta200.DecayedAt),
		int64(snapshot.TTFBEWMA), int64(snapshot.TotalEWMA), snapshot.LatencyHist.Encode(),
		int64(snapshot.TTFBSamples), timeNanos(snapshot.LastSampleAt),
		int64(snapshot.ConsecutiveFails), int64(snapshot.ConsecutiveSuccess),
		int64(snapshot.OpenCount), timeNanos(snapshot.OpenUntil),
		timeNanos(snapshot.LastSuccessAt), timeNanos(snapshot.LastFailureAt),
		timeNanos(snapshot.LastProbeAt), timeNanos(snapshot.LastReachableAt),
		snapshot.Window.Encode(), int64(snapshot.Window.Index),
		int64(snapshot.ActiveEpoch), snapshot.EnvironmentEpoch,
		boolInt(snapshot.PendingReverify), int64(snapshot.ReverifySuccess),
		int64(snapshot.LastCheckSamples), int64(snapshot.LastCheckValidSamples),
		int64(snapshot.HTTPCheckRound), int64(snapshot.ValidCheckRound),
		timeNanos(snapshot.NextProbeAt), timeNanos(snapshot.RecoveryStart),
		int64(snapshot.EpochSuccess), int64(snapshot.EpochFailure), string(snapshot.PrevState),
		int64(snapshot.LastStatus), int64(snapshot.NotFoundCount), timeNanos(snapshot.LastNotFoundAt),
		int64(snapshot.HalfOpenSuccess), snapshot.LastError, snapshot.StateVersion,
		0, string(payload), now.UnixNano(),
	}
}

// LoadHealthKeys restores every persisted key. payload_json wins for backward
// compatibility; rows without it are rebuilt from columns + BLOBs. A single
// malformed row is skipped and counted instead of aborting startup.
func (s *Store) LoadHealthKeys(ctx context.Context, cfg health.Config) ([]HealthRecord, HealthLoadStats, error) {
	cfg = cfg.Normalize()
	rows, err := s.db.QueryContext(ctx, healthSelectSQL)
	if err != nil {
		return nil, HealthLoadStats{}, fmt.Errorf("egress state: load health keys: %w", err)
	}
	defer rows.Close()
	var (
		out   []HealthRecord
		stats HealthLoadStats
	)
	for rows.Next() {
		var (
			nodeHash, payload, scalars    string
			httpReservoir, validReservoir *string
			windowBlob, latencyBlob       *string
		)
		if err := rows.Scan(&nodeHash, &payload, &scalars,
			&httpReservoir, &validReservoir, &windowBlob, &latencyBlob); err != nil {
			return nil, stats, fmt.Errorf("egress state: scan health key: %w", err)
		}
		if payload != "" {
			var snapshot health.KeyStats
			if err := json.Unmarshal([]byte(payload), &snapshot); err == nil && snapshot.Key.Target != "" {
				out = append(out, HealthRecord{NodeHash: nodeHash, Stats: snapshot})
				stats.Loaded++
				stats.Payload++
				continue
			}
		}
		snapshot, err := decodeHealthColumns(scalars, httpReservoir, validReservoir, windowBlob, latencyBlob, cfg)
		if err != nil {
			stats.Skipped++
			continue
		}
		out = append(out, HealthRecord{NodeHash: nodeHash, Stats: snapshot})
		stats.Loaded++
		stats.Columns++
	}
	return out, stats, rows.Err()
}

type legacyHealthRow struct {
	Target                 string  `json:"target"`
	Model                  string  `json:"model"`
	Protocol               string  `json:"protocol"`
	Mode                   string  `json:"mode"`
	ValidRequired          int64   `json:"valid_required"`
	State                  string  `json:"state"`
	HTTP200Success         int64   `json:"http_success"`
	HTTP200Failure         int64   `json:"http_failure"`
	ValidSuccess           int64   `json:"valid_success"`
	ValidFailure           int64   `json:"valid_failure"`
	PrimarySuccess         int64   `json:"primary_success"`
	PrimaryFailure         int64   `json:"primary_failure"`
	StreamSuccess          int64   `json:"stream_success"`
	StreamFailure          int64   `json:"stream_failure"`
	ConsecutiveStreamFails int64   `json:"consecutive_stream_fails"`
	UnknownStreak          int64   `json:"unknown_streak"`
	LastUnknownAt          int64   `json:"last_unknown_at"`
	RateLimited            int64   `json:"rate_limited"`
	AdmissionHTTPSuccess   int64   `json:"admission_http_success"`
	AdmissionHTTPFailure   int64   `json:"admission_http_failure"`
	AdmissionValidSuccess  int64   `json:"admission_valid_success"`
	AdmissionValidFailure  int64   `json:"admission_valid_failure"`
	AdmissionHTTPBuckets   int64   `json:"admission_http_buckets"`
	AdmissionValidBuckets  int64   `json:"admission_valid_buckets"`
	BucketStart            int64   `json:"bucket_start"`
	HTTPSeen               int64   `json:"bucket_http_seen"`
	ValidSeen              int64   `json:"bucket_valid_seen"`
	Alpha200               float64 `json:"alpha200"`
	Beta200                float64 `json:"beta200"`
	AlphaValid             float64 `json:"alpha_valid"`
	BetaValid              float64 `json:"beta_valid"`
	DecayedAt              int64   `json:"decayed_at"`
	TTFBEWMA               int64   `json:"ttfb_ewma_ns"`
	TotalEWMA              int64   `json:"total_ewma_ns"`
	TTFBSamples            int64   `json:"ttfb_samples"`
	LastSampleAt           int64   `json:"last_sample_at"`
	ConsecutiveFails       int64   `json:"consecutive_fails"`
	ConsecutiveSuccess     int64   `json:"consecutive_success"`
	OpenCount              int64   `json:"open_count"`
	OpenUntil              int64   `json:"open_until"`
	LastSuccessAt          int64   `json:"last_success_at"`
	LastFailureAt          int64   `json:"last_failure_at"`
	LastProbeAt            int64   `json:"last_probe_at"`
	LastReachableAt        int64   `json:"last_reachable_at"`
	WindowIndex            int64   `json:"window_index"`
	ActiveEpoch            int64   `json:"active_epoch"`
	EnvironmentEpoch       int64   `json:"environment_epoch"`
	PendingReverify        int64   `json:"pending_reverify"`
	ReverifySuccess        int64   `json:"reverify_success"`
	LastCheckSamples       int64   `json:"last_check_samples"`
	LastCheckValidSamples  int64   `json:"last_check_valid_samples"`
	HTTPCheckRound         int64   `json:"http_check_round"`
	ValidCheckRound        int64   `json:"valid_check_round"`
	NextProbeAt            int64   `json:"next_probe_at"`
	RecoveryStart          int64   `json:"recovery_start"`
	EpochSuccess           int64   `json:"epoch_success"`
	EpochFailure           int64   `json:"epoch_failure"`
	PrevState              string  `json:"prev_state"`
	LastStatus             int64   `json:"last_status"`
	NotFoundCount          int64   `json:"not_found_count"`
	LastNotFoundAt         int64   `json:"last_not_found_at"`
	HalfOpenSuccess        int64   `json:"half_open_success"`
	LastError              string  `json:"last_error"`
	StateVersion           int64   `json:"state_version"`
}

func decodeHealthColumns(scalars string, httpReservoir, validReservoir, windowBlob, latencyBlob *string, cfg health.Config) (health.KeyStats, error) {
	var row legacyHealthRow
	if err := json.Unmarshal([]byte(scalars), &row); err != nil {
		return health.KeyStats{}, err
	}
	httpRaw, err := decodeHex(httpReservoir)
	if err != nil {
		return health.KeyStats{}, err
	}
	validRaw, err := decodeHex(validReservoir)
	if err != nil {
		return health.KeyStats{}, err
	}
	windowRaw, err := decodeHex(windowBlob)
	if err != nil {
		return health.KeyStats{}, err
	}
	latencyRaw, err := decodeHex(latencyBlob)
	if err != nil {
		return health.KeyStats{}, err
	}
	httpSamples, err := health.DecodeReservoir(httpRaw, cfg.MaxSamplesPerBucket)
	if err != nil {
		return health.KeyStats{}, err
	}
	validSamples, err := health.DecodeReservoir(validRaw, cfg.MaxSamplesPerBucket)
	if err != nil {
		return health.KeyStats{}, err
	}
	window, err := health.DecodeWindow(windowRaw, cfg.EvidenceBucketSeconds)
	if err != nil {
		return health.KeyStats{}, err
	}
	window.Index = uint8(maxInt64(row.WindowIndex))
	histogram, err := health.DecodeLatencyHist(latencyRaw)
	if err != nil {
		return health.KeyStats{}, err
	}
	state := health.State(row.State)
	if state == "" {
		state = health.Candidate
	}
	return health.KeyStats{
		Key:                     health.HealthKey{Target: row.Target, Model: row.Model, Protocol: row.Protocol, Mode: row.Mode},
		ValidRequired:           row.ValidRequired != 0,
		HTTP200Success:          uint32(maxInt64(row.HTTP200Success)),
		HTTP200Failure:          uint32(maxInt64(row.HTTP200Failure)),
		ValidSuccess:            uint32(maxInt64(row.ValidSuccess)),
		ValidFailure:            uint32(maxInt64(row.ValidFailure)),
		PrimarySuccess:          uint32(maxInt64(row.PrimarySuccess)),
		PrimaryFailure:          uint32(maxInt64(row.PrimaryFailure)),
		StreamSuccess:           uint32(maxInt64(row.StreamSuccess)),
		StreamFailure:           uint32(maxInt64(row.StreamFailure)),
		ConsecutiveStreamFails:  uint8(maxInt64(row.ConsecutiveStreamFails)),
		UnknownStreak:           uint8(maxInt64(row.UnknownStreak)),
		LastUnknownAt:           unixNano(row.LastUnknownAt),
		RateLimited:             uint32(maxInt64(row.RateLimited)),
		AdmissionHTTP200Success: uint32(maxInt64(row.AdmissionHTTPSuccess)),
		AdmissionHTTP200Failure: uint32(maxInt64(row.AdmissionHTTPFailure)),
		AdmissionValidSuccess:   uint32(maxInt64(row.AdmissionValidSuccess)),
		AdmissionValidFailure:   uint32(maxInt64(row.AdmissionValidFailure)),
		AdmissionHTTPBuckets:    uint16(maxInt64(row.AdmissionHTTPBuckets)),
		AdmissionValidBuckets:   uint16(maxInt64(row.AdmissionValidBuckets)),
		BucketStart:             unixNano(row.BucketStart),
		HTTPSeen:                uint32(maxInt64(row.HTTPSeen)),
		ValidSeen:               uint32(maxInt64(row.ValidSeen)),
		HTTPReservoir:           httpSamples,
		ValidReservoir:          validSamples,
		HTTPCheckRound:          uint8(maxInt64(row.HTTPCheckRound)),
		ValidCheckRound:         uint8(maxInt64(row.ValidCheckRound)),
		Window:                  window,
		LastCheckSamples:        uint32(maxInt64(row.LastCheckSamples)),
		LastCheckValidSamples:   uint32(maxInt64(row.LastCheckValidSamples)),
		LastReachableAt:         unixNano(row.LastReachableAt),
		Beta200:                 health.BetaState{Alpha: row.Alpha200, Beta: row.Beta200, DecayedAt: unixNano(row.DecayedAt)},
		BetaValid:               health.BetaState{Alpha: row.AlphaValid, Beta: row.BetaValid, DecayedAt: unixNano(row.DecayedAt)},
		TTFBEWMA:                time.Duration(row.TTFBEWMA),
		TotalEWMA:               time.Duration(row.TotalEWMA),
		LatencyHist:             histogram,
		TTFBSamples:             uint32(maxInt64(row.TTFBSamples)),
		LastSampleAt:            unixNano(row.LastSampleAt),
		State:                   state,
		PrevState:               health.State(row.PrevState),
		EnvironmentEpoch:        uint64(maxInt64(row.EnvironmentEpoch)),
		PendingReverify:         row.PendingReverify != 0,
		ReverifySuccess:         uint8(maxInt64(row.ReverifySuccess)),
		ActiveEpoch:             uint32(maxInt64(row.ActiveEpoch)),
		ConsecutiveFails:        uint8(maxInt64(row.ConsecutiveFails)),
		ConsecutiveSuccess:      uint16(maxInt64(row.ConsecutiveSuccess)),
		OpenCount:               uint8(maxInt64(row.OpenCount)),
		OpenUntil:               unixNano(row.OpenUntil),
		RecoveryStart:           unixNano(row.RecoveryStart),
		EpochSuccess:            uint8(maxInt64(row.EpochSuccess)),
		EpochFailure:            uint8(maxInt64(row.EpochFailure)),
		HalfOpenSuccess:         uint8(maxInt64(row.HalfOpenSuccess)),
		LastSuccessAt:           unixNano(row.LastSuccessAt),
		LastFailureAt:           unixNano(row.LastFailureAt),
		LastProbeAt:             unixNano(row.LastProbeAt),
		NextProbeAt:             unixNano(row.NextProbeAt),
		LastStatus:              int(row.LastStatus),
		NotFoundCount:           uint8(maxInt64(row.NotFoundCount)),
		LastNotFoundAt:          unixNano(row.LastNotFoundAt),
		LastError:               row.LastError,
		StateVersion:            uint64(maxInt64(row.StateVersion)),
	}, nil
}

func decodeHex(value *string) ([]byte, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	return hex.DecodeString(*value)
}

func maxInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
