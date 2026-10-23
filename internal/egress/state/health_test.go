package state

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

func TestHealthKeysAndEnvironmentRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	options := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 8080, Tag: "test"}
	raw, _ := json.Marshal(options)
	hash := node.HashOptions(options)
	if err := store.Save(ctx, Snapshot{Nodes: []NodeRecord{{
		Hash: hash.String(), Raw: raw, Enabled: true, EverHealthy: true, CreatedAt: time.Now(),
	}}}); err != nil {
		t.Fatal(err)
	}

	key, _ := health.NewKey("https://opencode.ai/zen", "model-a", health.ProtocolChat, health.ModeStream)
	cfg := health.DefaultConfig()
	now := time.Unix(10_000, 0)
	stats := health.NewKeyStats(key, cfg, now)
	stats.Apply(health.Observation{Kind: health.OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true}, cfg, now)
	stats.Apply(health.Observation{Kind: health.OutcomeExitFailure, Status: 503}, cfg, now.Add(time.Second))
	stats.Acquire()
	stats.State = health.Suspect
	stats.StateVersion = 7
	stats.PendingReverify = true
	stats.EnvironmentEpoch = 3
	stats.Release()
	snapshot := stats.Snapshot()

	if err := store.SaveHealthKeys(ctx, []HealthRecord{{NodeHash: hash.String(), Stats: snapshot}}); err != nil {
		t.Fatal(err)
	}
	records, _, err := store.LoadHealthKeys(ctx, health.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	loaded := records[0].Stats
	if loaded.Key != key || loaded.State != health.Suspect || loaded.StateVersion != 7 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.HTTP200Success != 1 || loaded.HTTP200Failure != 1 || loaded.PrimaryFailure != 1 {
		t.Fatalf("counters lost: %+v", loaded)
	}
	if !loaded.PendingReverify || loaded.EnvironmentEpoch != 3 {
		t.Fatalf("environment fields lost: %+v", loaded)
	}

	environment := health.EnvironmentSnapshot{
		Epoch: 4, State: health.EnvDegraded, ProbeFailures: 2,
		BootstrapFailures: 1, EnvironmentFailures: 2,
		LastProbeAt: now, BootstrapLatency: 2 * time.Second, UpdatedAt: now,
	}
	if err := store.SaveEnvironment(ctx, environment); err != nil {
		t.Fatal(err)
	}
	loadedEnv, ok, err := store.LoadEnvironment(ctx)
	if err != nil || !ok {
		t.Fatalf("load environment: %v %v", err, ok)
	}
	if loadedEnv.Epoch != 4 || loadedEnv.State != health.EnvDegraded || loadedEnv.BootstrapLatency != 2*time.Second {
		t.Fatalf("environment = %+v", loadedEnv)
	}
}

func TestAccuracySampleIsUniquePerEpoch(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	options := node.Options{Type: node.Direct, Tag: "direct"}
	raw, _ := json.Marshal(options)
	hash := node.HashOptions(options)
	if err := store.Save(ctx, Snapshot{Nodes: []NodeRecord{{Hash: hash.String(), Raw: raw, Enabled: true, CreatedAt: time.Now()}}}); err != nil {
		t.Fatal(err)
	}
	key, _ := health.NewKey("https://opencode.ai/zen", "m", health.ProtocolChat, health.ModeStream)
	sample := AccuracySample{NodeHash: hash.String(), Key: key, ActiveEpoch: 3, RequestID: "r1", Success: true, At: time.Now()}
	if err := store.RecordAccuracySample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	sample.RequestID = "r2"
	sample.Success = false
	if err := store.RecordAccuracySample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	var count int
	var success int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(success) FROM egress_accuracy_samples`).Scan(&count, &success); err != nil {
		t.Fatal(err)
	}
	if count != 1 || success != 1 {
		t.Fatalf("accuracy cohort must keep the first request only: count=%d success=%d", count, success)
	}
}

func TestSaveHealthKeysKeepsNewerState(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	options := node.Options{Type: node.Direct, Tag: "direct"}
	raw, _ := json.Marshal(options)
	hash := node.HashOptions(options)
	if err := store.Save(ctx, Snapshot{Nodes: []NodeRecord{{Hash: hash.String(), Raw: raw, Enabled: true, CreatedAt: time.Now()}}}); err != nil {
		t.Fatal(err)
	}
	key, _ := health.NewKey("https://opencode.ai/zen", "m", health.ProtocolChat, health.ModeStream)
	stats := health.NewKeyStats(key, health.DefaultConfig(), time.Now())
	newer := stats.Snapshot()
	newer.State = health.Active
	newer.StateVersion = 5
	if err := store.SaveHealthKeys(ctx, []HealthRecord{{NodeHash: hash.String(), Stats: newer}}); err != nil {
		t.Fatal(err)
	}
	older := stats.Snapshot()
	older.State = health.Candidate
	older.StateVersion = 4
	if err := store.SaveHealthKeys(ctx, []HealthRecord{{NodeHash: hash.String(), Stats: older}}); err != nil {
		t.Fatal(err)
	}
	records, _, err := store.LoadHealthKeys(ctx, health.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Stats.State != health.Active {
		t.Fatalf("older write overwrote newer state: %+v", records)
	}
}

func seedHealthNode(t *testing.T, store *Store, tag string) (string, health.HealthKey) {
	t.Helper()
	ctx := context.Background()
	options := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 19000, Tag: tag}
	raw, _ := json.Marshal(options)
	hash := node.HashOptions(options)
	if err := store.Save(ctx, Snapshot{Nodes: []NodeRecord{{
		Hash: hash.String(), Raw: raw, Enabled: true, EverHealthy: true, CreatedAt: time.Now(),
	}}}); err != nil {
		t.Fatal(err)
	}
	key, _ := health.NewKey("https://opencode.ai/zen", tag, health.ProtocolChat, health.ModeStream)
	return hash.String(), key
}

func TestHealthKeysBLOBColumnsRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	nodeHash, key := seedHealthNode(t, store, "blob")
	cfg := health.DefaultConfig()
	now := time.Unix(100_000, 0)
	stats := health.NewKeyStats(key, cfg, now)
	stats.HTTP200Success = 120
	stats.HTTP200Failure = 3
	stats.ValidSuccess = 90
	stats.ValidFailure = 2
	stats.PrimarySuccess = 88
	stats.PrimaryFailure = 1
	stats.StreamSuccess = 50
	stats.StreamFailure = 1
	stats.ConsecutiveStreamFails = 1
	stats.UnknownStreak = 2
	stats.LastUnknownAt = now.Add(-time.Minute)
	stats.RateLimited = 4
	stats.AdmissionHTTP200Success = 30
	stats.AdmissionHTTP200Failure = 1
	stats.AdmissionValidSuccess = 20
	stats.AdmissionValidFailure = 1
	stats.AdmissionHTTPBuckets = 4
	stats.AdmissionValidBuckets = 3
	stats.BucketStart = now.Add(-2 * time.Minute)
	stats.HTTPSeen = 7
	stats.ValidSeen = 5
	stats.HTTPCheckRound = 2
	stats.ValidCheckRound = 1
	for _, sample := range []bool{true, false, true} {
		stats.HTTPReservoir.Add(sample)
	}
	for _, sample := range []bool{true, true} {
		stats.ValidReservoir.Add(sample)
	}
	for i := 0; i < 11; i++ {
		stats.Window.Add(i%4 != 0, now.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 4; i++ {
		stats.RateWindow.Add(i != 0, now.Add(time.Duration(i)*time.Minute))
	}
	stats.Beta200.Observe(true, now, cfg.DecayHalfLife)
	stats.Beta200.Observe(false, now, cfg.DecayHalfLife)
	stats.BetaValid.Observe(true, now, cfg.DecayHalfLife)
	stats.TTFBEWMA = 123 * time.Millisecond
	stats.TotalEWMA = 456 * time.Millisecond
	stats.TTFBSamples = 77
	stats.LatencyHist.Observe(120 * time.Millisecond)
	stats.LatencyHist.Observe(3 * time.Second)
	stats.LastSampleAt = now
	stats.State = health.Active
	stats.PrevState = health.Suspect
	stats.EnvironmentEpoch = 9
	stats.PendingReverify = true
	stats.ReverifySuccess = 2
	stats.ActiveEpoch = 5
	stats.ConsecutiveFails = 1
	stats.ConsecutiveSuccess = 12
	stats.OpenCount = 2
	stats.OpenUntil = now.Add(time.Hour)
	stats.RecoveryStart = now.Add(-time.Hour)
	stats.EpochSuccess = 3
	stats.EpochFailure = 1
	stats.HalfOpenSuccess = 2
	stats.LastSuccessAt = now
	stats.LastFailureAt = now.Add(-time.Minute)
	stats.LastProbeAt = now.Add(-2 * time.Minute)
	stats.LastReachableAt = now
	stats.NextProbeAt = now.Add(5 * time.Minute)
	stats.LastStatus = 404
	stats.NotFoundCount = 1
	stats.LastNotFoundAt = now
	stats.LastError = "boom"
	stats.StateVersion = 42

	if err := store.SaveHealthKeys(ctx, []HealthRecord{{NodeHash: nodeHash, Stats: stats.Snapshot()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE egress_health_keys SET payload_json = ''`); err != nil {
		t.Fatal(err)
	}
	records, loadStats, err := store.LoadHealthKeys(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if loadStats.Loaded != 1 || loadStats.Columns != 1 || loadStats.Skipped != 0 {
		t.Fatalf("stats = %+v", loadStats)
	}
	got := records[0].Stats
	if got.Key != key || got.State != health.Active || got.StateVersion != 42 {
		t.Fatalf("identity lost: %+v", got)
	}
	if got.HTTP200Success != 120 || got.PrimaryFailure != 1 || got.ValidSuccess != 90 {
		t.Fatalf("counters: %+v", got)
	}
	if got.AdmissionHTTPBuckets != 4 || got.HTTPCheckRound != 2 || got.LastCheckSamples != 0 {
		t.Fatalf("admission: %+v", got)
	}
	if len(got.HTTPReservoir.Samples) != 3 || len(got.ValidReservoir.Samples) != 2 {
		t.Fatalf("reservoir: %+v %+v", got.HTTPReservoir, got.ValidReservoir)
	}
	if got.Window.Index != stats.Window.Index || got.Window.Buckets != stats.Window.Buckets {
		t.Fatalf("window: %+v vs %+v", got.Window, stats.Window)
	}
	if got.RateWindow.Buckets != stats.RateWindow.Buckets {
		t.Fatalf("rate window: %+v vs %+v", got.RateWindow, stats.RateWindow)
	}
	if got.LatencyHist != stats.LatencyHist {
		t.Fatalf("histogram: %+v vs %+v", got.LatencyHist, stats.LatencyHist)
	}
	if got.Beta200.Alpha != stats.Beta200.Alpha || got.BetaValid.Beta != stats.BetaValid.Beta {
		t.Fatalf("beta: %+v %+v", got.Beta200, got.BetaValid)
	}
	if got.PrevState != health.Suspect || !got.PendingReverify || got.EnvironmentEpoch != 9 {
		t.Fatalf("state fields: %+v", got)
	}
	if got.LastStatus != 404 || got.NotFoundCount != 1 || got.LastError != "boom" {
		t.Fatalf("diagnostics: %+v", got)
	}
	if !got.OpenUntil.Equal(stats.OpenUntil) || !got.LastReachableAt.Equal(stats.LastReachableAt) {
		t.Fatalf("times: %+v", got)
	}
}

func TestHealthKeysMalformedBLOBSkipped(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	goodHash, goodKey := seedHealthNode(t, store, "good")
	badHash, badKey := seedHealthNode(t, store, "bad")
	cfg := health.DefaultConfig()
	now := time.Now()
	good := health.NewKeyStats(goodKey, cfg, now)
	bad := health.NewKeyStats(badKey, cfg, now)
	if err := store.SaveHealthKeys(ctx, []HealthRecord{
		{NodeHash: goodHash, Stats: good.Snapshot()},
		{NodeHash: badHash, Stats: bad.Snapshot()},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE egress_health_keys SET payload_json = ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE egress_health_keys SET bucket_http_reservoir = x'09' WHERE model = 'bad'`); err != nil {
		t.Fatal(err)
	}
	records, stats, err := store.LoadHealthKeys(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Loaded != 1 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(records) != 1 || records[0].Stats.Key.Model != "good" {
		t.Fatalf("records = %+v", records)
	}
}
