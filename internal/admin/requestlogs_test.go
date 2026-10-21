package admin

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/logstore"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

func newRequestLogServer(t *testing.T, service *logstore.Service) *Server {
	t.Helper()
	return &Server{requestLogProvider: func() *logstore.Service { return service }}
}

func openTestRequestLog(t *testing.T) *logstore.Service {
	t.Helper()
	service, err := logstore.Open(logstore.Options{
		Path: filepath.Join(t.TempDir(), "requests.db"), RetentionDays: 7, MaxRows: 1000,
		Logger: slog.New(slog.NewTextHandler(nopWriter{}, nil)),
	})
	if err != nil {
		t.Fatalf("open log service: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = service.Close()
	})
	service.Start(ctx)
	now := time.Now().UTC()
	service.PersistRequest(telemetry.UpstreamRequest{RequestID: "abc123", Time: now, Model: "gpt-x",
		Channel: "zen", Tier: "zen", Status: 429, DurationMS: 40, Outcome: "rate_limited", ExitID: "node-1"})
	service.PersistAttempt(telemetry.UpstreamAttempt{RequestID: "abc123", Time: now, Attempt: 1,
		Channel: "zen", Status: 429, DurationMS: 40, Outcome: "rate_limited"})
	if err := service.Close(); err != nil {
		t.Fatalf("flush log service: %v", err)
	}
	reopened, err := logstore.Open(logstore.Options{Path: service.Path(), RetentionDays: 7, MaxRows: 1000})
	if err != nil {
		t.Fatalf("reopen log service: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestRequestLogEndpoints(t *testing.T) {
	service := openTestRequestLog(t)
	server := newRequestLogServer(t, service)

	recorder := httptest.NewRecorder()
	server.handleRequestLogs(recorder, httptest.NewRequest(http.MethodGet, "/api/logs/requests?model=gpt-x", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	if body["enabled"] != true {
		t.Fatalf("expected an enabled log, got %+v", body)
	}
	items, _ := body["requests"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one filtered row, got %+v", body["requests"])
	}
	stats, _ := body["stats"].(map[string]any)
	if stats["requests"].(float64) != 1 || stats["attempts"].(float64) != 1 {
		t.Fatalf("unexpected stats %+v", stats)
	}
	facets, _ := body["facets"].(map[string]any)
	if models, _ := facets["model"].([]any); len(models) != 1 || models[0] != "gpt-x" {
		t.Fatalf("facets missing the model value: %+v", facets)
	}

	recorder = httptest.NewRecorder()
	server.handleRequestLogDetail(recorder, httptest.NewRequest(http.MethodGet, "/api/logs/requests/detail?request_id=abc123", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", recorder.Code, recorder.Body.String())
	}
	detail := decodeBody(t, recorder)
	attempts, _ := detail["attempts"].([]any)
	if len(attempts) != 1 {
		t.Fatalf("expected the attempt chain, got %+v", detail["attempts"])
	}

	recorder = httptest.NewRecorder()
	server.handleRequestLogs(recorder, httptest.NewRequest(http.MethodGet, "/api/logs/requests?limit=abc", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("an invalid limit must be rejected, got %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	server.handleRequestLogDetail(recorder, httptest.NewRequest(http.MethodGet, "/api/logs/requests/detail?request_id=nope", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("an unknown request id must be 404, got %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	server.handlePurgeRequestLogs(recorder, httptest.NewRequest(http.MethodDelete, "/api/logs/requests", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("purge status %d: %s", recorder.Code, recorder.Body.String())
	}
	if removed := decodeBody(t, recorder)["removed"].(float64); removed != 1 {
		t.Fatalf("expected one removed row, got %v", removed)
	}
}

func TestRequestLogEndpointsWhenDisabled(t *testing.T) {
	server := newRequestLogServer(t, nil)
	recorder := httptest.NewRecorder()
	server.handleRequestLogs(recorder, httptest.NewRequest(http.MethodGet, "/api/logs/requests", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("a disabled log still answers the list route, got %d", recorder.Code)
	}
	if enabled := decodeBody(t, recorder)["enabled"]; enabled != false {
		t.Fatalf("expected enabled false, got %v", enabled)
	}

	recorder = httptest.NewRecorder()
	server.handleRequestLogDetail(recorder, httptest.NewRequest(http.MethodGet, "/api/logs/requests/detail?request_id=x", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("detail on a disabled log must be 404, got %d", recorder.Code)
	}
}
