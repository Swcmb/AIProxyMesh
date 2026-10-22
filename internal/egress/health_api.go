package egress

import (
	"sort"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// HealthCounts is the key-state histogram.
type HealthCounts struct {
	Candidate int `json:"candidate"`
	Verifying int `json:"verifying"`
	Active    int `json:"active"`
	Suspect   int `json:"suspect"`
	Open      int `json:"open"`
	HalfOpen  int `json:"half_open"`
	Dead      int `json:"dead"`
}

// HealthEnvironmentView is the environment section of the health API.
type HealthEnvironmentView struct {
	State               string     `json:"state"`
	Epoch               uint64     `json:"epoch"`
	BootstrapMode       string     `json:"bootstrap_mode"`
	BootstrapLatencyMS  int64      `json:"bootstrap_latency_ms"`
	ProbeLastAt         *time.Time `json:"probe_last_at,omitempty"`
	ProbeFailures       int        `json:"probe_failures"`
	BootstrapFailures   uint64     `json:"bootstrap_failures"`
	EnvironmentFailures uint64     `json:"environment_failures"`
}

// HealthTargetView is the per-target state split.
type HealthTargetView struct {
	Target    string `json:"target"`
	Active    int    `json:"active"`
	Verifying int    `json:"verifying"`
}

// HealthProbeView is the scheduling budget section.
type HealthProbeView struct {
	QueueDepth        int        `json:"queue_depth"`
	Due               int        `json:"due"`
	BudgetPerSecond   float64    `json:"budget_per_second"`
	CanaryToday       int        `json:"canary_today"`
	CanaryDailyBudget int        `json:"canary_daily_budget"`
	LastProbeAt       *time.Time `json:"last_probe_at,omitempty"`
}

// HealthSummary backs GET /api/egress/health.
type HealthSummary struct {
	Mode              string                `json:"mode"`
	Counts            HealthCounts          `json:"counts"`
	Environment       HealthEnvironmentView `json:"environment"`
	StablePool        int                   `json:"stable_pool"`
	StableMinCapacity int                   `json:"stable_min_capacity"`
	Targets           []HealthTargetView    `json:"targets"`
	Probe             HealthProbeView       `json:"probe"`
}

// HealthSummary aggregates key states across the pool.
func (e *Engine) HealthSummary() HealthSummary {
	current := e.settings.Load()
	summary := HealthSummary{Mode: "shadow"}
	if current == nil || current.egress == nil {
		return summary
	}
	cfg := current.egress
	summary.Mode = cfg.Health.Mode
	summary.StableMinCapacity = cfg.Health.StableMinCapacity
	summary.Probe.BudgetPerSecond = cfg.Probe.BudgetPerSecond
	summary.Probe.CanaryDailyBudget = cfg.Probe.CanaryDailyBudget
	targets := map[string]*HealthTargetView{}
	for _, entry := range e.pool.List() {
		table := entry.ExistingHealthTable()
		if table == nil {
			continue
		}
		for _, record := range table.Snapshot() {
			switch record.State {
			case health.Candidate:
				summary.Counts.Candidate++
			case health.Verifying:
				summary.Counts.Verifying++
			case health.Active:
				summary.Counts.Active++
			case health.Suspect:
				summary.Counts.Suspect++
			case health.Open:
				summary.Counts.Open++
			case health.HalfOpen:
				summary.Counts.HalfOpen++
			case health.Dead:
				summary.Counts.Dead++
			}
			view := targets[record.Key.Target]
			if view == nil {
				view = &HealthTargetView{Target: record.Key.Target}
				targets[record.Key.Target] = view
			}
			if record.State == health.Active {
				view.Active++
			}
			if record.State == health.Verifying {
				view.Verifying++
			}
		}
	}
	summary.StablePool = summary.Counts.Active
	summary.Targets = make([]HealthTargetView, 0, len(targets))
	for _, view := range targets {
		summary.Targets = append(summary.Targets, *view)
	}
	sort.Slice(summary.Targets, func(i, j int) bool { return summary.Targets[i].Target < summary.Targets[j].Target })
	summary.Environment.BootstrapMode = cfg.Bootstrap.Mode
	if e.scheduler != nil {
		now := time.Now()
		summary.Probe.QueueDepth = e.scheduler.QueueDepth()
		summary.Probe.Due = e.scheduler.DueCount(now)
		summary.Probe.CanaryToday = e.scheduler.DailyCount()
	}
	if e.environment != nil {
		snapshot := e.environment.Snapshot()
		summary.Environment.State = string(snapshot.State)
		summary.Environment.Epoch = snapshot.Epoch
		summary.Environment.BootstrapLatencyMS = snapshot.BootstrapLatency.Milliseconds()
		summary.Environment.ProbeFailures = snapshot.ProbeFailures
		summary.Environment.BootstrapFailures = snapshot.BootstrapFailures
		summary.Environment.EnvironmentFailures = snapshot.EnvironmentFailures
		if !snapshot.LastProbeAt.IsZero() {
			at := snapshot.LastProbeAt
			summary.Environment.ProbeLastAt = &at
			summary.Probe.LastProbeAt = &at
		}
	}
	if summary.Environment.State == "" {
		summary.Environment.State = string(health.EnvHealthy)
	}
	return summary
}

// KeyHealthView is the read-only health-key row shown in node details.
type KeyHealthView struct {
	Target           string     `json:"target"`
	Model            string     `json:"model"`
	Protocol         string     `json:"protocol"`
	Mode             string     `json:"mode"`
	State            string     `json:"state"`
	ValidRequired    bool       `json:"valid_required"`
	HTTP200Success   uint32     `json:"http200_success"`
	HTTP200Failure   uint32     `json:"http200_failure"`
	ValidSuccess     uint32     `json:"valid_success"`
	ValidFailure     uint32     `json:"valid_failure"`
	PrimarySuccess   uint32     `json:"primary_success"`
	PrimaryFailure   uint32     `json:"primary_failure"`
	Samples          int        `json:"samples"`
	HTTP200Lower     *float64   `json:"http200_lower"`
	ValidLower       *float64   `json:"valid_lower"`
	TTFBP95MS        int64      `json:"ttfb_p95_ms"`
	EnvironmentEpoch uint64     `json:"environment_epoch"`
	PendingReverify  bool       `json:"pending_reverify"`
	ReverifySuccess  uint8      `json:"reverify_success"`
	ActiveEpoch      uint32     `json:"active_epoch"`
	NextProbeAt      *time.Time `json:"next_probe_at,omitempty"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
}

// keyViews converts stored keys for one node.
func (e *Engine) keyViews(entryID string) []KeyHealthView {
	hash, err := parseNodeID(entryID)
	if err != nil {
		return nil
	}
	entry := e.pool.Get(hash)
	if entry == nil {
		return nil
	}
	table := entry.ExistingHealthTable()
	if table == nil {
		return nil
	}
	current := e.settings.Load()
	if current == nil || current.egress == nil {
		return nil
	}
	cfg := current.egress.Health.HealthKeyConfig()
	views := make([]KeyHealthView, 0, table.Len())
	for _, record := range table.Snapshot() {
		httpLower, validLower := record.LowerBounds(cfg)
		view := KeyHealthView{
			Target: record.Key.Target, Model: record.Key.Model,
			Protocol: record.Key.Protocol, Mode: record.Key.Mode,
			State: string(record.State), ValidRequired: record.ValidRequired,
			HTTP200Success: record.HTTP200Success, HTTP200Failure: record.HTTP200Failure,
			ValidSuccess: record.ValidSuccess, ValidFailure: record.ValidFailure,
			PrimarySuccess: record.PrimarySuccess, PrimaryFailure: record.PrimaryFailure,
			Samples:          int(record.HTTP200Success + record.HTTP200Failure),
			TTFBP95MS:        record.LatencyHist.Quantile(0.95).Milliseconds(),
			EnvironmentEpoch: record.EnvironmentEpoch,
			PendingReverify:  record.PendingReverify,
			ReverifySuccess:  record.ReverifySuccess,
			ActiveEpoch:      record.ActiveEpoch,
		}
		if httpLower >= 0 {
			view.HTTP200Lower = &httpLower
		}
		if validLower >= 0 {
			view.ValidLower = &validLower
		}
		if !record.NextProbeAt.IsZero() {
			at := record.NextProbeAt
			view.NextProbeAt = &at
		}
		if !record.LastSuccessAt.IsZero() {
			at := record.LastSuccessAt
			view.LastSuccessAt = &at
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].State != views[j].State {
			return statePriority(views[i].State) < statePriority(views[j].State)
		}
		return views[i].Samples > views[j].Samples
	})
	return views
}

func statePriority(state string) int {
	switch health.State(state) {
	case health.Open, health.HalfOpen:
		return 0
	case health.Suspect:
		return 1
	case health.Dead:
		return 2
	case health.Active:
		return 3
	case health.Verifying:
		return 4
	default:
		return 5
	}
}

// entryHealth summarizes one node for list rows.
func (e *Engine) entryHealth(entryID string) (string, *float64, *float64, *time.Time) {
	views := e.keyViews(entryID)
	if len(views) == 0 {
		return "", nil, nil, nil
	}
	var primary *KeyHealthView
	for i := range views {
		if views[i].State == string(health.Active) {
			primary = &views[i]
			break
		}
	}
	if primary == nil {
		primary = &views[0]
	}
	return views[0].State, primary.HTTP200Lower, primary.ValidLower, primary.NextProbeAt
}

func parseNodeID(id string) (node.Hash, error) {
	return node.ParseHex(id)
}
