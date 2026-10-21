package gateway

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/ranking"
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

func TestRankingsMergeProvidersPerModel(t *testing.T) {
	g := rankingTestGateway(t)
	_, results, err := g.Rankings("agent-coding")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, result := range results {
		if seen[result.ModelID] {
			t.Fatalf("model %s ranked twice", result.ModelID)
		}
		seen[result.ModelID] = true
		if result.Provider != "" {
			t.Fatalf("merged ranking must not name a provider, got %q", result.Provider)
		}
		if result.TaskType != "agent-coding" {
			t.Fatalf("task = %s", result.TaskType)
		}
	}
	for _, model := range []string{"strong", "weak", "zen-only", "no-tools"} {
		if !seen[model] {
			t.Fatalf("missing model %s in %+v", model, seen)
		}
	}
	if results[0].ModelID != "strong" {
		t.Fatalf("top model = %s", results[0].ModelID)
	}
	// Go publishes zero prices, so the merged cost takes the cheapest route.
	if results[0].CostPerTask == nil || *results[0].CostPerTask != 0 {
		t.Fatalf("merged cost = %v", results[0].CostPerTask)
	}
	for _, result := range results {
		if result.ModelID != "zen-only" {
			continue
		}
		if result.CostPerTask == nil || *result.CostPerTask <= 0 {
			t.Fatalf("zen-only cost = %v", result.CostPerTask)
		}
		return
	}
	t.Fatal("zen-only missing from results")
}

func TestRankingRejectsRouteWithoutRequiredCapability(t *testing.T) {
	g := rankingTestGateway(t)
	_, results, err := g.Rankings("agent-coding")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, result := range results {
		if result.ModelID != "no-tools" {
			continue
		}
		found = true
		if result.Gate {
			t.Fatal("model without tool calling must be gated")
		}
		if result.FinalScore != 0 {
			t.Fatalf("gated score = %v", result.FinalScore)
		}
	}
	if !found {
		t.Fatal("no-tools model missing")
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

func TestMergeInputKeepsBestRouteFacts(t *testing.T) {
	merged := ranking.Input{ModelID: "m"}
	mergeInput(&merged, ranking.Input{
		Provider: "zen", Available: true, Capability: ranking.Capability{L: f64ptr(70)},
		CostPerTask: f64ptr(0.5), QuotaCostPerTask: f64ptr(0.5), QuotaHeadroom: f64ptr(40),
		ContextWindow: 128000, MaxOutput: 8000, ToolCall: true, InputModalities: []string{"text"},
	})
	mergeInput(&merged, ranking.Input{
		Provider: "go", Available: false, Capability: ranking.Capability{L: f64ptr(90), B: f64ptr(80)},
		CostPerTask: f64ptr(0.2), QuotaCostPerTask: f64ptr(0.9), QuotaHeadroom: f64ptr(80),
		ContextWindow: 256000, MaxOutput: 16000, StructuredOutput: true, InputModalities: []string{"text", "image"},
	})
	if !merged.Available {
		t.Fatal("a model available on either route must be available")
	}
	if merged.Capability.L == nil || *merged.Capability.L != 90 || merged.Capability.B == nil || *merged.Capability.B != 80 {
		t.Fatalf("capability must keep the best route values: %+v", merged.Capability)
	}
	if merged.CostPerTask == nil || *merged.CostPerTask != 0.2 {
		t.Fatalf("cost must take the cheapest route: %v", merged.CostPerTask)
	}
	if merged.QuotaCostPerTask == nil || *merged.QuotaCostPerTask != 0.5 {
		t.Fatalf("quota cost must take the cheapest route: %v", merged.QuotaCostPerTask)
	}
	if merged.QuotaHeadroom == nil || *merged.QuotaHeadroom != 80 || merged.ContextWindow != 256000 || merged.MaxOutput != 16000 {
		t.Fatalf("headroom and limits must take the best route: %+v", merged)
	}
	if !merged.ToolCall || !merged.StructuredOutput || len(merged.InputModalities) != 2 {
		t.Fatalf("capabilities must union across routes: %+v", merged)
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
