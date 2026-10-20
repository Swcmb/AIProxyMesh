package gateway

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/ranking"
)

const rankingSampleWindow = 24 * time.Hour

type routeSample struct {
	total     int
	success   int
	durations []float64
}

func (g *Gateway) rankingOptions() ranking.Options {
	cfg := g.cfg.Ranking
	return ranking.Options{
		LatencyGoodMS:    float64(cfg.LatencyGoodMS),
		LatencyBadMS:     float64(cfg.LatencyBadMS),
		MinContextTokens: cfg.MinContextTokens,
	}
}

// rankingInputs builds one route record per provider and model: Zen and Go are
// ranked separately even when the base model is shared, because price, quota,
// latency and success rate differ per access route.
func (g *Gateway) rankingInputs() []ranking.Input {
	now := time.Now().UTC()
	samples := map[string]*routeSample{}
	if g.monitor != nil {
		for _, request := range g.monitor.RequestSamples(rankingSampleWindow) {
			if request.Anonymous || request.Tier == "" {
				continue
			}
			key := request.Tier + "|" + request.Model
			sample := samples[key]
			if sample == nil {
				sample = &routeSample{}
				samples[key] = sample
			}
			sample.total++
			if request.Success {
				sample.success++
			}
			sample.durations = append(sample.durations, float64(request.DurationMS))
		}
	}

	cfg := g.cfg.Ranking
	hasZen, hasGo := g.zenNodes.Len() > 0, g.goNodes.Len() > 0
	var inputs []ranking.Input
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		provider := string(tier)
		for _, model := range g.catalog.List() {
			route, err := g.catalog.RouteForTier(model, tier, hasZen, hasGo)
			metadata := g.catalog.MetadataForTier(model, tier)
			input := ranking.Input{
				Provider:         provider,
				ModelID:          model,
				Available:        err == nil && route.Protocol != wire.SystemOne,
				Capability:       rankingCapability(cfg, provider, model),
				ContextWindow:    metadata.ContextWindow,
				MaxOutput:        metadata.MaxOutput,
				ToolCall:         metadata.ToolCall,
				StructuredOutput: metadata.StructuredOutput,
				InputModalities:  append([]string(nil), metadata.InputModalities...),
				UpdatedAt:        now,
			}
			tokenCost := standardTaskCost(metadata, cfg)
			input.QuotaCostPerTask = tokenCost
			input.CostPerTask = tokenCost
			if tier == config.TierGo && cfg.GoMonthlyCostUSD > 0 && cfg.GoMonthlyTasks > 0 {
				// A subscription is a flat cash cost per task; quota burn stays
				// visible through QuotaCostPerTask.
				cash := cfg.GoMonthlyCostUSD / float64(cfg.GoMonthlyTasks)
				input.CostPerTask = &cash
			}
			if sample := samples[provider+"|"+model]; sample != nil && sample.total > 0 {
				rate := float64(sample.success) / float64(sample.total)
				p95 := percentile(sample.durations, 0.95)
				input.SuccessRate = &rate
				input.P95LatencyMS = &p95
				input.SampleCount = sample.total
			}
			headroom, ok := cfg.QuotaHeadroom[provider]
			if !ok {
				headroom = 100
			}
			input.QuotaHeadroom = &headroom
			inputs = append(inputs, input)
		}
	}
	return inputs
}

// Rankings scores every route for one task preset, gated routes last.
func (g *Gateway) Rankings(task string) (ranking.Preset, []ranking.Result, error) {
	preset, ok := ranking.PresetFor(task)
	if !ok {
		return ranking.Preset{}, nil, fmt.Errorf("unknown ranking task %q", task)
	}
	return preset, ranking.Evaluate(g.rankingInputs(), preset, g.rankingOptions()), nil
}

// rotationPlan maps the two rotation aliases to ranked Go model orders.
func (g *Gateway) rotationPlan() (map[string][]string, map[string]bool) {
	inputs := g.rankingInputs()
	goInputs := make([]ranking.Input, 0, len(inputs))
	available := map[string]bool{}
	for _, input := range inputs {
		if input.Provider != string(config.TierGo) {
			continue
		}
		goInputs = append(goInputs, input)
		if input.Available {
			available[input.ModelID] = true
		}
	}

	options := g.rankingOptions()
	orders := map[string][]string{}
	for id, task := range map[string]string{"sota": g.cfg.Ranking.SOTATask, "sweet": g.cfg.Ranking.SweetTask} {
		preset, ok := ranking.PresetFor(task)
		if !ok {
			preset, _ = ranking.PresetFor(string(ranking.AgentCoding))
		}
		order := ranking.Order(ranking.Evaluate(goInputs, preset, options))
		if len(order) == 0 {
			// With no capability metadata yet there is nothing to rank; keep
			// every routable Go model selectable in name order.
			for _, input := range goInputs {
				if input.Available {
					order = append(order, input.ModelID)
				}
			}
			sort.Strings(order)
		}
		orders[id] = order
	}
	return orders, available
}

func rankingCapability(cfg config.RankingConfig, provider, model string) ranking.Capability {
	entry, ok := cfg.Capabilities[provider+":"+model]
	if !ok {
		entry, ok = cfg.Capabilities[model]
	}
	if !ok {
		return ranking.Capability{}
	}
	return ranking.Capability{L: entry.L, B: entry.B, I: entry.I}
}

// standardTaskCost converts published prices into the cost of the configured
// standard task. A model without input and output prices stays unpriced.
func standardTaskCost(metadata models.Metadata, cfg config.RankingConfig) *float64 {
	if metadata.Cost == nil || metadata.Cost.Input == nil || metadata.Cost.Output == nil {
		return nil
	}
	cacheRead := 0.0
	if metadata.Cost.CacheRead != nil {
		cacheRead = *metadata.Cost.CacheRead
	}
	cost := (float64(cfg.StandardInputTokens)*(*metadata.Cost.Input) +
		float64(cfg.StandardOutputTokens)*(*metadata.Cost.Output) +
		float64(cfg.StandardCachedTokens)*cacheRead) / 1e6
	return &cost
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
