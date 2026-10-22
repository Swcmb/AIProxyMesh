//go:build live

package egress_test

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/probe"
)

// Run with:
//
//	MESH_LIVE_PRESETS=1 go test -tags live ./internal/egress \
//	  -run TestBench86kLive -v -timeout 120m
//	MESH_LIVE_PRESETS=1 MESH_LIVE_FULL_REFRESH=1 go test -tags live \
//	  ./internal/egress -run TestBench86kLive -v -timeout 120m
func TestBench86kLive(t *testing.T) {
	if os.Getenv("MESH_LIVE_PRESETS") != "1" {
		t.Skip("set MESH_LIVE_PRESETS=1 to run the 86k preset benchmark")
	}
	engine := benchEngine(t)
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	engine.Subscriptions().RefreshAll(ctx)
	pool := engine.Pool().List()
	if len(pool) < 50000 {
		t.Fatalf("pool = %d, want the preset lists", len(pool))
	}
	t.Logf("pool=%d nodes", len(pool))

	if os.Getenv("MESH_LIVE_FULL_REFRESH") == "1" {
		runFullRefresh(t, engine, len(pool))
	}

	now := time.Now()
	keys := benchKeys(t)
	if raw := os.Getenv("MESH_BENCH_KEYS"); raw != "" {
		count, err := strconv.Atoi(raw)
		if err == nil && count >= 1 && count <= len(keys) {
			keys = keys[:count]
		}
	}
	t.Logf("health keys per node=%d (total %d)", len(keys), len(pool)*len(keys))
	baselineHeap, baselineRSS := memorySnapshot()
	t.Logf("baseline heap=%s rss=%s", bytes(baselineHeap), bytes(baselineRSS))

	// Four shared keys per node: 344k KeyStats in total (86k × 4).
	for _, entry := range pool {
		entry.MarkHealthy()
		table := entry.HealthTable(4, 1000)
		for i, key := range keys {
			stats := health.NewKeyStats(key, health.DefaultConfig(), now)
			stats.Acquire()
			stats.State = []health.State{health.Active, health.Suspect, health.Verifying, health.Open}[i]
			stats.HTTP200Success = 120
			stats.PrimarySuccess = 118
			stats.LastReachableAt = now
			stats.LastSuccessAt = now
			stats.Release()
			table.Upsert(stats)
			engine.Router().Index().Upsert(key, entry.Hash, stats.StateOf(), now)
		}
	}
	runtime.GC()
	afterHeap, afterRSS := memorySnapshot()
	t.Logf("health+index heap delta=%s rss delta=%s (heap now=%s rss now=%s)",
		bytes(afterHeap-baselineHeap), bytes(afterRSS-baselineRSS), bytes(afterHeap), bytes(afterRSS))

	if os.Getenv("MESH_BENCH_MEMORY_ONLY") == "1" {
		return
	}
	benchSelection(t, engine, keys[0])
	benchFairness(t, engine, keys, len(pool)*len(keys))
}

// TestFullRefresh86kLive runs only the 86k fast discovery sweep.
func TestFullRefresh86kLive(t *testing.T) {
	if os.Getenv("MESH_LIVE_PRESETS") != "1" || os.Getenv("MESH_LIVE_FULL_REFRESH") != "1" {
		t.Skip("set MESH_LIVE_PRESETS=1 and MESH_LIVE_FULL_REFRESH=1")
	}
	engine := benchEngine(t)
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	engine.Subscriptions().RefreshAll(ctx)
	pool := engine.Pool().List()
	if len(pool) < 50000 {
		t.Fatalf("pool = %d", len(pool))
	}
	t.Logf("pool=%d nodes", len(pool))
	runFullRefresh(t, engine, len(pool))
}

func benchEngine(t *testing.T) *egress.Engine {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	statePath := filepath.Join(dir, "egress.db")
	body := `{
	  "server_keys": ["bench"],
	  "upstream": {
	    "zen": "https://opencode.ai/zen",
	    "go": "https://opencode.ai/zen/go",
	    "anonymous": "https://opencode.ai/inference"
	  },
	  "egress": {
	    "enabled": true,
	    "import_legacy_proxies": false,
	    "state_path": "` + filepath.ToSlash(statePath) + `",
	    "subscriptions": [
	      {"id":"api-proxy-pool-hproxy","name":"hproxy","url":"https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.csv","enabled":true},
	      {"id":"api-proxy-pool-proxifly","name":"proxifly","url":"https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt","enabled":true}
	    ],
	    "probe": {"interval_seconds": 3600, "timeout_seconds": 5, "concurrency": 8, "canary_enabled": false},
	    "health": {"mode": "prefer"}
	  }
	}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := egress.New(egress.Options{
		Egress: cfg.Egress, Performance: cfg.Performance, Upstream: cfg.Upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func benchKeys(t *testing.T) []health.HealthKey {
	t.Helper()
	specs := [][4]string{
		{"https://opencode.ai/inference", "bench-a", health.ProtocolChat, health.ModeStream},
		{"https://opencode.ai/inference", "bench-b", health.ProtocolChat, health.ModeStream},
		{"https://opencode.ai/inference", "bench-c", health.ProtocolResponses, health.ModeStream},
		{"https://opencode.ai/inference", "bench-d", health.ProtocolAnthropic, health.ModeJSON},
	}
	out := make([]health.HealthKey, 0, len(specs))
	for _, spec := range specs {
		key, err := health.NewKey(spec[0], spec[1], spec[2], spec[3])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, key)
	}
	return out
}

func runFullRefresh(t *testing.T, engine *egress.Engine, poolSize int) {
	t.Helper()
	before := processRSS()
	started := time.Now()
	job, ok := engine.StartBatchProbeAll()
	if !ok {
		t.Fatal("another batch probe is already running")
	}
	t.Logf("full refresh started: id=%s total=%d", job.ID, job.Total)
	timeout := 45 * time.Minute
	if raw := os.Getenv("MESH_LIVE_FULL_REFRESH_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			timeout = parsed
		}
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		status := engine.BatchProbeStatus()
		if !status.Running {
			everHealthy := 0
			for _, entry := range engine.Pool().List() {
				if entry.EverHealthy() {
					everHealthy++
				}
			}
			after := processRSS()
			t.Logf("full refresh done in %s: total=%d done=%d tcp_checked=%d tcp_reachable=%d http_probed=%d http_reachable=%d ever_healthy=%d rss_delta=%s",
				time.Since(started).Round(time.Second), status.Total, status.Done,
				status.TCPChecked, status.TCPReachable, status.HTTPProbed, status.HTTPReachable,
				everHealthy, bytes(after-before))
			if status.Done != poolSize {
				t.Logf("warning: done=%d pool=%d", status.Done, poolSize)
			}
			return
		}
		if time.Now().After(deadline) {
			engine.CancelBatchProbe()
			t.Fatalf("full refresh exceeded %s at %d/%d", timeout, status.Done, status.Total)
		}
		t.Logf("full refresh %d/%d tcp_reachable=%d http_probed=%d",
			status.Done, status.Total, status.TCPReachable, status.HTTPProbed)
		select {
		case <-ticker.C:
		case <-time.After(5 * time.Second):
		}
	}
}

func parseURL(raw string) (*url.URL, error) { return url.Parse(raw) }

func benchSelection(t *testing.T, engine *egress.Engine, key health.HealthKey) {
	t.Helper()
	router := engine.Router()
	cfg := router.Config()
	cfg.HealthMode = "prefer"
	cfg.FailureGrace = 0
	cfg.ExplorationRatio = 0.10
	cfg.CandidateExplorationRatio = 0.02
	cfg.ColdStartFallback = true
	router.Configure(cfg)
	target, err := parseURL("https://opencode.ai/inference")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 20000; i++ {
		attempt, err := router.SelectKey(ctx, target, key, nil, "")
		if err != nil {
			t.Fatalf("warmup: %v", err)
		}
		attempt.Release()
	}
	const iterations = 20000
	samples := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		started := time.Now()
		attempt, err := router.SelectKey(ctx, target, key, nil, "")
		elapsed := time.Since(started)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		attempt.Release()
		samples = append(samples, elapsed)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("SelectKey n=%d p50=%s p95=%s p99=%s max=%s",
		iterations, percentile(samples, 0.50), percentile(samples, 0.95),
		percentile(samples, 0.99), samples[len(samples)-1])
}

func benchFairness(t *testing.T, engine *egress.Engine, keys []health.HealthKey, totalKeys int) {
	t.Helper()
	cfg := probe.ScheduleConfig{
		Enabled: true, BudgetPerSecond: 1000, Burst: 1000, MaxPerScan: 2000,
		MaxKeys: 50000, MaxInflight: 64, Health: health.DefaultConfig(), MaxKeysPerNode: 4,
		VerifyingRecheck: time.Second, ActiveRecheck: time.Hour, DeadRecheck: time.Hour,
	}
	scheduler := probe.NewScheduler(engine.Pool(), cfg, nil)
	scheduler.SetExecutor(func(context.Context, probe.CanaryRequest) probe.CanaryResult {
		return probe.CanaryResult{}
	})
	var mu sync.Mutex
	seen := make(map[string]struct{}, totalKeys)
	byModel := make(map[string]map[string]struct{})
	scheduler.SetEnqueueHook(func(id string) {
		parts := strings.Split(id, "|")
		model := ""
		if len(parts) > 2 {
			model = parts[2]
		}
		mu.Lock()
		seen[id] = struct{}{}
		if byModel[model] == nil {
			byModel[model] = make(map[string]struct{})
		}
		byModel[model][id] = struct{}{}
		mu.Unlock()
	})
	now := time.Now()
	scheduler.SetClock(func() time.Time { return now })
	start := time.Now()
	for scan := 0; scan < 300; scan++ {
		now = now.Add(15 * time.Second)
		scheduler.ScanOnce(now)
	}
	mu.Lock()
	covered := len(seen)
	perModel := make(map[string]int, len(byModel))
	for model, ids := range byModel {
		perModel[model] = len(ids)
	}
	mu.Unlock()
	stats := scheduler.Stats()
	t.Logf("fairness per model: %v", perModel)
	t.Logf("fairness: scans=%d examined=%d enqueued=%d unique=%d/%d coverage=%.2f%% elapsed=%s",
		stats.Scans, stats.Examined, stats.Enqueued, covered, totalKeys,
		100*float64(covered)/float64(totalKeys), time.Since(start).Round(time.Second))
	if covered < totalKeys*99/100 {
		t.Fatalf("coverage %d/%d below 99%%", covered, totalKeys)
	}
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}

func memorySnapshot() (heap uint64, rss uint64) {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc, processRSS()
}

func processRSS() uint64 {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", os.Getpid()), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return 0
		}
		records, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
		if err != nil || len(records) == 0 || len(records[0]) < 5 {
			return 0
		}
		value := strings.TrimSpace(records[0][4])
		value = strings.TrimSuffix(value, "K")
		value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
		kb, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	case "linux":
		file, err := os.Open("/proc/self/statm")
		if err != nil {
			return 0
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		if !scanner.Scan() {
			return 0
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			return 0
		}
		pages, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return pages * uint64(os.Getpagesize())
	}
	return 0
}

func bytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := uint64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
