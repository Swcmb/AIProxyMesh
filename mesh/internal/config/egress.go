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
	StatePath           string               `json:"state_path,omitempty"`
	MaxLatencyEntries   int                  `json:"max_latency_entries,omitempty"`
}

// EgressSubscription is one remote subscription source.
type EgressSubscription struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	URL            string `json:"url"`
	Enabled        bool   `json:"enabled"`
	RefreshSeconds int    `json:"refresh_seconds,omitempty"`
}

// EgressSchedule holds the scoring weights. All-zero means "use defaults".
type EgressSchedule struct {
	HealthWeight          float64 `json:"health_weight,omitempty"`
	LatencyWeight         float64 `json:"latency_weight,omitempty"`
	RoutingWeight         float64 `json:"routing_weight,omitempty"`
	UpstreamSuccessWeight float64 `json:"upstream_success_weight,omitempty"`
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
		if sub.ID == "" {
			return errors.New("egress.subscriptions id must not be empty")
		}
		if sub.Name == "" {
			sub.Name = sub.ID
		}
		u, err := url.Parse(sub.URL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("egress.subscriptions[%q] url must be an http(s) URL", sub.ID)
		}
		if sub.RefreshSeconds < 0 {
			return fmt.Errorf("egress.subscriptions[%q] refresh_seconds must not be negative", sub.ID)
		}
		if sub.RefreshSeconds > 0 && sub.RefreshSeconds < 30 {
			return fmt.Errorf("egress.subscriptions[%q] refresh_seconds must be at least 30", sub.ID)
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
		e.Schedule.RoutingWeight < 0 || e.Schedule.UpstreamSuccessWeight < 0 {
		return errors.New("egress.schedule weights must not be negative")
	}
	if e.Schedule.HealthWeight+e.Schedule.LatencyWeight+e.Schedule.RoutingWeight+e.Schedule.UpstreamSuccessWeight <= 0 {
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
	for _, target := range e.Probe.Targets {
		u, err := url.Parse(strings.TrimSpace(target))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("egress.probe.targets contains invalid URL %q", target)
		}
	}
	if e.MaxLatencyEntries < 1 || e.MaxLatencyEntries > 4096 {
		return errors.New("egress.max_latency_entries must be between 1 and 4096")
	}
	e.StatePath = strings.TrimSpace(e.StatePath)
	if e.StatePath == "" {
		e.StatePath = filepath.Clean(configPath + ".egress.db")
	}
	return nil
}

func cloneEgress(in *EgressConfig) *EgressConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Subscriptions = append([]EgressSubscription(nil), in.Subscriptions...)
	out.Probe.Targets = append([]string(nil), in.Probe.Targets...)
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
