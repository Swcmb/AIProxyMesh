//go:build live

package egress_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// Run with:
//
//	MESH_LIVE_PRESETS=1 go test -tags live ./internal/egress \
//	  -run TestEngineWithPresetSubscriptionsLive -v -timeout 15m
func TestEngineWithPresetSubscriptionsLive(t *testing.T) {
	if os.Getenv("MESH_LIVE_PRESETS") != "1" {
		t.Skip("set MESH_LIVE_PRESETS=1 to fetch the public preset subscriptions")
	}
	dir := t.TempDir()
	statePath := filepath.Join(dir, "egress.db")
	configPath := filepath.Join(dir, "config.json")
	body := `{
	  "server_keys": ["live-test"],
	  "upstream": {
	    "zen": "https://opencode.ai/zen",
	    "go": "https://opencode.ai/zen/go",
	    "anonymous": "https://opencode.ai/inference"
	  },
	  "egress": {
	    "enabled": true,
	    "import_legacy_proxies": false,
	    "state_path": "` + filepath.ToSlash(statePath) + `",
	    "subscriptions": [
	      {"id":"api-proxy-pool-hproxy","name":"hproxy","url":"https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.csv","enabled":true},
	      {"id":"api-proxy-pool-proxifly","name":"proxifly","url":"https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt","enabled":true}
	    ],
	    "probe": {"interval_seconds": 3600, "timeout_seconds": 5, "concurrency": 8, "canary_enabled": false},
	    "health": {"mode": "shadow"}
	  }
	}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := egress.New(egress.Options{
		Egress:      cfg.Egress,
		Performance: cfg.Performance,
		Upstream:    cfg.Upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	engine.Subscriptions().RefreshAll(ctx)
	for _, id := range []string{"api-proxy-pool-hproxy", "api-proxy-pool-proxifly"} {
		status, ok := engine.Subscriptions().Status(id)
		if !ok || status.Nodes == 0 {
			t.Fatalf("subscription %s status = %+v ok=%v", id, status, ok)
		}
		t.Logf("%s: nodes=%d supported=%d format=%s", id, status.Nodes, status.Supported, status.Format)
	}

	page := engine.Nodes(egress.NodeQuery{Limit: 20})
	if page.Pool < 50000 {
		t.Fatalf("pool = %d, want the preset lists loaded", page.Pool)
	}
	t.Logf("pool=%d eligible=%d regions=%d", page.Pool, page.Counts.Eligible, len(page.Facets.Regions))

	summary := engine.HealthSummary()
	if summary.Mode != "shadow" || summary.Environment.State == "" {
		t.Fatalf("health summary = %+v", summary)
	}
	t.Logf("health: mode=%s env=%s stable=%d targets=%d",
		summary.Mode, summary.Environment.State, summary.StablePool, len(summary.Targets))

	ids := make([]string, 0, 20)
	for _, node := range page.Nodes {
		if node.Supported {
			ids = append(ids, node.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("no supported node sampled from the page")
	}
	probed := engine.ProbeNodes(ctx, ids)
	t.Logf("probe batch reported %d HTTP-stage probes for %d ids", probed, len(ids))
	marked, healthy := 0, 0
	for _, id := range ids {
		hash, err := node.ParseHex(id)
		if err != nil {
			continue
		}
		entry := engine.PoolEntry(hash)
		if entry == nil {
			continue
		}
		if entry.LastProbeAt() > 0 {
			marked++
		}
		if entry.EverHealthy() {
			healthy++
		}
	}
	if marked != len(ids) {
		t.Fatalf("fast discovery marked %d/%d sampled nodes", marked, len(ids))
	}
	t.Logf("fast discovery marked %d/%d nodes; ever_healthy=%d", marked, len(ids), healthy)
}
