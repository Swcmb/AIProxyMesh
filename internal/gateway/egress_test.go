package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

func boolPtr(v bool) *bool { return &v }

func testConfig(zenURL string, zenKeys []string) config.Config {
	cfg := config.Config{
		Listen:     "127.0.0.1:0",
		ServerKeys: []string{"local-test-key"},
		ZenKeys:    zenKeys,
		Upstream: config.UpstreamConfig{
			Zen: zenURL, Go: zenURL, Anonymous: zenURL + "/anonymous",
		},
		Retry:  config.RetryConfig{MaxAttempts: 3, TimeoutSeconds: 10},
		Models: config.ModelsConfig{RefreshSeconds: 300, Protocols: map[string]string{}},
		Performance: config.PerformanceConfig{
			MaxIdleConns: 16, MaxIdleConnsPerHost: 8, IdleConnTimeoutSeconds: 30,
			ConnectTimeoutSeconds: 2, FailureCooldownSeconds: 15,
		},
		Logging: config.LoggingConfig{Level: "error", RingSize: 100},
		WebUI:   config.WebUIConfig{Listen: "127.0.0.1:0", SessionTTLMinutes: 60},
		Prefer:  config.TierZen,
	}
	cfg.Rotation.Defaults()
	cfg.Ranking.Defaults()
	return cfg
}

func testEgressEngine(t *testing.T, cfg config.Config, legacy string) *egress.Engine {
	t.Helper()
	engine, err := egress.New(egress.Options{
		Egress: &config.EgressConfig{
			Enabled:             true,
			ImportLegacyProxies: boolPtr(true),
			Schedule: config.EgressSchedule{
				HealthWeight: 0.30, LatencyWeight: 0.15, RoutingWeight: 0.15, UpstreamSuccessWeight: 0.40,
			},
			Protocols: config.EgressProtocols{
				HTTP: boolPtr(true), HTTPS: boolPtr(true), SOCKS5: boolPtr(true), SOCKS5H: boolPtr(true),
			},
			Cooldown:          config.EgressCooldown{BaseSeconds: 30, MaxSeconds: 900, RecoveryProbe: boolPtr(true)},
			Failover:          config.EgressFailover{HonorRetryAfter: boolPtr(true)},
			Probe:             config.EgressProbe{IntervalSeconds: 60, TimeoutSeconds: 1, Concurrency: 2},
			StatePath:         filepath.Join(t.TempDir(), "egress.db"),
			MaxLatencyEntries: 8,
		},
		Upstream:    cfg.Upstream,
		Performance: cfg.Performance,
		Legacy:      legacy,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.RefreshAll(context.Background())
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func TestClassifyEgressOutcome(t *testing.T) {
	ctx := context.Background()
	resp := func(status int) *http.Response { return &http.Response{StatusCode: status, Header: http.Header{}} }
	cases := []struct {
		name   string
		resp   *http.Response
		err    error
		direct bool
		want   routing.OutcomeKind
	}{
		{"success", resp(200), nil, false, routing.Success},
		{"unauthorized", resp(401), nil, false, routing.KeyFailure},
		{"forbidden", resp(403), nil, false, routing.KeyFailure},
		{"throttled", resp(429), nil, false, routing.ExitFailure},
		{"server", resp(503), nil, false, routing.UpstreamFailure},
		{"client", resp(400), nil, false, routing.Neutral},
		{"proxy transport", nil, io.ErrUnexpectedEOF, false, routing.ProxyFailure},
		{"direct transport", nil, io.ErrUnexpectedEOF, true, routing.UpstreamFailure},
	}
	for _, tc := range cases {
		got := classifyEgressOutcome(ctx, tc.resp, tc.err, tc.direct, true)
		if got.Kind != tc.want {
			t.Fatalf("%s: got %s want %s", tc.name, got.Kind, tc.want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got := classifyEgressOutcome(canceled, nil, context.Canceled, false, true); got.Kind != routing.ClientCancel {
		t.Fatalf("cancel = %s", got.Kind)
	}
}

func TestKeyUpstreamSingleBudgetLegacy(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	keys := []string{"key-1", "key-2", "key-3", "key-4"}
	cfg := testConfig(upstream.URL, keys)
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	ctx := telemetry.WithRequestMeta(context.Background(), &telemetry.RequestMeta{Request: "req-budget"})
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, KeyTiers: []config.Tier{config.TierZen}}
	resp, err, used := g.doKeyUpstream(ctx, route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-budget", Session: "session-budget"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	if used > cfg.Retry.MaxAttempts {
		t.Fatalf("attempts %d exceed budget %d", used, cfg.Retry.MaxAttempts)
	}
	if hits > cfg.Retry.MaxAttempts {
		t.Fatalf("upstream hits %d exceed budget %d", hits, cfg.Retry.MaxAttempts)
	}
	if hits != cfg.Retry.MaxAttempts || used != cfg.Retry.MaxAttempts {
		t.Fatalf("expected the full budget to be consumed exactly once each: used=%d hits=%d", used, hits)
	}
}

func TestEgressFailoverToHealthyExit(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL, nil)
	engine := testEgressEngine(t, cfg, "http://127.0.0.1:1#dead\ndirect")

	target, _ := url.Parse(upstream.URL)
	var deadID, aliveID string
	for _, entry := range engine.Pool().List() {
		if entry.Options.Type == "direct" {
			aliveID = entry.ID()
		} else {
			deadID = entry.ID()
		}
	}
	if deadID == "" || aliveID == "" {
		t.Fatalf("pool = %+v", engine.Snapshot().Nodes)
	}

	key := keyAffinityTo(t, engine, target, deadID)
	cfg.ZenKeys = []string{key}
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.SetEgress(engine)

	meta := &telemetry.RequestMeta{Request: "req-failover"}
	ctx := telemetry.WithRequestMeta(context.Background(), meta)
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, KeyTiers: []config.Tier{config.TierZen}}
	resp, err, used := g.doKeyUpstream(ctx, route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-failover", Session: "session-failover"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		t.Fatalf("status = %d", status)
	}
	defer resp.Body.Close()
	if used != 2 {
		t.Fatalf("attempts = %d, want 2 (dead exit then direct)", used)
	}
	if upstreamHits != 1 {
		t.Fatalf("upstream hits = %d, want 1", upstreamHits)
	}
	if meta.ExitNode == "" || meta.Attempts != 2 {
		t.Fatalf("telemetry meta = %+v", meta)
	}
	deadHash, _ := node.ParseHex(deadID)
	if entry := engine.Pool().Get(deadHash); entry == nil || !entry.Cooling(time.Now()) {
		t.Fatal("failed exit must be cooling")
	}
	aliveHash, _ := node.ParseHex(aliveID)
	if entry := engine.Pool().Get(aliveHash); entry == nil || entry.Failures() != 0 {
		t.Fatal("healthy exit must not be penalized")
	}
}

func keyAffinityTo(t *testing.T, engine *egress.Engine, target *url.URL, wantID string) string {
	t.Helper()
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("sk-affinity-%d", i)
		keyID := config.KeyDisplayID(key)
		stable := true
		// A forced affinity returns the preferred node every time. A key that
		// merely won one weighted draw fails this stability check.
		for n := 0; n < 20; n++ {
			attempt, err := engine.Router().SelectAffinity(context.Background(), target, nil, keyID)
			if err != nil {
				t.Fatal(err)
			}
			got := attempt.NodeID
			attempt.Release()
			if got != wantID {
				stable = false
				break
			}
		}
		if stable {
			return key
		}
	}
	t.Fatalf("no stable affinity for %s", wantID)
	return ""
}
