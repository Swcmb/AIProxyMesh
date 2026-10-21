package admin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
)

func boolPtr(v bool) *bool { return &v }

func testEngine(t *testing.T) *egress.Engine {
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
			StatePath:         filepath.Join(t.TempDir(), "state.db"),
			MaxLatencyEntries: 8,
		},
		Upstream: config.UpstreamConfig{Zen: "https://api.example.com/zen"},
		Legacy:   "http://1.1.1.1:8080#a\ndirect",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.RefreshAll(context.Background())
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func newTestServer(engine *egress.Engine) *Server {
	return &Server{egressProvider: func() *egress.Engine { return engine }}
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return body
}

func TestEgressHandlersDisabled(t *testing.T) {
	server := &Server{egressProvider: func() *egress.Engine { return nil }}
	recorder := httptest.NewRecorder()
	server.handleEgressSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/egress", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEgressSummaryAndNodes(t *testing.T) {
	engine := testEngine(t)
	server := newTestServer(engine)

	recorder := httptest.NewRecorder()
	server.handleEgressSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/egress", nil))
	body := decodeBody(t, recorder)
	if body["enabled"] != true {
		t.Fatalf("summary = %v", body)
	}
	counts := body["counts"].(map[string]any)
	if counts["total"].(float64) != 2 {
		t.Fatalf("counts = %v", counts)
	}

	recorder = httptest.NewRecorder()
	server.handleEgressSubscriptions(recorder, httptest.NewRequest(http.MethodGet, "/api/egress/subscriptions", nil))
	body = decodeBody(t, recorder)
	subs := body["subscriptions"].([]any)
	// The legacy proxies/proxyfile source feeds the pool but is not a
	// user-visible subscription.
	if len(subs) != 0 {
		t.Fatalf("subscriptions = %v", subs)
	}
}

func TestEgressToggleNode(t *testing.T) {
	engine := testEngine(t)
	server := newTestServer(engine)
	var nodeID string
	for _, item := range engine.Snapshot().Nodes {
		if item.Protocol == "http" {
			nodeID = item.ID
		}
	}
	if nodeID == "" {
		t.Fatal("setup: http node missing")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/egress/nodes/"+nodeID+"/enabled", strings.NewReader(`{"enabled":false}`))
	request.SetPathValue("id", nodeID)
	recorder := httptest.NewRecorder()
	server.handleEgressToggle(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	node := body["node"].(map[string]any)
	if node["enabled"] != false {
		t.Fatalf("node = %v", node)
	}
}

func TestEgressRefreshSubscription(t *testing.T) {
	engine := testEngine(t)
	server := newTestServer(engine)
	request := httptest.NewRequest(http.MethodPost, "/api/egress/subscriptions/legacy/refresh", nil)
	request.SetPathValue("id", "legacy")
	recorder := httptest.NewRecorder()
	server.handleEgressRefresh(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	sub := body["subscription"].(map[string]any)
	if sub["nodes"].(float64) != 2 {
		t.Fatalf("subscription = %v", sub)
	}
}

func TestEgressNodeDetailAndProbe(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	engine, err := egress.New(egress.Options{
		Egress: &config.EgressConfig{
			Enabled:             true,
			ImportLegacyProxies: boolPtr(true),
			Schedule:            config.EgressSchedule{HealthWeight: 0.30, LatencyWeight: 0.15, RoutingWeight: 0.15, UpstreamSuccessWeight: 0.40},
			Protocols:           config.EgressProtocols{HTTP: boolPtr(true), HTTPS: boolPtr(true), SOCKS5: boolPtr(true), SOCKS5H: boolPtr(true)},
			Cooldown:            config.EgressCooldown{BaseSeconds: 30, MaxSeconds: 900, RecoveryProbe: boolPtr(true)},
			Failover:            config.EgressFailover{HonorRetryAfter: boolPtr(true)},
			Probe:               config.EgressProbe{IntervalSeconds: 60, TimeoutSeconds: 2, Concurrency: 2},
			StatePath:           filepath.Join(t.TempDir(), "state.db"),
			MaxLatencyEntries:   8,
		},
		Upstream: config.UpstreamConfig{Zen: upstream.URL},
		Legacy:   "direct",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.RefreshAll(context.Background())
	t.Cleanup(func() { _ = engine.Close() })
	server := newTestServer(engine)

	nodes := engine.Snapshot().Nodes
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v", nodes)
	}
	id := nodes[0].ID

	request := httptest.NewRequest(http.MethodGet, "/api/egress/nodes/"+id, nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleEgressNodeDetail(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", recorder.Code, recorder.Body.String())
	}
	detail := decodeBody(t, recorder)["node"].(map[string]any)
	if detail["id"] != id {
		t.Fatalf("detail = %v", detail)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/egress/nodes/"+id+"/probe", nil)
	request.SetPathValue("id", id)
	recorder = httptest.NewRecorder()
	server.handleEgressProbe(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", recorder.Code, recorder.Body.String())
	}
	probed := decodeBody(t, recorder)["node"].(map[string]any)
	if probed["last_probe_at"] == nil {
		t.Fatalf("probe did not record a timestamp: %v", probed)
	}
}

func TestEgressDiagnose(t *testing.T) {
	engine := testEngine(t)
	server := newTestServer(engine)
	request := httptest.NewRequest(http.MethodGet, "/api/egress/diagnose?target=https://api.example.com/v1", nil)
	recorder := httptest.NewRecorder()
	server.handleEgressDiagnose(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	candidates := body["candidates"].([]any)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %v", candidates)
	}
}
