package ranking

import (
	"math"
	"testing"
)

func f64(value float64) *float64 { return &value }

func assertScore(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if math.Abs(*got-want) > 0.6 {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func TestEvaluateGatesHardRequirements(t *testing.T) {
	preset, ok := PresetFor(string(Vision))
	if !ok {
		t.Fatal("vision preset missing")
	}
	inputs := []Input{
		{Provider: "go", ModelID: "vision-model", Available: true, InputModalities: []string{"text", "image"}, ContextWindow: 200000, ToolCall: true},
		{Provider: "go", ModelID: "text-model", Available: true, InputModalities: []string{"text"}, ContextWindow: 200000, ToolCall: true},
		{Provider: "zen", ModelID: "offline-model", Available: false, InputModalities: []string{"text", "image"}, ContextWindow: 200000},
	}
	results := Evaluate(inputs, preset, Options{})
	byModel := map[string]Result{}
	for _, result := range results {
		byModel[result.ModelID] = result
	}
	if !byModel["vision-model"].Gate {
		t.Fatalf("vision model gated: %+v", byModel["vision-model"].GateReasons)
	}
	if byModel["text-model"].Gate {
		t.Fatal("text model must fail the vision gate")
	}
	if byModel["text-model"].FinalScore != 0 {
		t.Fatalf("gated final score = %v", byModel["text-model"].FinalScore)
	}
	if len(byModel["text-model"].GateReasons) == 0 {
		t.Fatal("gated route must explain why")
	}
	if byModel["offline-model"].Gate {
		t.Fatal("unavailable route must be gated")
	}
	if results[0].ModelID != "vision-model" {
		t.Fatalf("eligible routes must sort first, got %q", results[0].ModelID)
	}
}

func TestCapabilityPercentilesRenormalizeMissingMetrics(t *testing.T) {
	preset, _ := PresetFor(string(GeneralText))
	inputs := []Input{
		{Provider: "go", ModelID: "full", Available: true, Capability: Capability{L: f64(90), B: f64(60), I: f64(50)}},
		{Provider: "go", ModelID: "benchmark-only", Available: true, Capability: Capability{L: f64(95)}},
		{Provider: "go", ModelID: "unmeasured", Available: true},
	}
	results := Evaluate(inputs, preset, Options{})
	byModel := map[string]Result{}
	for _, result := range results {
		byModel[result.ModelID] = result
	}
	// L has two entries (90 and 95), B and I have one each.
	assertScore(t, "full L", byModel["full"].CapabilityParts.L, 25)
	assertScore(t, "full B", byModel["full"].CapabilityParts.B, 50)
	assertScore(t, "full I", byModel["full"].CapabilityParts.I, 50)
	if byModel["full"].CapabilityScore == nil || math.Abs(*byModel["full"].CapabilityScore-37.5) > 0.6 {
		t.Fatalf("full capability = %v, want 37.5", byModel["full"].CapabilityScore)
	}
	// A single-metric route is scored from that metric alone.
	if byModel["benchmark-only"].CapabilityScore == nil || math.Abs(*byModel["benchmark-only"].CapabilityScore-75) > 0.6 {
		t.Fatalf("benchmark-only capability = %v, want 75", byModel["benchmark-only"].CapabilityScore)
	}
	if byModel["unmeasured"].CapabilityScore != nil {
		t.Fatalf("unmeasured capability = %v, want nil", *byModel["unmeasured"].CapabilityScore)
	}
	// A missing dimension is neutral in the final score, not zero.
	if math.Abs(byModel["unmeasured"].FinalScore-50) > 0.01 {
		t.Fatalf("unmeasured final = %v, want neutral 50", byModel["unmeasured"].FinalScore)
	}
	if byModel["unmeasured"].Confidence > byModel["full"].Confidence {
		t.Fatal("unmeasured route must carry less confidence")
	}
}

func TestEconomyUsesMedianCost(t *testing.T) {
	preset, _ := PresetFor(string(GeneralText))
	inputs := []Input{
		{Provider: "zen", ModelID: "free", Available: true, CostPerTask: f64(0)},
		{Provider: "go", ModelID: "cheap", Available: true, CostPerTask: f64(0.01)},
		{Provider: "zen", ModelID: "pricey", Available: true, CostPerTask: f64(0.10)},
	}
	results := Evaluate(inputs, preset, Options{})
	byModel := map[string]Result{}
	for _, result := range results {
		byModel[result.ModelID] = result
	}
	// Median of 0, 0.01, 0.10 is 0.01.
	assertScore(t, "reference", byModel["cheap"].CostReference, 0.01)
	assertScore(t, "free economy", byModel["free"].EconomyScore, 100)
	assertScore(t, "median economy", byModel["cheap"].EconomyScore, 50)
	assertScore(t, "pricey economy", byModel["pricey"].EconomyScore, 100/(1+10))
}

func TestAvailabilityLatencyClip(t *testing.T) {
	preset, _ := PresetFor(string(GeneralText))
	input := Input{
		Provider: "go", ModelID: "route", Available: true,
		SuccessRate: f64(0.9), P95LatencyMS: f64(8000), QuotaHeadroom: f64(40),
	}
	results := Evaluate([]Input{input}, preset, Options{LatencyGoodMS: 2000, LatencyBadMS: 12000})
	score := results[0].AvailabilityScore
	if score == nil {
		t.Fatal("availability score missing")
	}
	// 0.6*90 + 0.25*40 + 0.15*40 = 54 + 10 + 6
	assertScore(t, "availability", score, 54+10+6)
	if *results[0].AvailabilityParts.LatencyScore != 40 {
		t.Fatalf("latency score = %v", *results[0].AvailabilityParts.LatencyScore)
	}
}

func TestFitnessRewardsTaskAlignment(t *testing.T) {
	preset, _ := PresetFor(string(AgentCoding))
	toolModel := Input{
		Provider: "go", ModelID: "agent", Available: true, ToolCall: true, StructuredOutput: true,
		ContextWindow: 256000, MaxOutput: 16000, InputModalities: []string{"text"},
	}
	shortModel := Input{
		Provider: "go", ModelID: "short", Available: true, ToolCall: true,
		ContextWindow: 64000, MaxOutput: 4000, InputModalities: []string{"text"},
	}
	results := Evaluate([]Input{toolModel, shortModel}, preset, Options{})
	byModel := map[string]Result{}
	for _, result := range results {
		byModel[result.ModelID] = result
	}
	if !byModel["short"].Gate {
		t.Fatalf("64k context should pass the default coding gate: %+v", byModel["short"].GateReasons)
	}
	if *byModel["agent"].FitParts.Context != 100 {
		t.Fatalf("context fit = %v", *byModel["agent"].FitParts.Context)
	}
	if *byModel["agent"].FitScore <= *byModel["short"].FitScore {
		t.Fatalf("agent fit %v <= short fit %v", *byModel["agent"].FitScore, *byModel["short"].FitScore)
	}
	if Order(results)[0] != "agent" {
		t.Fatalf("order = %v", Order(results))
	}
}

func TestSmallContextFailsCodingGate(t *testing.T) {
	preset, _ := PresetFor(string(AgentCoding))
	inputs := []Input{
		{Provider: "go", ModelID: "tiny", Available: true, ToolCall: true, ContextWindow: 32000, MaxOutput: 8000},
	}
	results := Evaluate(inputs, preset, Options{})
	if results[0].Gate {
		t.Fatal("32k context must be gated by the 64k coding requirement")
	}
	found := false
	for _, reason := range results[0].GateReasons {
		if reason == "上下文窗口不足" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gate reasons = %v", results[0].GateReasons)
	}
	// The operator can relax the requirement.
	relaxed := Evaluate(inputs, preset, Options{MinContextTokens: 16000})
	if !relaxed[0].Gate {
		t.Fatalf("relaxed gate = %v", relaxed[0].GateReasons)
	}
}

func TestRotationOrderUsesGoRoutesOnly(t *testing.T) {
	preset, _ := PresetFor(string(AgentCoding))
	inputs := []Input{
		{Provider: "go", ModelID: "go-a", Available: true, ToolCall: true, ContextWindow: 200000, Capability: Capability{L: f64(70)}},
		{Provider: "go", ModelID: "go-b", Available: true, ToolCall: true, ContextWindow: 200000, Capability: Capability{L: f64(90)}},
		{Provider: "zen", ModelID: "zen-top", Available: true, ToolCall: true, ContextWindow: 200000, Capability: Capability{L: f64(100)}},
	}
	order := Order(Evaluate(inputs, preset, Options{}))
	if len(order) != 3 || order[0] != "zen-top" {
		t.Fatalf("full ranking order = %v", order)
	}
	results := Evaluate(inputs, preset, Options{})
	var goOrder []string
	for _, result := range results {
		if result.Provider == "go" {
			goOrder = append(goOrder, result.ModelID)
		}
	}
	if len(goOrder) != 2 || goOrder[0] != "go-b" {
		t.Fatalf("go subset order = %v", goOrder)
	}
}

func TestPresetsCoverTasksAndWeights(t *testing.T) {
	for _, preset := range Presets() {
		total := preset.CapabilityWeight + preset.EconomyWeight + preset.AvailabilityWeight + preset.FitnessWeight
		if math.Abs(total-1) > 0.001 {
			t.Fatalf("%s weights sum to %v", preset.Task, total)
		}
		if _, ok := PresetFor(string(preset.Task)); !ok {
			t.Fatalf("preset %s not resolvable", preset.Task)
		}
	}
	coding, _ := PresetFor(string(AgentCoding))
	if !coding.NeedsTools {
		t.Fatal("agent coding must require tool calling")
	}
	if _, ok := PresetFor("unknown"); ok {
		t.Fatal("unknown task must not resolve")
	}
}
