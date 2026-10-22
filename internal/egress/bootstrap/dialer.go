// Package bootstrap owns the Mesh-to-node network path. It never consults
// system proxy environment variables: the operator chooses direct dialing or
// an explicit HTTP CONNECT tunnel.
package bootstrap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// ErrUnavailable marks a bootstrap failure. It is an environment signal and
// never attributes to a node.
var ErrUnavailable = errors.New("bootstrap unavailable")

// ErrUnsupportedProtocol marks a node protocol that cannot traverse the
// configured bootstrap tunnel.
var ErrUnsupportedProtocol = errors.New("bootstrap does not support this node protocol")

// Dialer opens TCP connections through the configured bootstrap path.
type Dialer struct {
	url     *url.URL
	timeout time.Duration
	net     net.Dialer
	latency atomic.Int64
}

// NewDialer creates a tunnel dialer. A nil URL means direct dialing.
func NewDialer(rawURL string, timeout time.Duration) (*Dialer, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &Dialer{timeout: timeout}
	if strings.TrimSpace(rawURL) == "" {
		return dialer, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("bootstrap: url must be an http(s) URL")
	}
	dialer.url = parsed
	return dialer, nil
}

// Tunnel reports whether an HTTP CONNECT tunnel is configured.
func (d *Dialer) Tunnel() bool { return d != nil && d.url != nil }

// LastLatency returns the most recent tunnel setup latency.
func (d *Dialer) LastLatency() time.Duration {
	if d == nil {
		return 0
	}
	return time.Duration(d.latency.Load())
}

// DialContext connects to addr, tunnelling through the bootstrap proxy when
// configured. The bootstrap hop itself is always dialed directly.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d == nil || d.url == nil {
		return d.net.DialContext(ctx, network, addr)
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("bootstrap: unsupported network %s", network)
	}
	start := time.Now()
	host := d.url.Host
	if d.url.Port() == "" {
		port := "80"
		if d.url.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(d.url.Hostname(), port)
	}
	conn, err := d.net.DialContext(ctx, "tcp", bootstrapAddress(host))
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %v", ErrUnavailable, d.url.Host, err)
	}
	deadline := time.Now().Add(d.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: keep-alive\r\n\r\n", addr, addr)
	if _, err := conn.Write([]byte(request)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: write CONNECT: %v", ErrUnavailable, err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: read CONNECT response: %v", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("%w: CONNECT rejected with %d", ErrUnavailable, resp.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	d.latency.Store(time.Since(start).Nanoseconds())
	return &tunnelConn{Conn: conn, reader: reader}, nil
}

func bootstrapAddress(hostport string) string {
	if _, _, err := net.SplitHostPort(hostport); err == nil {
		return hostport
	}
	return net.JoinHostPort(hostport, "80")
}

type tunnelConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *tunnelConn) Read(b []byte) (int, error) {
	if c.reader != nil && c.reader.Buffered() > 0 {
		return c.reader.Read(b)
	}
	return c.Conn.Read(b)
}

// Probe fetches probeURL through the bootstrap path, never through a node.
func (d *Dialer) Probe(ctx context.Context, probeURL string) (time.Duration, error) {
	if d == nil || !d.Tunnel() {
		return 0, errors.New("bootstrap: probe requires http_connect mode")
	}
	target, err := url.Parse(probeURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return 0, errors.New("bootstrap: probe_url must be an http(s) URL")
	}
	transport := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   d.timeout,
		ResponseHeaderTimeout: d.timeout,
		DisableKeepAlives:     true,
	}
	client := &http.Client{Transport: transport, Timeout: d.timeout}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("%w: probe: %v", ErrUnavailable, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("%w: probe status %d", ErrUnavailable, resp.StatusCode)
	}
	return time.Since(start), nil
}
