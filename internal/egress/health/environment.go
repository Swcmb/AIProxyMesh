package health

import (
	"sync"
	"time"
)

// EnvironmentState tracks the health of the Mesh-to-node link, which is
// independent of node and upstream health.
type EnvironmentState string

const (
	EnvHealthy     EnvironmentState = "healthy"
	EnvDegraded    EnvironmentState = "degraded"
	EnvUnavailable EnvironmentState = "unavailable"
)

// EnvironmentConfig tunes local-failure detection.
type EnvironmentConfig struct {
	ProbeFailureThreshold  int
	BootstrapLatencyBudget time.Duration
	FailureGrace           time.Duration
	LocalFailureThreshold  float64
	LocalFailureWindow     time.Duration
	TargetPause            time.Duration
}

// DefaultEnvironmentConfig returns the approved defaults.
func DefaultEnvironmentConfig() EnvironmentConfig {
	return EnvironmentConfig{
		ProbeFailureThreshold:  3,
		BootstrapLatencyBudget: 3 * time.Second,
		FailureGrace:           60 * time.Second,
		LocalFailureThreshold:  0.3,
		LocalFailureWindow:     5 * time.Minute,
		TargetPause:            60 * time.Second,
	}
}

func (c EnvironmentConfig) normalized() EnvironmentConfig {
	def := DefaultEnvironmentConfig()
	if c.ProbeFailureThreshold <= 0 {
		c.ProbeFailureThreshold = def.ProbeFailureThreshold
	}
	if c.BootstrapLatencyBudget <= 0 {
		c.BootstrapLatencyBudget = def.BootstrapLatencyBudget
	}
	if c.FailureGrace < 0 {
		c.FailureGrace = def.FailureGrace
	}
	if c.LocalFailureThreshold <= 0 || c.LocalFailureThreshold > 1 {
		c.LocalFailureThreshold = def.LocalFailureThreshold
	}
	if c.LocalFailureWindow <= 0 {
		c.LocalFailureWindow = def.LocalFailureWindow
	}
	if c.TargetPause <= 0 {
		c.TargetPause = def.TargetPause
	}
	return c
}

type transportFailure struct {
	At     time.Time
	Node   string
	Target string
}

// EnvironmentSnapshot is the persisted environment row.
type EnvironmentSnapshot struct {
	Epoch               uint64
	State               EnvironmentState
	LastProbeAt         time.Time
	ProbeFailures       int
	BootstrapFailures   uint64
	EnvironmentFailures uint64
	BootstrapLatency    time.Duration
	UpdatedAt           time.Time
}

// Environment freezes health accounting when the local link itself is broken,
// so a laptop losing Wi-Fi cannot condemn the whole exit pool.
type Environment struct {
	mu                  sync.Mutex
	cfg                 EnvironmentConfig
	state               EnvironmentState
	epoch               uint64
	lastProbe           time.Time
	probeFailures       int
	bootstrapFailures   uint64
	environmentFailures uint64
	bootstrapLatency    time.Duration
	activeKeys          int
	failures            []transportFailure
	conditionSince      time.Time
	targets             map[string]time.Time
	onEpoch             []func(uint64)

	UpdatedAt time.Time
}

// NewEnvironment creates a healthy environment.
func NewEnvironment(cfg EnvironmentConfig, now time.Time) *Environment {
	return &Environment{
		cfg:       cfg.normalized(),
		state:     EnvHealthy,
		targets:   make(map[string]time.Time),
		UpdatedAt: now,
	}
}

// RestoreEnvironment rebuilds the persisted state. Unavailable stays frozen
// across restarts until the probe recovers.
func RestoreEnvironment(snapshot EnvironmentSnapshot, cfg EnvironmentConfig) *Environment {
	env := NewEnvironment(cfg, snapshot.UpdatedAt)
	env.epoch = snapshot.Epoch
	if snapshot.State == EnvDegraded || snapshot.State == EnvUnavailable {
		env.state = snapshot.State
	}
	env.lastProbe = snapshot.LastProbeAt
	env.probeFailures = snapshot.ProbeFailures
	env.bootstrapFailures = snapshot.BootstrapFailures
	env.environmentFailures = snapshot.EnvironmentFailures
	env.bootstrapLatency = snapshot.BootstrapLatency
	return env
}

// Configure replaces the environment tuning on hot reload.
func (e *Environment) Configure(cfg EnvironmentConfig) {
	e.mu.Lock()
	e.cfg = cfg.normalized()
	e.mu.Unlock()
}

// OnEpoch registers a callback invoked after recovery increments the epoch.
func (e *Environment) OnEpoch(fn func(uint64)) {
	e.mu.Lock()
	e.onEpoch = append(e.onEpoch, fn)
	e.mu.Unlock()
}

// State returns the current state and epoch.
func (e *Environment) State() (EnvironmentState, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state, e.epoch
}

// Frozen reports whether health updates must be skipped.
func (e *Environment) Frozen() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state == EnvUnavailable
}

// Snapshot copies the persisted fields.
func (e *Environment) Snapshot() EnvironmentSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EnvironmentSnapshot{
		Epoch:               e.epoch,
		State:               e.state,
		LastProbeAt:         e.lastProbe,
		ProbeFailures:       e.probeFailures,
		BootstrapFailures:   e.bootstrapFailures,
		EnvironmentFailures: e.environmentFailures,
		BootstrapLatency:    e.bootstrapLatency,
		UpdatedAt:           e.UpdatedAt,
	}
}

// SetActiveKeys feeds the denominator used by local-failure detection.
func (e *Environment) SetActiveKeys(count int) {
	e.mu.Lock()
	e.activeKeys = count
	e.mu.Unlock()
}

// ReportBootstrapProbe records one probe through the bootstrap path.
func (e *Environment) ReportBootstrapProbe(ok bool, latency time.Duration, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastProbe = now
	e.bootstrapLatency = latency
	e.UpdatedAt = now
	if ok {
		e.probeFailures = 0
		if e.state == EnvUnavailable {
			e.recoverLocked(now)
		} else if e.state == EnvDegraded {
			e.state = EnvHealthy
		}
		return
	}
	e.probeFailures++
	if e.state == EnvUnavailable {
		return
	}
	if e.probeFailures >= e.cfg.ProbeFailureThreshold {
		if e.conditionSince.IsZero() {
			e.conditionSince = now
		}
		if now.Sub(e.conditionSince) >= e.cfg.FailureGrace {
			e.state = EnvUnavailable
			e.environmentFailures++
			e.conditionSince = time.Time{}
		}
	}
}

// ReportBootstrapLatency raises a degraded warning without freezing updates.
func (e *Environment) ReportBootstrapLatency(latency time.Duration, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bootstrapLatency = latency
	e.UpdatedAt = now
	if e.state == EnvUnavailable {
		return
	}
	if latency > e.cfg.BootstrapLatencyBudget {
		e.state = EnvDegraded
		return
	}
	if e.state == EnvDegraded {
		e.state = EnvHealthy
	}
}

// ReportTransportFailure records one attributable transport failure. It never
// reports bootstrap failures: those are local by definition.
func (e *Environment) ReportTransportFailure(nodeHash, target string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures = append(e.failures, transportFailure{At: now, Node: nodeHash, Target: target})
	e.pruneLocked(now)
	e.evaluateLocalLocked(now)
}

// ReportTransportSuccess lets a success clear a pending local outage.
func (e *Environment) ReportTransportSuccess(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked(now)
	if e.state == EnvUnavailable && len(e.failures) == 0 {
		e.recoverLocked(now)
	}
}

func (e *Environment) pruneLocked(now time.Time) {
	cutoff := now.Add(-e.cfg.LocalFailureWindow)
	kept := e.failures[:0]
	for _, failure := range e.failures {
		if !failure.At.Before(cutoff) {
			kept = append(kept, failure)
		}
	}
	e.failures = kept
	for target, until := range e.targets {
		if now.After(until) {
			delete(e.targets, target)
		}
	}
}

func (e *Environment) evaluateLocalLocked(now time.Time) {
	if e.state == EnvUnavailable {
		return
	}
	nodes := make(map[string]struct{})
	targets := make(map[string]struct{})
	for _, failure := range e.failures {
		nodes[failure.Node] = struct{}{}
		targets[failure.Target] = struct{}{}
	}
	active := e.activeKeys
	if active < 1 {
		active = 1
	}
	ratio := float64(len(nodes)) / float64(active)
	local := len(nodes) >= 3 && len(targets) >= 2 && ratio >= e.cfg.LocalFailureThreshold
	if !local {
		e.conditionSince = time.Time{}
		return
	}
	if len(targets) == 1 {
		for target := range targets {
			e.targets[target] = now.Add(e.cfg.TargetPause)
		}
		return
	}
	if e.conditionSince.IsZero() {
		e.conditionSince = now
	}
	if now.Sub(e.conditionSince) >= e.cfg.FailureGrace {
		e.state = EnvUnavailable
		e.environmentFailures++
		e.conditionSince = time.Time{}
		e.bootstrapFailures++
	}
}

func (e *Environment) recoverLocked(now time.Time) {
	e.state = EnvHealthy
	e.epoch++
	e.probeFailures = 0
	e.failures = nil
	e.conditionSince = time.Time{}
	e.bootstrapLatency = 0
	e.UpdatedAt = now
	epoch := e.epoch
	for _, fn := range e.onEpoch {
		go fn(epoch)
	}
}

// TargetPaused reports whether a target's probing is paused after upstream
// consensus or a single-target local failure.
func (e *Environment) TargetPaused(target string, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	until, ok := e.targets[target]
	return ok && now.Before(until)
}

// PauseTarget pauses probing for one target.
func (e *Environment) PauseTarget(target string, until time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.targets[target] = until
}

// Epoch returns the current environment epoch.
func (e *Environment) Epoch() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epoch
}
