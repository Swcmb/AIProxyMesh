package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/rotation"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

const localTestKey = "sk-local-e2e-123456"

type fakeUpstream struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	paths  []string
	auth   []string
	uas    []string
	// reply decides the response for one request. Returning false falls back
	// to a successful chat completion.
	reply func(w http.ResponseWriter, r *http.Request, body []byte) bool
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	fake := &fakeUpstream{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		fake.mu.Lock()
		fake.bodies = append(fake.bodies, body)
		fake.paths = append(fake.paths, r.URL.Path)
		fake.auth = append(fake.auth, r.Header.Get("Authorization"))
		fake.uas = append(fake.uas, r.Header.Get("User-Agent"))
		reply := fake.reply
		fake.mu.Unlock()
		if reply != nil && reply(w, r, body) {
			return
		}
		writeChatCompletion(w, r, body, "hello from upstream")
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func writeChatCompletion(w http.ResponseWriter, r *http.Request, body []byte, text string) {
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	if stream, _ := payload["stream"].(bool); stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{"hello ", "from upstream"} {
			payload := map[string]any{
				"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "created": 1, "model": "test-model",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": chunk}}},
			}
			encoded, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	response := map[string]any{
		"id": "chatcmpl-e2e", "object": "chat.completion", "created": 1, "model": "test-model",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (f *fakeUpstream) snapshot() (bodies [][]byte, paths, auth []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.bodies...), append([]string(nil), f.paths...), append([]string(nil), f.auth...)
}

func e2eConfig(upstream string, keys []string) config.Config {
	cfg := testConfig(upstream, keys)
	cfg.ServerKeys = []string{localTestKey}
	cfg.Models.Protocols = map[string]string{"test-model": "chat"}
	return cfg
}

func postInference(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+localTestKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-opencode-session", "ses_e2e")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestGatewayEndToEndProtocols(t *testing.T) {
	fake := newFakeUpstream(t)
	cfg := e2eConfig(fake.server.URL, []string{"zen-key-e2e"})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.catalog.ReplaceWithCapabilities([]string{"test-model"}, nil, nil, nil, nil)
	handler := g.Handler()

	modelsRecorder := httptest.NewRecorder()
	modelsRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsRequest.Header.Set("Authorization", "Bearer "+localTestKey)
	handler.ServeHTTP(modelsRecorder, modelsRequest)
	if modelsRecorder.Code != http.StatusOK || !strings.Contains(modelsRecorder.Body.String(), "test-model") {
		t.Fatalf("model listing = %d %s", modelsRecorder.Code, modelsRecorder.Body.String())
	}

	chat := postInference(t, handler, "/v1/chat/completions", map[string]any{
		"model": "test-model",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "describe"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
			},
		}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}},
	})
	if chat.Code != http.StatusOK || !strings.Contains(chat.Body.String(), "hello from upstream") {
		t.Fatalf("chat = %d %s", chat.Code, chat.Body.String())
	}
	bodies, _, _ := fake.snapshot()
	last := string(bodies[len(bodies)-1])
	if !strings.Contains(last, "image_url") || !strings.Contains(last, `"tools"`) {
		t.Fatalf("multimodal/tools were dropped: %s", last)
	}

	responses := postInference(t, handler, "/v1/responses", map[string]any{
		"model": "test-model", "input": "hi",
	})
	if responses.Code != http.StatusOK || !strings.Contains(responses.Body.String(), "hello from upstream") {
		t.Fatalf("responses = %d %s", responses.Code, responses.Body.String())
	}

	anthropic := postInference(t, handler, "/v1/messages", map[string]any{
		"model": "test-model", "max_tokens": 32,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if anthropic.Code != http.StatusOK || !strings.Contains(anthropic.Body.String(), "hello from upstream") {
		t.Fatalf("anthropic = %d %s", anthropic.Code, anthropic.Body.String())
	}

	stream := postInference(t, handler, "/v1/chat/completions", map[string]any{
		"model": "test-model", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if stream.Code != http.StatusOK {
		t.Fatalf("stream status = %d %s", stream.Code, stream.Body.String())
	}
	if contentType := stream.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("stream content-type = %q", contentType)
	}
	if !strings.Contains(stream.Body.String(), "hello ") || !strings.Contains(stream.Body.String(), "[DONE]") {
		t.Fatalf("stream body = %s", stream.Body.String())
	}
	if _, paths, _ := fake.snapshot(); paths[len(paths)-1] != "/v1/chat/completions" {
		t.Fatalf("upstream path = %s", paths[len(paths)-1])
	}
	fake.mu.Lock()
	lastUA := ""
	if len(fake.uas) > 0 {
		lastUA = fake.uas[len(fake.uas)-1]
	}
	fake.mu.Unlock()
	if lastUA != httpx.UserAgent() {
		t.Fatalf("upstream user agent = %q, want %q", lastUA, httpx.UserAgent())
	}
}

func TestNonRetryableClientResponseStopsImmediately(t *testing.T) {
	fake := newFakeUpstream(t)
	fake.reply = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad request shape"}`)
		return true
	}
	cfg := e2eConfig(fake.server.URL, []string{"key-a", "key-b"})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.catalog.ReplaceWithCapabilities([]string{"test-model"}, nil, nil, nil, nil)
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, KeyTiers: []config.Tier{config.TierZen}}
	resp, err, used := g.doKeyUpstream(context.Background(), route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-400", Session: "ses-400"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %v", resp)
	}
	defer resp.Body.Close()
	if used != 1 {
		t.Fatalf("non-retryable 400 must not rotate keys, attempts = %d", used)
	}
	if _, _, auth := fake.snapshot(); len(auth) != 1 {
		t.Fatalf("upstream hits = %d", len(auth))
	}
}

func TestSystemOnePassesThroughVerbatim(t *testing.T) {
	fake := newFakeUpstream(t)
	fake.reply = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		if r.URL.Path != "/v1/systemone" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":[{"question_id":"q1","answer":"yes"}]}`))
		return true
	}
	cfg := e2eConfig(fake.server.URL, []string{"zen-key-e2e"})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.catalog.ReplaceWithCapabilities([]string{"sys-model"}, nil,
		map[config.Tier]map[string]wire.Protocol{config.TierZen: {"sys-model": wire.SystemOne}}, nil, nil)

	payload := map[string]any{"model": "sys-model", "state": map[string]any{"step": 1}, "questions": []any{map[string]any{"id": "q1"}}}
	recorder := postInference(t, g.Handler(), "/v1/systemone", payload)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"answer":"yes"`) {
		t.Fatalf("systemone = %d %s", recorder.Code, recorder.Body.String())
	}
	bodies, paths, _ := fake.snapshot()
	if len(paths) != 1 || paths[0] != "/v1/systemone" {
		t.Fatalf("paths = %v", paths)
	}
	var sent, want map[string]any
	_ = json.Unmarshal(bodies[0], &sent)
	encoded, _ := json.Marshal(payload)
	_ = json.Unmarshal(encoded, &want)
	got, _ := json.Marshal(sent)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("systemone body changed: got %s want %s", got, expected)
	}
}

func TestGatewayKeyRotation(t *testing.T) {
	fake := newFakeUpstream(t)
	fake.reply = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if strings.HasSuffix(r.Header.Get("Authorization"), "key-first") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"bad key"}`)
			return true
		}
		return false
	}
	cfg := e2eConfig(fake.server.URL, []string{"key-first", "key-second"})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.catalog.ReplaceWithCapabilities([]string{"test-model"}, nil, nil, nil, nil)

	// CursorFor hashes the session modulo the key count; pick a session that
	// starts at the failing key.
	session := ""
	for i := 0; i < 200; i++ {
		candidate := fmt.Sprintf("ses-rotation-%d", i)
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(candidate))
		if hash.Sum64()%2 == 0 {
			session = candidate
			break
		}
	}
	if session == "" {
		t.Fatal("no session mapped to the first key")
	}
	meta := &telemetry.RequestMeta{Request: "req-rotation"}
	ctx := telemetry.WithRequestMeta(context.Background(), meta)
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, KeyTiers: []config.Tier{config.TierZen}}
	resp, err, used := g.doKeyUpstream(ctx, route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-rotation", Session: session}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %v", resp)
	}
	defer resp.Body.Close()
	if used != 2 {
		t.Fatalf("attempts = %d", used)
	}
	_, _, auth := fake.snapshot()
	if len(auth) < 2 || !strings.HasSuffix(auth[0], "key-first") || !strings.HasSuffix(auth[1], "key-second") {
		t.Fatalf("auth chain = %v", auth)
	}
}

func TestAnonymousLaneUsesPublicCredential(t *testing.T) {
	fake := newFakeUpstream(t)
	cfg := testConfig(fake.server.URL, nil)
	cfg.Anonymous = true
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, Anonymous: true}
	resp, err, used := g.doAnonymousUpstream(context.Background(), route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[],"stream":false}`)},
		identity.RequestIDs{Request: "req-anon", Session: "ses-anon"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK || used != 1 {
		t.Fatalf("anonymous = %v used=%d", resp, used)
	}
	defer resp.Body.Close()
	_, _, auth := fake.snapshot()
	if len(auth) != 1 || auth[0] != "Bearer public" {
		t.Fatalf("anonymous credential = %v", auth)
	}
}

// --- egress fault injection ---

func forwardingProxy(t *testing.T, override func(w http.ResponseWriter, r *http.Request) bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		for key, values := range r.Header {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
		request.Header.Del("Proxy-Connection")
		response, err := http.DefaultTransport.RoundTrip(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(server.Close)
	return server
}

func egressGateway(t *testing.T, upstream string, legacy string) (*Gateway, *egress.Engine) {
	return egressGatewayNamed(t, upstream, legacy, "")
}

func egressGatewayNamed(t *testing.T, upstream string, legacy string, preferName string) (*Gateway, *egress.Engine) {
	t.Helper()
	base := testConfig(upstream, nil)
	engine := testEgressEngine(t, base, legacy)
	target := mustParseURL(t, upstream)
	var firstID string
	for _, entry := range engine.Pool().List() {
		if entry.Options.Type == node.Direct {
			continue
		}
		if preferName != "" && entry.Name() != preferName {
			continue
		}
		firstID = entry.ID()
		break
	}
	if firstID == "" {
		t.Fatalf("no proxy node %q in pool", preferName)
	}
	key := keyAffinityTo(t, engine, target, firstID)
	cfg := e2eConfig(upstream, []string{key})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.SetEgress(engine)
	return g, engine
}

func runKeyAttempt(t *testing.T, g *Gateway) (*http.Response, error, int, *telemetry.RequestMeta) {
	t.Helper()
	meta := &telemetry.RequestMeta{Request: "req-egress"}
	ctx := telemetry.WithRequestMeta(context.Background(), meta)
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, KeyTiers: []config.Tier{config.TierZen}}
	resp, err, used := g.doKeyUpstream(ctx, route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-egress", Session: "ses-egress"}, 0)
	return resp, err, used, meta
}

func TestModelCatalogRefreshUsesEgress(t *testing.T) {
	fake := newFakeUpstream(t)
	fake.reply = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if r.URL.Path != "/v1/models" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"catalog-model"}]}`)
		return true
	}
	var hits atomic.Int32
	proxy := forwardingProxy(t, func(w http.ResponseWriter, r *http.Request) bool {
		hits.Add(1)
		return false
	})
	engine := testEgressEngine(t, testConfig(fake.server.URL, nil), proxy.URL+"#refresh")
	cfg := e2eConfig(fake.server.URL, []string{"zen-key-refresh"})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.SetEgress(engine)

	models := g.refreshTier(context.Background(), fake.server.URL, g.zenNodes)
	if len(models) != 1 || models[0] != "catalog-model" {
		t.Fatalf("models = %v", models)
	}
	if hits.Load() == 0 {
		t.Fatal("catalog refresh did not use the egress proxy")
	}
	now := time.Now()
	for _, entry := range engine.Pool().List() {
		if entry.Cooling(now) {
			t.Fatalf("catalog refresh must not cool exit %s", entry.Name())
		}
	}
}

func TestFreeModelAvailabilityProbeUsesEgress(t *testing.T) {
	fake := newFakeUpstream(t)
	proxy := forwardingProxy(t, nil)
	engine := testEgressEngine(t, testConfig(fake.server.URL, nil), proxy.URL+"#probe")

	cfg := e2eConfig(fake.server.URL, []string{"zen-key-probe"})
	cfg.Models.Protocols = map[string]string{"test-free": "chat"}
	cfg.Reasoning.Effort = ""
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.SetEgress(engine)
	g.catalog.ReplaceWithCapabilities([]string{"test-free"}, nil, nil, nil, nil)

	result := g.probeFreeModel(context.Background(), "test-free")
	if !result.success {
		t.Fatalf("probe failed: %+v", result)
	}
	if result.channel != "zen" {
		t.Fatalf("probe channel = %q", result.channel)
	}
	if hits, _, _ := fake.snapshot(); len(hits) == 0 {
		t.Fatal("probe did not reach the upstream")
	}
	now := time.Now()
	for _, entry := range engine.Pool().List() {
		if entry.Cooling(now) {
			t.Fatalf("diagnostic probe must not cool exit %s", entry.Name())
		}
	}
}

func TestRotationFailoverUsesEgress(t *testing.T) {
	upstream := newFakeUpstream(t)
	upstream.reply = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		model, _ := payload["model"].(string)
		if model == config.SOTAModels[0] {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":"quota_exceeded"}`)
			return true
		}
		return false
	}
	proxyA := forwardingProxy(t, nil)
	proxyB := forwardingProxy(t, nil)
	engine := testEgressEngine(t, testConfig(upstream.server.URL, nil), proxyA.URL+"#a\n"+proxyB.URL+"#b")

	cfg := testConfig(upstream.server.URL, nil)
	cfg.ServerKeys = []string{localTestKey}
	cfg.GoKeys = []string{"go-key-rotation"}
	cfg.Models.Protocols = map[string]string{}
	for _, model := range config.SOTAModels {
		cfg.Models.Protocols[model] = "chat"
	}
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.SetEgress(engine)
	g.catalog.ReplaceWithCapabilities(nil, append([]string(nil), config.SOTAModels...), nil, nil, nil)
	manager, err := rotation.New("", cfg.Rotation)
	if err != nil {
		t.Fatal(err)
	}
	g.rotation = manager
	g.syncRotation()

	recorder := postInference(t, g.Handler(), "/v1/chat/completions", map[string]any{
		"model": "sota", "messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("rotation = %d %s", recorder.Code, recorder.Body.String())
	}
	if resolved := recorder.Header().Get("X-Resolved-Model"); resolved != config.SOTAModels[1] {
		t.Fatalf("resolved model = %q, want %q", resolved, config.SOTAModels[1])
	}
	var document map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document["model"] != "sota" {
		t.Fatalf("response model alias = %v", document["model"])
	}
	snapshot := manager.Snapshot()
	if snapshot.Groups[0].Current != config.SOTAModels[1] {
		t.Fatalf("rotation current = %s", snapshot.Groups[0].Current)
	}
	now := time.Now()
	cooling := 0
	for _, entry := range engine.Pool().List() {
		if entry.Cooling(now) {
			cooling++
		}
	}
	if cooling != 1 {
		t.Fatalf("429 exit must be cooling, cooling = %d", cooling)
	}
	hits, paths, _ := upstream.snapshot()
	if len(hits) != 2 || paths[0] != "/v1/chat/completions" {
		t.Fatalf("upstream hits = %d paths=%v", len(hits), paths)
	}

	stream := postInference(t, g.Handler(), "/v1/chat/completions", map[string]any{
		"model": "sota", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if stream.Code != http.StatusOK {
		t.Fatalf("rotation stream = %d %s", stream.Code, stream.Body.String())
	}
	if contentType := stream.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("rotation stream content-type = %q", contentType)
	}
	if !strings.Contains(stream.Body.String(), "hello ") || !strings.Contains(stream.Body.String(), "[DONE]") {
		t.Fatalf("rotation stream body = %s", stream.Body.String())
	}
}

func TestEgress429SwitchesExit(t *testing.T) {
	upstream := newFakeUpstream(t)
	limited := forwardingProxy(t, func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	})
	healthy := forwardingProxy(t, nil)
	g, engine := egressGatewayNamed(t, upstream.server.URL, limited.URL+"#limited\n"+healthy.URL+"#healthy", "limited")

	resp, err, used, meta := runKeyAttempt(t, g)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %v", resp)
	}
	defer resp.Body.Close()
	bodies, _, _ := upstream.snapshot()
	if used != 2 {
		t.Fatalf("attempts = %d, want 2 (exit=%s hits=%d)", used, meta.ExitNode, len(bodies))
	}
	if len(bodies) != 1 {
		t.Fatalf("upstream hits = %d, want 1", len(bodies))
	}
	now := time.Now()
	for _, entry := range engine.Pool().List() {
		if entry.Options.Type != node.Direct && entry.Cooling(now) {
			if entry.Name() != "limited" {
				t.Fatalf("unexpected node cooled: %s", entry.Name())
			}
			if until := time.Unix(0, entry.CooldownUntil()); until.Sub(now) < time.Second {
				t.Fatalf("Retry-After not applied: %v", until.Sub(now))
			}
		}
	}
}

func TestEgressUpstreamWideStopsWithoutCoolingNodes(t *testing.T) {
	upstream := newFakeUpstream(t)
	upstream.reply = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"down"}`)
		return true
	}
	proxyA := forwardingProxy(t, nil)
	proxyB := forwardingProxy(t, nil)
	g, engine := egressGateway(t, upstream.server.URL, proxyA.URL+"#a\n"+proxyB.URL+"#b")

	resp, err, used, _ := runKeyAttempt(t, g)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v", resp)
	}
	defer resp.Body.Close()
	if used != 2 {
		t.Fatalf("attempts = %d, want 2 before upstream consensus", used)
	}
	if hits, _, _ := upstream.snapshot(); len(hits) != 2 {
		t.Fatalf("upstream hits = %d", len(hits))
	}
	now := time.Now()
	for _, entry := range engine.Pool().List() {
		if entry.Cooling(now) {
			t.Fatalf("upstream-wide failure must not cool node %s", entry.Name())
		}
	}
}

func TestEgressAllNodesFailWithinBudget(t *testing.T) {
	upstream := newFakeUpstream(t)
	g, engine := egressGateway(t, upstream.server.URL, "http://127.0.0.1:1#dead-a\nhttp://127.0.0.1:2#dead-b")

	resp, err, used, _ := runKeyAttempt(t, g)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("all-dead exits must return an error")
	}
	if used > 3 {
		t.Fatalf("attempts = %d exceed budget 3", used)
	}
	if hits, _, _ := upstream.snapshot(); len(hits) != 0 {
		t.Fatalf("upstream must not be reached, hits = %d", len(hits))
	}
	now := time.Now()
	cooling := 0
	for _, entry := range engine.Pool().List() {
		if entry.Cooling(now) {
			cooling++
		}
	}
	if cooling < 2 {
		t.Fatalf("failed exits must be cooling, cooling = %d", cooling)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
