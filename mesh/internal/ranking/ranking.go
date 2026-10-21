// Package ranking scores provider+model routes for a task type.
//
// The score follows a four-dimension model: capability (Q), economy (E),
// availability (A) and task fitness (F), gated by hard requirements:
//
//	S = G * (wQ*Q + wE*E + wA*A + wF*F)
//
// Every dimension is normalized to 0-100. Hard requirements zero the gate, so
// a text-only model never wins a vision task regardless of price or speed.
package ranking

import (
	"math"
	"sort"
	"time"
)

type Task string

const (
	AgentCoding Task = "agent-coding"
	GeneralText Task = "general-text"
	Vision      Task = "vision"
)

// Preset carries the task-specific weights and hard requirements.
type Preset struct {
	Task               Task    `json:"task_type"`
	Label              string  `json:"label"`
	Description        string  `json:"description"`
	CapabilityWeight   float64 `json:"capability_weight"`
	EconomyWeight      float64 `json:"economy_weight"`
	AvailabilityWeight float64 `json:"availability_weight"`
	FitnessWeight      float64 `json:"fitness_weight"`
	NeedsTools         bool    `json:"needs_tools"`
	NeedsVision        bool    `json:"needs_vision"`
	MinContextTokens   int     `json:"min_context_tokens"`
	ContextTarget      int     `json:"context_target"`
	OutputTarget       int     `json:"output_target"`
}

var presets = []Preset{
	{
		Task: AgentCoding, Label: "Agent 编程",
		Description:      "以代码生成、缺陷修复与多步工具调用为主，能力与执行力优先。",
		CapabilityWeight: 0.40, EconomyWeight: 0.20, AvailabilityWeight: 0.25, FitnessWeight: 0.15,
		NeedsTools: true, MinContextTokens: 64000, ContextTarget: 256000, OutputTarget: 16000,
	},
	{
		Task: GeneralText, Label: "通用文本",
		Description:      "日常问答与写作，适当提高费用权重。",
		CapabilityWeight: 0.35, EconomyWeight: 0.25, AvailabilityWeight: 0.25, FitnessWeight: 0.15,
		MinContextTokens: 16000, ContextTarget: 128000, OutputTarget: 8000,
	},
	{
		Task: Vision, Label: "视觉与多模态",
		Description:      "必须支持图像输入，适配度权重提高。",
		CapabilityWeight: 0.35, EconomyWeight: 0.15, AvailabilityWeight: 0.25, FitnessWeight: 0.25,
		NeedsVision: true, MinContextTokens: 16000, ContextTarget: 128000, OutputTarget: 8000,
	},
}

// Presets returns the built-in task presets in display order.
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

// PresetFor resolves a task name to its preset.
func PresetFor(task string) (Preset, bool) {
	for _, preset := range presets {
		if string(preset.Task) == task {
			return preset, true
		}
	}
	return Preset{}, false
}

// Capability holds the 0-100 benchmark inputs behind Q. A nil value means the
// metric is unavailable and is renormalized out of the score.
type Capability struct {
	L *float64 `json:"l,omitempty"`
	B *float64 `json:"b,omitempty"`
	I *float64 `json:"i,omitempty"`
}

// Input is one provider+model route as observed by the gateway.
type Input struct {
	Provider string
	ModelID  string
	// Available reports whether a request can currently be routed here.
	Available bool
	// Capability is shared by a base model across providers; callers may
	// override it per route.
	Capability Capability
	// CostPerTask is the effective cost in USD for the standard task profile.
	CostPerTask *float64
	// QuotaCostPerTask is the quota-based cost, reported alongside the cash
	// cost but not used in E.
	QuotaCostPerTask *float64
	// SuccessRate is 0-1.
	SuccessRate  *float64
	P95LatencyMS *float64
	// QuotaHeadroom is 0-100.
	QuotaHeadroom    *float64
	ContextWindow    int
	MaxOutput        int
	ToolCall         bool
	StructuredOutput bool
	InputModalities  []string
	SampleCount      int
	UpdatedAt        time.Time
}

// Options carries operator thresholds that are not task-specific.
type Options struct {
	LatencyGoodMS    float64
	LatencyBadMS     float64
	MinContextTokens int
	Now              time.Time
}

type CapabilityParts struct {
	L *float64 `json:"l,omitempty"`
	B *float64 `json:"b,omitempty"`
	I *float64 `json:"i,omitempty"`
}

type AvailabilityParts struct {
	SuccessRate   *float64 `json:"success_rate,omitempty"`
	LatencyScore  *float64 `json:"latency_score,omitempty"`
	QuotaHeadroom *float64 `json:"quota_headroom,omitempty"`
}

type FitParts struct {
	Modality *float64 `json:"modality,omitempty"`
	Context  *float64 `json:"context,omitempty"`
	Tools    *float64 `json:"tools,omitempty"`
}

// Result is one scored route. Dimension scores are nil when no input data was
// available; the final score then treats the dimension as neutral (50) and
// lowers confidence.
type Result struct {
	Provider          string             `json:"provider,omitempty"`
	ModelID           string             `json:"model_id"`
	TaskType          Task               `json:"task_type"`
	Gate              bool               `json:"gate"`
	GateReasons       []string           `json:"gate_reasons,omitempty"`
	CapabilityScore   *float64           `json:"capability_score,omitempty"`
	EconomyScore      *float64           `json:"economy_score,omitempty"`
	AvailabilityScore *float64           `json:"availability_score,omitempty"`
	FitScore          *float64           `json:"fit_score,omitempty"`
	FinalScore        float64            `json:"final_score"`
	CapabilityParts   *CapabilityParts   `json:"capability_parts,omitempty"`
	AvailabilityParts *AvailabilityParts `json:"availability_parts,omitempty"`
	FitParts          *FitParts          `json:"fit_parts,omitempty"`
	CostPerTask       *float64           `json:"cost_per_task,omitempty"`
	QuotaCostPerTask  *float64           `json:"quota_cost_per_task,omitempty"`
	CostReference     *float64           `json:"cost_reference,omitempty"`
	SuccessRate       *float64           `json:"success_rate,omitempty"`
	P95LatencyMS      *float64           `json:"p95_latency_ms,omitempty"`
	QuotaHeadroom     *float64           `json:"quota_headroom,omitempty"`
	SampleCount       int                `json:"sample_count"`
	Confidence        float64            `json:"confidence"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

type weighted struct {
	value  float64
	weight float64
}

// weightedMean renormalizes over the parts that have data. It returns nil
// when none do, so the caller can distinguish "unmeasured" from "score 0".
func weightedMean(parts []weighted) *float64 {
	total := 0.0
	sum := 0.0
	for _, part := range parts {
		if part.weight <= 0 {
			continue
		}
		total += part.weight
		sum += part.weight * part.value
	}
	if total == 0 {
		return nil
	}
	return ptr(round(sum / total))
}

func ptr(value float64) *float64 { return &value }

func round(value float64) float64 { return math.Round(value*100) / 100 }

func clamp(value, low, high float64) float64 {
	return math.Max(low, math.Min(high, value))
}

func percentile(value float64, pool []float64) float64 {
	if len(pool) == 0 {
		return 50
	}
	below, equal := 0, 0
	for _, candidate := range pool {
		switch {
		case candidate < value:
			below++
		case candidate == value:
			equal++
		}
	}
	return 100 * (float64(below) + 0.5*float64(equal)) / float64(len(pool))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func smallestPositive(values []float64) float64 {
	result := 0.0
	for _, value := range values {
		if value > 0 && (result == 0 || value < result) {
			result = value
		}
	}
	return result
}

func hasModality(modalities []string, wanted string) bool {
	for _, modality := range modalities {
		if modality == wanted {
			return true
		}
	}
	return false
}

func knownMetadata(in Input) bool {
	return in.ContextWindow > 0 || in.MaxOutput > 0 || len(in.InputModalities) > 0 || in.ToolCall || in.StructuredOutput
}

// gate applies the hard requirements. Unknown metadata does not pass a hard
// requirement that the task depends on.
func gate(in Input, preset Preset, minContext int) []string {
	var reasons []string
	if !in.Available {
		reasons = append(reasons, "当前不可路由")
	}
	if preset.NeedsTools && !in.ToolCall {
		reasons = append(reasons, "缺少工具调用能力")
	}
	if preset.NeedsVision && !hasModality(in.InputModalities, "image") {
		reasons = append(reasons, "不支持图像输入")
	}
	if minContext > 0 && in.ContextWindow > 0 && in.ContextWindow < minContext {
		reasons = append(reasons, "上下文窗口不足")
	}
	return reasons
}

// Evaluate scores every input for one preset. Gated routes stay in the output
// (so operators can see why they were excluded) but sort after eligible ones.
func Evaluate(inputs []Input, preset Preset, options Options) []Result {
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	if options.LatencyGoodMS <= 0 {
		options.LatencyGoodMS = 2000
	}
	if options.LatencyBadMS <= options.LatencyGoodMS {
		options.LatencyBadMS = options.LatencyGoodMS + 1
	}
	minContext := preset.MinContextTokens
	if options.MinContextTokens > 0 {
		minContext = options.MinContextTokens
	}

	results := make([]Result, len(inputs))
	gated := make([]bool, len(inputs))
	for i, in := range inputs {
		reasons := gate(in, preset, minContext)
		gated[i] = len(reasons) == 0
		results[i] = Result{
			Provider: in.Provider, ModelID: in.ModelID, TaskType: preset.Task,
			Gate: len(reasons) == 0, GateReasons: reasons,
			CostPerTask: in.CostPerTask, QuotaCostPerTask: in.QuotaCostPerTask,
			SuccessRate: in.SuccessRate, P95LatencyMS: in.P95LatencyMS,
			QuotaHeadroom: in.QuotaHeadroom, SampleCount: in.SampleCount,
			UpdatedAt: in.UpdatedAt,
		}
	}

	// Percentiles are computed inside the eligible set: a gated route must not
	// shift the capability distribution.
	var poolL, poolB, poolI, poolCost []float64
	for i, in := range inputs {
		if !gated[i] {
			continue
		}
		if in.Capability.L != nil {
			poolL = append(poolL, *in.Capability.L)
		}
		if in.Capability.B != nil {
			poolB = append(poolB, *in.Capability.B)
		}
		if in.Capability.I != nil {
			poolI = append(poolI, *in.Capability.I)
		}
		if in.CostPerTask != nil {
			poolCost = append(poolCost, *in.CostPerTask)
		}
	}
	costReference := median(poolCost)
	if costReference <= 0 {
		costReference = smallestPositive(poolCost)
	}

	for i, in := range inputs {
		result := &results[i]
		if gated[i] {
			q := capabilityScore(in.Capability, poolL, poolB, poolI)
			result.CapabilityScore = q
			if q != nil {
				result.CapabilityParts = &CapabilityParts{
					L: percentilePtr(in.Capability.L, poolL),
					B: percentilePtr(in.Capability.B, poolB),
					I: percentilePtr(in.Capability.I, poolI),
				}
			}
			result.EconomyScore = economyScore(in.CostPerTask, costReference)
			result.AvailabilityScore, result.AvailabilityParts = availabilityScore(in, options)
			result.FitScore, result.FitParts = fitnessScore(in, preset)
			if costReference > 0 {
				result.CostReference = ptr(round(costReference))
			}
		}
		result.FinalScore = finalScore(result, preset)
		result.Confidence = confidence(result)
	}
	sortResults(results)
	return results
}

func percentilePtr(value *float64, pool []float64) *float64 {
	if value == nil {
		return nil
	}
	return ptr(round(percentile(*value, pool)))
}

func capabilityScore(capability Capability, poolL, poolB, poolI []float64) *float64 {
	var parts []weighted
	if capability.L != nil {
		parts = append(parts, weighted{percentile(*capability.L, poolL), 0.50})
	}
	if capability.B != nil {
		parts = append(parts, weighted{percentile(*capability.B, poolB), 0.30})
	}
	if capability.I != nil {
		parts = append(parts, weighted{percentile(*capability.I, poolI), 0.20})
	}
	return weightedMean(parts)
}

func economyScore(cost *float64, reference float64) *float64 {
	if cost == nil {
		return nil
	}
	if *cost <= 0 {
		return ptr(100)
	}
	if reference <= 0 {
		return ptr(100)
	}
	return ptr(round(100 / (1 + *cost/reference)))
}

func availabilityScore(in Input, options Options) (*float64, *AvailabilityParts) {
	parts := &AvailabilityParts{}
	var values []weighted
	if in.SuccessRate != nil {
		score := clamp(*in.SuccessRate*100, 0, 100)
		parts.SuccessRate = ptr(round(score))
		values = append(values, weighted{score, 0.60})
	}
	if in.P95LatencyMS != nil {
		score := 100 * clamp(
			(options.LatencyBadMS-*in.P95LatencyMS)/(options.LatencyBadMS-options.LatencyGoodMS), 0, 1)
		parts.LatencyScore = ptr(round(score))
		values = append(values, weighted{score, 0.25})
	}
	if in.QuotaHeadroom != nil {
		score := clamp(*in.QuotaHeadroom, 0, 100)
		parts.QuotaHeadroom = ptr(round(score))
		values = append(values, weighted{score, 0.15})
	}
	if len(values) == 0 {
		return nil, nil
	}
	return weightedMean(values), parts
}

func fitnessScore(in Input, preset Preset) (*float64, *FitParts) {
	if !knownMetadata(in) {
		return nil, nil
	}
	parts := &FitParts{}
	var modality *float64
	if preset.NeedsVision {
		if hasModality(in.InputModalities, "image") {
			modality = ptr(100)
		} else {
			modality = ptr(0)
		}
	} else if len(in.InputModalities) == 0 || hasModality(in.InputModalities, "text") {
		modality = ptr(100)
	} else {
		modality = ptr(0)
	}
	parts.Modality = modality

	var contextParts []weighted
	if in.ContextWindow > 0 && preset.ContextTarget > 0 {
		contextScore := 100 * clamp(float64(in.ContextWindow)/float64(preset.ContextTarget), 0, 1)
		contextParts = append(contextParts, weighted{contextScore, 0.70})
	}
	if in.MaxOutput > 0 && preset.OutputTarget > 0 {
		outputScore := 100 * clamp(float64(in.MaxOutput)/float64(preset.OutputTarget), 0, 1)
		contextParts = append(contextParts, weighted{outputScore, 0.30})
	}
	context := weightedMean(contextParts)
	parts.Context = context

	toolScore := 0.0
	if in.ToolCall {
		toolScore += 70
	}
	if in.StructuredOutput {
		toolScore += 30
	}
	tools := ptr(toolScore)
	parts.Tools = tools

	var values []weighted
	if modality != nil {
		values = append(values, weighted{*modality, 0.50})
	}
	if context != nil {
		values = append(values, weighted{*context, 0.25})
	}
	if tools != nil {
		values = append(values, weighted{*tools, 0.25})
	}
	return weightedMean(values), parts
}

func dimension(value *float64) float64 {
	if value == nil {
		return 50
	}
	return *value
}

func finalScore(result *Result, preset Preset) float64 {
	if !result.Gate {
		return 0
	}
	total := preset.CapabilityWeight + preset.EconomyWeight + preset.AvailabilityWeight + preset.FitnessWeight
	if total <= 0 {
		return 0
	}
	score := preset.CapabilityWeight*dimension(result.CapabilityScore) +
		preset.EconomyWeight*dimension(result.EconomyScore) +
		preset.AvailabilityWeight*dimension(result.AvailabilityScore) +
		preset.FitnessWeight*dimension(result.FitScore)
	return round(score / total)
}

// confidence separates "looks good but unmeasured" from "measured".
func confidence(result *Result) float64 {
	measured := 0
	for _, value := range []*float64{result.CapabilityScore, result.EconomyScore, result.AvailabilityScore, result.FitScore} {
		if value != nil {
			measured++
		}
	}
	samples := math.Min(1, float64(result.SampleCount)/100)
	return round(0.5*float64(measured)/4 + 0.5*samples)
}

func sortResults(results []Result) {
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Gate != b.Gate {
			return a.Gate
		}
		if a.FinalScore != b.FinalScore {
			return a.FinalScore > b.FinalScore
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.Provider < b.Provider
	})
}

// Order returns eligible model IDs in descending score order.
func Order(results []Result) []string {
	var models []string
	for _, result := range results {
		if result.Gate {
			models = append(models, result.ModelID)
		}
	}
	return models
}
