package egress

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
)

func boolPtr(v bool) *bool { return &v }

func testEgressConfig(t *testing.T) *config.EgressConfig {
	t.Helper()
	return &config.EgressConfig{
		Enabled:             true,
		ImportLegacyProxies: boolPtr(true),
		Schedule: config.EgressSchedule{
			HealthWeight:          0.30,
			LatencyWeight:         0.15,
			RoutingWeight:         0.15,
			UpstreamSuccessWeight: 0.40,
		},
		Protocols: config.EgressProtocols{
			HTTP: boolPtr(true), HTTPS: boolPtr(true), SOCKS5: boolPtr(true), SOCKS5H: boolPtr(true),
		},
		Cooldown:          config.EgressCooldown{BaseSeconds: 30, MaxSeconds: 900, RecoveryProbe: boolPtr(true)},
		Failover:          config.EgressFailover{HonorRetryAfter: boolPtr(true)},
		Probe:             config.EgressProbe{IntervalSeconds: 60, TimeoutSeconds: 1, Concurrency: 4},
		StatePath:         filepath.Join(t.TempDir(), "egress.db"),
		MaxLatencyEntries: 16,
	}
}

func testOptions(t *testing.T, cfg *config.EgressConfig, legacy string) Options {
	t.Helper()
	return Options{
		Egress:   cfg,
		Upstream: config.UpstreamConfig{Zen: "https://api.example.com/zen", Go: "https://api.example.com/go", Anonymous: "https://api.example.com/inference"},
		Legacy:   legacy,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestEngineRestoresAndSelects(t *testing.T) {
	cfg := testEgressConfig(t)
	engine, err := New(testOptions(t, cfg, "http://1.1.1.1:8080#a\ndirect"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	engine.RefreshAll(context.Background())
	if engine.Pool().Len() != 2 {
		t.Fatalf("pool = %d", engine.Pool().Len())
	}
	target, _ := url.Parse("https://api.example.com/v1/chat")
	attempt, err := engine.Router().Select(context.Background(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := attempt.Report(routing.Outcome{Kind: routing.Success, Latency: 80 * time.Millisecond})
	if result.Kind != routing.Success {
		t.Fatalf("classification = %+v", result)
	}
	snapshot := engine.Snapshot()
	if !snapshot.Enabled || snapshot.Counts.Total != 2 || snapshot.Counts.Eligible != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(snapshot.Subscriptions) != 1 || snapshot.Subscriptions[0].ID != "legacy" {
		t.Fatalf("subscriptions = %+v", snapshot.Subscriptions)
	}
}

func TestEnginePersistsNodeState(t *testing.T) {
	cfg := testEgressConfig(t)
	path := cfg.StatePath
	engine, err := New(testOptions(t, cfg, "http://1.1.1.1:8080#a\ndirect"))
	if err != nil {
		t.Fatal(err)
	}
	engine.RefreshAll(context.Background())
	var target *url.URL
	for _, entry := range engine.Pool().List() {
		if entry.Options.Type == "http" {
			target, _ = url.Parse("https://api.example.com/x")
			entry.SetEnabled(false)
			entry.RestoreCooldown(2, time.Now().Add(5*time.Minute).UnixNano())
		}
	}
	if target == nil {
		t.Fatal("setup: http node missing")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(testOptions(t, cfg, "http://1.1.1.1:8080#a\ndirect"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Pool().Len() != 2 {
		t.Fatalf("restored pool = %d (db %s)", reopened.Pool().Len(), path)
	}
	var found bool
	for _, entry := range reopened.Pool().List() {
		if entry.Options.Type != "http" {
			continue
		}
		found = true
		if entry.Enabled() {
			t.Fatal("manual disable must survive restart")
		}
		if entry.Failures() != 2 || !entry.Cooling(time.Now()) {
			t.Fatalf("cooldown must survive restart: failures=%d cooldown=%d", entry.Failures(), entry.CooldownUntil())
		}
	}
	if !found {
		t.Fatal("http node missing after restore")
	}
}

func TestEngineApplyUpdatesProtocols(t *testing.T) {
	cfg := testEgressConfig(t)
	engine, err := New(testOptions(t, cfg, "http://1.1.1.1:8080#a\ndirect"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	engine.RefreshAll(context.Background())

	updated := *cfg
	updated.Protocols = config.EgressProtocols{
		HTTP: boolPtr(false), HTTPS: boolPtr(true), SOCKS5: boolPtr(true), SOCKS5H: boolPtr(true),
	}
	engine.Apply(&updated, config.UpstreamConfig{Zen: "https://api.example.com/zen"}, "http://1.1.1.1:8080#a\ndirect")

	target, _ := url.Parse("https://api.example.com/v1")
	for i := 0; i < 20; i++ {
		attempt, err := engine.Router().Select(context.Background(), target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Protocol == "http" {
			t.Fatal("disabled protocol must not be selected")
		}
		attempt.Report(routing.Outcome{Kind: routing.Success})
	}
}

func TestEngineDisabled(t *testing.T) {
	if _, err := New(Options{}); err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	cfg := testEgressConfig(t)
	cfg.Enabled = false
	if _, err := New(testOptions(t, cfg, "")); err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestEngineImportDedupProbeSmoke(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct\n"))
	}))
	defer feed.Close()

	cfg := testEgressConfig(t)
	cfg.Subscriptions = []config.EgressSubscription{{ID: "remote", Name: "remote", URL: feed.URL, Enabled: true, RefreshSeconds: 3600}}
	opts := testOptions(t, cfg, "direct")
	opts.Upstream = config.UpstreamConfig{Zen: upstream.URL}
	engine, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	engine.RefreshAll(context.Background())
	if engine.Pool().Len() != 1 {
		t.Fatalf("deduplicated pool = %d", engine.Pool().Len())
	}
	entry := engine.Pool().List()[0]
	if refs := entry.SubscriptionIDs(); len(refs) != 2 {
		t.Fatalf("references = %v", refs)
	}

	engine.ProbeAll(context.Background())
	if latency, ok := entry.LatencyFor("127.0.0.1"); !ok || latency <= 0 {
		t.Fatalf("probe latency = %v %v", latency, ok)
	}
	target, _ := url.Parse(upstream.URL + "/v1/chat")
	attempt, err := engine.Router().Select(context.Background(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Report(routing.Outcome{Kind: routing.Success})
	snapshot := engine.Snapshot()
	if snapshot.Counts.Total != 1 || snapshot.Counts.Eligible != 1 {
		t.Fatalf("counts = %+v", snapshot.Counts)
	}
	if len(snapshot.Subscriptions) != 2 {
		t.Fatalf("subscriptions = %+v", snapshot.Subscriptions)
	}
}
