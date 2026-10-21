package subscription

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

type sourceSet struct {
	mu      sync.Mutex
	sources []Source
}

func (s *sourceSet) set(sources ...Source) {
	s.mu.Lock()
	s.sources = sources
	s.mu.Unlock()
}

func (s *sourceSet) get() []Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Source(nil), s.sources...)
}

func newTestManager(t *testing.T, set *sourceSet) (*Manager, *pool.Pool) {
	t.Helper()
	p := pool.New(16)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(p, &http.Client{Timeout: 5 * time.Second}, set.get, logger)
	return manager, p
}

func TestRefreshHTTPSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http://1.1.1.1:8080#a\nsocks5://2.2.2.2:1080#b\n"))
	}))
	defer server.Close()

	set := &sourceSet{}
	set.set(Source{ID: "sub-a", Name: "primary", URL: server.URL, Enabled: true, Refresh: time.Hour})
	manager, p := newTestManager(t, set)
	if err := manager.Refresh(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 2 {
		t.Fatalf("pool size = %d", p.Len())
	}
	status, ok := manager.Status("sub-a")
	if !ok || status.Nodes != 2 || status.Supported != 2 || status.LastFetchAt == nil {
		t.Fatalf("status = %+v", status)
	}
	if status.LastError != "" || status.NextFetchAt == nil {
		t.Fatalf("status = %+v", status)
	}
}

func TestRefreshReplacesAndKeepsOnFailure(t *testing.T) {
	var payload atomicString
	payload.set("http://1.1.1.1:8080#a\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := payload.get()
		if value == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(value))
	}))
	defer server.Close()

	set := &sourceSet{}
	set.set(Source{ID: "sub-a", Name: "primary", URL: server.URL, Enabled: true, Refresh: time.Hour})
	manager, p := newTestManager(t, set)
	if err := manager.Refresh(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}

	payload.set("http://3.3.3.3:8080#c\n")
	if err := manager.Refresh(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 1 {
		t.Fatalf("old node must be removed, size = %d", p.Len())
	}
	status, _ := manager.Status("sub-a")
	if status.Added != 1 || status.Removed != 1 {
		t.Fatalf("diff = %+v", status)
	}

	payload.set("")
	if err := manager.Refresh(context.Background(), "sub-a"); err == nil {
		t.Fatal("fetch failure must be reported")
	}
	if p.Len() != 1 {
		t.Fatal("failed refresh must keep the previous node set")
	}
	status, _ = manager.Status("sub-a")
	if status.LastError == "" {
		t.Fatal("failure must be recorded")
	}
}

func TestDisabledSourceGatesNodes(t *testing.T) {
	set := &sourceSet{}
	set.set(Source{ID: "legacy", Name: "legacy", Content: "http://1.1.1.1:8080#a", Enabled: true})
	manager, p := newTestManager(t, set)
	if err := manager.Refresh(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	entry := p.List()[0]
	if !p.Active(entry) {
		t.Fatal("enabled subscription must keep nodes active")
	}
	set.set(Source{ID: "legacy", Name: "legacy", Content: "http://1.1.1.1:8080#a", Enabled: false})
	if err := manager.Refresh(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	if p.Active(entry) {
		t.Fatal("disabled subscription must gate nodes")
	}
	set.set(Source{ID: "legacy", Name: "legacy", Content: "http://1.1.1.1:8080#a", Enabled: true})
	if err := manager.Refresh(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	if !p.Active(entry) {
		t.Fatal("re-enabled subscription must restore eligibility")
	}
}

func TestInlineLegacyAndBase64(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("socks5h://4.4.4.4:1080#remote\n"))
	set := &sourceSet{}
	set.set(Source{ID: "legacy", Name: "legacy", Content: encoded, Enabled: true})
	manager, p := newTestManager(t, set)
	if err := manager.Refresh(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 1 {
		t.Fatalf("pool = %d", p.Len())
	}
	status, _ := manager.Status("legacy")
	if !status.Inline || status.Format != "uri" {
		t.Fatalf("status = %+v", status)
	}
}

func TestUnknownSourceRemoved(t *testing.T) {
	set := &sourceSet{}
	set.set(Source{ID: "sub-a", Name: "a", Content: "http://1.1.1.1:8080#a", Enabled: true})
	manager, p := newTestManager(t, set)
	if err := manager.Refresh(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}
	set.set()
	if err := manager.Refresh(context.Background(), "sub-a"); err != ErrUnknownSource {
		t.Fatalf("expected ErrUnknownSource, got %v", err)
	}
	if p.Len() != 0 {
		t.Fatal("unknown source must be removed from the pool")
	}
	if _, ok := manager.Status("sub-a"); ok {
		t.Fatal("status must be removed")
	}
}

func TestReconcileRemovesMissingSources(t *testing.T) {
	set := &sourceSet{}
	set.set(
		Source{ID: "sub-a", Name: "a", Content: "http://1.1.1.1:8080#a", Enabled: true},
		Source{ID: "sub-b", Name: "b", Content: "http://2.2.2.2:8080#b", Enabled: true},
	)
	manager, p := newTestManager(t, set)
	manager.RefreshAll(context.Background())
	if p.Len() != 2 {
		t.Fatalf("pool = %d", p.Len())
	}
	set.set(Source{ID: "sub-a", Name: "a", Content: "http://1.1.1.1:8080#a", Enabled: true})
	manager.Reconcile()
	if p.Len() != 1 {
		t.Fatalf("reconcile must drop removed sources, size = %d", p.Len())
	}
	if statuses := manager.Statuses(); len(statuses) != 1 || statuses[0].ID != "sub-a" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestRefreshDue(t *testing.T) {
	set := &sourceSet{}
	set.set(Source{ID: "legacy", Name: "legacy", Content: "http://1.1.1.1:8080#a", Enabled: true})
	manager, p := newTestManager(t, set)
	now := time.Unix(1_700_000_000, 0)
	manager.SetClock(func() time.Time { return now })
	set.set(Source{ID: "legacy", Name: "legacy", Content: "http://1.1.1.1:8080#a", Enabled: true, Refresh: time.Minute})
	manager.RefreshDue(context.Background())
	if p.Len() != 1 {
		t.Fatal("first due run must refresh")
	}
	now = now.Add(30 * time.Second)
	set.set(Source{ID: "legacy", Name: "legacy", Content: "http://2.2.2.2:8080#b", Enabled: true, Refresh: time.Minute})
	manager.RefreshDue(context.Background())
	if p.Len() != 1 || p.List()[0].Options.Server != "1.1.1.1" {
		t.Fatal("source must not refresh before its interval")
	}
	now = now.Add(31 * time.Second)
	manager.RefreshDue(context.Background())
	if p.List()[0].Options.Server != "2.2.2.2" {
		t.Fatal("source must refresh after its interval")
	}
}

type atomicString struct {
	mu    sync.Mutex
	value string
}

func (a *atomicString) set(value string) {
	a.mu.Lock()
	a.value = value
	a.mu.Unlock()
}

func (a *atomicString) get() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.value
}

func TestStatusMasksFeedURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "http://1.1.1.1:8080#a\n")
	}))
	defer server.Close()

	sources := &sourceSet{}
	p := pool.New(8)
	manager := New(p, nil, sources.get, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sources.set(Source{
		ID: "remote", Name: "remote", URL: server.URL + "/private/feed?token=secret",
		Enabled: true, Refresh: time.Hour,
	})
	if err := manager.Refresh(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	status, ok := manager.Status("remote")
	if !ok {
		t.Fatal("status missing")
	}
	if status.URL != server.URL+"/***" {
		t.Fatalf("status url = %q, want origin only", status.URL)
	}
}

func TestRefreshFiltersDisallowedProtocols(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http://1.1.1.1:8080#a\nhttps://3.3.3.3:443#c\nsocks5://2.2.2.2:1080#b\n"))
	}))
	defer server.Close()

	set := &sourceSet{}
	set.set(Source{ID: "sub-a", Name: "filtered", URL: server.URL, Enabled: true, Refresh: time.Hour, Protocols: []string{"http", "https"}})
	manager, p := newTestManager(t, set)
	if err := manager.Refresh(context.Background(), "sub-a"); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 2 {
		t.Fatalf("socks5 must be filtered out, pool size = %d", p.Len())
	}
	status, ok := manager.Status("sub-a")
	if !ok || status.Nodes != 2 || status.Supported != 2 {
		t.Fatalf("status = %+v", status)
	}
	// The filter must not invent nodes: only the two allowed schemes remain.
	for _, entry := range p.List() {
		if name := entry.Name(); name == "b" {
			t.Fatal("socks5 node leaked into the pool")
		}
	}
}
