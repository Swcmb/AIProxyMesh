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
	records, err := store.LoadHealthKeys(ctx)
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
	records, err := store.LoadHealthKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Stats.State != health.Active {
		t.Fatalf("older write overwrote newer state: %+v", records)
	}
}
