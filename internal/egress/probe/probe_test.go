package probe

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

func newManager(t *testing.T, targets []string) (*Manager, *pool.Pool) {
	t.Helper()
	p := pool.New(8)
	manager := outbound.NewManager(outbound.DefaultConfig())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := DefaultConfig()
	cfg.Targets = targets
	cfg.Timeout = 2 * time.Second
	return New(p, manager, cfg, logger), p
}

func directEntry(p *pool.Pool) *node.Entry {
	opts := node.Options{Type: node.Direct, Tag: "direct"}
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("legacy", pool.Input{Options: opts, Raw: raw})
	return entry
}

func TestProbeRecordsLatencyAndHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager, p := newManager(t, []string{server.URL})
	entry := directEntry(p)
	manager.ProbeEntry(context.Background(), entry)

	if latency, ok := entry.LatencyFor("127.0.0.1"); !ok || latency < 0 {
		t.Fatalf("probe must record latency, got %v %v", latency, ok)
	}
	if score := entry.HealthScore(); score <= 0.5 {
		t.Fatalf("probe success must raise health, got %f", score)
	}
	if entry.LastProbeAt() == 0 {
		t.Fatal("probe timestamp missing")
	}
}

func TestProbeCountsHTTPStatusAsReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	manager, p := newManager(t, []string{server.URL})
	entry := directEntry(p)
	before := entry.HealthScore()
	manager.ProbeEntry(context.Background(), entry)
	if entry.HealthScore() <= before {
		t.Fatalf("a 429 response still proves the exit is reachable, health=%f", entry.HealthScore())
	}
}

func TestProbeTransportFailure(t *testing.T) {
	manager, p := newManager(t, []string{"http://127.0.0.1:1"})
	entry := directEntry(p)
	before := entry.HealthScore()
	manager.ProbeEntry(context.Background(), entry)
	if entry.HealthScore() >= before {
		t.Fatalf("connection failure must lower health, health=%f", entry.HealthScore())
	}
	if entry.LastError() == "" {
		t.Fatal("probe failure must be recorded")
	}
}

func TestProbeIDsStaysBoundedToRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	manager, p := newManager(t, []string{server.URL})
	optsA := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 1}
	optsB := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 2}
	rawA, _ := json.Marshal(optsA)
	rawB, _ := json.Marshal(optsB)
	entryA, _ := p.Add("legacy", pool.Input{Options: optsA, Raw: rawA})
	entryB, _ := p.Add("legacy", pool.Input{Options: optsB, Raw: rawB})
	other := directEntry(p)

	probed := manager.ProbeIDs(context.Background(), []string{
		entryA.ID(), entryB.ID(), "not-a-hash", "00000000000000000000000000000000",
	})
	if probed != 2 {
		t.Fatalf("probed = %d, want 2", probed)
	}
	if entryA.LastProbeAt() == 0 || entryB.LastProbeAt() == 0 {
		t.Fatal("requested nodes must be probed")
	}
	if other.LastProbeAt() != 0 {
		t.Fatal("nodes outside the requested batch must not be probed")
	}
}

func TestProbeAllSkipsUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	manager, p := newManager(t, []string{server.URL})
	opts := node.Options{Type: "ss", RawType: "ss", Server: "1.1.1.1", Port: 8388, Tag: "ss"}
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("legacy", pool.Input{Options: opts, Raw: raw})
	manager.ProbeAll(context.Background())
	if entry.LastProbeAt() != 0 {
		t.Fatal("unsupported nodes must not be probed")
	}
}

func TestConfigureNormalizesTargets(t *testing.T) {
	manager, _ := newManager(t, nil)
	manager.Configure(Config{
		Targets:     []string{"https://api.example.com/v1/chat", "https://api.example.com/other", "not a url"},
		Interval:    0,
		Concurrency: 0,
	})
	cfg := *manager.cfg.Load()
	if len(cfg.Targets) != 1 || cfg.Targets[0] != "https://api.example.com" {
		t.Fatalf("targets = %v", cfg.Targets)
	}
	if cfg.Interval != time.Minute || cfg.Concurrency != 8 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestParseEgressIP(t *testing.T) {
	if got := parseEgressIP([]byte("fl=abc\nip=203.0.113.7\nts=1\n")); got != "203.0.113.7" {
		t.Fatalf("ip = %q", got)
	}
	if got := parseEgressIP([]byte("ip=not-an-ip\n")); got != "" {
		t.Fatalf("invalid ip must be rejected, got %q", got)
	}
}

func TestProbeEgressIPRecordsAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fl=test\nip=203.0.113.7\n"))
	}))
	defer server.Close()

	manager, p := newManager(t, nil)
	manager.Configure(Config{
		EgressIPEnabled:  true,
		EgressIPInterval: time.Hour,
		EgressIPTarget:   server.URL,
		Timeout:          time.Second,
		Concurrency:      1,
	})
	entry := directEntry(p)
	manager.ProbeEgressIP(context.Background(), entry)
	if entry.EgressIP() != "203.0.113.7" {
		t.Fatalf("egress ip = %q", entry.EgressIP())
	}
	if entry.LastEgressAttempt() == 0 {
		t.Fatal("attempt timestamp must advance")
	}
	if !entry.EverHealthy() {
		t.Fatal("a successful trace must admit the node")
	}
}

func TestEnqueuePrioritizesPendingNodes(t *testing.T) {
	manager, p := newManager(t, []string{"http://127.0.0.1:1"})
	opts := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 1}
	raw, _ := json.Marshal(opts)
	pending, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	other := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 2}
	otherRaw, _ := json.Marshal(other)
	healthy, _ := p.Add("sub", pool.Input{Options: other, Raw: otherRaw})
	healthy.MarkHealthy()

	ctx := context.Background()
	manager.enqueue(ctx, job{entry: pending, ctx: ctx})
	manager.enqueue(ctx, job{entry: healthy, ctx: ctx})
	if len(manager.highCh) != 1 || len(manager.normalCh) != 1 {
		t.Fatalf("priority routing: high=%d normal=%d", len(manager.highCh), len(manager.normalCh))
	}
}

func TestProbeBatchFastFiltersUnreachable(t *testing.T) {
	manager, p := newManager(t, []string{"http://127.0.0.1:1"})
	direct := directEntry(p)
	opts := node.Options{Type: node.HTTP, Server: "127.0.0.1", Port: 1}
	raw, _ := json.Marshal(opts)
	dead, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})

	var lastDone, lastTotal int
	probed := manager.ProbeBatchFast(context.Background(), []string{direct.ID(), dead.ID()}, func(done, total int) {
		lastDone, lastTotal = done, total
	})
	if lastDone != 2 || lastTotal != 2 {
		t.Fatalf("progress = %d/%d", lastDone, lastTotal)
	}
	if probed != 1 {
		t.Fatalf("probed = %d, want 1 (only the direct exit passes precheck)", probed)
	}
	if dead.LastProbeAt() == 0 {
		t.Fatal("a TCP-precheck failure must still record a probe attempt")
	}
}

func TestScanIntervalStaysInResinWindow(t *testing.T) {
	for i := 0; i < 100; i++ {
		interval := scanInterval()
		if interval < 13*time.Second || interval > 17*time.Second {
			t.Fatalf("scan interval = %s", interval)
		}
	}
}
