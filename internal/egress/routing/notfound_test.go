package routing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

func notFoundFixture(t *testing.T) (*Router, *pool.Pool, health.HealthKey) {
	t.Helper()
	r, p := testRouter(t)
	env := health.NewEnvironment(health.EnvironmentConfig{TargetPause: 60 * time.Second}, time.Now())
	r.SetEnvironment(env)
	key, err := health.NewKey("https://api.example.com", "m", health.ProtocolChat, health.ModeStream)
	if err != nil {
		t.Fatal(err)
	}
	return r, p, key
}

func addNotFoundNode(t *testing.T, p *pool.Pool, port uint16, tag string) *node.Entry {
	t.Helper()
	opts := httpExit("127.0.0.1", port, tag)
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	entry.MarkHealthy()
	return entry
}

func TestNotFoundBecomesDeadWithOtherExitSuccess(t *testing.T) {
	r, p, key := notFoundFixture(t)
	now := time.Unix(60_000, 0)
	r.SetClock(func() time.Time { return now })
	other := addNotFoundNode(t, p, 18201, "other")
	otherTable := other.HealthTable(4, 100)
	otherStats := health.NewKeyStats(key, r.Config().Health, now)
	otherStats.Apply(health.Observation{Kind: health.OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true}, r.Config().Health, now)
	otherTable.Upsert(otherStats)
	r.Index().Upsert(key, other.Hash, health.Verifying, now)

	primary := addNotFoundNode(t, p, 18202, "primary")
	target := mustURL(t, "https://api.example.com/v1")
	exclude := map[string]struct{}{other.ID(): {}}
	report404 := func() {
		attempt, err := r.SelectKey(context.Background(), target, key, exclude, "")
		if err != nil {
			t.Fatal(err)
		}
		if attempt.NodeID != primary.ID() {
			t.Fatalf("selected %s", attempt.NodeName)
		}
		classification := attempt.Report(Outcome{Kind: NotFound, StatusCode: 404})
		if classification.NodeCooldown != nil {
			t.Fatal("404 must not cool the node")
		}
	}
	report404()
	if !envPaused(r, key.Target, now) {
		t.Fatal("first 404 should pause the target")
	}
	report404()
	stats := primary.HealthTable(4, 100).Get(key)
	if stats == nil || stats.StateOf() != health.Dead {
		t.Fatalf("state = %v", stats)
	}
}

func TestNotFoundWithoutOtherSuccessPausesOnly(t *testing.T) {
	r, p, key := notFoundFixture(t)
	now := time.Unix(70_000, 0)
	r.SetClock(func() time.Time { return now })
	entry := addNotFoundNode(t, p, 18203, "solo")
	target := mustURL(t, "https://api.example.com/v1")
	for i := 0; i < 2; i++ {
		attempt, err := r.SelectKey(context.Background(), target, key, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		attempt.Report(Outcome{Kind: NotFound, StatusCode: 404})
	}
	if entry.Cooling(now) {
		t.Fatal("404 must not cool the node")
	}
	stats := entry.HealthTable(4, 100).Get(key)
	if stats == nil || stats.StateOf() == health.Dead {
		t.Fatalf("without cross-exit success the key must not die")
	}
	if !envPaused(r, key.Target, now) {
		t.Fatal("target should be paused")
	}
}

func TestNotFoundCountResetsOnOtherOutcome(t *testing.T) {
	r, p, key := notFoundFixture(t)
	now := time.Unix(80_000, 0)
	r.SetClock(func() time.Time { return now })
	entry := addNotFoundNode(t, p, 18204, "reset")
	target := mustURL(t, "https://api.example.com/v1")
	attempt, err := r.SelectKey(context.Background(), target, key, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	attempt.Report(Outcome{Kind: NotFound, StatusCode: 404})
	if got := entry.HealthTable(4, 100).Get(key).Snapshot().NotFoundCount; got != 1 {
		t.Fatalf("not found count = %d", got)
	}
	attempt, err = r.SelectKey(context.Background(), target, key, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	attempt.Report(Outcome{Kind: Success, StatusCode: 200})
	if got := entry.HealthTable(4, 100).Get(key).Snapshot().NotFoundCount; got != 0 {
		t.Fatalf("not found count did not reset: %d", got)
	}
}

func envPaused(r *Router, target string, now time.Time) bool {
	env := r.environment.Load()
	return env != nil && env.TargetPaused(target, now)
}
