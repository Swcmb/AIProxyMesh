// Package subscription fetches, parses and reconciles proxy subscriptions into
// the global node pool. A subscription may also carry inline content, which is
// how legacy `proxies` / `proxyfile` entries enter the same pool.
package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/parser"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

// ErrUnknownSource reports a refresh for a subscription that is no longer
// configured.
var ErrUnknownSource = errors.New("subscription: unknown source")

// Source is one configured subscription.
type Source struct {
	ID      string
	Name    string
	URL     string
	Content string
	Enabled bool
	Refresh time.Duration
}

// Status is the operator-facing state of one subscription.
type Status struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url,omitempty"`
	Enabled         bool       `json:"enabled"`
	Inline          bool       `json:"inline,omitempty"`
	Format          string     `json:"format,omitempty"`
	Nodes           int        `json:"nodes"`
	Supported       int        `json:"supported"`
	Unsupported     int        `json:"unsupported"`
	Added           int        `json:"added"`
	Removed         int        `json:"removed"`
	LastError       string     `json:"last_error,omitempty"`
	LastFetchAt     *time.Time `json:"last_fetch_at,omitempty"`
	NextFetchAt     *time.Time `json:"next_fetch_at,omitempty"`
	IntervalSeconds int        `json:"refresh_seconds"`
}

// SourceProvider returns the current subscription sources. It is called on
// every refresh so configuration hot reload takes effect without a restart.
type SourceProvider func() []Source

// Manager owns subscription state and the refresh schedule.
type Manager struct {
	pool     *pool.Pool
	client   *http.Client
	provider SourceProvider
	logger   *slog.Logger

	mu       sync.Mutex
	statuses map[string]*Status
	lastSeen map[string]time.Time
	refresh  sync.Mutex
	now      func() time.Time
}

// New creates a subscription manager.
func New(p *pool.Pool, client *http.Client, provider SourceProvider, logger *slog.Logger) *Manager {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Manager{
		pool:     p,
		client:   client,
		provider: provider,
		logger:   logger,
		statuses: make(map[string]*Status),
		lastSeen: make(map[string]time.Time),
		now:      time.Now,
	}
}

// SetClock replaces the clock (tests).
func (m *Manager) SetClock(now func() time.Time) {
	m.mu.Lock()
	m.now = now
	m.mu.Unlock()
}

// Start refreshes everything once, then reconciles and refreshes due sources
// on a fixed scan interval until ctx ends.
func (m *Manager) Start(ctx context.Context) {
	m.RefreshAll(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Reconcile()
			m.RefreshDue(ctx)
		}
	}
}

// RefreshAll refreshes every configured source, ignoring due times.
func (m *Manager) RefreshAll(ctx context.Context) {
	for _, source := range m.sources() {
		if err := m.Refresh(ctx, source.ID); err != nil && ctx.Err() == nil {
			m.logf(source.ID, err)
		}
	}
}

// RefreshDue refreshes enabled sources whose interval has elapsed.
func (m *Manager) RefreshDue(ctx context.Context) {
	now := m.clock()
	for _, source := range m.sources() {
		if !source.Enabled || source.Refresh <= 0 {
			continue
		}
		last := m.lastFetch(source.ID)
		if last.IsZero() || now.Sub(last) >= source.Refresh {
			if err := m.Refresh(ctx, source.ID); err != nil && ctx.Err() == nil {
				m.logf(source.ID, err)
			}
		}
	}
}

// Reconcile removes subscriptions that disappeared from the configuration.
func (m *Manager) Reconcile() {
	known := make(map[string]struct{})
	for _, source := range m.sources() {
		known[source.ID] = struct{}{}
	}
	m.mu.Lock()
	var removed []string
	for id := range m.statuses {
		if _, ok := known[id]; !ok {
			removed = append(removed, id)
		}
	}
	m.mu.Unlock()
	for _, id := range removed {
		m.pool.RemoveSubscription(id)
		m.mu.Lock()
		delete(m.statuses, id)
		delete(m.lastSeen, id)
		m.mu.Unlock()
	}
}

// Refresh fetches and reconciles one source. A failed fetch or parse keeps the
// previous node set intact.
func (m *Manager) Refresh(ctx context.Context, id string) error {
	m.refresh.Lock()
	defer m.refresh.Unlock()

	source, found := m.find(id)
	if !found {
		m.pool.RemoveSubscription(id)
		m.mu.Lock()
		delete(m.statuses, id)
		delete(m.lastSeen, id)
		m.mu.Unlock()
		return ErrUnknownSource
	}

	if !source.Enabled {
		m.pool.SetSubscriptionEnabled(id, false)
		m.mu.Lock()
		status := m.statusFor(source)
		status.Enabled = false
		m.mu.Unlock()
		return nil
	}

	data, err := m.load(ctx, source)
	if err != nil {
		m.fail(source, fmt.Errorf("fetch: %w", err))
		return err
	}
	result, err := parser.Parse(data)
	if err != nil {
		m.fail(source, fmt.Errorf("parse: %w", err))
		return err
	}
	inputs := make([]pool.Input, 0, len(result.Nodes))
	for _, entry := range result.Nodes {
		inputs = append(inputs, pool.Input{Options: entry.Options, Raw: entry.Raw})
	}
	diff := m.pool.ReplaceSubscription(id, inputs)
	m.pool.SetSubscriptionEnabled(id, true)
	m.succeed(source, result.Stats, diff)
	return nil
}

// Statuses returns subscription state sorted by ID.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.statuses))
	for _, status := range m.statuses {
		out = append(out, *status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Status returns one subscription's state.
func (m *Manager) Status(id string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[id]
	if !ok {
		return Status{}, false
	}
	return *status, true
}

func (m *Manager) load(ctx context.Context, source Source) ([]byte, error) {
	if source.Content != "" {
		return []byte(source.Content), nil
	}
	if source.URL == "" {
		return nil, errors.New("subscription has neither url nor content")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mesh/1.0")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}

func (m *Manager) fail(source Source, err error) {
	now := m.clock()
	m.mu.Lock()
	status := m.statusFor(source)
	status.LastError = err.Error()
	m.lastSeen[source.ID] = now
	m.mu.Unlock()
}

func (m *Manager) succeed(source Source, stats parser.Stats, diff pool.Diff) {
	now := m.clock()
	m.mu.Lock()
	status := m.statusFor(source)
	status.Enabled = true
	status.Format = stats.Format
	status.Nodes = diff.Total
	status.Supported = diff.Total - diff.Unsupported
	status.Unsupported = diff.Unsupported
	status.Added = diff.Added
	status.Removed = diff.Removed
	status.LastError = ""
	last := now.UTC()
	status.LastFetchAt = &last
	if source.Refresh > 0 {
		next := now.Add(source.Refresh).UTC()
		status.NextFetchAt = &next
	} else {
		status.NextFetchAt = nil
	}
	m.lastSeen[source.ID] = now
	m.mu.Unlock()
}

// maskURL keeps only the origin. Feed URLs routinely carry access tokens in
// the path or query, and statuses are shown to every authenticated operator;
// the management API serves the full URL only for a single named subscription.
func maskURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "***"
	}
	return parsed.Scheme + "://" + parsed.Host + "/***"
}

func (m *Manager) statusFor(source Source) *Status {
	status, ok := m.statuses[source.ID]
	if !ok {
		status = &Status{ID: source.ID}
		m.statuses[source.ID] = status
	}
	status.Name = source.Name
	status.URL = maskURL(source.URL)
	status.Enabled = source.Enabled
	status.Inline = source.Content != ""
	status.IntervalSeconds = int(source.Refresh / time.Second)
	return status
}

func (m *Manager) lastFetch(id string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastSeen[id]
}

func (m *Manager) sources() []Source {
	if m.provider == nil {
		return nil
	}
	return m.provider()
}

func (m *Manager) find(id string) (Source, bool) {
	for _, source := range m.sources() {
		if source.ID == id {
			return source, true
		}
	}
	return Source{}, false
}

func (m *Manager) clock() time.Time {
	m.mu.Lock()
	now := m.now
	m.mu.Unlock()
	return now()
}

func (m *Manager) logf(id string, err error) {
	if m.logger == nil {
		return
	}
	m.logger.Warn("subscription refresh failed", "component", "egress", "event", "subscription_refresh_failed", "subscription", id, "error", err)
}
