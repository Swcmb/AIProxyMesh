// Package transport is the only factory allowed to build HTTP transports for
// Mesh-to-node traffic. It makes the Mesh-to-node link explicit and keeps
// system proxy environment variables out of egress evidence.
package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/bootstrap"
)

// Mode selects how Mesh reaches node endpoints.
type Mode string

const (
	// ModeDirect dials node endpoints directly, ignoring HTTP_PROXY and friends.
	ModeDirect Mode = "direct"
	// ModeHTTPConnect tunnels node traffic through an explicit HTTP CONNECT proxy.
	ModeHTTPConnect Mode = "http_connect"
)

// Config controls transport construction.
type Config struct {
	Mode                  Mode
	BootstrapURL          string
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	ExpectContinueTimeout time.Duration
}

// DefaultConfig returns conservative direct-mode defaults.
func DefaultConfig() Config {
	return Config{
		Mode:                  ModeDirect,
		ConnectTimeout:        10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       120 * time.Second,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		ExpectContinueTimeout: time.Second,
	}
}

// Factory builds node-link transports and dialers.
type Factory struct {
	cfg    Config
	dialer *bootstrap.Dialer
}

// New validates the configuration and creates the factory.
func New(cfg Config) (*Factory, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModeDirect
	}
	if cfg.Mode != ModeDirect && cfg.Mode != ModeHTTPConnect {
		return nil, fmt.Errorf("transport: unsupported bootstrap mode %q", cfg.Mode)
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = DefaultConfig().ConnectTimeout
	}
	if cfg.TLSHandshakeTimeout <= 0 {
		cfg.TLSHandshakeTimeout = DefaultConfig().TLSHandshakeTimeout
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = DefaultConfig().IdleConnTimeout
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = DefaultConfig().MaxIdleConns
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		cfg.MaxIdleConnsPerHost = DefaultConfig().MaxIdleConnsPerHost
	}
	if cfg.Mode == ModeHTTPConnect && strings.TrimSpace(cfg.BootstrapURL) == "" {
		return nil, fmt.Errorf("transport: bootstrap.url is required in http_connect mode")
	}
	bootstrapURL := ""
	if cfg.Mode == ModeHTTPConnect {
		bootstrapURL = cfg.BootstrapURL
	}
	dialer, err := bootstrap.NewDialer(bootstrapURL, cfg.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	return &Factory{cfg: cfg, dialer: dialer}, nil
}

// NewDirect returns a direct-mode factory used by tests and callers without a
// bootstrap configuration.
func NewDirect() *Factory {
	factory, _ := New(DefaultConfig())
	return factory
}

// Mode returns the active bootstrap mode.
func (f *Factory) Mode() Mode {
	if f == nil {
		return ModeDirect
	}
	return f.cfg.Mode
}

// Dialer exposes the bootstrap dialer for probes and diagnostics.
func (f *Factory) Dialer() *bootstrap.Dialer {
	if f == nil {
		return nil
	}
	return f.dialer
}

// DialContext dials a node endpoint through the configured bootstrap path.
func (f *Factory) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if f == nil {
		dialer := &net.Dialer{Timeout: DefaultConfig().ConnectTimeout, KeepAlive: 30 * time.Second}
		return dialer.DialContext(ctx, network, addr)
	}
	return f.dialer.DialContext(ctx, network, addr)
}

// NewTransport builds a transport for one node. proxyURL is the node's own
// proxy endpoint (nil for a direct exit). SOCKS5 nodes cannot traverse an
// HTTP CONNECT bootstrap and are rejected in that mode.
func (f *Factory) NewTransport(proxyURL *url.URL, protocol string) (*http.Transport, error) {
	if f == nil {
		f = NewDirect()
	}
	if f.cfg.Mode == ModeHTTPConnect && (protocol == "socks5" || protocol == "socks5h") {
		return nil, fmt.Errorf("%w: %s", bootstrap.ErrUnsupportedProtocol, protocol)
	}
	transport := &http.Transport{
		DialContext:           f.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          f.cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   f.cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       f.cfg.MaxConnsPerHost,
		IdleConnTimeout:       f.cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   f.cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: f.cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: f.cfg.ExpectContinueTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	// Proxy is always explicit. ProxyFromEnvironment is forbidden here: the
	// client's system proxy must not shape node evidence.
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return transport, nil
}
