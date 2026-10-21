package outbound

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

func TestTracedTransportRecordsTLSLatency(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	opts := node.Options{Type: node.Direct, Tag: "direct"}
	raw, _ := json.Marshal(opts)
	entry := node.NewEntry(node.HashOptions(opts), raw, opts, time.Now(), 8)
	client := &http.Client{Transport: &tracedTransport{base: server.Client().Transport, entry: entry}}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	parsed, _ := url.Parse(server.URL)
	latency, ok := entry.LatencyFor(parsed.Hostname())
	if !ok || latency <= 0 {
		t.Fatalf("latency = %s ok=%v", latency, ok)
	}
	if !entry.EverHealthy() {
		t.Fatal("successful traffic must admit the node")
	}
}
