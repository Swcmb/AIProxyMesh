package routing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

func stagingRouter(t *testing.T) (*Router, *pool.Pool, *health.Environment) {
	t.Helper()
	r, p := testRouter(t)
	cfg := r.Config()
	cfg.FailureGrace = 60 * time.Second
	r.Configure(cfg)
	env := health.NewEnvironment(health.EnvironmentConfig{}, time.Now())
	r.SetEnvironment(env)
	return r, p, env
}

func TestPendingFailureCommitsAfterGrace(t *testing.T) {
	r, p, _ := stagingRouter(t)
	now := time.Unix(20_000, 0)
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")
	opts := httpExit("127.0.0.1", 18101, "pending")
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	entry.MarkHealthy()
	key, _ := health.NewKey("https://api.example.com", "m", health.ProtocolChat, health.ModeStream)
	attempt, err := r.SelectKey(context.Background(), u, key, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	classification := attempt.Report(Outcome{Kind: ProxyFailure, Err: errors.New("dial failed")})
	if classification.Reason != "transport_failure_pending" {
		t.Fatalf("reason = %s", classification.Reason)
	}
	if entry.Cooling(now) {
		t.Fatal("grace window must not cool immediately")
	}
	if pending, _ := r.ObservationStats(); pending != 1 {
		t.Fatalf("pending = %d", pending)
	}
	if committed, dropped := r.SweepPending(now.Add(30 * time.Second)); committed != 0 || dropped != 0 {
		t.Fatalf("early sweep = %d/%d", committed, dropped)
	}
	committed, dropped := r.SweepPending(now.Add(61 * time.Second))
	if committed != 1 || dropped != 0 {
		t.Fatalf("sweep = %d/%d", committed, dropped)
	}
	if !entry.Cooling(now.Add(61 * time.Second)) {
		t.Fatal("expired grace must cool the exit")
	}
	table := entry.ExistingHealthTable()
	if table == nil || table.Get(key) == nil {
		t.Fatal("committed failure must write key evidence")
	}
	if table.Get(key).Snapshot().PrimaryFailure != 1 {
		t.Fatalf("primary failure = %d", table.Get(key).Snapshot().PrimaryFailure)
	}
}

func TestPendingFailureDroppedWhenEnvironmentDegraded(t *testing.T) {
	r, p, env := stagingRouter(t)
	now := time.Unix(30_000, 0)
	r.SetClock(func() time.Time { return now })
	env.ReportBootstrapLatency(10*time.Second, now)
	if state, _ := env.State(); state != health.EnvDegraded {
		t.Fatalf("environment = %s", state)
	}
	u := mustURL(t, "https://api.example.com/v1")
	opts := httpExit("127.0.0.1", 18102, "degraded")
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	entry.MarkHealthy()
	key, _ := health.NewKey("https://api.example.com", "m", health.ProtocolChat, health.ModeStream)
	attempt, err := r.SelectKey(context.Background(), u, key, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	attempt.Report(Outcome{Kind: ProxyFailure, Err: errors.New("dial failed")})
	committed, dropped := r.SweepPending(now.Add(61 * time.Second))
	if committed != 0 || dropped != 1 {
		t.Fatalf("sweep = %d/%d", committed, dropped)
	}
	if entry.Cooling(now.Add(61 * time.Second)) {
		t.Fatal("environment-classified failure must not cool the node")
	}
	if pending, droppedTotal := r.ObservationStats(); pending != 0 || droppedTotal != 1 {
		t.Fatalf("stats = %d/%d", pending, droppedTotal)
	}
}

func TestBootstrapFailureIsNotStaged(t *testing.T) {
	r, p, _ := stagingRouter(t)
	now := time.Unix(40_000, 0)
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")
	opts := httpExit("127.0.0.1", 18103, "bootstrap")
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	entry.MarkHealthy()
	key, _ := health.NewKey("https://api.example.com", "m", health.ProtocolChat, health.ModeStream)
	attempt, err := r.SelectKey(context.Background(), u, key, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	classification := attempt.Report(Outcome{Kind: BootstrapFailure, Err: errors.New("bootstrap down")})
	if classification.Reason != "environment_failure" {
		t.Fatalf("reason = %s", classification.Reason)
	}
	if pending, _ := r.ObservationStats(); pending != 0 {
		t.Fatalf("bootstrap failures must not be staged: %d", pending)
	}
	if entry.Cooling(now) {
		t.Fatal("bootstrap failure must not cool the node")
	}
}

func TestPendingQueueOverflowCountsDrop(t *testing.T) {
	r, p, _ := stagingRouter(t)
	cfg := r.Config()
	cfg.MaxPendingObservations = 1
	r.Configure(cfg)
	now := time.Unix(50_000, 0)
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")
	key, _ := health.NewKey("https://api.example.com", "m", health.ProtocolChat, health.ModeStream)
	exclude := map[string]struct{}{}
	for i := 0; i < 2; i++ {
		opts := httpExit("127.0.0.1", uint16(18110+i), "overflow")
		raw, _ := json.Marshal(opts)
		entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
		entry.MarkHealthy()
		attempt, err := r.SelectKey(context.Background(), u, key, exclude, "")
		if err != nil {
			t.Fatal(err)
		}
		exclude[attempt.NodeID] = struct{}{}
		attempt.Report(Outcome{Kind: ProxyFailure, Err: errors.New("dial failed")})
	}
	pending, dropped := r.ObservationStats()
	if pending != 1 || dropped != 1 {
		t.Fatalf("stats = %d/%d", pending, dropped)
	}
}
