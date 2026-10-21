package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "egress", "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func testSnapshot() Snapshot {
	created := time.Unix(1_700_000_000, 0).UTC()
	cooldown := created.Add(2 * time.Minute)
	return Snapshot{Nodes: []NodeRecord{
		{
			Hash:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Raw:           []byte(`{"type":"http","server":"1.1.1.1","server_port":8080}`),
			Enabled:       true,
			Failures:      2,
			CooldownUntil: cooldown,
			CreatedAt:     created,
			Subscriptions: []string{"sub-a", "legacy"},
			Latency: map[string]LatencyRecord{
				"api.example.com": {EWMA: 120 * time.Millisecond, UpdatedAt: created},
			},
			Upstream: map[string]UpstreamRecord{
				"api.example.com": {Success: 10, Failure: 1},
			},
		},
		{
			Hash:          "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Raw:           []byte(`{"type":"direct"}`),
			Enabled:       false,
			CreatedAt:     created,
			Subscriptions: []string{"legacy"},
			Latency:       map[string]LatencyRecord{},
			Upstream:      map[string]UpstreamRecord{},
		},
	}}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	want := testSnapshot()
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(got.Nodes))
	}
	byHash := map[string]NodeRecord{}
	for _, record := range got.Nodes {
		byHash[record.Hash] = record
	}
	first := byHash["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]
	if !first.Enabled || first.Failures != 2 {
		t.Fatalf("node = %+v", first)
	}
	if !first.CooldownUntil.Equal(want.Nodes[0].CooldownUntil) {
		t.Fatalf("cooldown = %v", first.CooldownUntil)
	}
	if len(first.Subscriptions) != 2 {
		t.Fatalf("subscriptions = %v", first.Subscriptions)
	}
	if latency := first.Latency["api.example.com"]; latency.EWMA != 120*time.Millisecond {
		t.Fatalf("latency = %v", latency)
	}
	if stats := first.Upstream["api.example.com"]; stats.Success != 10 || stats.Failure != 1 {
		t.Fatalf("upstream = %+v", stats)
	}
	second := byHash["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]
	if second.Enabled {
		t.Fatal("manual disable must persist")
	}
}

func TestSaveRemovesStaleNodes(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, testSnapshot()); err != nil {
		t.Fatal(err)
	}
	snapshot := testSnapshot()
	snapshot.Nodes = snapshot.Nodes[:1]
	if err := store.Save(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("stale node must be removed: %+v", got.Nodes)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	store, path := openTestStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, testSnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("reopen lost nodes: %d", len(got.Nodes))
	}
}

func TestEvents(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := 0; i < 3; i++ {
		event := Event{
			At:        base.Add(time.Duration(i) * time.Second),
			RequestID: "req",
			NodeHash:  "hash",
			NodeName:  "edge",
			Target:    "api.example.com",
			Kind:      "exit_failure",
			Status:    429,
			Attempt:   i + 1,
			Reason:    "exit_status_429",
		}
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.Events(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d", len(events))
	}
	if !events[0].At.Before(events[2].At) {
		t.Fatal("events must be returned oldest first")
	}
	if events[2].Status != 429 || events[2].Reason != "exit_status_429" {
		t.Fatalf("event = %+v", events[2])
	}
	if err := store.PruneEvents(ctx, 2); err != nil {
		t.Fatal(err)
	}
	events, err = store.Events(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("prune kept %d events", len(events))
	}
}

func TestEmptySnapshot(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, Snapshot{}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 0 {
		t.Fatalf("nodes = %d", len(got.Nodes))
	}
}
