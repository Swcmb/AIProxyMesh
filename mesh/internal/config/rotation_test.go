package config

import "testing"

func TestRotationAcceptsAnyAliasWithoutOrder(t *testing.T) {
	cfg := RotationConfig{}
	cfg.Defaults()
	if err := cfg.Validate([]string{"deepseek-v4.1-flash"}); err != nil {
		t.Fatalf("default rotation = %v", err)
	}
	if cfg.SOTA.Alias != "sota" || cfg.Sweet.Alias != "sweet" || cfg.FirstOutputSeconds != 30 {
		t.Fatalf("defaults = %+v", cfg)
	}

	cfg.Sweet.Alias = "sota"
	if err := cfg.Validate(nil); err == nil {
		t.Fatal("duplicate aliases must be rejected")
	}

	cfg.SOTA.Alias = "sota"
	cfg.Sweet.Alias = "deepseek-v4.1-flash"
	if err := cfg.Validate([]string{"deepseek-v4.1-flash"}); err == nil {
		t.Fatal("alias equal to a real model id must be rejected")
	}

	cfg.Sweet.Alias = "sweet"
	cfg.FirstOutputSeconds = 0
	if err := cfg.Validate(nil); err == nil {
		t.Fatal("first output timeout must be validated")
	}
}

func TestRankingDefaultsAndValidation(t *testing.T) {
	cfg := RankingConfig{}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults = %v", err)
	}
	if cfg.SOTATask != "agent-coding" || cfg.SweetTask != "general-text" {
		t.Fatalf("task defaults = %+v", cfg)
	}
	if cfg.LatencyGoodMS != 2000 || cfg.LatencyBadMS != 15000 {
		t.Fatalf("latency defaults = %+v", cfg)
	}
	if cfg.StandardInputTokens == 0 || cfg.StandardOutputTokens == 0 || cfg.StandardCachedTokens == 0 {
		t.Fatalf("standard token profile = %+v", cfg)
	}

	cfg.SOTATask = "unknown"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown preset must be rejected")
	}
	cfg.SOTATask = "vision"
	cfg.LatencyBadMS = cfg.LatencyGoodMS
	if err := cfg.Validate(); err == nil {
		t.Fatal("bad latency threshold must be rejected")
	}
	cfg.LatencyBadMS = 15000
	cfg.QuotaHeadroom = map[string]float64{"go": 130}
	if err := cfg.Validate(); err == nil {
		t.Fatal("quota headroom above 100 must be rejected")
	}
	cfg.QuotaHeadroom = map[string]float64{"go": 80}
	over := 120.0
	cfg.Capabilities = map[string]RankingCapability{"model": {L: &over}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("benchmark score above 100 must be rejected")
	}
}

func TestNormalizeCarriesRanking(t *testing.T) {
	cfg := Config{
		Listen:     "127.0.0.1:0",
		ServerKeys: []string{"k"},
		GoKeys:     []string{"go"},
		Anonymous:  false,
		Prefer:     TierGo,
		Upstream:   UpstreamConfig{Zen: "https://opencode.ai/zen", Go: "https://opencode.ai/zen/go", Anonymous: "https://opencode.ai/inference"},
		Retry:      RetryConfig{MaxAttempts: 3, TimeoutSeconds: 60},
		Models:     ModelsConfig{RefreshSeconds: 300, Protocols: map[string]string{}},
		Performance: PerformanceConfig{
			MaxIdleConns: 16, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 0,
			IdleConnTimeoutSeconds: 30, ConnectTimeoutSeconds: 2, FailureCooldownSeconds: 15,
		},
		Logging: LoggingConfig{Level: "info", RingSize: 100},
		WebUI:   WebUIConfig{Listen: "127.0.0.1:0", SessionTTLMinutes: 60},
		Ranking: RankingConfig{SOTATask: "vision", GoMonthlyCostUSD: 10, GoMonthlyTasks: 1000},
	}
	normalized, err := Normalize("", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Ranking.SOTATask != "vision" || normalized.Ranking.GoMonthlyCostUSD != 10 {
		t.Fatalf("ranking = %+v", normalized.Ranking)
	}
	if normalized.Ranking.LatencyBadMS != 15000 {
		t.Fatalf("ranking defaults = %+v", normalized.Ranking)
	}
	clone := Clone(normalized)
	clone.Ranking.Capabilities["x"] = RankingCapability{}
	if _, ok := normalized.Ranking.Capabilities["x"]; ok {
		t.Fatal("Clone shared the ranking capability map")
	}
}
