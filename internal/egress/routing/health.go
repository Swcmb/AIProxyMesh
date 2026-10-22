package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// SelectKey selects an exit for a concrete health key. The shadow mode keeps
// the legacy routing path and only records evidence; prefer and strict apply
// the key-level state machine.
func (r *Router) SelectKey(ctx context.Context, target *url.URL, key health.HealthKey, exclude map[string]struct{}, affinity string) (*Attempt, error) {
	if !key.Valid() {
		return nil, fmt.Errorf("egress: invalid health key %+v", key)
	}
	cfg := *r.cfg.Load()
	if cfg.HealthMode == "" || cfg.HealthMode == "shadow" {
		attempt, err := r.SelectAffinity(ctx, target, exclude, affinity)
		if attempt != nil {
			attempt.Key = key
		}
		return attempt, err
	}
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
	weights := cfg.Weights
	blocked := r.blockedRegions(host, now)

	type bucket struct {
		active    []pick
		verifying []pick
		reachable []pick
	}
	var buckets bucket
	reachableWindow := cfg.ReachableWindow
	if reachableWindow <= 0 {
		reachableWindow = 10 * time.Minute
	}
	sampleSize := cfg.RouteSampleSize
	if sampleSize <= 0 {
		sampleSize = 64
	}
	activeHashes := r.index.Sample(key, "active", sampleSize)
	verifyHashes := r.index.Sample(key, "verifying", sampleSize)
	reachHashes := r.index.ReachableSample(key.Target, reachableWindow, sampleSize, now)
	if len(activeHashes)+len(verifyHashes)+len(reachHashes) == 0 {
		// Unknown key: seed the index at most once per 30 seconds. Later
		// requests are served from the index without scanning the pool.
		if r.shouldSeed(key, now) {
			activeHashes, verifyHashes, reachHashes = r.seedKey(key, host, exclude, cfg, now, blocked)
		}
	}
	for _, hash := range activeHashes {
		entry := r.pool.Get(hash)
		if entry == nil || !r.eligible(entry, host, now, exclude, cfg) || blockedCountry(entry, blocked) {
			continue
		}
		if entry.Options.Type == node.Direct {
			// The direct exit is local and has no key-level evidence; the spec
			// injects it as an independent candidate in both prefer and strict.
			buckets.active = append(buckets.active, pick{entry: entry, score: computeScore(entry, host, r.inflightFor(entry.Hash), weights).Total})
			continue
		}
		stats := entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys).Get(key)
		if stats == nil {
			continue
		}
		stats.Tick(now, cfg.Health)
		score := computeScore(entry, host, r.inflightFor(entry.Hash), weights).Total
		switch stats.StateOf() {
		case health.Active:
			if snapshot := stats.Snapshot(); snapshot.PendingReverify {
				score *= 0.5
			}
			buckets.active = append(buckets.active, pick{entry: entry, score: score})
		case health.Suspect, health.HalfOpen:
			buckets.active = append(buckets.active, pick{entry: entry, score: score * 0.3})
		}
	}
	for _, hash := range verifyHashes {
		entry := r.pool.Get(hash)
		if entry == nil || !r.eligible(entry, host, now, exclude, cfg) || blockedCountry(entry, blocked) {
			continue
		}
		stats := entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys).Get(key)
		buckets.verifying = append(buckets.verifying, pick{entry: entry, score: recentScore(stats, cfg)})
	}
	for _, hash := range reachHashes {
		entry := r.pool.Get(hash)
		if entry == nil || !r.eligible(entry, host, now, exclude, cfg) || blockedCountry(entry, blocked) {
			continue
		}
		buckets.reachable = append(buckets.reachable, pick{entry: entry, score: 0})
	}

	strict := cfg.HealthMode == "strict"
	if strict {
		buckets.active = filterActiveOnly(buckets.active, key, cfg)
		buckets.verifying = nil
		buckets.reachable = nil
	}
	chosen := r.chooseBucket(key, buckets.active, buckets.verifying, buckets.reachable, cfg)
	if chosen == nil {
		if cfg.ColdStartFallback && !strict && len(buckets.reachable) > 0 {
			chosen = sampleUniform(buckets.reachable)
		} else {
			return nil, ErrNoEligible
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
		Key:      key,
		entry:    chosen.entry,
		client:   client,
	}, nil
}

// shouldSeed rate-limits cold-start scans for an unknown key.
func (r *Router) shouldSeed(key health.HealthKey, now time.Time) bool {
	r.seedMu.Lock()
	defer r.seedMu.Unlock()
	last := r.lastSeed[key]
	if !last.IsZero() && now.Sub(last) < 30*time.Second {
		return false
	}
	r.lastSeed[key] = now
	return true
}

// seedKey is the only full pool scan on a request path; it runs once per key
// per 30 seconds and then the index serves every subsequent request.
func (r *Router) seedKey(key health.HealthKey, host string, exclude map[string]struct{}, cfg Config, now time.Time, blocked map[string]struct{}) ([]node.Hash, []node.Hash, []node.Hash) {
	var active, verifying, reachable []node.Hash
	for _, entry := range r.pool.List() {
		if !r.eligible(entry, host, now, exclude, cfg) || blockedCountry(entry, blocked) {
			continue
		}
		if entry.Options.Type == node.Direct {
			active = append(active, entry.Hash)
			r.index.Upsert(key, entry.Hash, health.Active, now)
			continue
		}
		table := entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys)
		stats := table.Get(key)
		if stats != nil {
			stats.Tick(now, cfg.Health)
			switch stats.StateOf() {
			case health.Active, health.Suspect, health.HalfOpen:
				active = append(active, entry.Hash)
			case health.Verifying:
				verifying = append(verifying, entry.Hash)
			}
			if last := stats.Snapshot().LastReachableAt; !last.IsZero() {
				r.index.Upsert(key, entry.Hash, stats.StateOf(), last)
			}
			continue
		}
		if last := entry.LastSuccessAt(); last > 0 {
			at := time.Unix(0, last)
			if cfg.ReachableWindow <= 0 || now.Sub(at) <= cfg.ReachableWindow {
				reachable = append(reachable, entry.Hash)
				r.index.MarkReachable(key.Target, entry.Hash, at)
			}
		}
	}
	return active, verifying, reachable
}

func blockedCountry(entry *node.Entry, blocked map[string]struct{}) bool {
	if len(blocked) == 0 || entry == nil {
		return false
	}
	country := entry.Region()
	if country == "" {
		return false
	}
	_, bad := blocked[country]
	return bad
}

func filterActiveOnly(picks []pick, key health.HealthKey, cfg Config) []pick {
	out := picks[:0]
	for _, candidate := range picks {
		if candidate.entry.Options.Type == node.Direct {
			out = append(out, candidate)
			continue
		}
		table := candidate.entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys)
		stats := table.Get(key)
		if stats != nil && stats.StateOf() == health.Active {
			out = append(out, candidate)
		}
	}
	return out
}

func recentScore(stats *health.KeyStats, cfg Config) float64 {
	score := 0.1
	if stats != nil {
		snapshot := stats.Snapshot()
		beta := snapshot.Beta200
		if snapshot.ValidRequired {
			beta = snapshot.BetaValid
		}
		score = 0.05 + beta.Quantile(cfg.Health.RecentQuantile)
	}
	return score
}

func (r *Router) chooseBucket(key health.HealthKey, active, verifying, reachable []pick, cfg Config) *pick {
	now := r.clock()
	exploration := cfg.ExplorationRatio
	candidateRatio := cfg.CandidateExplorationRatio
	if exploration < 0 {
		exploration = 0
	}
	if candidateRatio < 0 || candidateRatio > exploration {
		candidateRatio = 0
	}
	roll := rand.Float64()
	if len(active) > 0 {
		if roll < exploration && (len(verifying) > 0 || len(reachable) > 0) {
			if roll < candidateRatio && len(reachable) > 0 {
				return sampleUniform(reachable)
			}
			if len(verifying) > 0 {
				return mixedSample(verifying)
			}
		}
		return weightedSample(active)
	}
	if len(verifying) > 0 {
		return mixedSample(verifying)
	}
	if len(reachable) > 0 {
		return sampleUniform(reachable)
	}
	_ = key
	_ = now
	return nil
}

func weightedSample(picks []pick) *pick {
	total := 0.0
	for _, candidate := range picks {
		total += math.Max(candidate.score, 0) + 0.001
	}
	if total <= 0 {
		return &picks[0]
	}
	point := rand.Float64() * total
	for i := range picks {
		point -= math.Max(picks[i].score, 0) + 0.001
		if point <= 0 {
			return &picks[i]
		}
	}
	return &picks[len(picks)-1]
}

func mixedSample(picks []pick) *pick {
	if len(picks) == 0 {
		return nil
	}
	if rand.Float64() < 0.5 {
		return &picks[rand.IntN(len(picks))]
	}
	return weightedSample(picks)
}

func sampleUniform(picks []pick) *pick {
	if len(picks) == 0 {
		return nil
	}
	return &picks[rand.IntN(len(picks))]
}

// healthObservation converts an attributed outcome into a health observation.
func healthObservation(key health.HealthKey, outcome Outcome) health.Observation {
	obs := health.Observation{
		Key:             key,
		ValidRequired:   key.ValidRequired(),
		Status:          outcome.StatusCode,
		HeadersReceived: outcome.HeadersReceived,
		ValidInference:  outcome.ValidInference,
		ValidKnown:      outcome.ValidKnown,
		Stream:          health.StreamNone,
		TTFB:            outcome.Latency,
		RetryAfter:      outcome.RetryAfter,
		Diagnostic:      outcome.Diagnostic,
		Direct:          outcome.Direct,
	}
	switch outcome.Kind {
	case Success:
		switch {
		case outcome.StatusCode == http.StatusOK:
			if obs.ValidRequired && outcome.ValidKnown && !outcome.ValidInference {
				obs.Kind = health.OutcomeInvalidInference
			} else {
				obs.Kind = health.OutcomeSuccess
			}
		case outcome.StatusCode >= 200 && outcome.StatusCode < 300:
			obs.Kind = health.OutcomeNonTarget2xx
		default:
			obs.Kind = health.OutcomeNeutral
		}
	case ProxyFailure:
		obs.Kind = health.OutcomeTransportFailure
	case ExitFailure:
		switch outcome.StatusCode {
		case http.StatusTooManyRequests:
			obs.Kind = health.OutcomeRateLimited
		case http.StatusNotFound:
			obs.Kind = health.OutcomeNotFound
		default:
			obs.Kind = health.OutcomeExitFailure
		}
	case UpstreamFailure:
		if outcome.UpstreamWide {
			obs.Kind = health.OutcomeUpstreamWide
		} else {
			obs.Kind = health.OutcomeExitFailure
		}
	case KeyFailure:
		obs.Kind = health.OutcomeKeyFailure
	case RegionBlocked:
		obs.Kind = health.OutcomeRegionBlocked
	case ClientCancel:
		obs.Kind = health.OutcomeClientCancel
	case BootstrapFailure:
		obs.Kind = health.OutcomeBootstrapFailure
	case EnvironmentFailure:
		obs.Kind = health.OutcomeEnvironmentFailure
	case EnvironmentSlow:
		obs.Kind = health.OutcomeEnvironmentSlow
	case InferenceTimeout:
		obs.Kind = health.OutcomeInvalidInference
		obs.Status = http.StatusOK
		obs.HeadersReceived = true
		obs.ValidKnown = true
		obs.ValidInference = false
	default:
		obs.Kind = health.OutcomeNeutral
	}
	return obs
}

// applyHealth writes one observation into the key's table and reports local
// transport failures to the environment classifier.
func (r *Router) applyHealth(attempt *Attempt, outcome Outcome, now time.Time) {
	if attempt == nil || attempt.entry == nil || !attempt.Key.Valid() {
		return
	}
	cfg := *r.cfg.Load()
	if outcome.Diagnostic || outcome.Direct {
		return
	}
	env := r.environment.Load()
	if env != nil && env.Frozen() {
		return
	}
	if outcome.Kind == BootstrapFailure {
		if env != nil {
			env.ReportBootstrapProbe(false, 0, now)
		}
	} else if outcome.Kind == ProxyFailure && !outcome.Bootstrap {
		if env != nil {
			env.ReportTransportFailure(attempt.NodeID, attempt.Key.Target, now)
		}
	} else if outcome.Kind == Success {
		if env != nil {
			env.ReportTransportSuccess(now)
		}
	}
	table := attempt.entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys)
	obs := healthObservation(attempt.Key, outcome)
	transition, stats, ok := table.Apply(attempt.Key, obs, cfg.Health, now)
	if !ok {
		return
	}
	if stats != nil {
		snapshot := stats.Snapshot()
		r.index.Upsert(attempt.Key, attempt.entry.Hash, snapshot.State, snapshot.LastReachableAt)
	}
	if transition.Changed {
		r.recordHealthTransition(attempt, transition)
	}
}

func (r *Router) recordHealthTransition(attempt *Attempt, transition health.Transition) {
	if r.logger == nil {
		return
	}
	r.logger.Debug("egress health state changed",
		"component", "egress", "event", "health_state_changed",
		"node_id", attempt.NodeID, "target", attempt.Key.Target,
		"model", attempt.Key.Model, "protocol", attempt.Key.Protocol,
		"mode", attempt.Key.Mode, "from", transition.From, "to", transition.To,
		"reason", transition.Reason)
}

// ReportStream records the complete stream result without re-releasing the
// attempt.
func (a *Attempt) ReportStream(result health.StreamResult) {
	if a == nil || a.entry == nil || !a.Key.Valid() || result == health.StreamNone {
		return
	}
	a.streamOnce.Do(func() {
		cfg := *a.Router.cfg.Load()
		table := a.entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys)
		stats := table.Get(a.Key)
		if stats == nil {
			return
		}
		obs := health.Observation{Key: a.Key, Kind: health.OutcomeNeutral, Stream: result}
		stats.Apply(obs, cfg.Health, a.Router.clock())
	})
}

// ReportComplete records the full response duration for non-streaming calls.
func (a *Attempt) ReportComplete(total time.Duration) {
	if a == nil || a.entry == nil || !a.Key.Valid() || total <= 0 {
		return
	}
	a.completeOnce.Do(func() {
		cfg := *a.Router.cfg.Load()
		table := a.entry.HealthTable(cfg.MaxKeysPerNode, cfg.MaxPendingKeys)
		stats := table.Get(a.Key)
		if stats == nil {
			return
		}
		stats.Apply(health.Observation{Key: a.Key, Kind: health.OutcomeNeutral, Total: total}, cfg.Health, a.Router.clock())
	})
}

var errNoKey = errors.New("egress: health key required")
