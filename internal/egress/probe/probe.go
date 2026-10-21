// Package probe actively measures exit reachability and latency against the
// real upstream hosts.
package probe

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

const (
	maxEgressScanPerTick = 250
	maxProbeScanPerTick  = 2000
)

// Config controls the probe loop. Targets are derived from the configured
// upstream URLs; the prober never owns a separate target domain setting.
type Config struct {
	Targets     []string
	Interval    time.Duration
	Timeout     time.Duration
	Concurrency int
	// EgressIPEnabled turns on the low-frequency public exit-address probe.
	EgressIPEnabled  bool
	EgressIPInterval time.Duration
	EgressIPTarget   string
}

// DefaultConfig returns a conservative probe cadence.
func DefaultConfig() Config {
	return Config{
		Interval:         time.Minute,
		Timeout:          10 * time.Second,
		Concurrency:      8,
		EgressIPEnabled:  true,
		EgressIPInterval: 6 * time.Hour,
		EgressIPTarget:   "https://cloudflare.com/cdn-cgi/trace",
	}
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
	if c.EgressIPInterval <= 0 {
		c.EgressIPInterval = 6 * time.Hour
	}
	if strings.TrimSpace(c.EgressIPTarget) == "" {
		c.EgressIPTarget = "https://cloudflare.com/cdn-cgi/trace"
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

// job is one queued probe. Egress jobs fetch the public exit address; the
// default job measures upstream reachability and latency.
type job struct {
	entry  *node.Entry
	egress bool
	ctx    context.Context
	key    string
	done   func()
}

// Manager runs periodic probes over eligible pool entries.
type Manager struct {
	pool     *pool.Pool
	outbound *outbound.Manager
	logger   *slog.Logger
	cfg      atomic.Pointer[Config]

	highCh   chan job
	normalCh chan job
	queued   sync.Map // job key -> struct{}, dedupes scans against a slow queue
	started  atomic.Bool
	once     sync.Once
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
	m := &Manager{
		pool:     p,
		outbound: manager,
		logger:   logger,
		highCh:   make(chan job, 4096),
		normalCh: make(chan job, 4096),
	}
	m.cfg.Store(&normalized)
	return m
}

// Configure swaps targets and cadence (hot reload).
func (m *Manager) Configure(cfg Config) {
	normalized := cfg.normalized()
	m.cfg.Store(&normalized)
}

// Run starts the fixed worker pool and scans for due work every 13-17 seconds
// until ctx ends. The scan only enqueues; workers execute probes.
func (m *Manager) Run(ctx context.Context) {
	m.startWorkers(ctx)
	m.scheduleDue(ctx, time.Now(), false)
	m.scheduleDue(ctx, time.Now(), true)
	timer := time.NewTimer(scanInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			now := time.Now()
			m.scheduleDue(ctx, now, false)
			m.scheduleDue(ctx, now, true)
			timer.Reset(scanInterval())
		}
	}
}

// scanInterval returns the Resin-style 13-17s jittered scan cadence.
func scanInterval() time.Duration {
	return 13*time.Second + time.Duration(rand.IntN(4001))*time.Millisecond
}

func (m *Manager) startWorkers(ctx context.Context) {
	m.once.Do(func() {
		m.started.Store(true)
		count := m.cfg.Load().Concurrency
		for i := 0; i < count; i++ {
			go m.worker(ctx)
		}
	})
}

func (m *Manager) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		j, ok := m.next(ctx)
		if !ok {
			return
		}
		if j.egress {
			m.ProbeEgressIP(j.ctx, j.entry)
		} else {
			m.ProbeEntry(j.ctx, j.entry)
		}
		m.queued.Delete(j.key)
		if j.done != nil {
			j.done()
		}
	}
}

// next prefers the high-priority queue. Ten percent of the time the normal
// queue is tried first so routine refreshes are not starved by a continuous
// stream of new or recovering nodes.
func (m *Manager) next(ctx context.Context) (job, bool) {
	if rand.IntN(10) == 0 {
		select {
		case j := <-m.normalCh:
			return j, true
		case j := <-m.highCh:
			return j, true
		case <-ctx.Done():
			return job{}, false
		}
	}
	select {
	case j := <-m.highCh:
		return j, true
	default:
	}
	select {
	case j := <-m.highCh:
		return j, true
	case j := <-m.normalCh:
		return j, true
	case <-ctx.Done():
		return job{}, false
	}
}

func (m *Manager) enqueue(ctx context.Context, j job) bool {
	prefix := "p:"
	if j.egress {
		prefix = "e:"
	}
	j.key = prefix + j.entry.ID()
	if _, loaded := m.queued.LoadOrStore(j.key, struct{}{}); loaded {
		return true
	}
	ch := m.normalCh
	if !j.egress && (!j.entry.EverHealthy() || j.entry.Cooling(time.Now())) {
		ch = m.highCh
	}
	select {
	case ch <- j:
		return true
	case <-ctx.Done():
		m.queued.Delete(j.key)
		return false
	}
}

// tryEnqueue never blocks a scan: when the queue is saturated the job is
// skipped and the next scan re-evaluates it.
func (m *Manager) tryEnqueue(j job) bool {
	prefix := "p:"
	if j.egress {
		prefix = "e:"
	}
	j.key = prefix + j.entry.ID()
	if _, loaded := m.queued.LoadOrStore(j.key, struct{}{}); loaded {
		return true
	}
	ch := m.normalCh
	if !j.egress && (!j.entry.EverHealthy() || j.entry.Cooling(time.Now())) {
		ch = m.highCh
	}
	select {
	case ch <- j:
		return true
	default:
		m.queued.Delete(j.key)
		return false
	}
}

// scheduleDue enqueues nodes whose probe interval elapsed. Egress jobs are
// capped per scan so a large pool fills the queue gradually instead of
// flooding it in one tick.
func (m *Manager) scheduleDue(ctx context.Context, now time.Time, egress bool) {
	cfg := *m.cfg.Load()
	if egress {
		if !cfg.EgressIPEnabled || cfg.EgressIPTarget == "" {
			return
		}
	} else if len(cfg.Targets) == 0 {
		return
	}
	queued := 0
	for _, entry := range m.pool.List() {
		if entry == nil || !entry.Options.Supported() || !entry.Enabled() {
			continue
		}
		if egress {
			if queued >= maxEgressScanPerTick {
				return
			}
			if last := entry.LastEgressAttempt(); last > 0 && now.Sub(time.Unix(0, last)) < cfg.EgressIPInterval {
				continue
			}
			queued++
			if !m.tryEnqueue(job{entry: entry, egress: true, ctx: ctx}) {
				return
			}
			continue
		}
		if last := entry.LastProbeAt(); last > 0 && now.Sub(time.Unix(0, last)) < cfg.Interval {
			continue
		}
		if queued >= maxProbeScanPerTick {
			return
		}
		queued++
		if !m.tryEnqueue(job{entry: entry, ctx: ctx}) {
			return
		}
	}
}

// ProbeAll probes every supported node concurrently, bounded by Concurrency.
// It runs synchronously and is the path used by tests and one-shot callers.
func (m *Manager) ProbeAll(ctx context.Context) {
	cfg := *m.cfg.Load()
	if len(cfg.Targets) == 0 {
		return
	}
	entries := make([]*node.Entry, 0, 256)
	for _, entry := range m.pool.List() {
		if entry == nil || !entry.Options.Supported() {
			continue
		}
		entries = append(entries, entry)
	}
	m.runBatch(ctx, entries, nil)
}

// ProbeIDs probes the given node IDs, bounded by Concurrency. Unknown,
// unsupported or disabled entries are skipped. It returns how many entries
// were actually probed. Manual probes bypass the async queue so the caller
// gets its result without competing with background scans.
func (m *Manager) ProbeIDs(ctx context.Context, ids []string) int {
	return m.ProbeBatch(ctx, ids, nil)
}

// ProbeBatch probes the given node IDs and reports processed/total progress.
// Skipped IDs still advance the progress counter so the caller can render a
// truthful completion bar.
func (m *Manager) ProbeBatch(ctx context.Context, ids []string, progress func(done, total int)) int {
	cfg := *m.cfg.Load()
	total := len(ids)
	entries := make([]*node.Entry, 0, len(ids))
	for _, raw := range ids {
		hash, err := node.ParseHex(raw)
		if err != nil {
			continue
		}
		entry := m.pool.Get(hash)
		if entry == nil || !entry.Options.Supported() || !entry.Enabled() {
			continue
		}
		entries = append(entries, entry)
	}
	skipped := total - len(entries)
	if progress != nil {
		progress(skipped, total)
	}
	if len(cfg.Targets) == 0 {
		if progress != nil {
			progress(total, total)
		}
		return 0
	}
	return m.runBatch(ctx, entries, func(done, _ int) {
		if progress != nil {
			progress(done+skipped, total)
		}
	})
}

// runBatch executes one synchronous batch with the configured concurrency and
// returns how many probes completed.
func (m *Manager) runBatch(ctx context.Context, entries []*node.Entry, progress func(done, total int)) int {
	cfg := *m.cfg.Load()
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	var done atomic.Int64
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			wg.Wait()
			return int(done.Load())
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(entry *node.Entry) {
			defer wg.Done()
			defer func() { <-sem }()
			m.ProbeEntry(ctx, entry)
			current := int(done.Add(1))
			if progress != nil {
				progress(current, len(entries))
			}
		}(entry)
	}
	wg.Wait()
	return int(done.Load())
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

// ProbeEgressIP fetches the public exit address through the node and records
// it together with the probe outcome. The attempt timestamp advances on every
// call so the next due scan waits a full interval.
func (m *Manager) ProbeEgressIP(ctx context.Context, entry *node.Entry) {
	if entry == nil || !entry.Options.Supported() {
		return
	}
	cfg := *m.cfg.Load()
	if !cfg.EgressIPEnabled || cfg.EgressIPTarget == "" {
		return
	}
	now := time.Now()
	client, err := m.outbound.Client(entry)
	if err != nil {
		entry.RecordEgressAttempt("", now)
		entry.RecordProbeResult(false, now)
		entry.SetLastError(err.Error())
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, cfg.EgressIPTarget, nil)
	if err != nil {
		entry.RecordEgressAttempt("", now)
		entry.RecordProbeResult(false, now)
		entry.SetLastError(err.Error())
		return
	}
	req.Header.Set("User-Agent", httpx.UserAgent())
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		entry.RecordEgressAttempt("", now)
		entry.RecordProbeResult(false, now)
		entry.SetLastError(err.Error())
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	ip := parseEgressIP(body)
	entry.RecordEgressAttempt(ip, time.Now())
	entry.RecordProbeResult(true, time.Now())
	if table := entry.Latency(); table != nil {
		if parsed, parseErr := url.Parse(cfg.EgressIPTarget); parseErr == nil {
			table.Update(parsed.Hostname(), latency, 10*time.Minute)
		}
	}
	if ip == "" {
		entry.SetLastError("egress ip missing from trace response")
	} else {
		entry.SetLastError("")
	}
}

// parseEgressIP extracts the ip= line from a cloudflare cdn-cgi/trace body.
func parseEgressIP(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ip=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
		if net.ParseIP(value) != nil {
			return value
		}
	}
	return ""
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
