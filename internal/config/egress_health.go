package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
)

func applyProbeDefaults(p *EgressProbe) {
	if p.Scheduler == "" {
		p.Scheduler = "heap"
	}
	if p.BudgetPerSecond == 0 {
		p.BudgetPerSecond = 2
	}
	if p.BudgetBurst == 0 {
		p.BudgetBurst = 4
	}
	setBoolDefault(&p.CanaryEnabled, true)
	setBoolDefault(&p.CanaryAllowKeyTiers, false)
	if p.CanaryDefaultMode == "" {
		p.CanaryDefaultMode = "stream"
	}
	if p.CanaryConfigBackoffSeconds == 0 {
		p.CanaryConfigBackoffSeconds = 300
	}
	if p.CanaryMaxTokens == 0 {
		p.CanaryMaxTokens = 1
	}
	if p.CanaryMaxInflight == 0 {
		p.CanaryMaxInflight = 8
	}
	if p.CanaryTimeoutSeconds == 0 {
		p.CanaryTimeoutSeconds = 8
	}
	if p.CanaryDailyBudget == 0 {
		p.CanaryDailyBudget = 20000
	}
	if p.ActiveRecheckSeconds == 0 {
		p.ActiveRecheckSeconds = 600
	}
	if p.VerifyingRecheckSeconds == 0 {
		p.VerifyingRecheckSeconds = 30
	}
	if p.BackoffBaseSeconds == 0 {
		p.BackoffBaseSeconds = 30
	}
	if p.BackoffMaxSeconds == 0 {
		p.BackoffMaxSeconds = 1800
	}
	if p.JitterRatio == 0 {
		p.JitterRatio = 0.5
	}
	if p.TargetPauseSeconds == 0 {
		p.TargetPauseSeconds = 60
	}
	if p.MaxSchedulePerScan == 0 {
		p.MaxSchedulePerScan = 2000
	}
	if p.ClusterRecheckSeconds == 0 {
		p.ClusterRecheckSeconds = 600
	}
	if p.MaxScheduledKeys == 0 {
		p.MaxScheduledKeys = 50000
	}
}

func applyBootstrapDefaults(b *EgressBootstrap) {
	if b.Mode == "" {
		b.Mode = "direct"
	}
	b.URL = strings.TrimSpace(b.URL)
	b.ProbeURL = strings.TrimSpace(b.ProbeURL)
	if b.ProbeIntervalSeconds == 0 {
		b.ProbeIntervalSeconds = 30
	}
	if b.ProbeFailureThreshold == 0 {
		b.ProbeFailureThreshold = 3
	}
	if b.BootstrapLatencyBudgetMS == 0 {
		b.BootstrapLatencyBudgetMS = 3000
	}
	if b.FailureGraceSeconds == nil {
		value := 60
		b.FailureGraceSeconds = &value
	}
	if b.LocalFailureThreshold == 0 {
		b.LocalFailureThreshold = 0.3
	}
	if b.LocalFailureWindowSeconds == 0 {
		b.LocalFailureWindowSeconds = 300
	}
}

func applyHealthDefaults(h *EgressHealth) {
	if h.Mode == "" {
		h.Mode = "shadow"
	}
	if h.ConfidenceZ == 0 {
		h.ConfidenceZ = 1.96
	}
	if h.MinSamples == 0 {
		h.MinSamples = 96
	}
	if h.ValidMinSamples == 0 {
		h.ValidMinSamples = 46
	}
	if h.EvidenceBucketSeconds == 0 {
		h.EvidenceBucketSeconds = 60
	}
	if h.MaxAdmissionSamplesPerBucket == 0 {
		h.MaxAdmissionSamplesPerBucket = 10
	}
	if h.MinEvidenceBuckets == 0 {
		h.MinEvidenceBuckets = 5
	}
	if h.HTTP200Target == 0 {
		h.HTTP200Target = 0.95
	}
	if h.ValidTarget == 0 {
		h.ValidTarget = 0.90
	}
	if h.DecayHalfLifeSeconds == 0 {
		h.DecayHalfLifeSeconds = 3600
	}
	if h.RecentQuantile == 0 {
		h.RecentQuantile = 0.10
	}
	if h.SuspectRecentLower == 0 {
		h.SuspectRecentLower = 0.50
	}
	if h.SuspectRecoveryLower == 0 {
		h.SuspectRecoveryLower = 0.80
	}
	if h.MinRecentWeight == 0 {
		h.MinRecentWeight = 10
	}
	if h.SuspectFailures == 0 {
		h.SuspectFailures = 2
	}
	if h.SuspectLatencyMinSamples == 0 {
		h.SuspectLatencyMinSamples = 5
	}
	if h.SuspectRecoverySuccesses == 0 {
		h.SuspectRecoverySuccesses = 3
	}
	if h.ReverifySuccesses == 0 {
		h.ReverifySuccesses = 3
	}
	if h.UnknownStreakSuspect == 0 {
		h.UnknownStreakSuspect = 5
	}
	if h.StreamFailureSuspect == 0 {
		h.StreamFailureSuspect = 2
	}
	if h.OpenFailures == 0 {
		h.OpenFailures = 3
	}
	if h.OpenRateWindowSeconds == 0 {
		h.OpenRateWindowSeconds = 600
	}
	if h.OpenRateThreshold == 0 {
		h.OpenRateThreshold = 0.50
	}
	if h.HalfOpenSuccesses == 0 {
		h.HalfOpenSuccesses = 3
	}
	if h.HalfOpenMaxInflight == 0 {
		h.HalfOpenMaxInflight = 1
	}
	if h.DeadConfirmations == 0 {
		h.DeadConfirmations = 2
	}
	if h.DeadRecheckSeconds == 0 {
		h.DeadRecheckSeconds = 21600
	}
	if h.LatencyBudgetMS == 0 {
		h.LatencyBudgetMS = 8000
	}
	if h.MaxValidPeekBytes == 0 {
		h.MaxValidPeekBytes = 8192
	}
	if h.MaxKeysPerNode == 0 {
		h.MaxKeysPerNode = 4
	}
	if h.MaxPendingKeys == 0 {
		h.MaxPendingKeys = 10000
	}
	if h.MaxPendingObservations == 0 {
		h.MaxPendingObservations = 10000
	}
	if h.StableMinCapacity == 0 {
		h.StableMinCapacity = 50
	}
	if h.QuotaStaleSeconds == 0 {
		h.QuotaStaleSeconds = 300
	}
	if h.ExplorationRatio == 0 {
		h.ExplorationRatio = 0.10
	}
	if h.CandidateExplorationRatio == 0 {
		h.CandidateExplorationRatio = 0.02
	}
	if h.RouteSampleSize == 0 {
		h.RouteSampleSize = 64
	}
	setBoolDefault(&h.ColdStartFallback, true)
	if h.ReachableWindowSeconds == 0 {
		h.ReachableWindowSeconds = 600
	}
	if h.MaxDirtyRows == 0 {
		h.MaxDirtyRows = 5000
	}
}

func validateProbeScheduling(p *EgressProbe) error {
	if p.Scheduler != "heap" {
		return errors.New("egress.probe.scheduler must be heap")
	}
	if p.BudgetPerSecond < 0.1 || p.BudgetPerSecond > 100 {
		return errors.New("egress.probe.budget_per_second must be between 0.1 and 100")
	}
	if p.BudgetBurst < 1 || p.BudgetBurst > 1000 {
		return errors.New("egress.probe.budget_burst must be between 1 and 1000")
	}
	if p.CanaryMaxTokens < 1 || p.CanaryMaxTokens > 64 {
		return errors.New("egress.probe.canary_max_tokens must be between 1 and 64")
	}
	if p.CanaryConfigBackoffSeconds < 0 || p.CanaryConfigBackoffSeconds > 3600 {
		return errors.New("egress.probe.canary_config_backoff_seconds must be 0-3600")
	}
	if p.CanaryMaxInflight < 1 || p.CanaryMaxInflight > 64 {
		return errors.New("egress.probe.canary_max_inflight must be between 1 and 64")
	}
	if p.CanaryTimeoutSeconds < 1 || p.CanaryTimeoutSeconds > 120 {
		return errors.New("egress.probe.canary_timeout_seconds must be between 1 and 120")
	}
	if p.CanaryDailyBudget < 0 || p.CanaryDailyBudget > 1000000 {
		return errors.New("egress.probe.canary_daily_budget must be 0-1000000")
	}
	if p.CanaryKeyDailyBudget < 0 || p.CanaryKeyDailyBudget > 1000000 {
		return errors.New("egress.probe.canary_key_daily_budget must be 0-1000000")
	}
	if p.CanaryAllowKeyTiers != nil && *p.CanaryAllowKeyTiers && p.CanaryKeyDailyBudget == 0 {
		return errors.New("egress.probe.canary_allow_key_tiers requires canary_key_daily_budget > 0")
	}
	if p.CanaryDefaultMode != "stream" && p.CanaryDefaultMode != "json" {
		return errors.New("egress.probe.canary_default_mode must be stream or json")
	}
	if p.ActiveRecheckSeconds < 60 || p.ActiveRecheckSeconds > 86400 {
		return errors.New("egress.probe.active_recheck_seconds must be between 60 and 86400")
	}
	if p.VerifyingRecheckSeconds < 10 || p.VerifyingRecheckSeconds > 3600 {
		return errors.New("egress.probe.verifying_recheck_seconds must be between 10 and 3600")
	}
	if p.BackoffBaseSeconds < 1 || p.BackoffBaseSeconds > 3600 {
		return errors.New("egress.probe.backoff_base_seconds must be between 1 and 3600")
	}
	if p.BackoffMaxSeconds < p.BackoffBaseSeconds || p.BackoffMaxSeconds > 86400 {
		return errors.New("egress.probe.backoff_max_seconds must be >= base and <= 86400")
	}
	if p.JitterRatio < 0 || p.JitterRatio > 1 {
		return errors.New("egress.probe.jitter_ratio must be 0-1")
	}
	if p.TargetPauseSeconds < 0 || p.TargetPauseSeconds > 3600 {
		return errors.New("egress.probe.target_pause_seconds must be 0-3600")
	}
	if p.MaxSchedulePerScan < 1 || p.MaxSchedulePerScan > 100000 {
		return errors.New("egress.probe.max_schedule_per_scan must be between 1 and 100000")
	}
	if p.ClusterRecheckSeconds < 0 || p.ClusterRecheckSeconds > 86400 {
		return errors.New("egress.probe.cluster_recheck_seconds must be between 0 and 86400")
	}
	if p.MaxScheduledKeys < 100 || p.MaxScheduledKeys > 1000000 {
		return errors.New("egress.probe.max_scheduled_keys must be between 100 and 1000000")
	}
	return nil
}

func validateBootstrap(b *EgressBootstrap) error {
	if b.Mode != "direct" && b.Mode != "http_connect" {
		return errors.New("egress.bootstrap.mode must be direct or http_connect")
	}
	if b.Mode == "http_connect" {
		if b.URL == "" {
			return errors.New("egress.bootstrap.url is required when mode is http_connect")
		}
		parsed, err := url.Parse(b.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("egress.bootstrap.url must be an http(s) URL")
		}
	}
	if b.ProbeURL != "" {
		parsed, err := url.Parse(b.ProbeURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("egress.bootstrap.probe_url must be an http(s) URL")
		}
	}
	if b.ProbeIntervalSeconds < 10 || b.ProbeIntervalSeconds > 3600 {
		return errors.New("egress.bootstrap.probe_interval_seconds must be between 10 and 3600")
	}
	if b.ProbeFailureThreshold < 1 || b.ProbeFailureThreshold > 20 {
		return errors.New("egress.bootstrap.probe_failure_threshold must be between 1 and 20")
	}
	if b.BootstrapLatencyBudgetMS < 100 || b.BootstrapLatencyBudgetMS > 60000 {
		return errors.New("egress.bootstrap.bootstrap_latency_budget_ms must be between 100 and 60000")
	}
	if *b.FailureGraceSeconds < 0 || *b.FailureGraceSeconds > 600 {
		return errors.New("egress.bootstrap.failure_grace_seconds must be 0-600")
	}
	if b.LocalFailureThreshold < 0.1 || b.LocalFailureThreshold > 1 {
		return errors.New("egress.bootstrap.local_failure_threshold must be between 0.1 and 1.0")
	}
	if b.LocalFailureWindowSeconds < 60 || b.LocalFailureWindowSeconds > 3600 {
		return errors.New("egress.bootstrap.local_failure_window_seconds must be between 60 and 3600")
	}
	return nil
}

func validateHealth(h *EgressHealth) error {
	switch h.Mode {
	case "shadow", "prefer", "strict":
	default:
		return errors.New("egress.health.mode must be shadow, prefer or strict")
	}
	if h.ConfidenceZ < 1 || h.ConfidenceZ > 3 {
		return errors.New("egress.health.confidence_z must be between 1.0 and 3.0")
	}
	z1 := health.CheckpointZ(h.ConfidenceZ, 1)
	minHTTP := int(math.Ceil(h.HTTP200Target * z1 * z1 / (1 - h.HTTP200Target)))
	minValid := int(math.Ceil(h.ValidTarget * z1 * z1 / (1 - h.ValidTarget)))
	if h.MinSamples < 10 || h.MinSamples > 1000 || h.MinSamples < minHTTP {
		return fmt.Errorf("egress.health.min_samples must be between 10 and 1000 and at least %d", minHTTP)
	}
	if h.ValidMinSamples < 10 || h.ValidMinSamples > 1000 || h.ValidMinSamples < minValid {
		return fmt.Errorf("egress.health.valid_min_samples must be between 10 and 1000 and at least %d", minValid)
	}
	if h.HTTP200Target < 0.5 || h.HTTP200Target > 0.999 {
		return errors.New("egress.health.http200_target must be between 0.5 and 0.999")
	}
	if h.ValidTarget < 0.5 || h.ValidTarget > 0.999 {
		return errors.New("egress.health.valid_target must be between 0.5 and 0.999")
	}
	if h.EvidenceBucketSeconds < 10 || h.EvidenceBucketSeconds > 3600 {
		return errors.New("egress.health.evidence_bucket_seconds must be between 10 and 3600")
	}
	if h.MaxAdmissionSamplesPerBucket < 1 || h.MaxAdmissionSamplesPerBucket > 100 {
		return errors.New("egress.health.max_admission_samples_per_bucket must be between 1 and 100")
	}
	if h.MinEvidenceBuckets < 1 || h.MinEvidenceBuckets > 100 {
		return errors.New("egress.health.min_evidence_buckets must be between 1 and 100")
	}
	if h.DecayHalfLifeSeconds < 60 || h.DecayHalfLifeSeconds > 86400 {
		return errors.New("egress.health.decay_half_life_seconds must be between 60 and 86400")
	}
	if h.RecentQuantile < 0.01 || h.RecentQuantile > 0.5 {
		return errors.New("egress.health.recent_quantile must be between 0.01 and 0.50")
	}
	if h.SuspectRecentLower < 0.1 || h.SuspectRecentLower > 1 {
		return errors.New("egress.health.suspect_recent_lower must be between 0.1 and 1.0")
	}
	if h.SuspectRecoveryLower < 0.1 || h.SuspectRecoveryLower > 1 {
		return errors.New("egress.health.suspect_recovery_lower must be between 0.1 and 1.0")
	}
	if h.MinRecentWeight < 1 || h.MinRecentWeight > 100 {
		return errors.New("egress.health.min_recent_weight must be between 1 and 100")
	}
	if h.SuspectFailures < 1 || h.SuspectFailures > 20 {
		return errors.New("egress.health.suspect_failures must be between 1 and 20")
	}
	if h.SuspectLatencyMinSamples < 1 || h.SuspectLatencyMinSamples > 100 {
		return errors.New("egress.health.suspect_latency_min_samples must be between 1 and 100")
	}
	if h.SuspectRecoverySuccesses < 1 || h.SuspectRecoverySuccesses > 20 {
		return errors.New("egress.health.suspect_recovery_successes must be between 1 and 20")
	}
	if h.ReverifySuccesses < 1 || h.ReverifySuccesses > 20 {
		return errors.New("egress.health.reverify_successes must be between 1 and 20")
	}
	if h.UnknownStreakSuspect < 1 || h.UnknownStreakSuspect > 20 {
		return errors.New("egress.health.unknown_streak_suspect must be between 1 and 20")
	}
	if h.StreamFailureSuspect < 1 || h.StreamFailureSuspect > 20 {
		return errors.New("egress.health.stream_failure_suspect must be between 1 and 20")
	}
	if h.OpenFailures < 2 || h.OpenFailures > 20 {
		return errors.New("egress.health.open_failures must be between 2 and 20")
	}
	if h.OpenRateWindowSeconds < 60 || h.OpenRateWindowSeconds > 3600 {
		return errors.New("egress.health.open_rate_window_seconds must be between 60 and 3600")
	}
	if h.OpenRateThreshold < 0.05 || h.OpenRateThreshold > 1 {
		return errors.New("egress.health.open_rate_threshold must be between 0.05 and 1.0")
	}
	if h.HalfOpenSuccesses < 1 || h.HalfOpenSuccesses > 20 {
		return errors.New("egress.health.half_open_successes must be between 1 and 20")
	}
	if h.HalfOpenMaxInflight < 1 || h.HalfOpenMaxInflight > 4 {
		return errors.New("egress.health.half_open_max_inflight must be between 1 and 4")
	}
	if h.DeadConfirmations < 1 || h.DeadConfirmations > 10 {
		return errors.New("egress.health.dead_confirmations must be between 1 and 10")
	}
	if h.DeadRecheckSeconds < 300 || h.DeadRecheckSeconds > 604800 {
		return errors.New("egress.health.dead_recheck_seconds must be between 300 and 604800")
	}
	if h.LatencyBudgetMS < 100 || h.LatencyBudgetMS > 120000 {
		return errors.New("egress.health.latency_budget_ms must be between 100 and 120000")
	}
	if h.MaxValidPeekBytes < 1024 || h.MaxValidPeekBytes > 1048576 {
		return errors.New("egress.health.max_valid_peek_bytes must be between 1024 and 1048576")
	}
	if h.MaxKeysPerNode < 1 || h.MaxKeysPerNode > 32 {
		return errors.New("egress.health.max_keys_per_node must be between 1 and 32")
	}
	if h.MaxPendingKeys < 100 || h.MaxPendingKeys > 1000000 {
		return errors.New("egress.health.max_pending_keys must be between 100 and 1000000")
	}
	if h.MaxPendingObservations < 100 || h.MaxPendingObservations > 1000000 {
		return errors.New("egress.health.max_pending_observations must be between 100 and 1000000")
	}
	if h.StableMinCapacity < 1 || h.StableMinCapacity > 10000 {
		return errors.New("egress.health.stable_min_capacity must be between 1 and 10000")
	}
	if h.QuotaStaleSeconds < 0 || h.QuotaStaleSeconds > 86400 {
		return errors.New("egress.health.quota_stale_seconds must be between 0 and 86400")
	}
	if h.ExplorationRatio < 0 || h.ExplorationRatio > 0.5 {
		return errors.New("egress.health.exploration_ratio must be between 0 and 0.5")
	}
	if h.CandidateExplorationRatio < 0 || h.CandidateExplorationRatio > 0.1 ||
		h.CandidateExplorationRatio > h.ExplorationRatio {
		return errors.New("egress.health.candidate_exploration_ratio must be 0-0.1 and <= exploration_ratio")
	}
	if h.RouteSampleSize < 8 || h.RouteSampleSize > 256 {
		return errors.New("egress.health.route_sample_size must be between 8 and 256")
	}
	if h.ReachableWindowSeconds < 60 || h.ReachableWindowSeconds > 86400 {
		return errors.New("egress.health.reachable_window_seconds must be between 60 and 86400")
	}
	if h.MaxDirtyRows < 100 || h.MaxDirtyRows > 100000 {
		return errors.New("egress.health.max_dirty_rows must be between 100 and 100000")
	}
	return nil
}

// HealthKeyConfig converts the configuration into the health package form.
func (h *EgressHealth) HealthKeyConfig() health.Config {
	return health.Config{
		ConfidenceZ:            h.ConfidenceZ,
		MinSamples:             h.MinSamples,
		ValidMinSamples:        h.ValidMinSamples,
		EvidenceBucketSeconds:  h.EvidenceBucketSeconds,
		MaxSamplesPerBucket:    h.MaxAdmissionSamplesPerBucket,
		MinEvidenceBuckets:     h.MinEvidenceBuckets,
		HTTP200Target:          h.HTTP200Target,
		ValidTarget:            h.ValidTarget,
		DecayHalfLife:          time.Duration(h.DecayHalfLifeSeconds) * time.Second,
		RecentQuantile:         h.RecentQuantile,
		SuspectRecentLower:     h.SuspectRecentLower,
		MinRecentWeight:        h.MinRecentWeight,
		SuspectFailures:        h.SuspectFailures,
		SuspectLatencySamples:  h.SuspectLatencyMinSamples,
		SuspectRecoverySuccess: h.SuspectRecoverySuccesses,
		ReverifySuccesses:      h.ReverifySuccesses,
		UnknownStreakSuspect:   h.UnknownStreakSuspect,
		StreamFailureSuspect:   h.StreamFailureSuspect,
		OpenFailures:           h.OpenFailures,
		OpenRateWindow:         time.Duration(h.OpenRateWindowSeconds) * time.Second,
		OpenRateThreshold:      h.OpenRateThreshold,
		HalfOpenSuccesses:      h.HalfOpenSuccesses,
		HalfOpenMaxInflight:    h.HalfOpenMaxInflight,
		DeadConfirmations:      h.DeadConfirmations,
		DeadRecheck:            time.Duration(h.DeadRecheckSeconds) * time.Second,
		LatencyBudget:          time.Duration(h.LatencyBudgetMS) * time.Millisecond,
		BackoffBase:            30 * time.Second,
		BackoffMax:             30 * time.Minute,
		JitterRatio:            0.5,
	}
}

// FailureGrace returns the staging window, defaulting an unset value to 60s.
func (b *EgressBootstrap) FailureGrace() time.Duration {
	if b == nil || b.FailureGraceSeconds == nil {
		return 60 * time.Second
	}
	return time.Duration(*b.FailureGraceSeconds) * time.Second
}

// EnvironmentConfig converts bootstrap settings into the health form.
func (b *EgressBootstrap) EnvironmentConfig() health.EnvironmentConfig {
	return health.EnvironmentConfig{
		ProbeFailureThreshold:  b.ProbeFailureThreshold,
		BootstrapLatencyBudget: time.Duration(b.BootstrapLatencyBudgetMS) * time.Millisecond,
		FailureGrace:           b.FailureGrace(),
		LocalFailureThreshold:  b.LocalFailureThreshold,
		LocalFailureWindow:     time.Duration(b.LocalFailureWindowSeconds) * time.Second,
		TargetPause:            60 * time.Second,
	}
}
