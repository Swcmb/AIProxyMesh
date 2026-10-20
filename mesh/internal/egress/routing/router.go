// Package routing implements eligibility filtering, scoring, weighted
// selection and failure attribution for the egress pool.
package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

// ErrNoEligible reports that no node can serve the target right now.
var ErrNoEligible = errors.New("egress: no eligible exit node")

// Config controls scoring, cooldown and attribution behaviour.
type Config struct {
	Weights        Weights
	CooldownBase   time.Duration
	CooldownMax    time.Duration
	UpstreamWindow time.Duration
}

// DefaultConfig returns the approved initial parameters.
func DefaultConfig() Config {
	return Config{
		Weights:        DefaultWeights(),
		CooldownBase:   30 * time.Second,
		CooldownMax:    15 * time.Minute,
		UpstreamWindow: time.Minute,
	}
}

func (c Config) normalized() Config {
	c.Weights = c.Weights.Normalize()
	if c.CooldownBase <= 0 {
		c.CooldownBase = 30 * time.Second
	}
	if c.CooldownMax < c.CooldownBase {
		c.CooldownMax = 15 * time.Minute
	}
	if c.UpstreamWindow <= 0 {
		c.UpstreamWindow = time.Minute
	}
	return c
}

// Router selects exits and turns attempt outcomes into node state updates.
type Router struct {
	pool     *pool.Pool
	outbound *outbound.Manager
	logger   *slog.Logger
	tracker  *UpstreamTracker
	cfg      atomic.Pointer[Config]

	mu       sync.Mutex
	inflight map[node.Hash]int
	now      func() time.Time
}

// New creates a router.
func New(p *pool.Pool, manager *outbound.Manager, cfg Config, logger *slog.Logger) *Router {
	if p == nil {
		panic("routing: nil pool")
	}
	if manager == nil {
		panic("routing: nil outbound manager")
	}
	cfg = cfg.normalized()
	r := &Router{
		pool:     p,
		outbound: manager,
		logger:   logger,
		tracker:  NewUpstreamTracker(cfg.UpstreamWindow),
		inflight: make(map[node.Hash]int),
		now:      time.Now,
	}
	r.cfg.Store(&cfg)
	return r
}

// Configure swaps scoring and cooldown parameters (hot reload).
func (r *Router) Configure(cfg Config) {
	cfg = cfg.normalized()
	r.cfg.Store(&cfg)
	r.tracker.SetWindow(cfg.UpstreamWindow)
}

// Config returns the active configuration.
func (r *Router) Config() Config { return *r.cfg.Load() }

// SetClock replaces the clock (tests).
func (r *Router) SetClock(now func() time.Time) {
	r.mu.Lock()
	r.now = now
	r.mu.Unlock()
}

// Attempt is one selected exit reserved for one upstream request.
type Attempt struct {
	Router   *Router
	NodeID   string
	NodeName string
	Protocol string
	ProxyURL *url.URL
	Target   string
	// KeyID lets attribution distinguish "this key is rate limited" from
	// "the upstream rejects every exit". Set before Report.
	KeyID string

	entry  *node.Entry
	client *http.Client
	once   sync.Once
}

// Client returns the HTTP client bound to this exit.
func (a *Attempt) Client() *http.Client { return a.client }

// Report records the outcome exactly once and returns its classification.
func (a *Attempt) Report(outcome Outcome) Classification {
	var result Classification
	a.once.Do(func() {
		result = a.Router.report(a, outcome)
	})
	return result
}

// Select chooses the highest-scoring eligible exit using weighted randomness.
// exclude holds node IDs already attempted in this request.
func (r *Router) Select(ctx context.Context, target *url.URL, exclude map[string]struct{}) (*Attempt, error) {
	if target == nil || target.Hostname() == "" {
		return nil, fmt.Errorf("egress: invalid target")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	host := node.NormalizeHost(target.Hostname())
	now := r.clock()
	cfg := *r.cfg.Load()
	weights := cfg.Weights

	type pick struct {
		entry  *node.Entry
		client *http.Client
		score  float64
	}
	var picks []pick
	total := 0.0
	for _, entry := range r.pool.List() {
		if !r.eligible(entry, host, now, exclude) {
			continue
		}
		client, err := r.outbound.Client(entry)
		if err != nil {
			continue
		}
		score := computeScore(entry, host, r.inflightFor(entry.Hash), weights).Total
		picks = append(picks, pick{entry: entry, client: client, score: score})
		total += score + 0.001
	}
	if len(picks) == 0 {
		return nil, ErrNoEligible
	}

	point := rand.Float64() * total
	chosen := picks[len(picks)-1]
	for _, candidate := range picks {
		point -= candidate.score + 0.001
		if point <= 0 {
			chosen = candidate
			break
		}
	}

	r.mu.Lock()
	r.inflight[chosen.entry.Hash]++
	r.mu.Unlock()

	proxyURL, err := chosen.entry.Options.ProxyURL()
	if err != nil {
		r.release(chosen.entry.Hash)
		return nil, err
	}
	return &Attempt{
		Router:   r,
		NodeID:   chosen.entry.ID(),
		NodeName: chosen.entry.Name(),
		Protocol: string(chosen.entry.Options.Type),
		ProxyURL: proxyURL,
		Target:   host,
		entry:    chosen.entry,
		client:   chosen.client,
	}, nil
}

func (r *Router) eligible(entry *node.Entry, host string, now time.Time, exclude map[string]struct{}) bool {
	if entry == nil || !entry.Enabled() || !entry.Options.Supported() {
		return false
	}
	if !r.pool.Active(entry) {
		return false
	}
	if entry.Cooling(now) {
		return false
	}
	if _, blocked := exclude[entry.ID()]; blocked {
		return false
	}
	return true
}

func (r *Router) inflightFor(h node.Hash) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inflight[h]
}

func (r *Router) release(h node.Hash) {
	r.mu.Lock()
	if r.inflight[h] > 1 {
		r.inflight[h]--
	} else {
		delete(r.inflight, h)
	}
	r.mu.Unlock()
}

func (r *Router) clock() time.Time {
	r.mu.Lock()
	now := r.now
	r.mu.Unlock()
	return now()
}

// OutcomeKind classifies one attempt result.
type OutcomeKind string

const (
	Success         OutcomeKind = "success"
	ProxyFailure    OutcomeKind = "proxy_failure"
	ExitFailure     OutcomeKind = "exit_failure"
	UpstreamFailure OutcomeKind = "upstream_failure"
	KeyFailure      OutcomeKind = "key_failure"
	ClientCancel    OutcomeKind = "client_cancel"
	Neutral         OutcomeKind = "neutral"
)

// Outcome is the raw attempt result reported by the gateway.
type Outcome struct {
	Kind       OutcomeKind
	StatusCode int
	RetryAfter time.Duration
	Latency    time.Duration
	Err        error
}

// Classification is what the router decided about the outcome.
type Classification struct {
	Kind         OutcomeKind `json:"kind"`
	UpstreamWide bool        `json:"upstream_wide"`
	Retryable    bool        `json:"retryable"`
	NodeCooldown *time.Time  `json:"node_cooldown,omitempty"`
	Reason       string      `json:"reason,omitempty"`
}

func (r *Router) report(attempt *Attempt, outcome Outcome) Classification {
	entry := attempt.entry
	if entry == nil {
		return Classification{Kind: outcome.Kind, Reason: "unknown_node"}
	}
	r.release(entry.Hash)
	now := r.clock()
	cfg := *r.cfg.Load()

	result := Classification{Kind: outcome.Kind}
	switch outcome.Kind {
	case Success:
		entry.RecordSuccess()
		entry.RecordNetworkResult(true)
		entry.RecordUpstreamResult(attempt.Target, true)
		r.tracker.RecordSuccess(attempt.Target, entry.ID(), now)
		if outcome.Latency > 0 {
			if table := entry.Latency(); table != nil {
				table.Update(attempt.Target, outcome.Latency, 10*time.Minute)
			}
		}
		result.Reason = "ok"

	case ProxyFailure:
		entry.RecordNetworkResult(false)
		until := r.cooldown(entry, 0, cfg, now)
		result.NodeCooldown = &until
		result.Retryable = true
		result.Reason = "transport_failure"
		entry.SetLastError(errorText(outcome.Err))

	case ExitFailure:
		entry.RecordNetworkResult(true)
		entry.RecordUpstreamResult(attempt.Target, false)
		consensus := r.tracker.Record(attempt.Target, attempt.KeyID, statusClass(outcome.StatusCode), entry.ID(), now)
		if consensus {
			result.Kind = UpstreamFailure
			result.UpstreamWide = true
			result.Retryable = true
			result.Reason = "upstream_consensus"
			return result
		}
		until := r.cooldown(entry, outcome.RetryAfter, cfg, now)
		result.NodeCooldown = &until
		result.Retryable = true
		result.Reason = fmt.Sprintf("exit_status_%d", outcome.StatusCode)
		entry.SetLastError(fmt.Sprintf("upstream status %d", outcome.StatusCode))

	case UpstreamFailure:
		entry.RecordNetworkResult(true)
		consensus := r.tracker.Record(attempt.Target, attempt.KeyID, statusClass(outcome.StatusCode), entry.ID(), now)
		if consensus {
			result.UpstreamWide = true
			result.Retryable = true
			result.Reason = "upstream_consensus"
			return result
		}
		if r.tracker.RecentSuccessByOther(attempt.Target, entry.ID(), now) {
			// Another exit reached the same upstream recently, so this failure
			// belongs to the current exit.
			entry.RecordUpstreamResult(attempt.Target, false)
			until := r.cooldown(entry, outcome.RetryAfter, cfg, now)
			result.Kind = ExitFailure
			result.NodeCooldown = &until
			result.Reason = "exit_specific_after_success"
		} else {
			result.Reason = "upstream_unattributed"
		}
		result.Retryable = true

	case KeyFailure:
		entry.RecordNetworkResult(true)
		result.Retryable = true
		result.Reason = "key_or_model_failure"

	case ClientCancel:
		result.Reason = "client_cancel"

	case Neutral:
		entry.RecordNetworkResult(true)
		result.Reason = "non_retryable_client_response"

	default:
		result.Reason = "unknown_outcome"
	}
	return result
}

func (r *Router) cooldown(entry *node.Entry, retryAfter time.Duration, cfg Config, now time.Time) time.Time {
	failures := entry.Failures() + 1
	shift := min(failures-1, 10)
	delay := cfg.CooldownBase * time.Duration(1<<shift)
	if delay > cfg.CooldownMax || delay <= 0 {
		delay = cfg.CooldownMax
	}
	if retryAfter > delay {
		if retryAfter > cfg.CooldownMax {
			retryAfter = cfg.CooldownMax
		}
		delay = retryAfter
	}
	until := now.Add(delay)
	entry.RecordCooldown(until)
	return until
}

func statusClass(status int) int {
	if status <= 0 {
		return 0
	}
	return status / 100
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 240 {
		text = text[:240]
	}
	return text
}

// Candidate is the diagnostic view of one pool entry.
type Candidate struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Protocol      string     `json:"protocol"`
	Eligible      bool       `json:"eligible"`
	Reason        string     `json:"reason,omitempty"`
	Score         Score      `json:"score"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
}

// Diagnose explains why each node is or is not eligible for a target.
func (r *Router) Diagnose(target *url.URL, exclude map[string]struct{}) []Candidate {
	if target == nil {
		return nil
	}
	host := node.NormalizeHost(target.Hostname())
	now := r.clock()
	cfg := *r.cfg.Load()
	entries := r.pool.List()
	out := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		candidate := Candidate{
			ID:       entry.ID(),
			Name:     entry.Name(),
			Protocol: string(entry.Options.Type),
			Score:    computeScore(entry, host, r.inflightFor(entry.Hash), cfg.Weights),
		}
		switch {
		case !entry.Enabled():
			candidate.Reason = "disabled"
		case !entry.Options.Supported():
			candidate.Reason = "unsupported_protocol"
		case !r.pool.Active(entry):
			candidate.Reason = "subscription_disabled"
		case entry.Cooling(now):
			candidate.Reason = "cooling_down"
			until := time.Unix(0, entry.CooldownUntil()).UTC()
			candidate.CooldownUntil = &until
		default:
			if _, blocked := exclude[entry.ID()]; blocked {
				candidate.Reason = "already_attempted"
			} else {
				candidate.Eligible = true
				candidate.Reason = "eligible"
			}
		}
		out = append(out, candidate)
	}
	return out
}

// TargetHost extracts the canonical host from a target URL or raw host.
func TargetHost(target *url.URL) string {
	if target == nil {
		return ""
	}
	return node.NormalizeHost(strings.TrimSpace(target.Hostname()))
}
