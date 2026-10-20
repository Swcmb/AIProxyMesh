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

	if latency, ok := entry.LatencyFor("127.0.0.1"); !ok || latency <= 0 {
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
