package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

func TestIsDeterministicClientResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"context length", 400, `{"error":{"code":"context_length_exceeded"}}`, true},
		{"invalid request type", 400, `{"error":{"type":"invalid_request_error"}}`, true},
		{"maximum context text", 422, `{"message":"maximum context length is 128000"}`, true},
		{"prompt too long", 400, `{"message":"Prompt is too long"}`, true},
		{"generic bad request", 400, `{"error":"bad json"}`, false},
		{"unauthorized is credential-specific", 401, `{"error":{"type":"invalid_request_error"}}`, false},
		{"forbidden may be the exit", 403, `{"error":{"type":"invalid_request_error"}}`, false},
		{"server error is retryable", 500, `{"error":"overloaded"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeterministicClientResponse(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("status %d body %q = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

func TestPeekResponseBodyKeepsContent(t *testing.T) {
	const payload = `{"error":{"code":"context_length_exceeded"}}`
	resp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(payload))}
	if !peekDeterministic(resp) {
		t.Fatal("the response must classify as a deterministic rejection")
	}
	if got := peekResponseBody(resp); string(got) != payload {
		t.Fatalf("second peek = %q", got)
	}
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != payload {
		t.Fatalf("body after peeking = %q", rest)
	}
	// A 200 stream must never be peeked.
	ok := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data"))}
	if peekDeterministic(ok) {
		t.Fatal("a successful stream is not a rejection")
	}
}

func TestDeterministicRejectionStopsKeyRotation(t *testing.T) {
	fake := newFakeUpstream(t)
	fake.reply = func(w http.ResponseWriter, _ *http.Request, _ []byte) bool {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"maximum context length is 128000 tokens"}}`)
		return true
	}
	cfg := e2eConfig(fake.server.URL, []string{"key-first", "key-second"})
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.catalog.ReplaceWithCapabilities([]string{"test-model"}, nil, nil, nil, nil)

	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, KeyTiers: []config.Tier{config.TierZen}}
	resp, err, used := g.doKeyUpstream(context.Background(), route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-deterministic", Session: "ses-deterministic"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %v", resp)
	}
	defer resp.Body.Close()
	if used != 1 {
		t.Fatalf("a request-level rejection must not rotate keys: attempts = %d", used)
	}
}
