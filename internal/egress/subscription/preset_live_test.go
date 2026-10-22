//go:build live

package subscription_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/probe"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/subscription"
)

// Run with:
//
//	MESH_LIVE_PRESETS=1 go test -tags live ./internal/egress/subscription \
//	  -run TestPresetSubscriptionsLive -v -timeout 10m
func TestPresetSubscriptionsLive(t *testing.T) {
	if os.Getenv("MESH_LIVE_PRESETS") != "1" {
		t.Skip("set MESH_LIVE_PRESETS=1 to fetch the public preset subscriptions")
	}
	presets := []struct {
		id  string
		url string
	}{
		{"api-proxy-pool-hproxy", "https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.csv"},
		{"api-proxy-pool-proxifly", "https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt"},
	}
	for _, preset := range presets {
		t.Run(preset.id, func(t *testing.T) {
			p := pool.New(128)
			manager := subscription.New(p, nil, func() []subscription.Source {
				return []subscription.Source{{
					ID: preset.id, Name: preset.id, URL: preset.url, Enabled: true,
					Protocols: []string{"http", "https", "socks5", "socks5h"},
				}}
			}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if err := manager.Refresh(ctx, preset.id); err != nil {
				t.Fatalf("refresh %s: %v", preset.id, err)
			}
			status, ok := manager.Status(preset.id)
			if !ok || status.Nodes == 0 || status.Supported == 0 {
				t.Fatalf("status = %+v ok=%v", status, ok)
			}
			t.Logf("parsed: nodes=%d supported=%d unsupported=%d format=%s",
				status.Nodes, status.Supported, status.Unsupported, status.Format)

			entries := p.List()
			supported := make([]string, 0, len(entries))
			for _, entry := range entries {
				if entry.Enabled() && entry.Options.Supported() {
					supported = append(supported, entry.ID())
				}
			}
			if len(supported) == 0 {
				t.Fatal("no supported nodes to probe")
			}
			sample := min(len(supported), 50)
			prober := probe.New(p, outbound.NewManager(outbound.DefaultConfig()), probe.Config{
				Targets:     []string{"https://opencode.ai/inference"},
				Timeout:     5 * time.Second,
				Concurrency: 8,
			}, nil)
			prober.ProbeBatchFast(ctx, supported[:sample], nil)

			probed, reachable, failed := 0, 0, 0
			for _, id := range supported[:sample] {
				hash, err := node.ParseHex(id)
				if err != nil {
					continue
				}
				entry := p.Get(hash)
				if entry == nil || entry.LastProbeAt() == 0 {
					continue
				}
				probed++
				if entry.LastSuccessAt() > 0 || entry.EverHealthy() {
					reachable++
				} else {
					failed++
				}
			}
			if probed != sample {
				t.Fatalf("probe completion = %d/%d", probed, sample)
			}
			t.Logf("probed %d nodes: reachable=%d failed=%d", probed, reachable, failed)
			if reachable == 0 {
				t.Log("warning: no reachable nodes in this sample; public lists change quickly")
			}
		})
	}
}
