package gateway

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/rotation"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

func rankingTestGateway(t *testing.T) *Gateway {
	t.Helper()
	upstream := newFakeUpstream(t)
	cfg := testConfig(upstream.server.URL, []string{"zen-key"})
	cfg.GoKeys = []string{"go-key"}
	cfg.Models.Protocols = map[string]string{
		"strong":   "chat",
		"weak":     "chat",
		"no-tools": "chat",
		"zen-only": "chat",
	}
	cfg.Ranking.Capabilities = map[string]config.RankingCapability{
		"strong":   {L: f64ptr(90)},
		"weak":     {L: f64ptr(60)},
		"no-tools": {L: f64ptr(99)},
		"zen-only": {L: f64ptr(80)},
	}
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	worker := func(toolCall bool, input, output float64) models.Metadata {
		return models.Metadata{
			ToolCall: toolCall, ContextWindow: 200000, MaxOutput: 16000,
			InputModalities: []string{"text"},
			Cost:            &models.Cost{Input: f64ptr(input), Output: f64ptr(output)},
		}
	}
	metadata := map[config.Tier]map[string]models.Metadata{
		config.TierZen: {
			"strong":   worker(true, 3, 15),
			"weak":     worker(true, 0.1, 0.4),
			"zen-only": worker(true, 1, 4),
		},
		config.TierGo: {
			"strong":   worker(true, 0, 0),
			"weak":     worker(true, 0, 0),
			"no-tools": worker(false, 0, 0),
		},
	}
	g.catalog.ReplaceWithCapabilities([]string{"strong", "weak", "zen-only"}, []string{"strong", "weak", "no-tools"}, nil, nil, metadata)
	return g
}

func f64ptr(value float64) *float64 { return &value }

func TestRankingsCompareProvidersSeparately(t *testing.T) {
	g := rankingTestGateway(t)
	_, results, err := g.Rankings("agent-coding")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, result := range results {
		seen[result.Provider+"/"+result.ModelID] = result.ModelID
		if result.TaskType != "agent-coding" {
			t.Fatalf("task = %s", result.TaskType)
		}
	}
	for _, key := range []string{"go/strong", "zen/strong", "go/weak", "zen/weak", "zen/zen-only"} {
		if _, ok := seen[key]; !ok {
			t.Fatalf("missing route %s in %+v", key, seen)
		}
	}
	if results[0].Provider != "go" || results[0].ModelID != "strong" {
		t.Fatalf("top route = %s/%s", results[0].Provider, results[0].ModelID)
	}
	// The Go metadata publishes zero prices, so the route scores as free.
	if results[0].CostPerTask == nil || *results[0].CostPerTask != 0 {
		t.Fatalf("go cost = %v", results[0].CostPerTask)
	}
	for _, result := range results {
		if result.Provider == "zen" && result.ModelID == "weak" {
			if result.CostPerTask == nil || *result.CostPerTask <= 0 {
				t.Fatalf("zen paid route cost = %v", result.CostPerTask)
			}
			return
		}
	}
	t.Fatal("zen/weak missing from results")
}

func TestRankingRejectsRouteWithoutRequiredCapability(t *testing.T) {
	g := rankingTestGateway(t)
	_, results, err := g.Rankings("agent-coding")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, result := range results {
		if result.Provider != "go" || result.ModelID != "no-tools" {
			continue
		}
		found = true
		if result.Gate {
			t.Fatal("route without tool calling must be gated")
		}
		if result.FinalScore != 0 {
			t.Fatalf("gated score = %v", result.FinalScore)
		}
	}
	if !found {
		t.Fatal("no-tools route missing")
	}
	// The vision preset gates every text-only route instead.
	_, vision, err := g.Rankings("vision")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range vision {
		if result.Gate {
			t.Fatalf("text-only route %s unexpectedly passed the vision gate", result.ModelID)
		}
	}
	if _, _, err := g.Rankings("unknown"); err == nil {
		t.Fatal("unknown task must fail")
	}
}

func TestRotationPlanUsesRankedGoOrder(t *testing.T) {
	g := rankingTestGateway(t)
	manager, err := rotation.New("", g.cfg.Rotation)
	if err != nil {
		t.Fatal(err)
	}
	g.rotation = manager
	g.syncRotation()

	snapshot := manager.Snapshot()
	sota, sweet := snapshot.Groups[0], snapshot.Groups[1]
	if len(sota.Order) != 2 || sota.Order[0] != "strong" || sota.Order[1] != "weak" {
		t.Fatalf("sota order = %v", sota.Order)
	}
	if sota.Current != "strong" {
		t.Fatalf("sota current = %s", sota.Current)
	}
	if sota.Available["no-tools"] {
		t.Fatal("gated model must stay unavailable for rotation")
	}
	// general-text weights differ, but tool-less routes stay eligible there.
	if len(sweet.Order) != 3 || sweet.Order[0] != "no-tools" {
		t.Fatalf("sweet order = %v", sweet.Order)
	}
}
