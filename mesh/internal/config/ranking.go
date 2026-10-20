package config

import "fmt"

// RankingCapability is the 0-100 benchmark triple behind the capability score.
// A missing value is renormalized out of the score.
type RankingCapability struct {
	L *float64 `json:"l,omitempty"`
	B *float64 `json:"b,omitempty"`
	I *float64 `json:"i,omitempty"`
}

// RankingConfig controls the provider+model ranking that drives rotation.
type RankingConfig struct {
	// SOTATask and SweetTask map the two rotation aliases to task presets.
	SOTATask  string `json:"sota_task,omitempty"`
	SweetTask string `json:"sweet_task,omitempty"`
	// MinContextTokens overrides the preset hard requirement when positive.
	MinContextTokens int `json:"min_context_tokens,omitempty"`
	// Latency scores use a linear clip between good and bad.
	LatencyGoodMS int `json:"latency_good_ms,omitempty"`
	LatencyBadMS  int `json:"latency_bad_ms,omitempty"`
	// Standard task profile used to turn published prices into cost per task.
	StandardInputTokens  int `json:"standard_input_tokens,omitempty"`
	StandardOutputTokens int `json:"standard_output_tokens,omitempty"`
	StandardCachedTokens int `json:"standard_cached_tokens,omitempty"`
	// GoMonthlyCostUSD amortizes a subscription over GoMonthlyTasks. Zero
	// falls back to published token prices for Go routes.
	GoMonthlyCostUSD float64 `json:"go_monthly_cost_usd,omitempty"`
	GoMonthlyTasks   int     `json:"go_monthly_tasks,omitempty"`
	// QuotaHeadroom maps provider (zen/go) to a 0-100 remaining-quota score.
	QuotaHeadroom map[string]float64 `json:"quota_headroom,omitempty"`
	// Capabilities maps a model ID (or "provider:model") to L/B/I benchmarks.
	// A provider-qualified key wins over the bare model ID.
	Capabilities map[string]RankingCapability `json:"capabilities,omitempty"`
}

func (c *RankingConfig) Defaults() {
	if c.SOTATask == "" {
		c.SOTATask = "agent-coding"
	}
	if c.SweetTask == "" {
		c.SweetTask = "general-text"
	}
	if c.LatencyGoodMS == 0 {
		c.LatencyGoodMS = 2000
	}
	if c.LatencyBadMS == 0 {
		c.LatencyBadMS = 15000
	}
	if c.StandardInputTokens == 0 {
		c.StandardInputTokens = 60000
	}
	if c.StandardOutputTokens == 0 {
		c.StandardOutputTokens = 8000
	}
	if c.StandardCachedTokens == 0 {
		c.StandardCachedTokens = 20000
	}
	if c.GoMonthlyTasks == 0 {
		c.GoMonthlyTasks = 1000
	}
	if c.QuotaHeadroom == nil {
		c.QuotaHeadroom = map[string]float64{}
	}
	if c.Capabilities == nil {
		c.Capabilities = map[string]RankingCapability{}
	}
}

func (c RankingConfig) Validate() error {
	for _, task := range []string{c.SOTATask, c.SweetTask} {
		if task != "agent-coding" && task != "general-text" && task != "vision" {
			return fmt.Errorf("ranking task %q must be agent-coding, general-text or vision", task)
		}
	}
	if c.MinContextTokens < 0 {
		return fmt.Errorf("ranking.min_context_tokens must not be negative")
	}
	if c.LatencyGoodMS <= 0 || c.LatencyBadMS <= c.LatencyGoodMS {
		return fmt.Errorf("ranking latency thresholds must satisfy 0 < good < bad")
	}
	if c.StandardInputTokens < 0 || c.StandardOutputTokens < 0 || c.StandardCachedTokens < 0 {
		return fmt.Errorf("ranking standard token counts must not be negative")
	}
	if c.GoMonthlyCostUSD < 0 || c.GoMonthlyTasks < 0 {
		return fmt.Errorf("ranking Go subscription cost and task count must not be negative")
	}
	for provider, headroom := range c.QuotaHeadroom {
		if headroom < 0 || headroom > 100 {
			return fmt.Errorf("ranking.quota_headroom[%s] must be 0-100", provider)
		}
	}
	for model, capability := range c.Capabilities {
		for name, value := range map[string]*float64{"l": capability.L, "b": capability.B, "i": capability.I} {
			if value != nil && (*value < 0 || *value > 100) {
				return fmt.Errorf("ranking.capabilities[%s].%s must be 0-100", model, name)
			}
		}
	}
	return nil
}
