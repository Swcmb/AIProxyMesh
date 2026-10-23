package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// EgressConfig is the optional exit-pool section. A nil pointer means the
// feature is absent and the gateway keeps its legacy proxy behaviour.
type EgressConfig struct {
	Enabled             bool                 `json:"enabled"`
	Subscriptions       []EgressSubscription `json:"subscriptions,omitempty"`
	ImportLegacyProxies *bool                `json:"import_legacy_proxies,omitempty"`
	Schedule            EgressSchedule       `json:"schedule,omitempty"`
	Protocols           EgressProtocols      `json:"protocols,omitempty"`
	Cooldown            EgressCooldown       `json:"cooldown,omitempty"`
	Failover            EgressFailover       `json:"failover,omitempty"`
	Probe               EgressProbe          `json:"probe,omitempty"`
	Bootstrap           EgressBootstrap      `json:"bootstrap,omitempty"`
	Health              EgressHealth         `json:"health,omitempty"`
	StatePath           string               `json:"state_path,omitempty"`
	MaxLatencyEntries   int                  `json:"max_latency_entries,omitempty"`
	GeoIP               *EgressGeoIP         `json:"geoip,omitempty"`
}

// EgressGeoIP configures the MaxMind-compatible country database used to label
// node regions. It is downloaded from operator configuration only, never from
// an API request.
type EgressGeoIP struct {
	// Enabled defaults to true; set it to false to switch region resolution off.
	Enabled     *bool  `json:"enabled,omitempty"`
	URL         string `json:"url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Path        string `json:"path,omitempty"`
	RefreshDays int    `json:"refresh_days,omitempty"`
}

// IsEnabled reports whether region resolution should run.
func (g *EgressGeoIP) IsEnabled() bool {
	return g != nil && (g.Enabled == nil || *g.Enabled)
}

// EgressSubscription is one remote subscription source.
type EgressSubscription struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
	// Content is an inline feed (pasted proxy list). A subscription carries
	// either a URL or inline content, never both.
	Content        string `json:"content,omitempty"`
	Enabled        bool   `json:"enabled"`
	RefreshSeconds int    `json:"refresh_seconds,omitempty"`
	// Protocols optionally restricts this source to the listed schemes. Empty
	// means the global egress.protocols setting decides.
	Protocols []string `json:"protocols,omitempty"`
	// Ephemeral marks a transient source whose dead nodes are evicted instead
	// of being retried forever. EvictDelaySeconds is how long a node must stay
	// continuously cooling before it is dropped from this subscription.
	Ephemeral         bool `json:"ephemeral,omitempty"`
	EvictDelaySeconds int  `json:"evict_delay_seconds,omitempty"`
}

// EgressSchedule holds the scoring weights. All-zero means "use defaults".
type EgressSchedule struct {
	HealthWeight          float64 `json:"health_weight,omitempty"`
	LatencyWeight         float64 `json:"latency_weight,omitempty"`
	RoutingWeight         float64 `json:"routing_weight,omitempty"`
	UpstreamSuccessWeight float64 `json:"upstream_success_weight,omitempty"`
	// QuotaWeight ranks exits by how recently their source IP was told the
	// upstream quota is spent. It is separate from the primary health metrics
	// because a 429 says nothing about the exit's own quality.
	QuotaWeight float64 `json:"quota_weight,omitempty"`
}

// EgressProtocols switches Tier A outbound protocols. Nil means enabled.
type EgressProtocols struct {
	HTTP    *bool `json:"http,omitempty"`
	HTTPS   *bool `json:"https,omitempty"`
	SOCKS5  *bool `json:"socks5,omitempty"`
	SOCKS5H *bool `json:"socks5h,omitempty"`
}

// EgressCooldown controls node-layer cooldown.
type EgressCooldown struct {
	BaseSeconds   int   `json:"base_seconds,omitempty"`
	MaxSeconds    int   `json:"max_seconds,omitempty"`
	RecoveryProbe *bool `json:"recovery_probe,omitempty"`
}

// EgressFailover controls retryable status handling.
type EgressFailover struct {
	StatusPolicy        map[string]string `json:"status_policy,omitempty"`
	RequestMethodPolicy map[string]string `json:"request_method_policy,omitempty"`
	HonorRetryAfter     *bool             `json:"honor_retry_after,omitempty"`
}

// EgressProbe controls active latency probing. Empty targets derive the probe
// URLs from upstream.zen/go/anonymous.
type EgressProbe struct {
	Targets         []string `json:"targets,omitempty"`
	IntervalSeconds int      `json:"interval_seconds,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	Concurrency     int      `json:"concurrency,omitempty"`
	// EgressIPEnabled turns on the low-frequency public exit-address probe.
	EgressIPEnabled         *bool  `json:"egress_ip_enabled,omitempty"`
	EgressIPIntervalSeconds int    `json:"egress_ip_interval_seconds,omitempty"`
	EgressIPTarget          string `json:"egress_ip_target,omitempty"`

	// Canary scheduling and budget.
	Scheduler                  string  `json:"scheduler,omitempty"`
	BudgetPerSecond            float64 `json:"budget_per_second,omitempty"`
	BudgetBurst                int     `json:"budget_burst,omitempty"`
	CanaryEnabled              *bool   `json:"canary_enabled,omitempty"`
	CanaryModel                string  `json:"canary_model,omitempty"`
	CanaryDefaultMode          string  `json:"canary_default_mode,omitempty"`
	CanaryConfigBackoffSeconds int     `json:"canary_config_backoff_seconds,omitempty"`
	CanaryMaxTokens            int     `json:"canary_max_tokens,omitempty"`
	CanaryMaxInflight          int     `json:"canary_max_inflight,omitempty"`
	CanaryDailyBudget          int     `json:"canary_daily_budget,omitempty"`
	CanaryAllowKeyTiers        *bool   `json:"canary_allow_key_tiers,omitempty"`
	CanaryKeyDailyBudget       int     `json:"canary_key_daily_budget,omitempty"`
	ActiveRecheckSeconds       int     `json:"active_recheck_seconds,omitempty"`
	VerifyingRecheckSeconds    int     `json:"verifying_recheck_seconds,omitempty"`
	BackoffBaseSeconds         int     `json:"backoff_base_seconds,omitempty"`
	BackoffMaxSeconds          int     `json:"backoff_max_seconds,omitempty"`
	JitterRatio                float64 `json:"jitter_ratio,omitempty"`
	TargetPauseSeconds         int     `json:"target_pause_seconds,omitempty"`
	MaxSchedulePerScan         int     `json:"max_schedule_per_scan,omitempty"`
	MaxScheduledKeys           int     `json:"max_scheduled_keys,omitempty"`
	// ClusterRecheckSeconds keeps exploration to one representative per source
	// network (IPv4 /24, IPv6 /48) until it answers.
	ClusterRecheckSeconds int `json:"cluster_recheck_seconds,omitempty"`
}

// EgressBootstrap selects the explicit Mesh-to-node link. Environment proxy
// variables are never consulted.
type EgressBootstrap struct {
	Mode                      string  `json:"mode,omitempty"`
	URL                       string  `json:"url,omitempty"`
	ProbeURL                  string  `json:"probe_url,omitempty"`
	ProbeIntervalSeconds      int     `json:"probe_interval_seconds,omitempty"`
	ProbeFailureThreshold     int     `json:"probe_failure_threshold,omitempty"`
	BootstrapLatencyBudgetMS  int     `json:"bootstrap_latency_budget_ms,omitempty"`
	FailureGraceSeconds       *int    `json:"failure_grace_seconds,omitempty"`
	LocalFailureThreshold     float64 `json:"local_failure_threshold,omitempty"`
	LocalFailureWindowSeconds int     `json:"local_failure_window_seconds,omitempty"`
}

// EgressHealth configures admission, recent-health decay and the key-level
// state machine. Zero values select the approved defaults.
type EgressHealth struct {
	Mode                         string  `json:"mode,omitempty"`
	ConfidenceZ                  float64 `json:"confidence_z,omitempty"`
	MinSamples                   int     `json:"min_samples,omitempty"`
	ValidMinSamples              int     `json:"valid_min_samples,omitempty"`
	EvidenceBucketSeconds        int     `json:"evidence_bucket_seconds,omitempty"`
	MaxAdmissionSamplesPerBucket int     `json:"max_admission_samples_per_bucket,omitempty"`
	MinEvidenceBuckets           int     `json:"min_evidence_buckets,omitempty"`
	HTTP200Target                float64 `json:"http200_target,omitempty"`
	ValidTarget                  float64 `json:"valid_target,omitempty"`
	DecayHalfLifeSeconds         int     `json:"decay_half_life_seconds,omitempty"`
	RecentQuantile               float64 `json:"recent_quantile,omitempty"`
	SuspectRecentLower           float64 `json:"suspect_recent_lower,omitempty"`
	SuspectRecoveryLower         float64 `json:"suspect_recovery_lower,omitempty"`
	MinRecentWeight              float64 `json:"min_recent_weight,omitempty"`
	SuspectFailures              int     `json:"suspect_failures,omitempty"`
	SuspectLatencyMinSamples     int     `json:"suspect_latency_min_samples,omitempty"`
	SuspectRecoverySuccesses     int     `json:"suspect_recovery_successes,omitempty"`
	ReverifySuccesses            int     `json:"reverify_successes,omitempty"`
	UnknownStreakSuspect         int     `json:"unknown_streak_suspect,omitempty"`
	StreamFailureSuspect         int     `json:"stream_failure_suspect,omitempty"`
	OpenFailures                 int     `json:"open_failures,omitempty"`
	OpenRateWindowSeconds        int     `json:"open_rate_window_seconds,omitempty"`
	OpenRateThreshold            float64 `json:"open_rate_threshold,omitempty"`
	HalfOpenSuccesses            int     `json:"half_open_successes,omitempty"`
	HalfOpenMaxInflight          int     `json:"half_open_max_inflight,omitempty"`
	DeadConfirmations            int     `json:"dead_confirmations,omitempty"`
	DeadRecheckSeconds           int     `json:"dead_recheck_seconds,omitempty"`
	LatencyBudgetMS              int     `json:"latency_budget_ms,omitempty"`
	MaxValidPeekBytes            int     `json:"max_valid_peek_bytes,omitempty"`
	MaxKeysPerNode               int     `json:"max_keys_per_node,omitempty"`
	MaxPendingKeys               int     `json:"max_pending_keys,omitempty"`
	MaxPendingObservations       int     `json:"max_pending_observations,omitempty"`
	StableMinCapacity            int     `json:"stable_min_capacity,omitempty"`
	// QuotaStaleSeconds is how long a 429 keeps an exit out of the verified
	// set when the upstream sent no Retry-After.
	QuotaStaleSeconds         int     `json:"quota_stale_seconds,omitempty"`
	ExplorationRatio          float64 `json:"exploration_ratio,omitempty"`
	CandidateExplorationRatio float64 `json:"candidate_exploration_ratio,omitempty"`
	RouteSampleSize           int     `json:"route_sample_size,omitempty"`
	ColdStartFallback         *bool   `json:"cold_start_fallback,omitempty"`
	ReachableWindowSeconds    int     `json:"reachable_window_seconds,omitempty"`
	MaxDirtyRows              int     `json:"max_dirty_rows,omitempty"`
}

// EgressEnabled reports whether the unified pool is active.
func (cfg Config) EgressEnabled() bool {
	return cfg.Egress != nil && cfg.Egress.Enabled
}

// LegacyProxiesImported reports whether proxies/proxyfile feed the pool.
func (e *EgressConfig) LegacyProxiesImported() bool {
	return e != nil && (e.ImportLegacyProxies == nil || *e.ImportLegacyProxies)
}

// LegacyContent returns proxies + proxyfile entries as a URI list.
func (cfg Config) LegacyContent() string {
	return strings.Join(cfg.RuntimeProxies(), "\n")
}

func (e *EgressConfig) applyDefaults(configPath string) {
	if e.ImportLegacyProxies == nil {
		enabled := true
		e.ImportLegacyProxies = &enabled
	}
	if e.Schedule.HealthWeight == 0 && e.Schedule.LatencyWeight == 0 &&
		e.Schedule.RoutingWeight == 0 && e.Schedule.UpstreamSuccessWeight == 0 {
		e.Schedule.HealthWeight = 0.30
		e.Schedule.LatencyWeight = 0.15
		e.Schedule.RoutingWeight = 0.15
		e.Schedule.UpstreamSuccessWeight = 0.40
	}
	// Quota freshness is appended to whatever the operator set: without it a
	// spent source IP keeps outranking a fresh one on the anonymous channel.
	if e.Schedule.QuotaWeight == 0 {
		e.Schedule.QuotaWeight = 0.15
	}
	setBoolDefault(&e.Protocols.HTTP, true)
	setBoolDefault(&e.Protocols.HTTPS, true)
	setBoolDefault(&e.Protocols.SOCKS5, true)
	setBoolDefault(&e.Protocols.SOCKS5H, true)
	if e.Cooldown.BaseSeconds == 0 {
		e.Cooldown.BaseSeconds = 30
	}
	if e.Cooldown.MaxSeconds == 0 {
		e.Cooldown.MaxSeconds = 900
	}
	setBoolDefault(&e.Cooldown.RecoveryProbe, true)
	if e.Failover.HonorRetryAfter == nil {
		enabled := true
		e.Failover.HonorRetryAfter = &enabled
	}
	if len(e.Failover.StatusPolicy) == 0 {
		e.Failover.StatusPolicy = map[string]string{
			"429": "switch-or-wait",
			"401": "key-rotation",
			"403": "key-rotation",
			"5xx": "retry-or-switch",
		}
	}
	if len(e.Failover.RequestMethodPolicy) == 0 {
		e.Failover.RequestMethodPolicy = map[string]string{"default": "retry-allowed"}
	}
	if e.Probe.IntervalSeconds == 0 {
		e.Probe.IntervalSeconds = 60
	}
	if e.Probe.TimeoutSeconds == 0 {
		e.Probe.TimeoutSeconds = 10
	}
	if e.Probe.Concurrency == 0 {
		e.Probe.Concurrency = 8
	}
	setBoolDefault(&e.Probe.EgressIPEnabled, true)
	if e.Probe.EgressIPIntervalSeconds == 0 {
		e.Probe.EgressIPIntervalSeconds = 21600
	}
	if e.Probe.EgressIPTarget == "" {
		e.Probe.EgressIPTarget = "https://cloudflare.com/cdn-cgi/trace"
	}
	applyProbeDefaults(&e.Probe)
	applyBootstrapDefaults(&e.Bootstrap)
	applyHealthDefaults(&e.Health)
	if e.StatePath == "" {
		e.StatePath = configPath + ".egress.db"
	}
	if e.MaxLatencyEntries == 0 {
		e.MaxLatencyEntries = 128
	}
}

func setBoolDefault(target **bool, value bool) {
	if *target == nil {
		v := value
		*target = &v
	}
}

func (e *EgressConfig) normalize(configPath string) error {
	e.applyDefaults(configPath)
	for i := range e.Subscriptions {
		sub := &e.Subscriptions[i]
		sub.ID = strings.TrimSpace(sub.ID)
		sub.Name = strings.TrimSpace(sub.Name)
		sub.URL = strings.TrimSpace(sub.URL)
		sub.Content = strings.TrimSpace(sub.Content)
		if sub.ID == "" {
			return errors.New("egress.subscriptions id must not be empty")
		}
		if sub.Name == "" {
			sub.Name = sub.ID
		}
		switch {
		case sub.URL != "" && sub.Content != "":
			return fmt.Errorf("egress.subscriptions[%q] must have either url or content", sub.ID)
		case sub.URL != "":
			u, err := url.Parse(sub.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("egress.subscriptions[%q] url must be an http(s) URL", sub.ID)
			}
		case sub.Content == "":
			return fmt.Errorf("egress.subscriptions[%q] needs a url or inline content", sub.ID)
		}
		if sub.RefreshSeconds < 0 {
			return fmt.Errorf("egress.subscriptions[%q] refresh_seconds must not be negative", sub.ID)
		}
		if sub.RefreshSeconds > 0 && sub.RefreshSeconds < 30 {
			return fmt.Errorf("egress.subscriptions[%q] refresh_seconds must be at least 30", sub.ID)
		}
		if sub.Content != "" && sub.RefreshSeconds > 0 {
			return fmt.Errorf("egress.subscriptions[%q] uses inline content and must not set refresh_seconds", sub.ID)
		}
		sub.Protocols = normalizeProtocolNames(sub.Protocols)
		for _, protocol := range sub.Protocols {
			if !validEgressProtocol(protocol) {
				return fmt.Errorf("egress.subscriptions[%q] protocols contains unsupported scheme %q", sub.ID, protocol)
			}
		}
		if sub.Ephemeral && sub.EvictDelaySeconds == 0 {
			sub.EvictDelaySeconds = 600
		}
		if sub.EvictDelaySeconds < 0 {
			return fmt.Errorf("egress.subscriptions[%q] evict_delay_seconds must not be negative", sub.ID)
		}
		if sub.EvictDelaySeconds > 0 && sub.EvictDelaySeconds < 60 {
			return fmt.Errorf("egress.subscriptions[%q] evict_delay_seconds must be at least 60", sub.ID)
		}
		if sub.EvictDelaySeconds > 86400 {
			return fmt.Errorf("egress.subscriptions[%q] evict_delay_seconds must be at most 86400", sub.ID)
		}
	}
	seen := make(map[string]struct{}, len(e.Subscriptions))
	for _, sub := range e.Subscriptions {
		if _, duplicate := seen[sub.ID]; duplicate {
			return fmt.Errorf("egress.subscriptions contains duplicate id %q", sub.ID)
		}
		seen[sub.ID] = struct{}{}
	}
	if e.Schedule.HealthWeight < 0 || e.Schedule.LatencyWeight < 0 ||
		e.Schedule.RoutingWeight < 0 || e.Schedule.UpstreamSuccessWeight < 0 ||
		e.Schedule.QuotaWeight < 0 {
		return errors.New("egress.schedule weights must not be negative")
	}
	if e.Schedule.HealthWeight+e.Schedule.LatencyWeight+e.Schedule.RoutingWeight+
		e.Schedule.UpstreamSuccessWeight+e.Schedule.QuotaWeight <= 0 {
		return errors.New("egress.schedule weights must not all be zero")
	}
	if e.Cooldown.BaseSeconds < 1 || e.Cooldown.BaseSeconds > 86400 {
		return errors.New("egress.cooldown.base_seconds must be between 1 and 86400")
	}
	if e.Cooldown.MaxSeconds < e.Cooldown.BaseSeconds || e.Cooldown.MaxSeconds > 604800 {
		return errors.New("egress.cooldown.max_seconds must be >= base_seconds and <= 604800")
	}
	if e.Probe.IntervalSeconds < 10 || e.Probe.IntervalSeconds > 86400 {
		return errors.New("egress.probe.interval_seconds must be between 10 and 86400")
	}
	if e.Probe.TimeoutSeconds < 1 || e.Probe.TimeoutSeconds > 300 {
		return errors.New("egress.probe.timeout_seconds must be between 1 and 300")
	}
	if e.Probe.Concurrency < 1 || e.Probe.Concurrency > 64 {
		return errors.New("egress.probe.concurrency must be between 1 and 64")
	}
	if e.Probe.EgressIPIntervalSeconds < 60 || e.Probe.EgressIPIntervalSeconds > 604800 {
		return errors.New("egress.probe.egress_ip_interval_seconds must be between 60 and 604800")
	}
	if target := strings.TrimSpace(e.Probe.EgressIPTarget); target != "" {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("egress.probe.egress_ip_target must be an http(s) URL")
		}
	}
	for _, target := range e.Probe.Targets {
		u, err := url.Parse(strings.TrimSpace(target))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("egress.probe.targets contains invalid URL %q", target)
		}
	}
	if err := validateProbeScheduling(&e.Probe); err != nil {
		return err
	}
	if err := validateBootstrap(&e.Bootstrap); err != nil {
		return err
	}
	if err := validateHealth(&e.Health); err != nil {
		return err
	}
	if e.MaxLatencyEntries < 1 || e.MaxLatencyEntries > 4096 {
		return errors.New("egress.max_latency_entries must be between 1 and 4096")
	}
	e.StatePath = strings.TrimSpace(e.StatePath)
	if e.StatePath == "" {
		e.StatePath = filepath.Clean(configPath + ".egress.db")
	}
	if e.GeoIP != nil {
		geo := e.GeoIP
		geo.URL = strings.TrimSpace(geo.URL)
		geo.Path = strings.TrimSpace(geo.Path)
		geo.SHA256 = strings.TrimSpace(geo.SHA256)
		if geo.Path == "" {
			geo.Path = filepath.Clean(e.StatePath + ".geoip.mmdb")
		}
		if geo.RefreshDays < 0 || geo.RefreshDays > 365 {
			return errors.New("egress.geoip.refresh_days must be 0-365")
		}
		if geo.URL != "" {
			parsed, err := url.Parse(geo.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("egress.geoip.url must be an http(s) URL")
			}
		}
		if geo.SHA256 != "" && len(geo.SHA256) != 64 {
			return errors.New("egress.geoip.sha256 must be a 64-character hex digest")
		}
	}
	return nil
}

func cloneEgress(in *EgressConfig) *EgressConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Subscriptions = make([]EgressSubscription, len(in.Subscriptions))
	copy(out.Subscriptions, in.Subscriptions)
	for i := range out.Subscriptions {
		out.Subscriptions[i].Protocols = append([]string(nil), in.Subscriptions[i].Protocols...)
	}
	out.Probe.Targets = append([]string(nil), in.Probe.Targets...)
	if in.Bootstrap.FailureGraceSeconds != nil {
		value := *in.Bootstrap.FailureGraceSeconds
		out.Bootstrap.FailureGraceSeconds = &value
	}
	if in.Probe.CanaryEnabled != nil {
		value := *in.Probe.CanaryEnabled
		out.Probe.CanaryEnabled = &value
	}
	if in.Probe.CanaryAllowKeyTiers != nil {
		value := *in.Probe.CanaryAllowKeyTiers
		out.Probe.CanaryAllowKeyTiers = &value
	}
	if in.Health.ColdStartFallback != nil {
		value := *in.Health.ColdStartFallback
		out.Health.ColdStartFallback = &value
	}
	if in.GeoIP != nil {
		geo := *in.GeoIP
		out.GeoIP = &geo
	}
	if in.Failover.StatusPolicy != nil {
		out.Failover.StatusPolicy = make(map[string]string, len(in.Failover.StatusPolicy))
		for key, value := range in.Failover.StatusPolicy {
			out.Failover.StatusPolicy[key] = value
		}
	}
	if in.Failover.RequestMethodPolicy != nil {
		out.Failover.RequestMethodPolicy = make(map[string]string, len(in.Failover.RequestMethodPolicy))
		for key, value := range in.Failover.RequestMethodPolicy {
			out.Failover.RequestMethodPolicy[key] = value
		}
	}
	return &out
}

// ProtocolAllowed reports whether a Tier A scheme may be used.
func (e *EgressConfig) ProtocolAllowed(scheme string) bool {
	if e == nil {
		return true
	}
	enabled := func(value *bool) bool { return value == nil || *value }
	switch strings.ToLower(scheme) {
	case "http":
		return enabled(e.Protocols.HTTP)
	case "https":
		return enabled(e.Protocols.HTTPS)
	case "socks5":
		return enabled(e.Protocols.SOCKS5)
	case "socks5h":
		return enabled(e.Protocols.SOCKS5H)
	case "direct":
		return true
	default:
		return false
	}
}

func normalizeProtocolNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.ToLower(strings.TrimSpace(value))
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func validEgressProtocol(name string) bool {
	switch name {
	case "http", "https", "socks5", "socks5h":
		return true
	}
	return false
}
