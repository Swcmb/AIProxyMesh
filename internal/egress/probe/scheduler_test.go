package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

func TestSchedulerDispatchesWithinBudgetAndSingleFlight(t *testing.T) {
	options := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 8080, Tag: "sched"}
	raw, _ := json.Marshal(options)
	entry := node.NewEntry(node.HashOptions(options), raw, options, time.Now(), 16)
	poolInstance := pool.New(16)
	poolInstance.Restore(entry, []string{"sub"}, true)
	key, _ := health.NewKey("https://opencode.ai/zen", "model", health.ProtocolChat, health.ModeStream)
	table := entry.HealthTable(4, 100)
	cfg := health.DefaultConfig()
	stats := health.NewKeyStats(key, cfg, time.Now())
	stats.Acquire()
	stats.State = health.Verifying
	stats.LastReachableAt = time.Now()
	stats.Release()
	table.Upsert(stats)

	scheduleCfg := ScheduleConfig{
		Enabled: true, BudgetPerSecond: 100, Burst: 10, DailyBudget: 100,
		MaxPerScan: 10, MaxInflight: 2, DefaultMode: "stream",
		VerifyingRecheck: time.Second, ActiveRecheck: time.Minute,
		DeadRecheck: time.Hour, Health: cfg, MaxKeysPerNode: 4,
	}
	scheduler := NewScheduler(poolInstance, scheduleCfg, nil)
	calls := 0
	scheduler.SetExecutor(func(_ context.Context, request CanaryRequest) CanaryResult {
		calls++
		return CanaryResult{Status: 200, Valid: true, ValidKnown: true, Latency: 50 * time.Millisecond}
	})
	now := time.Now()
	scheduler.scan(now)
	if scheduler.QueueDepth() != 1 {
		t.Fatalf("queue depth = %d", scheduler.QueueDepth())
	}
	if !scheduler.dispatchOne(context.Background(), now) {
		t.Fatal("due task was not dispatched")
	}
	if calls != 1 {
		t.Fatalf("executor calls = %d", calls)
	}
	if stats.StateOf() != health.Candidate {
		// A success on a VERIFYING key keeps it verifying until admission.
		if stats.StateOf() != health.Verifying {
			t.Fatalf("state = %s", stats.StateOf())
		}
	}
	// The task records LastProbeAt, so an immediate rescan must not re-enqueue.
	scheduler.scan(time.Now())
	if scheduler.QueueDepth() != 0 {
		t.Fatalf("task re-enqueued too early: %d", scheduler.QueueDepth())
	}
}

func TestSchedulerRespectsFrozenEnvironment(t *testing.T) {
	options := node.Options{Type: node.Direct, Tag: "direct"}
	raw, _ := json.Marshal(options)
	entry := node.NewEntry(node.HashOptions(options), raw, options, time.Now(), 16)
	poolInstance := pool.New(16)
	poolInstance.Restore(entry, []string{"sub"}, true)
	key, _ := health.NewKey("https://opencode.ai/zen", "model", health.ProtocolChat, health.ModeStream)
	stats := health.NewKeyStats(key, health.DefaultConfig(), time.Now())
	stats.Acquire()
	stats.State = health.Verifying
	stats.Release()
	entry.HealthTable(4, 100).Upsert(stats)

	scheduler := NewScheduler(poolInstance, ScheduleConfig{
		Enabled: true, BudgetPerSecond: 10, Burst: 4, MaxPerScan: 10,
		VerifyingRecheck: time.Second, ActiveRecheck: time.Minute, DeadRecheck: time.Hour,
		Health: health.DefaultConfig(), MaxKeysPerNode: 4,
	}, nil)
	calls := 0
	scheduler.SetExecutor(func(context.Context, CanaryRequest) CanaryResult {
		calls++
		return CanaryResult{Status: 200, Valid: true, ValidKnown: true}
	})
	env := health.NewEnvironment(health.EnvironmentConfig{FailureGrace: 0}, time.Now())
	env.SetActiveKeys(1)
	env.ReportTransportFailure("a", "https://target-a", time.Now())
	env.ReportTransportFailure("b", "https://target-a", time.Now())
	env.ReportTransportFailure("c", "https://target-b", time.Now())
	scheduler.SetEnvironment(env)
	scheduler.scan(time.Now())
	if scheduler.QueueDepth() != 0 {
		t.Fatalf("frozen environment queued %d tasks", scheduler.QueueDepth())
	}
	if calls != 0 {
		t.Fatalf("executor ran while frozen: %d", calls)
	}
}

func TestSchedulerFirstEnqueueGuarantee(t *testing.T) {
	poolInstance := pool.New(16)
	keys := make([]health.HealthKey, 0, 4)
	for i := 0; i < 4; i++ {
		key, _ := health.NewKey("https://opencode.ai/zen", fmt.Sprintf("m%d", i), health.ProtocolChat, health.ModeStream)
		keys = append(keys, key)
	}
	cfg := health.DefaultConfig()
	for n := 0; n < 10; n++ {
		options := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: uint16(19000 + n), Tag: fmt.Sprintf("n%d", n)}
		raw, _ := json.Marshal(options)
		entry := node.NewEntry(node.HashOptions(options), raw, options, time.Now(), 0)
		poolInstance.Restore(entry, []string{"sub"}, true)
		table := entry.HealthTable(4, 100)
		for i, key := range keys {
			stats := health.NewKeyStats(key, cfg, time.Now())
			stats.Acquire()
			if i == 3 {
				stats.State = health.Open
			} else {
				stats.State = health.Candidate
			}
			stats.LastReachableAt = time.Now()
			stats.Release()
			table.Upsert(stats)
		}
	}
	scheduler := NewScheduler(poolInstance, ScheduleConfig{
		Enabled: true, BudgetPerSecond: 1000, Burst: 100, MaxPerScan: 10, MaxKeys: 5,
		MaxInflight: 8, Health: cfg, MaxKeysPerNode: 4,
		VerifyingRecheck: time.Second, ActiveRecheck: time.Hour, DeadRecheck: time.Hour,
	}, nil)
	scheduler.SetExecutor(func(context.Context, CanaryRequest) CanaryResult { return CanaryResult{} })
	seen := map[string]struct{}{}
	scheduler.SetEnqueueHook(func(id string) { seen[id] = struct{}{} })
	now := time.Now()
	for i := 0; i < 30; i++ {
		now = now.Add(15 * time.Second)
		scheduler.ScanOnce(now)
	}
	if len(seen) != 40 {
		t.Fatalf("first-enqueue coverage = %d/40", len(seen))
	}
}
