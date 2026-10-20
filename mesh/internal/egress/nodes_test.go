package egress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
)

func feedServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	return server
}

func nodeTestEngine(t *testing.T) *Engine {
	t.Helper()
	feedA := feedServer(t, "http://1.1.1.1:8080#shared\nhttp://2.2.2.2:8080#only-a\nsocks5://3.3.3.3:1080#socks-node\n")
	feedB := feedServer(t, "http://1.1.1.1:8080#shared-b\nhttp://4.4.4.4:8080#only-b\n")

	cfg := testEgressConfig(t)
	cfg.ImportLegacyProxies = boolPtr(false)
	cfg.Subscriptions = []config.EgressSubscription{
		{ID: "alpha", Name: "订阅甲", URL: feedA.URL, Enabled: true, RefreshSeconds: 3600},
		{ID: "beta", Name: "订阅乙", URL: feedB.URL, Enabled: true, RefreshSeconds: 3600},
	}
	engine, err := New(testOptions(t, cfg, ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	engine.RefreshAll(context.Background())
	return engine
}

func findNode(nodes []NodeView, name string) NodeView {
	for _, item := range nodes {
		if item.Name == name {
			return item
		}
	}
	return NodeView{}
}

func TestEngineNodesListsEveryNodeWithAliases(t *testing.T) {
	engine := nodeTestEngine(t)
	page := engine.Nodes(NodeQuery{Limit: 100})
	if !page.Enabled {
		t.Fatal("page must report the pool as enabled")
	}
	if page.Pool != 4 || page.Total != 4 || len(page.Nodes) != 4 {
		t.Fatalf("pool/total = %d/%d nodes=%d", page.Pool, page.Total, len(page.Nodes))
	}
	if page.SubscriptionNames["alpha"] != "订阅甲" || page.SubscriptionNames["beta"] != "订阅乙" {
		t.Fatalf("subscription names = %+v", page.SubscriptionNames)
	}

	// One exit shared by both subscriptions keeps both names.
	shared := findNode(page.Nodes, "shared")
	if !slices.Contains(shared.Subscriptions, "alpha") || !slices.Contains(shared.Subscriptions, "beta") {
		t.Fatalf("shared subscriptions = %v", shared.Subscriptions)
	}
	tags := map[string]string{}
	for _, alias := range shared.Aliases {
		tags[alias.Subscription] = alias.Tag
	}
	if tags["alpha"] != "shared" || tags["beta"] != "shared-b" {
		t.Fatalf("shared aliases = %+v", shared.Aliases)
	}

	// Statuses and facets come from the pool itself.
	for _, view := range page.Nodes {
		if view.Status != StatusUnprobed {
			t.Fatalf("node %s status = %s, want unprobed", view.Name, view.Status)
		}
	}
	if !slices.Contains(page.Facets.Protocols, "socks5") || !slices.Contains(page.Facets.Protocols, "http") {
		t.Fatalf("protocol facets = %v", page.Facets.Protocols)
	}
	if !slices.Equal(page.Facets.Subscriptions, []string{"alpha", "beta"}) {
		t.Fatalf("subscription facets = %v", page.Facets.Subscriptions)
	}
}

func TestEngineNodesFiltersAndPages(t *testing.T) {
	engine := nodeTestEngine(t)
	all := engine.Nodes(NodeQuery{Limit: 100})

	if got := engine.Nodes(NodeQuery{Subscription: "beta", Limit: 100}); got.Total != 2 {
		t.Fatalf("subscription filter total = %d", got.Total)
	}
	if got := engine.Nodes(NodeQuery{Protocol: "socks5", Limit: 100}); got.Total != 1 || got.Nodes[0].Name != "socks-node" {
		t.Fatalf("protocol filter = %+v", got.Nodes)
	}
	if got := engine.Nodes(NodeQuery{Keyword: "only-b", Limit: 100}); got.Total != 1 {
		t.Fatalf("keyword filter total = %d", got.Total)
	}
	// The alias of the shared node in beta is searchable by its subscription name.
	if got := engine.Nodes(NodeQuery{Keyword: "订阅乙", Limit: 100}); got.Total != 2 {
		t.Fatalf("keyword by subscription name total = %d", got.Total)
	}
	if got := engine.Nodes(NodeQuery{Status: StatusHealthy, Limit: 100}); got.Total != 0 {
		t.Fatalf("healthy filter on an unprobed pool total = %d", got.Total)
	}
	if got := engine.Nodes(NodeQuery{Status: StatusUnprobed, Limit: 100}); got.Total != all.Total {
		t.Fatalf("unprobed filter total = %d, want %d", got.Total, all.Total)
	}

	ascending := engine.Nodes(NodeQuery{SortBy: "name", SortDesc: false, Limit: 100})
	if ascending.Nodes[0].Name != "only-a" || ascending.Nodes[len(ascending.Nodes)-1].Name != "socks-node" {
		names := make([]string, 0, len(ascending.Nodes))
		for _, view := range ascending.Nodes {
			names = append(names, view.Name)
		}
		t.Fatalf("name asc order = %v", names)
	}
	page := engine.Nodes(NodeQuery{SortBy: "name", SortDesc: false, Offset: 1, Limit: 2})
	if page.Total != 4 || len(page.Nodes) != 2 || page.Nodes[0].Name != "only-b" {
		t.Fatalf("paged result total=%d nodes=%d first=%s", page.Total, len(page.Nodes), page.Nodes[0].Name)
	}
}

func TestEnginePrunesNodesOfDeletedSubscriptions(t *testing.T) {
	feed := feedServer(t, "http://1.1.1.1:8080#alpha-node\nhttp://2.2.2.2:8080#beta-node\n")
	statePath := filepath.Join(t.TempDir(), "egress.db")

	cfg := testEgressConfig(t)
	cfg.ImportLegacyProxies = boolPtr(false)
	cfg.StatePath = statePath
	cfg.Subscriptions = []config.EgressSubscription{{ID: "alpha", Name: "订阅甲", URL: feed.URL, Enabled: true, RefreshSeconds: 3600}}
	first, err := New(testOptions(t, cfg, ""))
	if err != nil {
		t.Fatal(err)
	}
	first.RefreshAll(context.Background())
	if first.Pool().Len() != 2 {
		t.Fatalf("pool after first run = %d", first.Pool().Len())
	}
	if err := first.save(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// The subscription is deleted from configuration: its exits must not come
	// back from the state database.
	cfg.Subscriptions = nil
	second, err := New(testOptions(t, cfg, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Pool().Len() != 0 {
		t.Fatalf("orphaned nodes survived: pool = %d", second.Pool().Len())
	}
}

func TestEngineInlineSubscriptionAndCleanup(t *testing.T) {
	cfg := testEgressConfig(t)
	cfg.ImportLegacyProxies = boolPtr(false)
	cfg.Subscriptions = []config.EgressSubscription{{
		ID: "local", Name: "本地粘贴", Enabled: true,
		Content: "http://1.1.1.1:8080#first\nhttp://2.2.2.2:8080#second\n",
	}}
	engine, err := New(testOptions(t, cfg, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	engine.RefreshAll(context.Background())

	if engine.Pool().Len() != 2 {
		t.Fatalf("pool = %d, want 2", engine.Pool().Len())
	}
	status, ok := engine.Subscriptions().Status("local")
	if !ok || !status.Inline || status.Nodes != 2 {
		t.Fatalf("inline status = %+v", status)
	}

	// Cool one exit down, then clean it: the refresh brings it back healthy.
	entries := engine.Pool().List()
	entries[0].RestoreCooldown(3, time.Now().Add(time.Minute).UnixNano())
	removed, err := engine.CleanupSubscription(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if engine.Pool().Len() != 2 {
		t.Fatalf("pool after refresh = %d, want 2", engine.Pool().Len())
	}
	for _, entry := range engine.Pool().List() {
		if entry.Failures() != 0 {
			t.Fatalf("cleaned node kept failures: %+v", entry.Failures())
		}
	}
	if _, err := engine.CleanupSubscription(context.Background(), "missing"); err == nil {
		t.Fatal("unknown subscription must be rejected")
	}
}
