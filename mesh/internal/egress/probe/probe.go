// Package probe actively measures exit reachability and latency against the
// real upstream hosts.
package probe

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

// Config controls the probe loop. Targets are derived from the configured
// upstream URLs; the prober never owns a separate target domain setting.
type Config struct {
	Targets     []string
	Interval    time.Duration
	Timeout     time.Duration
	Concurrency int
}

// DefaultConfig returns a conservative probe cadence.
func DefaultConfig() Config {
	return Config{Interval: time.Minute, Timeout: 10 * time.Second, Concurrency: 8}
}

func (c Config) normalized() Config {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
	seen := make(map[string]struct{}, len(c.Targets))
	targets := make([]string, 0, len(c.Targets))
	for _, target := range c.Targets {
		u, err := url.Parse(target)
		if err != nil || u.Hostname() == "" {
			continue
		}
		key := u.Scheme + "://" + u.Host
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, u.Scheme+"://"+u.Host)
	}
	c.Targets = targets
	return c
}

// Manager runs periodic probes over eligible pool entries.
type Manager struct {
	pool     *pool.Pool
	outbound *outbound.Manager
	logger   *slog.Logger
	cfg      atomic.Pointer[Config]
}

// New creates a probe manager.
func New(p *pool.Pool, manager *outbound.Manager, cfg Config, logger *slog.Logger) *Manager {
	if p == nil {
		panic("probe: nil pool")
	}
	if manager == nil {
		panic("probe: nil outbound manager")
	}
	normalized := cfg.normalized()
	m := &Manager{pool: p, outbound: manager, logger: logger}
	m.cfg.Store(&normalized)
	return m
}

// Configure swaps targets and cadence (hot reload).
func (m *Manager) Configure(cfg Config) {
	normalized := cfg.normalized()
	m.cfg.Store(&normalized)
}

// Run probes immediately and then on the configured interval until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	m.ProbeAll(ctx)
	timer := time.NewTimer(m.cfg.Load().Interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			cfg := m.cfg.Load()
			m.ProbeAll(ctx)
			timer.Reset(cfg.Interval)
		}
	}
}

// ProbeAll probes every supported node concurrently, bounded by Concurrency.
func (m *Manager) ProbeAll(ctx context.Context) {
	cfg := *m.cfg.Load()
	if len(cfg.Targets) == 0 {
		return
	}
	entries := m.pool.List()
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for _, entry := range entries {
		if entry == nil || !entry.Options.Supported() {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(entry *node.Entry) {
			defer wg.Done()
			defer func() { <-sem }()
			m.ProbeEntry(ctx, entry)
		}(entry)
	}
	wg.Wait()
}

// ProbeEntry measures one node against every target. A node is considered
// reachable when it produces any HTTP response; 401/403/429 still prove the
// route works, while transport errors do not.
func (m *Manager) ProbeEntry(ctx context.Context, entry *node.Entry) {
	if entry == nil || !entry.Options.Supported() {
		return
	}
	cfg := *m.cfg.Load()
	client, err := m.outbound.Client(entry)
	if err != nil {
		entry.RecordProbeResult(false, time.Now())
		entry.SetLastError(err.Error())
		return
	}
	okAny := true
	for _, target := range cfg.Targets {
		ok, latency, err := m.probeTarget(ctx, client, target, cfg.Timeout)
		now := time.Now()
		entry.RecordProbeResult(ok, now)
		if !ok {
			okAny = false
			entry.SetLastError(err.Error())
			continue
		}
		if table := entry.Latency(); table != nil {
			if parsed, parseErr := url.Parse(target); parseErr == nil {
				table.Update(parsed.Hostname(), latency, 10*time.Minute)
			}
		}
	}
	if okAny {
		entry.SetLastError("")
	}
}

func (m *Manager) probeTarget(ctx context.Context, client *http.Client, target string, timeout time.Duration) (bool, time.Duration, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("User-Agent", httpx.UserAgent())
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return false, 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	return true, latency, nil
}
