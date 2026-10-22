// Package egress wires the node pool, outbound transports, router, prober,
// subscription refresher and SQLite persistence into one lifecycle.
package egress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/geoip"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/probe"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/state"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/subscription"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/transport"
)

// ErrDisabled reports that the egress section is absent or switched off.
var ErrDisabled = errors.New("egress: disabled")

// Options carries the gateway configuration the engine needs.
type Options struct {
	Egress      *config.EgressConfig
	Performance config.PerformanceConfig
	Upstream    config.UpstreamConfig
	Legacy      string
	Logger      *slog.Logger
}

type settings struct {
	egress      *config.EgressConfig
	upstream    config.UpstreamConfig
	performance config.PerformanceConfig
	legacy      string
}

// Engine owns the egress subsystem.
type Engine struct {
	pool      *pool.Pool
	outbound  *outbound.Manager
	router    *routing.Router
	probe     *probe.Manager
	scheduler *probe.Scheduler
	subs      *subscription.Manager
	store     *state.Store
	geoip     *geoip.Service
	// geoipConfig records the section the service was built from, so a hot
	// reload only rebuilds it when something actually changed.
	geoipConfig *config.EgressGeoIP
	logger      *slog.Logger
	settings    atomic.Pointer[settings]

	transportFactory atomic.Pointer[transport.Factory]
	environment      *health.Environment

	events    chan state.Event
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once

	probeMu  sync.Mutex
	probeJob *batchProbeState
}

// New builds and restores the engine. Call Start to run background work.
func New(opts Options) (*Engine, error) {
	if opts.Egress == nil || !opts.Egress.Enabled {
		return nil, ErrDisabled
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cfg := opts.Egress
	store, err := state.Open(cfg.StatePath)
	if err != nil {
		return nil, err
	}

	maxLatency := cfg.MaxLatencyEntries
	if maxLatency <= 0 {
		maxLatency = 128
	}
	nodePool := pool.New(maxLatency)
	outboundManager := outbound.NewManager(outboundConfig(opts.Performance))
	factory, err := newTransportFactory(cfg, opts.Performance)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	outboundManager.SetFactory(factory)
	router := routing.New(nodePool, outboundManager, routerConfig(cfg), logger)
	environment := health.NewEnvironment(cfg.Bootstrap.EnvironmentConfig(), time.Now())
	router.SetEnvironment(environment)
	prober := probe.New(nodePool, outboundManager, probeConfig(cfg, opts.Upstream), logger)
	scheduler := probe.NewScheduler(nodePool, schedulerConfig(cfg), logger)
	scheduler.SetEnvironment(environment)

	e := &Engine{
		pool:        nodePool,
		outbound:    outboundManager,
		router:      router,
		probe:       prober,
		scheduler:   scheduler,
		store:       store,
		logger:      logger,
		environment: environment,
		events:      make(chan state.Event, 1024),
	}
	e.transportFactory.Store(factory)
	e.settings.Store(&settings{egress: cfg, upstream: opts.Upstream, performance: opts.Performance, legacy: opts.Legacy})
	e.subs = subscription.New(nodePool, nil, e.sources, logger)
	e.geoip = newGeoIPService(cfg, logger)
	e.router.SetRegionResolver(e.nodeRegionFor)
	if e.geoip != nil {
		geo := *cfg.GeoIP
		e.geoipConfig = &geo
	}

	if err := e.restore(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	e.pruneOrphanedReferences()
	return e, nil
}

// pruneOrphanedReferences drops node references left behind by subscriptions
// that are no longer configured, and persists the result. The state database is
// a cache of health, not a source of subscriptions: without this, deleting a
// subscription would keep its exits in the pool forever.
func (e *Engine) pruneOrphanedReferences() {
	keep := map[string]struct{}{}
	for _, source := range e.sources() {
		keep[source.ID] = struct{}{}
	}
	removed := e.pool.PruneSubscriptions(keep)
	if removed == 0 {
		return
	}
	if e.logger != nil {
		e.logger.Info("egress orphan cleanup", "component", "egress", "event", "orphan_nodes_removed", "nodes", removed)
	}
	if err := e.save(context.Background()); err != nil && e.logger != nil {
		e.logger.Warn("egress state save failed", "component", "egress", "event", "state_save_failed", "error", err)
	}
}

// Start launches subscription refresh, probing, periodic persistence and the
// event writer.
func (e *Engine) Start(parent context.Context) {
	e.ctx, e.cancel = context.WithCancel(parent)

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.subs.Start(e.ctx)
	}()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.probe.Run(e.ctx)
	}()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.bootstrapProbeLoop(e.ctx)
	}()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.scheduler.Run(e.ctx)
	}()

	e.StartGeoIP(e.ctx)

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-ticker.C:
				e.save(e.ctx)
			}
		}
	}()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for {
			select {
			case <-e.ctx.Done():
				return
			case event := <-e.events:
				if err := e.store.AppendEvent(e.ctx, event); err != nil && e.logger != nil {
					e.logger.Warn("egress event persist failed", "component", "egress", "error", err)
				}
			}
		}
	}()
}

// Close stops background work, persists the final snapshot and closes the
// state database.
func (e *Engine) Close() error {
	var err error
	e.closeOnce.Do(func() {
		if e.cancel != nil {
			e.cancel()
		}
		e.wg.Wait()
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if saveErr := e.save(saveCtx); saveErr != nil {
			err = saveErr
		}
		cancel()
		closeErr := e.store.Close()
		if err == nil {
			err = closeErr
		}
		if e.geoip != nil {
			_ = e.geoip.Close()
		}
	})
	return err
}

// Apply updates the live egress configuration and reconciles subscriptions.
func (e *Engine) Apply(egress *config.EgressConfig, upstream config.UpstreamConfig, legacy string) {
	if egress == nil || !egress.Enabled {
		return
	}
	current := e.settings.Load()
	perf := config.PerformanceConfig{}
	if current != nil {
		perf = current.performance
	}
	e.settings.Store(&settings{egress: egress, upstream: upstream, performance: perf, legacy: legacy})
	e.reconfigureGeoIP(egress)
	if e.outbound != nil {
		e.outbound.Reconfigure(outboundConfig(perf))
	}
	if factory, err := newTransportFactory(egress, perf); err == nil {
		e.transportFactory.Store(factory)
		if e.outbound != nil {
			e.outbound.SetFactory(factory)
		}
	}
	if e.environment != nil {
		e.environment.Configure(egress.Bootstrap.EnvironmentConfig())
	}
	e.router.Configure(routerConfig(egress))
	e.probe.Configure(probeConfig(egress, upstream))
	e.scheduler.Configure(schedulerConfig(egress))
	e.scheduler.SetEnvironment(e.environment)
	e.subs.Reconcile()
	e.pruneOrphanedReferences()
	if e.ctx != nil {
		go e.subs.RefreshAll(e.ctx)
	}
}

// Router returns the selection engine for gateway integration.
func (e *Engine) Router() *routing.Router { return e.router }

// Pool exposes the node registry for tests and diagnostics.
func (e *Engine) Pool() *pool.Pool { return e.pool }

// Client returns the HTTP client bound to one node.
func (e *Engine) Client(entry *node.Entry) (*http.Client, error) {
	return e.outbound.Client(entry)
}

// Subscriptions exposes the subscription manager.
func (e *Engine) Subscriptions() *subscription.Manager { return e.subs }

// RefreshAll synchronously refreshes every configured subscription.
func (e *Engine) RefreshAll(ctx context.Context) { e.subs.RefreshAll(ctx) }

// NodeRegion resolves one node's GeoIP country, caching it on the entry.
func (e *Engine) NodeRegion(id string) string {
	hash, err := node.ParseHex(id)
	if err != nil {
		return ""
	}
	entry := e.pool.Get(hash)
	if entry == nil {
		return ""
	}
	return e.nodeRegionFor(entry)
}

func (e *Engine) nodeRegionFor(entry *node.Entry) string {
	if entry == nil {
		return ""
	}
	if region := entry.Region(); region != "" {
		return region
	}
	if result, ok := e.regionFor(entryRegionAddress(entry)); ok {
		entry.SetRegion(result.Country)
		return result.Country
	}
	return ""
}

// ProbeAll synchronously probes every supported node.
func (e *Engine) ProbeAll(ctx context.Context) { e.probe.ProbeAll(ctx) }

// ProbeNodes probes the given node IDs and returns how many entries were
// actually probed; unknown or unsupported IDs are skipped. The caller bounds
// the batch so a manual refresh stays bounded even with a very large pool.
func (e *Engine) ProbeNodes(ctx context.Context, ids []string) int {
	return e.probe.ProbeBatchFast(ctx, ids, nil)
}

// BatchProbeView is the management view of one asynchronous probe job.
type BatchProbeView struct {
	ID         string     `json:"id"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	Running    bool       `json:"running"`
	Cancelled  bool       `json:"cancelled"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type batchProbeState struct {
	mu     sync.Mutex
	view   BatchProbeView
	cancel context.CancelFunc
}

func (s *batchProbeState) setDone(done int) {
	s.mu.Lock()
	s.view.Done = done
	s.mu.Unlock()
}

func (s *batchProbeState) finish() {
	s.mu.Lock()
	s.view.Running = false
	now := time.Now()
	s.view.FinishedAt = &now
	s.mu.Unlock()
}

func (s *batchProbeState) markCancelled() {
	s.mu.Lock()
	s.view.Cancelled = true
	s.mu.Unlock()
}

func (s *batchProbeState) snapshot() BatchProbeView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

// StartBatchProbe starts one asynchronous probe job for a large selection.
// Only one job runs at a time; the caller gets the running job back with
// started=false instead of stacking another sweep.
func (e *Engine) StartBatchProbe(ids []string) (BatchProbeView, bool) {
	return e.startBatchProbe(ids)
}

// StartBatchProbeAll queues every enabled, supported node in the pool.
func (e *Engine) StartBatchProbeAll() (BatchProbeView, bool) {
	entries := e.pool.List()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Enabled() && entry.Options.Supported() {
			ids = append(ids, entry.ID())
		}
	}
	return e.startBatchProbe(ids)
}

func (e *Engine) startBatchProbe(ids []string) (BatchProbeView, bool) {
	e.probeMu.Lock()
	if e.probeJob != nil && e.probeJob.snapshot().Running {
		view := e.probeJob.snapshot()
		e.probeMu.Unlock()
		return view, false
	}
	base := e.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	job := &batchProbeState{
		view: BatchProbeView{
			ID:        fmt.Sprintf("probe-%d", time.Now().UnixNano()),
			Total:     len(ids),
			Running:   true,
			StartedAt: time.Now(),
		},
		cancel: cancel,
	}
	e.probeJob = job
	e.probeMu.Unlock()
	go func() {
		defer cancel()
		e.probe.ProbeBatchFast(ctx, ids, func(done, _ int) { job.setDone(done) })
		job.finish()
	}()
	return job.snapshot(), true
}

// CancelBatchProbe stops the running job; finished nodes keep their results.
func (e *Engine) CancelBatchProbe() bool {
	e.probeMu.Lock()
	job := e.probeJob
	e.probeMu.Unlock()
	if job == nil || !job.snapshot().Running {
		return false
	}
	job.markCancelled()
	job.cancel()
	return true
}

// BatchProbeStatus returns the current asynchronous probe job, if any.
func (e *Engine) BatchProbeStatus() BatchProbeView {
	e.probeMu.Lock()
	job := e.probeJob
	e.probeMu.Unlock()
	if job == nil {
		return BatchProbeView{}
	}
	return job.snapshot()
}

// RecordEvent queues a failover event for persistence. It never blocks the
// request path; events are dropped when the buffer is full.
func (e *Engine) RecordEvent(event state.Event) {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	select {
	case e.events <- event:
	default:
	}
}

// Events returns persisted failover events.
func (e *Engine) Events(ctx context.Context, limit int) ([]state.Event, error) {
	return e.store.Events(ctx, limit)
}

// RefreshSubscription synchronously refreshes one configured subscription.
func (e *Engine) RefreshSubscription(ctx context.Context, id string) error {
	return e.subs.Refresh(ctx, id)
}

// ProbeNode actively probes one node and returns its refreshed snapshot.
func (e *Engine) ProbeNode(ctx context.Context, id string) (node.Snapshot, error) {
	hash, err := node.ParseHex(id)
	if err != nil {
		return node.Snapshot{}, err
	}
	entry := e.pool.Get(hash)
	if entry == nil {
		return node.Snapshot{}, fmt.Errorf("node %s not found", id)
	}
	e.probe.ProbeEntry(ctx, entry)
	target := ""
	if current := e.settings.Load(); current != nil {
		target = current.upstream.Zen
	}
	return entry.Snapshot(target, time.Now()), nil
}

// Node returns one node snapshot by ID.
// CleanupSubscription drops one subscription's cooling exits and refreshes it,
// so healthy nodes come back with cleared counters. It returns how many nodes
// left the pool.
func (e *Engine) CleanupSubscription(ctx context.Context, id string) (int, error) {
	found := false
	for _, source := range e.sources() {
		if source.ID == id {
			found = true
			break
		}
	}
	if !found {
		return 0, fmt.Errorf("subscription %q is not configured", id)
	}
	removed := e.pool.PruneCooling(id, time.Now())
	if err := e.RefreshSubscription(ctx, id); err != nil {
		return removed, err
	}
	return removed, nil
}

// SetNodeEnabled flips the manual switch on one node.
func (e *Engine) SetNodeEnabled(id string, enabled bool) error {
	hash, err := node.ParseHex(id)
	if err != nil {
		return err
	}
	return e.pool.SetEnabled(hash, enabled)
}

// Diagnose explains eligibility for a target.
func (e *Engine) Diagnose(target *url.URL, exclude map[string]struct{}) []routing.Candidate {
	return e.router.Diagnose(target, exclude)
}

// Counts summarizes pool health for the overview page.
type Counts struct {
	Total       int `json:"total"`
	Eligible    int `json:"eligible"`
	Cooling     int `json:"cooling"`
	Unsupported int `json:"unsupported"`
	Disabled    int `json:"disabled"`
}

// Snapshot is the management view of the egress subsystem.
type Snapshot struct {
	Enabled       bool                  `json:"enabled"`
	StatePath     string                `json:"state_path"`
	Counts        Counts                `json:"counts"`
	Nodes         []node.Snapshot       `json:"nodes"`
	Subscriptions []subscription.Status `json:"subscriptions"`
}

// Snapshot builds the management view. The upstream Zen host supplies the
// latency/score target shown to operators.
func (e *Engine) Snapshot() Snapshot {
	now := time.Now()
	current := e.settings.Load()
	target := ""
	if current != nil {
		if parsed, err := url.Parse(current.upstream.Zen); err == nil {
			target = parsed.String()
		}
	}
	nodes := e.pool.Snapshot(target, now)
	counts := Counts{Total: len(nodes)}
	for _, item := range nodes {
		switch {
		case item.CooldownUntil != nil:
			counts.Cooling++
		case !item.Supported:
			counts.Unsupported++
		case !item.Enabled:
			counts.Disabled++
		}
	}
	if target != "" {
		if parsed, err := url.Parse(target); err == nil {
			for _, candidate := range e.router.Diagnose(parsed, nil) {
				if candidate.Eligible {
					counts.Eligible++
				}
			}
		}
	}
	return Snapshot{
		Enabled:       true,
		StatePath:     e.store.Path(),
		Counts:        counts,
		Nodes:         nodes,
		Subscriptions: e.subs.Statuses(),
	}
}

func (e *Engine) sources() []subscription.Source {
	current := e.settings.Load()
	if current == nil || current.egress == nil {
		return nil
	}
	cfg := current.egress
	out := make([]subscription.Source, 0, len(cfg.Subscriptions)+1)
	for _, sub := range cfg.Subscriptions {
		out = append(out, subscription.Source{
			ID:         sub.ID,
			Name:       sub.Name,
			URL:        sub.URL,
			Content:    sub.Content,
			Enabled:    sub.Enabled,
			Protocols:  append([]string(nil), sub.Protocols...),
			Refresh:    time.Duration(sub.RefreshSeconds) * time.Second,
			Ephemeral:  sub.Ephemeral,
			EvictDelay: time.Duration(sub.EvictDelaySeconds) * time.Second,
		})
	}
	if cfg.LegacyProxiesImported() && current.legacy != "" {
		out = append(out, subscription.Source{
			ID:       "legacy",
			Name:     "legacy proxies/proxyfile",
			Content:  current.legacy,
			Enabled:  true,
			Internal: true,
		})
	}
	return out
}

func (e *Engine) restore(ctx context.Context) error {
	snapshot, err := e.store.Load(ctx)
	if err != nil {
		return err
	}
	maxLatency := e.settings.Load().egress.MaxLatencyEntries
	if maxLatency <= 0 {
		maxLatency = 128
	}
	for _, record := range snapshot.Nodes {
		var opts node.Options
		if err := json.Unmarshal(record.Raw, &opts); err != nil {
			continue
		}
		hash := node.HashOptions(opts)
		entry := node.NewEntry(hash, record.Raw, opts, record.CreatedAt, maxLatency)
		cooldown := int64(0)
		if !record.CooldownUntil.IsZero() {
			cooldown = record.CooldownUntil.UnixNano()
		}
		entry.RestoreCooldown(record.Failures, cooldown)
		cooldownSince := int64(0)
		if !record.CooldownSince.IsZero() {
			cooldownSince = record.CooldownSince.UnixNano()
		}
		lastEgress := int64(0)
		if !record.LastEgressUpdate.IsZero() {
			lastEgress = record.LastEgressUpdate.UnixNano()
		}
		// Legacy records carry no ever_healthy column and get its default (1),
		// so an upgrade keeps routing until the first probe sweep finishes.
		entry.RestoreHealth(record.EverHealthy, cooldownSince, record.EgressIP, lastEgress)
		if !record.LastSuccess.IsZero() {
			entry.RestoreLastSuccess(record.LastSuccess.UnixNano())
		}
		if latency := entry.Latency(); latency != nil {
			for host, sample := range record.Latency {
				latency.Load(host, node.DomainStats{EWMA: sample.EWMA, LastUpdated: sample.UpdatedAt})
			}
		}
		for target, stats := range record.Upstream {
			entry.RestoreUpstreamStats(target, stats.Success, stats.Failure)
		}
		e.pool.Restore(entry, record.Subscriptions, record.Enabled)
	}
	if err := e.restoreHealth(ctx); err != nil {
		return err
	}
	return nil
}

// restoreHealth reloads key evidence, the environment epoch and the scheduler
// cursor. A restart resumes from the same statistics instead of cold-starting.
func (e *Engine) restoreHealth(ctx context.Context) error {
	current := e.settings.Load()
	if current == nil || current.egress == nil {
		return nil
	}
	cfg := current.egress.Health.HealthKeyConfig()
	now := time.Now()
	records, err := e.store.LoadHealthKeys(ctx)
	if err != nil {
		return err
	}
	rebuild := make(map[health.HealthKey][]routing.RebuildEntry)
	for _, record := range records {
		hash, err := node.ParseHex(record.NodeHash)
		if err != nil {
			continue
		}
		entry := e.pool.Get(hash)
		if entry == nil {
			continue
		}
		table := entry.HealthTable(current.egress.Health.MaxKeysPerNode, current.egress.Health.MaxPendingKeys)
		table.Upsert(health.FromSnapshot(record.Stats, cfg, now))
		rebuild[record.Stats.Key] = append(rebuild[record.Stats.Key], routing.RebuildEntry{
			Hash: hash, State: record.Stats.State, ReachableAt: record.Stats.LastReachableAt,
		})
	}
	if len(rebuild) > 0 {
		e.router.Index().Rebuild(rebuild)
	}
	if blocks, err := e.store.LoadRegionBlocks(ctx, now); err != nil {
		return err
	} else if len(blocks) > 0 {
		restored := make([]routing.RegionBlock, 0, len(blocks))
		for _, block := range blocks {
			restored = append(restored, routing.RegionBlock{Target: block.Target, Country: block.Country, Until: block.Until})
		}
		e.router.RestoreRegionBlocks(restored)
	}
	snapshot, ok, err := e.store.LoadEnvironment(ctx)
	if err != nil {
		return err
	}
	if ok {
		environment := health.RestoreEnvironment(snapshot, current.egress.Bootstrap.EnvironmentConfig())
		e.environment = environment
		e.router.SetEnvironment(environment)
	}
	if e.environment != nil {
		e.environment.OnEpoch(e.applyEnvironmentEpoch)
	}
	return nil
}

// applyEnvironmentEpoch resets admission evidence after local connectivity
// recovers. ACTIVE keeps routing but must re-verify in the new epoch.
func (e *Engine) applyEnvironmentEpoch(epoch uint64) {
	current := e.settings.Load()
	if current == nil || current.egress == nil {
		return
	}
	cfg := current.egress.Health.HealthKeyConfig()
	now := time.Now()
	for _, entry := range e.pool.List() {
		table := entry.ExistingHealthTable()
		if table == nil {
			continue
		}
		for _, record := range table.Snapshot() {
			if stats := table.Get(record.Key); stats != nil {
				stats.MarkEnvironmentEpoch(epoch, cfg, now)
			}
		}
	}
}

func (e *Engine) save(ctx context.Context) error {
	snapshot := state.Snapshot{}
	for _, entry := range e.pool.List() {
		record := state.NodeRecord{
			Hash:          entry.ID(),
			Raw:           entry.Raw,
			Enabled:       entry.Enabled(),
			Failures:      int32(entry.Failures()),
			EverHealthy:   entry.EverHealthy(),
			EgressIP:      entry.EgressIP(),
			CreatedAt:     entry.CreatedAt,
			Subscriptions: entry.SubscriptionIDs(),
			Latency:       map[string]state.LatencyRecord{},
			Upstream:      map[string]state.UpstreamRecord{},
		}
		if until := entry.CooldownUntil(); until > 0 {
			record.CooldownUntil = time.Unix(0, until).UTC()
		}
		if since := entry.CooldownSince(); since > 0 {
			record.CooldownSince = time.Unix(0, since).UTC()
		}
		if at := entry.LastEgressAttempt(); at > 0 {
			record.LastEgressUpdate = time.Unix(0, at).UTC()
		}
		if at := entry.LastSuccessAt(); at > 0 {
			record.LastSuccess = time.Unix(0, at).UTC()
			record.LastReachable = time.Unix(0, at).UTC()
		}
		if latency := entry.Latency(); latency != nil {
			for host, stats := range latency.Snapshot() {
				record.Latency[host] = state.LatencyRecord{EWMA: stats.EWMA, UpdatedAt: stats.LastUpdated}
			}
		}
		for target, counts := range entry.UpstreamSnapshot() {
			record.Upstream[target] = state.UpstreamRecord{Success: counts[0], Failure: counts[1]}
		}
		snapshot.Nodes = append(snapshot.Nodes, record)
	}
	if err := e.store.Save(ctx, snapshot); err != nil {
		if e.logger != nil {
			e.logger.Warn("egress state save failed", "component", "egress", "error", err)
		}
		return err
	}
	_ = e.store.PruneEvents(ctx, 5000)
	return nil
}

// saveHealth flushes non-candidate keys, bounded by max_dirty_rows.
func (e *Engine) saveHealth(ctx context.Context) error {
	current := e.settings.Load()
	if current == nil || current.egress == nil {
		return nil
	}
	limit := current.egress.Health.MaxDirtyRows
	if limit <= 0 {
		limit = 5000
	}
	records := make([]state.HealthRecord, 0, min(limit, 1024))
	for _, entry := range e.pool.List() {
		table := entry.ExistingHealthTable()
		if table == nil {
			continue
		}
		for _, snapshot := range table.Snapshot() {
			if snapshot.State == health.Candidate && snapshot.HTTP200Success+snapshot.HTTP200Failure == 0 &&
				snapshot.LastReachableAt.IsZero() {
				continue
			}
			records = append(records, state.HealthRecord{NodeHash: entry.ID(), Stats: snapshot})
			if len(records) >= limit {
				break
			}
		}
		if len(records) >= limit {
			break
		}
	}
	if err := e.store.SaveHealthKeys(ctx, records); err != nil {
		return err
	}
	if e.environment != nil {
		if err := e.store.SaveEnvironment(ctx, e.environment.Snapshot()); err != nil {
			return err
		}
	}
	blocks := e.router.RegionBlocks()
	blockRecords := make([]state.RegionBlockRecord, 0, len(blocks))
	for _, block := range blocks {
		blockRecords = append(blockRecords, state.RegionBlockRecord{Target: block.Target, Country: block.Country, Until: block.Until})
	}
	return e.store.SaveRegionBlocks(ctx, blockRecords)
}

func schedulerConfig(cfg *config.EgressConfig) probe.ScheduleConfig {
	healthCfg := cfg.Health.HealthKeyConfig()
	return probe.ScheduleConfig{
		Enabled:          cfg.Probe.CanaryEnabled == nil || *cfg.Probe.CanaryEnabled,
		BudgetPerSecond:  cfg.Probe.BudgetPerSecond,
		Burst:            cfg.Probe.BudgetBurst,
		DailyBudget:      cfg.Probe.CanaryDailyBudget,
		MaxPerScan:       cfg.Probe.MaxSchedulePerScan,
		MaxKeys:          cfg.Probe.MaxScheduledKeys,
		MaxInflight:      cfg.Probe.CanaryMaxInflight,
		CanaryMaxTokens:  cfg.Probe.CanaryMaxTokens,
		DefaultMode:      cfg.Probe.CanaryDefaultMode,
		VerifyingRecheck: time.Duration(cfg.Probe.VerifyingRecheckSeconds) * time.Second,
		ActiveRecheck:    time.Duration(cfg.Probe.ActiveRecheckSeconds) * time.Second,
		DeadRecheck:      time.Duration(cfg.Health.DeadRecheckSeconds) * time.Second,
		BackoffBase:      time.Duration(cfg.Probe.BackoffBaseSeconds) * time.Second,
		BackoffMax:       time.Duration(cfg.Probe.BackoffMaxSeconds) * time.Second,
		JitterRatio:      cfg.Probe.JitterRatio,
		TargetPause:      time.Duration(cfg.Probe.TargetPauseSeconds) * time.Second,
		Health:           healthCfg,
		MaxKeysPerNode:   cfg.Health.MaxKeysPerNode,
	}
}

func outboundConfig(perf config.PerformanceConfig) outbound.Config {
	cfg := outbound.DefaultConfig()
	if perf.MaxIdleConns > 0 {
		cfg.MaxIdleConns = perf.MaxIdleConns
	}
	if perf.MaxIdleConnsPerHost > 0 {
		cfg.MaxIdleConnsPerHost = perf.MaxIdleConnsPerHost
	}
	cfg.MaxConnsPerHost = perf.MaxConnsPerHost
	if perf.IdleConnTimeoutSeconds > 0 {
		cfg.IdleConnTimeout = time.Duration(perf.IdleConnTimeoutSeconds) * time.Second
	}
	if perf.ConnectTimeoutSeconds > 0 {
		cfg.ConnectTimeout = time.Duration(perf.ConnectTimeoutSeconds) * time.Second
	}
	return cfg
}

func routerConfig(cfg *config.EgressConfig) routing.Config {
	result := routing.DefaultConfig()
	result.Weights = routing.Weights{
		Health:   cfg.Schedule.HealthWeight,
		Latency:  cfg.Schedule.LatencyWeight,
		Routing:  cfg.Schedule.RoutingWeight,
		Upstream: cfg.Schedule.UpstreamSuccessWeight,
	}
	result.CooldownBase = time.Duration(cfg.Cooldown.BaseSeconds) * time.Second
	result.CooldownMax = time.Duration(cfg.Cooldown.MaxSeconds) * time.Second
	result.HealthMode = cfg.Health.Mode
	result.Health = cfg.Health.HealthKeyConfig()
	result.ExplorationRatio = cfg.Health.ExplorationRatio
	result.CandidateExplorationRatio = cfg.Health.CandidateExplorationRatio
	result.StableMinCapacity = cfg.Health.StableMinCapacity
	result.ReachableWindow = time.Duration(cfg.Health.ReachableWindowSeconds) * time.Second
	result.MaxKeysPerNode = cfg.Health.MaxKeysPerNode
	result.MaxPendingKeys = cfg.Health.MaxPendingKeys
	result.RouteSampleSize = cfg.Health.RouteSampleSize
	if cfg.Health.ColdStartFallback != nil {
		result.ColdStartFallback = *cfg.Health.ColdStartFallback
	}
	result.AllowedProtocols = map[node.Protocol]bool{
		node.HTTP:    cfg.ProtocolAllowed("http"),
		node.HTTPS:   cfg.ProtocolAllowed("https"),
		node.SOCKS5:  cfg.ProtocolAllowed("socks5"),
		node.SOCKS5H: cfg.ProtocolAllowed("socks5h"),
		node.Direct:  true,
	}
	return result
}

func probeConfig(cfg *config.EgressConfig, upstream config.UpstreamConfig) probe.Config {
	result := probe.DefaultConfig()
	result.Interval = time.Duration(cfg.Probe.IntervalSeconds) * time.Second
	result.Timeout = time.Duration(cfg.Probe.TimeoutSeconds) * time.Second
	result.Concurrency = cfg.Probe.Concurrency
	result.EgressIPEnabled = cfg.Probe.EgressIPEnabled == nil || *cfg.Probe.EgressIPEnabled
	result.EgressIPInterval = time.Duration(cfg.Probe.EgressIPIntervalSeconds) * time.Second
	result.EgressIPTarget = cfg.Probe.EgressIPTarget
	result.Targets = append([]string(nil), cfg.Probe.Targets...)
	if len(result.Targets) == 0 {
		for _, raw := range []string{upstream.Zen, upstream.Go, upstream.Anonymous} {
			if raw != "" {
				result.Targets = append(result.Targets, raw)
			}
		}
	}
	return result
}

// PrimaryTarget returns the upstream URL used for diagnostics.
func (e *Engine) PrimaryTarget() *url.URL {
	current := e.settings.Load()
	if current == nil {
		return nil
	}
	parsed, err := url.Parse(current.upstream.Zen)
	if err != nil {
		return nil
	}
	return parsed
}

// SetCanaryExecutor installs the gateway-side canary implementation.
func (e *Engine) SetCanaryExecutor(executor probe.CanaryFunc) {
	if e.scheduler != nil {
		e.scheduler.SetExecutor(executor)
	}
}

// OutboundClient returns the node-bound HTTP client for canary execution.
func (e *Engine) OutboundClient(entry *node.Entry) (*http.Client, error) {
	return e.outbound.Client(entry)
}

// newTransportFactory builds the explicit Mesh-to-node link factory.
func newTransportFactory(cfg *config.EgressConfig, perf config.PerformanceConfig) (*transport.Factory, error) {
	transportCfg := transport.DefaultConfig()
	transportCfg.Mode = transport.Mode(cfg.Bootstrap.Mode)
	transportCfg.BootstrapURL = cfg.Bootstrap.URL
	if perf.ConnectTimeoutSeconds > 0 {
		transportCfg.ConnectTimeout = time.Duration(perf.ConnectTimeoutSeconds) * time.Second
	}
	transportCfg.ResponseHeaderTimeout = 0
	if out := outboundConfig(perf); out.ConnectTimeout > 0 {
		transportCfg.ConnectTimeout = out.ConnectTimeout
	}
	return transport.New(transportCfg)
}

// bootstrapProbeLoop measures the bootstrap path itself. Its latency never
// enters node TTFB statistics.
func (e *Engine) bootstrapProbeLoop(ctx context.Context) {
	for {
		current := e.settings.Load()
		if current == nil {
			return
		}
		bootstrapCfg := current.egress.Bootstrap
		if bootstrapCfg.Mode != "http_connect" || bootstrapCfg.ProbeURL == "" {
			return
		}
		interval := time.Duration(bootstrapCfg.ProbeIntervalSeconds) * time.Second
		if interval < 10*time.Second {
			interval = 10 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		factory := e.transportFactory.Load()
		if factory == nil || factory.Dialer() == nil {
			continue
		}
		latency, err := factory.Dialer().Probe(ctx, bootstrapCfg.ProbeURL)
		now := time.Now()
		if e.environment == nil {
			continue
		}
		e.environment.ReportBootstrapProbe(err == nil, latency, now)
		if err == nil {
			e.environment.ReportBootstrapLatency(latency, now)
		}
	}
}
