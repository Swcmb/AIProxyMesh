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

// routeSamples aggregates the last day of telemetry twice: by tier and model
// for the Go-only rotation order, and by model for the merged model ranking.
func (g *Gateway) routeSamples() (map[string]*routeSample, map[string]*routeSample) {
	byTier := map[string]*routeSample{}
	byModel := map[string]*routeSample{}
	if g.monitor == nil {
		return byTier, byModel
	}
	add := func(target map[string]*routeSample, key string, success bool, duration float64) {
		sample := target[key]
		if sample == nil {
			sample = &routeSample{}
			target[key] = sample
		}
		sample.total++
		if success {
			sample.success++
		}
		sample.durations = append(sample.durations, duration)
	}
	for _, request := range g.monitor.RequestSamples(rankingSampleWindow) {
		if request.Anonymous || request.Tier == "" {
			continue
		}
		add(byTier, request.Tier+"|"+request.Model, request.Success, float64(request.DurationMS))
		add(byModel, request.Model, request.Success, float64(request.DurationMS))
	}
	return byTier, byModel
}

// tierInput describes one access route (Zen or Go) to one model.
func (g *Gateway) tierInput(model string, tier config.Tier, hasZen, hasGo bool) ranking.Input {
	cfg := g.cfg.Ranking
	provider := string(tier)
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
	headroom, ok := cfg.QuotaHeadroom[provider]
	if !ok {
		headroom = 100
	}
	input.QuotaHeadroom = &headroom
	return input
}

// rankingInputs builds one record per model. Zen and Go are access routes to
// the same model, so the ranking does not split them: cost takes the cheapest
// route, availability the best, capability the highest, and telemetry is summed
// across channels.
func (g *Gateway) rankingInputs() []ranking.Input {
	now := time.Now().UTC()
	_, byModel := g.routeSamples()
	hasZen, hasGo := g.zenNodes.Len() > 0, g.goNodes.Len() > 0
	// Without any authenticated route the anonymous free lane is the only way
	// to serve a model; rank those models instead of gating the whole catalog.
	hasAnonymous := g.cfg.AnonymousEnabled() && !hasZen && !hasGo
	models := g.catalog.List()
	inputs := make([]ranking.Input, 0, len(models))
	for _, model := range models {
		merged := ranking.Input{ModelID: model, UpdatedAt: now}
		for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
			mergeInput(&merged, g.tierInput(model, tier, hasZen, hasGo))
		}
		if hasAnonymous && !merged.Available {
			if route, err := g.catalog.Route(model, hasZen, hasGo, true); err == nil && route.Anonymous && route.Protocol != wire.SystemOne {
				merged.Available = true
				merged.Provider = "anonymous"
				free := 0.0
				merged.CostPerTask = &free
				merged.QuotaCostPerTask = &free
			}
		}
		applySample(&merged, byModel[model])
		inputs = append(inputs, merged)
	}
	return inputs
}

// mergeInput folds one access route into the model-level record.
func mergeInput(dst *ranking.Input, src ranking.Input) {
	dst.Available = dst.Available || src.Available
	dst.Capability = ranking.Capability{
		L: pickHigher(dst.Capability.L, src.Capability.L),
		B: pickHigher(dst.Capability.B, src.Capability.B),
		I: pickHigher(dst.Capability.I, src.Capability.I),
	}
	dst.CostPerTask = pickLower(dst.CostPerTask, src.CostPerTask)
	dst.QuotaCostPerTask = pickLower(dst.QuotaCostPerTask, src.QuotaCostPerTask)
	dst.QuotaHeadroom = pickHigher(dst.QuotaHeadroom, src.QuotaHeadroom)
	dst.ContextWindow = max(dst.ContextWindow, src.ContextWindow)
	dst.MaxOutput = max(dst.MaxOutput, src.MaxOutput)
	dst.ToolCall = dst.ToolCall || src.ToolCall
	dst.StructuredOutput = dst.StructuredOutput || src.StructuredOutput
	dst.InputModalities = unionModalities(dst.InputModalities, src.InputModalities)
}

func applySample(input *ranking.Input, sample *routeSample) {
	if sample == nil || sample.total == 0 {
		return
	}
	rate := float64(sample.success) / float64(sample.total)
	p95 := percentile(sample.durations, 0.95)
	input.SuccessRate = &rate
	input.P95LatencyMS = &p95
	input.SampleCount = sample.total
}

func pickHigher(a, b *float64) *float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b > *a {
		return b
	}
	return a
}

func pickLower(a, b *float64) *float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b < *a {
		return b
	}
	return a
}

func unionModalities(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	result := make([]string, 0, len(a)+len(b))
	for _, value := range append(append([]string(nil), a...), b...) {
		if _, ok := seen[value]; ok || value == "" {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// Rankings scores every route for one task preset, gated routes last.
func (g *Gateway) Rankings(task string) (ranking.Preset, []ranking.Result, error) {
	preset, ok := ranking.PresetFor(task)
	if !ok {
		return ranking.Preset{}, nil, fmt.Errorf("unknown ranking task %q", task)
	}
	return preset, ranking.Evaluate(g.rankingInputs(), preset, g.rankingOptions()), nil
}

// rotationPlan maps the two rotation aliases to ranked Go model orders. Rotation
// switches Go subscriptions only, so this keeps the per-tier inputs: the merged
// model ranking never has to know which channel serves a model.
func (g *Gateway) rotationPlan() (map[string][]string, map[string]bool) {
	byTier, _ := g.routeSamples()
	hasZen, hasGo := g.zenNodes.Len() > 0, g.goNodes.Len() > 0
	now := time.Now().UTC()
	available := map[string]bool{}
	goInputs := make([]ranking.Input, 0)
	for _, model := range g.catalog.List() {
		input := g.tierInput(model, config.TierGo, hasZen, hasGo)
		input.UpdatedAt = now
		applySample(&input, byTier[string(config.TierGo)+"|"+model])
		goInputs = append(goInputs, input)
		if input.Available {
			available[model] = true
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
