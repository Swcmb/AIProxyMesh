package transport

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/bootstrap"
)

func TestDirectIgnoresEnvironmentProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	factory, err := New(Config{Mode: ModeDirect, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := factory.NewTransport(nil, "direct")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("direct request must ignore environment proxies: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestHTTPConnectTunnelsThroughBootstrap(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tunnelled"))
	}))
	defer target.Close()

	proxyAddr, hits := newConnectProxy(t)
	factory, err := New(Config{
		Mode:           ModeHTTPConnect,
		BootstrapURL:   "http://" + proxyAddr,
		ConnectTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := factory.NewTransport(nil, "direct")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through bootstrap failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "tunnelled" {
		t.Fatalf("body = %q", body)
	}
	if hits.Load() != 1 {
		t.Fatalf("bootstrap CONNECT count = %d", hits.Load())
	}
}

func TestHTTPConnectRejectsSOCKS5(t *testing.T) {
	factory, err := New(Config{Mode: ModeHTTPConnect, BootstrapURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.NewTransport(nil, "socks5"); !errors.Is(err, bootstrap.ErrUnsupportedProtocol) {
		t.Fatalf("socks5 over http_connect must be rejected, got %v", err)
	}
}

func TestBootstrapFailureIsIsolated(t *testing.T) {
	factory, err := New(Config{Mode: ModeHTTPConnect, BootstrapURL: "http://127.0.0.1:1", ConnectTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = factory.DialContext(context.Background(), "tcp", "127.0.0.1:9")
	if !errors.Is(err, bootstrap.ErrUnavailable) {
		t.Fatalf("bootstrap failure must wrap ErrUnavailable, got %v", err)
	}
}

func TestValidateBootstrapConfig(t *testing.T) {
	if _, err := New(Config{Mode: ModeHTTPConnect}); err == nil {
		t.Fatal("http_connect without url must fail")
	}
	if _, err := New(Config{Mode: "auto"}); err == nil {
		t.Fatal("unsupported mode must fail")
	}
}

// newConnectProxy serves HTTP CONNECT and pipes the tunnel to the requested
// destination. It is deliberately minimal: tests only need one hop.
func newConnectProxy(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var hits atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				req, err := http.ReadRequest(reader)
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				upstream, err := net.Dial("tcp", req.Host)
				if err != nil {
					_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					return
				}
				defer upstream.Close()
				hits.Add(1)
				_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				go func() { _, _ = io.Copy(upstream, reader) }()
				_, _ = io.Copy(conn, upstream)
			}(conn)
		}
	}()
	return listener.Addr().String(), &hits
}

func TestBootstrapURLValidation(t *testing.T) {
	if _, err := bootstrap.NewDialer("ftp://x.test", time.Second); err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("expected http(s) validation, got %v", err)
	}
}
