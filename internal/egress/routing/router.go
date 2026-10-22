// Package routing implements eligibility filtering, scoring, weighted
// selection and failure attribution for the egress pool.
package routing

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

// ErrNoEligible reports that no node can serve the target right now.
var ErrNoEligible = errors.New("egress: no eligible exit node")

// verifiedWindow is how long a successful signal keeps a node in the preferred
// routing set.
const verifiedWindow = 10 * time.Minute

// regionBlockTTL is how long an upstream's country rejection hides that
// country's exits from routing.
const regionBlockTTL = 30 * time.Minute

// Config controls scoring, cooldown and attribution behaviour.
type Config struct {
	Weights          Weights
	CooldownBase     time.Duration
	CooldownMax      time.Duration
	UpstreamWindow   time.Duration
	AllowedProtocols map[node.Protocol]bool

	// Health mode and admission parameters.
	HealthMode                string
	Health                    health.Config
	ExplorationRatio          float64
	CandidateExplorationRatio float64
	StableMinCapacity         int
	ColdStartFallback         bool
	ReachableWindow           time.Duration
	MaxKeysPerNode            int
	MaxPendingKeys            int
	RouteSampleSize           int
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

// Allows reports whether a node protocol may be selected. An empty map means
// every supported Tier A protocol is allowed.
func (c Config) Allows(protocol node.Protocol) bool {
	if !protocol.Supported() {
		return false
	}
	if len(c.AllowedProtocols) == 0 {
		return true
	}
	allowed, ok := c.AllowedProtocols[protocol]
	return ok && allowed
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
	if c.HealthMode == "" {
		c.HealthMode = "shadow"
	}
	c.Health = c.Health.Normalize()
	if c.ExplorationRatio < 0 || c.ExplorationRatio > 0.5 {
		c.ExplorationRatio = 0.10
	}
	if c.CandidateExplorationRatio < 0 || c.CandidateExplorationRatio > c.ExplorationRatio {
		c.CandidateExplorationRatio = 0.02
	}
	if c.StableMinCapacity <= 0 {
		c.StableMinCapacity = 50
	}
	if c.ReachableWindow <= 0 {
		c.ReachableWindow = 10 * time.Minute
	}
	if c.MaxKeysPerNode <= 0 {
		c.MaxKeysPerNode = 4
	}
	if c.MaxPendingKeys <= 0 {
		c.MaxPendingKeys = 10000
	}
	if c.RouteSampleSize <= 0 {
		c.RouteSampleSize = 64
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

	regionMu     sync.Mutex
	regionBlocks map[string]map[string]time.Time
	regionOf     func(*node.Entry) string

	environment atomic.Pointer[health.Environment]
	index       *Index
	seedMu      sync.Mutex
	lastSeed    map[health.HealthKey]time.Time
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
		pool:         p,
		outbound:     manager,
		logger:       logger,
		tracker:      NewUpstreamTracker(cfg.UpstreamWindow),
		inflight:     make(map[node.Hash]int),
		regionBlocks: make(map[string]map[string]time.Time),
		index:        NewIndex(),
		lastSeed:     make(map[health.HealthKey]time.Time),
		now:          time.Now,
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
	// Key is the concrete health key this attempt observes.
	Key health.HealthKey
	// Direct marks the special direct exit, which bypasses key accounting.
	Direct bool
	// KeyID lets attribution distinguish "this key is rate limited" from
	// "the upstream rejects every exit". Set before Report.
	KeyID string

	entry        *node.Entry
	client       *http.Client
	once         sync.Once
	streamOnce   sync.Once
	completeOnce sync.Once
}

// RegionBlock is one persisted hard region block.
type RegionBlock struct {
	Target  string
	Country string
	Until   time.Time
}

// RegionBlocks snapshots the live hard blocks for persistence.
func (r *Router) RegionBlocks() []RegionBlock {
	r.regionMu.Lock()
	defer r.regionMu.Unlock()
	now := r.clock()
	out := make([]RegionBlock, 0)
	for target, countries := range r.regionBlocks {
		for country, until := range countries {
			if now.Before(until) {
				out = append(out, RegionBlock{Target: target, Country: country, Until: until})
			}
		}
	}
	return out
}

// RestoreRegionBlocks reloads blocks that have not expired.
func (r *Router) RestoreRegionBlocks(blocks []RegionBlock) {
	r.regionMu.Lock()
	defer r.regionMu.Unlock()
	for _, block := range blocks {
		if block.Target == "" || block.Country == "" {
			continue
		}
		countries := r.regionBlocks[block.Target]
		if countries == nil {
			countries = make(map[string]time.Time)
			r.regionBlocks[block.Target] = countries
		}
		countries[block.Country] = block.Until
	}
}

// Index exposes the stable-pool index.
func (r *Router) Index() *Index { return r.index }

// SetEnvironment installs the local-link environment classifier.
func (r *Router) SetEnvironment(env *health.Environment) {
	r.environment.Store(env)
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

// Release frees the in-flight slot without recording any outcome. It is used by
// diagnostic requests that must not change pool state.
func (a *Attempt) Release() {
	a.once.Do(func() {
		if a.entry != nil {
			a.Router.release(a.entry.Hash)
		}
	})
}

// SetRegionResolver installs the GeoIP lookup used by region filters.
func (r *Router) SetRegionResolver(fn func(*node.Entry) string) {
	r.regionMu.Lock()
	r.regionOf = fn
	r.regionMu.Unlock()
}

// RecordRegionBlock hides one country's exits for a target. An upstream that
// answered "not available in your country" keeps rejecting that country's
// exits, so retrying with the same region only wastes the attempt budget.
func (r *Router) RecordRegionBlock(target, country string) {
	if target == "" || country == "" {
		return
	}
	host := node.NormalizeHost(target)
	until := r.clock().Add(regionBlockTTL)
	r.regionMu.Lock()
	blocked := r.regionBlocks[host]
	if blocked == nil {
		blocked = make(map[string]time.Time)
		r.regionBlocks[host] = blocked
	}
	blocked[country] = until
	r.regionMu.Unlock()
}

// blockedRegions returns the currently blocked countries for one target.
func (r *Router) blockedRegions(host string, now time.Time) map[string]struct{} {
	r.regionMu.Lock()
	defer r.regionMu.Unlock()
	blocked := r.regionBlocks[host]
	if len(blocked) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(blocked))
	for country, until := range blocked {
		if now.Before(until) {
			out[country] = struct{}{}
			continue
		}
		delete(blocked, country)
	}
	if len(blocked) == 0 {
		delete(r.regionBlocks, host)
	}
	return out
}

// filterBlocked drops candidates whose region is blocked. The caller falls
// back to the unfiltered set when this returns empty.
func (r *Router) filterBlocked(picks []pick, blocked map[string]struct{}) []pick {
	r.regionMu.Lock()
	resolve := r.regionOf
	r.regionMu.Unlock()
	out := picks[:0]
	for _, candidate := range picks {
		country := candidate.entry.Region()
		if country == "" && resolve != nil {
			country = resolve(candidate.entry)
			if country != "" {
				candidate.entry.SetRegion(country)
			}
		}
		if country != "" {
			if _, bad := blocked[country]; bad {
				continue
			}
		}
		out = append(out, candidate)
	}
	return out
}

// Select chooses the highest-scoring eligible exit using weighted randomness.
// exclude holds node IDs already attempted in this request.
func (r *Router) Select(ctx context.Context, target *url.URL, exclude map[string]struct{}) (*Attempt, error) {
	return r.SelectAffinity(ctx, target, exclude, "")
}

// SelectAffinity adds a stickiness preference: the exit picked by rendezvous
// hashing of affinity wins unless its score falls below half of the best
// candidate, in which case weighted selection decides. An empty affinity
// behaves exactly like Select.
func (r *Router) SelectAffinity(ctx context.Context, target *url.URL, exclude map[string]struct{}, affinity string) (*Attempt, error) {
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

	var picks, verified []pick
	for _, entry := range r.pool.List() {
		if !r.eligible(entry, host, now, exclude, cfg) {
			continue
		}
		score := computeScore(entry, host, r.inflightFor(entry.Hash), weights).Total
		candidate := pick{entry: entry, score: score}
		picks = append(picks, candidate)
		if last := entry.LastSuccessAt(); last > 0 && now.Sub(time.Unix(0, last)) <= verifiedWindow {
			verified = append(verified, candidate)
		}
	}
	// Prefer exits that recently proved they can carry traffic. The pool is
	// dominated by unverified free proxies; choosing uniformly from it mostly
	// hits dead nodes and ends in 503 after the retry budget. The full eligible
	// set stays as a fallback until the first sweep finds verified nodes.
	if blocked := r.blockedRegions(host, now); len(blocked) > 0 {
		// Region blocks are hard: an empty filtered set stays empty and the
		// request fails with ErrNoEligible instead of retrying a blocked region.
		picks = r.filterBlocked(picks, blocked)
		verified = r.filterBlocked(verified, blocked)
	}
	if len(verified) > 0 {
		picks = verified
	}
	if len(picks) == 0 {
		return nil, ErrNoEligible
	}

	forced := -1
	if affinity != "" {
		preferred := rendezvousPick(affinity, picks)
		best := 0.0
		for _, candidate := range picks {
			best = max(best, candidate.score)
		}
		for i := range picks {
			if picks[i].entry.ID() == preferred && picks[i].score >= best*0.5 {
				forced = i
				break
			}
		}
	}
	chosen := picks[len(picks)-1]
	if forced >= 0 {
		chosen = picks[forced]
	} else {
		total := 0.0
		for _, candidate := range picks {
			total += candidate.score + 0.001
		}
		point := rand.Float64() * total
		for _, candidate := range picks {
			point -= candidate.score + 0.001
			if point <= 0 {
				chosen = candidate
				break
			}
		}
	}

	r.mu.Lock()
	r.inflight[chosen.entry.Hash]++
	r.mu.Unlock()

	client, err := r.outbound.Client(chosen.entry)
	if err != nil {
		r.release(chosen.entry.Hash)
		return nil, err
	}
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
		Direct:   chosen.entry.Options.Type == node.Direct,
		entry:    chosen.entry,
		client:   client,
	}, nil
}

type pick struct {
	entry *node.Entry
	score float64
}

func rendezvousPick(affinity string, picks []pick) string {
	best := ""
	var bestHash uint64
	for _, candidate := range picks {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(affinity))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(candidate.entry.ID()))
		if value := hash.Sum64(); best == "" || value > bestHash {
			best, bestHash = candidate.entry.ID(), value
		}
	}
	return best
}

func (r *Router) eligible(entry *node.Entry, host string, now time.Time, exclude map[string]struct{}, cfg Config) bool {
	if entry == nil || !entry.Enabled() || !cfg.Allows(entry.Options.Type) {
		return false
	}
	if !r.pool.Active(entry) {
		return false
	}
	if !entry.EverHealthy() {
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
	Success            OutcomeKind = "success"
	ProxyFailure       OutcomeKind = "proxy_failure"
	ExitFailure        OutcomeKind = "exit_failure"
	UpstreamFailure    OutcomeKind = "upstream_failure"
	KeyFailure         OutcomeKind = "key_failure"
	RegionBlocked      OutcomeKind = "region_blocked"
	ClientCancel       OutcomeKind = "client_cancel"
	Neutral            OutcomeKind = "neutral"
	BootstrapFailure   OutcomeKind = "bootstrap_failure"
	EnvironmentFailure OutcomeKind = "environment_failure"
	EnvironmentSlow    OutcomeKind = "environment_degraded"
	InferenceTimeout   OutcomeKind = "inference_timeout"
)

// Outcome is the raw attempt result reported by the gateway.
type Outcome struct {
	Kind            OutcomeKind
	StatusCode      int
	RetryAfter      time.Duration
	Latency         time.Duration
	HeadersReceived bool
	ValidInference  bool
	ValidKnown      bool
	Diagnostic      bool
	Direct          bool
	UpstreamWide    bool
	// Bootstrap marks a failure of the Mesh-to-node bootstrap link. It is an
	// environment signal and never attributes to the node.
	Bootstrap bool
	// Region is the GeoIP country of the exit that hit a regional rejection.
	Region string
	Err    error
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
	if attempt.Direct {
		outcome.Direct = true
	}

	result := Classification{Kind: outcome.Kind}
outcomeSwitch:
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
		until := r.cooldown(entry, 0, cfg, now, "proxy_failure")
		result.NodeCooldown = &until
		result.Retryable = true
		result.Reason = "transport_failure"
		entry.SetLastError(errorText(outcome.Err))

	case ExitFailure:
		entry.RecordNetworkResult(true)
		entry.RecordUpstreamResult(attempt.Target, false)
		class := statusClass(outcome.StatusCode)
		consensus, nodes := r.tracker.Record(attempt.Target, attempt.KeyID, class, entry.ID(), now)
		if consensus {
			r.clearExitCooldowns(nodes, class)
			result.Kind = UpstreamFailure
			result.UpstreamWide = true
			result.Retryable = true
			result.Reason = "upstream_consensus"
			break outcomeSwitch
		}
		until := r.cooldown(entry, outcome.RetryAfter, cfg, now, fmt.Sprintf("exit_class_%d", class))
		result.NodeCooldown = &until
		result.Retryable = true
		result.Reason = fmt.Sprintf("exit_status_%d", outcome.StatusCode)
		entry.SetLastError(fmt.Sprintf("upstream status %d", outcome.StatusCode))

	case UpstreamFailure:
		entry.RecordNetworkResult(true)
		class := statusClass(outcome.StatusCode)
		consensus, nodes := r.tracker.Record(attempt.Target, attempt.KeyID, class, entry.ID(), now)
		if consensus {
			r.clearExitCooldowns(nodes, class)
			result.UpstreamWide = true
			result.Retryable = true
			result.Reason = "upstream_consensus"
			break outcomeSwitch
		}
		if r.tracker.RecentSuccessByOther(attempt.Target, entry.ID(), now) {
			// Another exit reached the same upstream recently, so this failure
			// belongs to the current exit.
			entry.RecordUpstreamResult(attempt.Target, false)
			until := r.cooldown(entry, outcome.RetryAfter, cfg, now, fmt.Sprintf("exit_class_%d", class))
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

	case RegionBlocked:
		entry.RecordNetworkResult(true)
		if outcome.Region != "" {
			r.RecordRegionBlock(attempt.Target, outcome.Region)
		}
		result.Retryable = true
		result.Reason = "region_blocked"

	case InferenceTimeout:
		entry.RecordNetworkResult(true)
		result.Retryable = true
		result.Reason = "inference_timeout"

	case ClientCancel:
		result.Reason = "client_cancel"

	case BootstrapFailure, EnvironmentFailure:
		entry.RecordNetworkResult(true)
		result.Reason = "environment_failure"
		result.Retryable = true

	case EnvironmentSlow:
		entry.RecordNetworkResult(true)
		result.Reason = "environment_degraded"

	case Neutral:
		entry.RecordNetworkResult(true)
		result.Reason = "non_retryable_client_response"

	default:
		result.Reason = "unknown_outcome"
	}
	outcome.Kind = result.Kind
	outcome.UpstreamWide = result.UpstreamWide
	r.applyHealth(attempt, outcome, now)
	return result
}

func (r *Router) clearExitCooldowns(nodeIDs []string, class int) {
	reason := fmt.Sprintf("exit_class_%d", class)
	for _, id := range nodeIDs {
		hash, err := node.ParseHex(id)
		if err != nil {
			continue
		}
		if entry := r.pool.Get(hash); entry != nil {
			entry.ClearCooldownFor(reason)
		}
	}
}

func (r *Router) cooldown(entry *node.Entry, retryAfter time.Duration, cfg Config, now time.Time, reason string) time.Time {
	failures := entry.Failures() + 1
	shift := min(failures-1, 10)
	delay := cfg.CooldownBase * time.Duration(1<<shift)
	if delay > cfg.CooldownMax || delay <= 0 {
		delay = cfg.CooldownMax
	}
	if retryAfter > delay {
		// The upstream explicitly asked for this delay; the exponential cap
		// governs inferred cooldowns and must not shorten a server request.
		const hardCap = 24 * time.Hour
		if retryAfter > hardCap {
			retryAfter = hardCap
		}
		delay = retryAfter
	}
	until := now.Add(delay)
	entry.RecordCooldownReason(until, reason)
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
		case !cfg.Allows(entry.Options.Type):
			candidate.Reason = "protocol_disabled"
		case !r.pool.Active(entry):
			candidate.Reason = "subscription_disabled"
		case !entry.EverHealthy():
			candidate.Reason = "unprobed"
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
