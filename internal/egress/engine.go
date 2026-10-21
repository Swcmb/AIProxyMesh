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
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/probe"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/state"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/subscription"
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
	pool     *pool.Pool
	outbound *outbound.Manager
	router   *routing.Router
	probe    *probe.Manager
	subs     *subscription.Manager
	store    *state.Store
	geoip    *geoip.Service
	// geoipConfig records the section the service was built from, so a hot
	// reload only rebuilds it when something actually changed.
	geoipConfig *config.EgressGeoIP
	logger      *slog.Logger
	settings    atomic.Pointer[settings]

	events    chan state.Event
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
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
	router := routing.New(nodePool, outboundManager, routerConfig(cfg), logger)
	prober := probe.New(nodePool, outboundManager, probeConfig(cfg, opts.Upstream), logger)

	e := &Engine{
		pool:     nodePool,
		outbound: outboundManager,
		router:   router,
		probe:    prober,
		store:    store,
		logger:   logger,
		events:   make(chan state.Event, 1024),
	}
	e.settings.Store(&settings{egress: cfg, upstream: opts.Upstream, performance: opts.Performance, legacy: opts.Legacy})
	e.subs = subscription.New(nodePool, nil, e.sources, logger)
	e.geoip = newGeoIPService(cfg, logger)
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
	e.router.Configure(routerConfig(egress))
	e.probe.Configure(probeConfig(egress, upstream))
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

// ProbeAll synchronously probes every supported node.
func (e *Engine) ProbeAll(ctx context.Context) { e.probe.ProbeAll(ctx) }

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
			ID:        sub.ID,
			Name:      sub.Name,
			URL:       sub.URL,
			Content:   sub.Content,
			Enabled:   sub.Enabled,
			Protocols: append([]string(nil), sub.Protocols...),
			Refresh:   time.Duration(sub.RefreshSeconds) * time.Second,
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
	return nil
}

func (e *Engine) save(ctx context.Context) error {
	snapshot := state.Snapshot{}
	for _, entry := range e.pool.List() {
		record := state.NodeRecord{
			Hash:          entry.ID(),
			Raw:           entry.Raw,
			Enabled:       entry.Enabled(),
			Failures:      int32(entry.Failures()),
			CreatedAt:     entry.CreatedAt,
			Subscriptions: entry.SubscriptionIDs(),
			Latency:       map[string]state.LatencyRecord{},
			Upstream:      map[string]state.UpstreamRecord{},
		}
		if until := entry.CooldownUntil(); until > 0 {
			record.CooldownUntil = time.Unix(0, until).UTC()
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
