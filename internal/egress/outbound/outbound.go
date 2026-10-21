// Package outbound builds and caches the HTTP transports used to reach the
// upstream through an egress node. Only Tier A protocols are implemented.
package outbound

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// Config mirrors the performance knobs the gateway exposes so pooled
// exits behave like the legacy transports.
type Config struct {
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
}

// DefaultConfig returns conservative defaults for tests and callers that do
// not have a gateway configuration yet.
func DefaultConfig() Config {
	return Config{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     120 * time.Second,
		ConnectTimeout:      10 * time.Second,
	}
}

// Manager caches one client per node identity.
type Manager struct {
	mu      sync.Mutex
	cfg     Config
	clients map[node.Hash]*http.Client
}

// NewManager creates an empty manager.
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg, clients: make(map[node.Hash]*http.Client)}
}

// Client returns the cached client for an entry, building it on first use.
func (m *Manager) Client(entry *node.Entry) (*http.Client, error) {
	if entry == nil {
		return nil, fmt.Errorf("outbound: nil node")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if client, ok := m.clients[entry.Hash]; ok {
		return client, nil
	}
	client, err := m.build(entry)
	if err != nil {
		return nil, err
	}
	m.clients[entry.Hash] = client
	return client, nil
}

// tracedTransport records passive latency from real traffic. TLS handshake
// duration is preferred; plain HTTP or a reused connection falls back to the
// full round-trip time. A successful round trip also marks the node reachable.
type tracedTransport struct {
	base  http.RoundTripper
	entry *node.Entry
}

func (t *tracedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	var handshakeStart time.Time
	var handshake time.Duration
	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { handshakeStart = time.Now() },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil && !handshakeStart.IsZero() {
				handshake = time.Since(handshakeStart)
			}
		},
	}
	traced := req.Clone(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := t.base.RoundTrip(traced)
	if err != nil {
		return resp, err
	}
	t.entry.MarkHealthy()
	latency := handshake
	if latency <= 0 {
		latency = time.Since(start)
	}
	if host := req.URL.Hostname(); host != "" && latency > 0 {
		if table := t.entry.Latency(); table != nil {
			table.Update(host, latency, 10*time.Minute)
		}
	}
	return resp, nil
}

func (m *Manager) build(entry *node.Entry) (*http.Client, error) {
	opts := entry.Options
	proxyURL, err := opts.ProxyURL()
	if err != nil {
		return nil, fmt.Errorf("outbound: node %s: %w", opts.Tag, err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if m.cfg.MaxIdleConns > 0 {
		transport.MaxIdleConns = m.cfg.MaxIdleConns
	}
	if m.cfg.MaxIdleConnsPerHost > 0 {
		transport.MaxIdleConnsPerHost = m.cfg.MaxIdleConnsPerHost
	}
	transport.MaxConnsPerHost = m.cfg.MaxConnsPerHost
	if m.cfg.IdleConnTimeout > 0 {
		transport.IdleConnTimeout = m.cfg.IdleConnTimeout
	}
	transport.ResponseHeaderTimeout = m.cfg.ResponseHeaderTimeout
	transport.ForceAttemptHTTP2 = true
	transport.DialContext = (&net.Dialer{
		Timeout:   m.cfg.ConnectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.Proxy = nil
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: &tracedTransport{base: transport, entry: entry}}, nil
}

// Remove drops a cached client and closes its idle connections.
func (m *Manager) Remove(hash node.Hash) {
	m.mu.Lock()
	client, ok := m.clients[hash]
	delete(m.clients, hash)
	m.mu.Unlock()
	if ok {
		closeClient(client)
	}
}

// Reconfigure replaces the transport configuration, invalidating every cached
// client so new requests pick up the change.
func (m *Manager) Reconfigure(cfg Config) {
	m.mu.Lock()
	old := m.clients
	m.cfg = cfg
	m.clients = make(map[node.Hash]*http.Client)
	m.mu.Unlock()
	for _, client := range old {
		closeClient(client)
	}
}

// Close releases every cached transport.
func (m *Manager) Close() {
	m.mu.Lock()
	old := m.clients
	m.clients = make(map[node.Hash]*http.Client)
	m.mu.Unlock()
	for _, client := range old {
		closeClient(client)
	}
}

func closeClient(client *http.Client) {
	if client == nil {
		return
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
