package node

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestHashIgnoresTag(t *testing.T) {
	a := Options{Type: HTTP, Server: "127.0.0.1", Port: 8080, Username: "u", Password: "p", Tag: "one"}
	b := a
	b.Tag = "two"
	if HashOptions(a) != HashOptions(b) {
		t.Fatalf("hash must ignore tag: %s != %s", HashOptions(a), HashOptions(b))
	}
	c := a
	c.Port = 8081
	if HashOptions(a) == HashOptions(c) {
		t.Fatal("hash must include endpoint")
	}
}

func TestHashIgnoresRawType(t *testing.T) {
	a := Options{Type: HTTP, Server: "h", Port: 80, RawType: "x"}
	b := a
	b.RawType = "y"
	if HashOptions(a) != HashOptions(b) {
		t.Fatal("hash must ignore raw type")
	}
}

func TestProxyURL(t *testing.T) {
	cases := []struct {
		opts   Options
		scheme string
		host   string
	}{
		{Options{Type: HTTP, Server: "h", Port: 8080}, "http", "h:8080"},
		{Options{Type: HTTPS, Server: "h", Port: 443, Username: "u", Password: "p"}, "https", "h:443"},
		{Options{Type: SOCKS5, Server: "h", Port: 1080}, "socks5", "h:1080"},
		{Options{Type: SOCKS5H, Server: "h", Port: 1080}, "socks5h", "h:1080"},
	}
	for _, tc := range cases {
		u, err := tc.opts.ProxyURL()
		if err != nil {
			t.Fatalf("%s: %v", tc.opts.Type, err)
		}
		if u.Scheme != tc.scheme || u.Host != tc.host {
			t.Fatalf("%s: got %s://%s", tc.opts.Type, u.Scheme, u.Host)
		}
	}
	if u, err := (Options{Type: Direct}).ProxyURL(); err != nil || u != nil {
		t.Fatalf("direct must yield nil url, got %v %v", u, err)
	}
	if _, err := (Options{Type: Unsupported, RawType: "ss"}).ProxyURL(); err == nil {
		t.Fatal("unsupported protocol must error")
	}
}

func TestEntryScoresAndCooldown(t *testing.T) {
	now := time.Now()
	e := NewEntry(HashOptions(Options{Type: HTTP, Server: "h", Port: 80}), json.RawMessage(`{}`), Options{Type: HTTP, Server: "h", Port: 80}, now, 4)
	if !e.Enabled() || !e.Options.Supported() {
		t.Fatal("new node must be enabled and supported")
	}
	if score := e.HealthScore(); score <= 0 || score >= 1 {
		t.Fatalf("prior health score out of range: %f", score)
	}
	e.RecordNetworkResult(true)
	if score := e.HealthScore(); score <= 0.5 {
		t.Fatalf("success must raise health score, got %f", score)
	}
	e.RecordProbeResult(false, now)
	if score := e.HealthScore(); score >= 0.9 {
		t.Fatalf("probe failure must lower health score, got %f", score)
	}
	if score := e.UpstreamScore("api.example.com"); score != 0.5 {
		t.Fatalf("unseen target prior = %f", score)
	}
	e.RecordUpstreamResult("api.example.com", true)
	if score := e.UpstreamScore("api.example.com"); score <= 0.5 {
		t.Fatalf("success must raise upstream score, got %f", score)
	}
	e.RecordSuccess()
	e.RecordCooldown(now.Add(time.Minute))
	if !e.Cooling(now) {
		t.Fatal("node must be cooling")
	}
	if e.Cooling(now.Add(2 * time.Minute)) {
		t.Fatal("cooldown must expire")
	}
	e.RecordSuccess()
	if e.Cooling(now) || e.Failures() != 0 {
		t.Fatal("success must clear cooldown")
	}
}

func TestEntrySubscriptions(t *testing.T) {
	e := NewEntry(HashOptions(Options{Type: Direct}), nil, Options{Type: Direct}, time.Now(), 0)
	e.AddSubscriptionID("a")
	e.AddSubscriptionID("a")
	e.AddSubscriptionID("b")
	if got := len(e.SubscriptionIDs()); got != 2 {
		t.Fatalf("subscription refs = %d", got)
	}
	if empty := e.RemoveSubscriptionID("a"); empty {
		t.Fatal("one ref must remain")
	}
	if empty := e.RemoveSubscriptionID("b"); !empty {
		t.Fatal("last ref must report empty")
	}
}

func TestLatencyTableBoundAndEWMA(t *testing.T) {
	table := NewLatencyTable(2)
	table.Update("A.example.", 100*time.Millisecond, time.Minute)
	table.Update("b.example", 200*time.Millisecond, time.Minute)
	if got := table.Size(); got != 2 {
		t.Fatalf("size = %d", got)
	}
	stats, ok := table.Get("a.example")
	if !ok || stats.EWMA != 100*time.Millisecond {
		t.Fatalf("first observation must be raw latency, got %v %v", stats.EWMA, ok)
	}
	table.Update("c.example", 50*time.Millisecond, time.Minute)
	if got := table.Size(); got != 2 {
		t.Fatalf("table must stay bounded, size = %d", got)
	}
	if _, ok := table.Get("b.example"); !ok {
		t.Fatal("most recently updated host must survive eviction")
	}
	if _, ok := table.Get("a.example"); ok {
		t.Fatal("least recently updated host must be evicted")
	}
	hosts := table.SortedHosts()
	if len(hosts) != 2 {
		t.Fatalf("hosts = %v", hosts)
	}
	avg, ok := table.Average()
	if !ok || avg <= 0 {
		t.Fatalf("average = %v %v", avg, ok)
	}
}

func TestSnapshot(t *testing.T) {
	now := time.Now()
	e := NewEntry(HashOptions(Options{Type: SOCKS5, Server: "1.2.3.4", Port: 1080, Tag: "edge"}), nil, Options{Type: SOCKS5, Server: "1.2.3.4", Port: 1080, Tag: "edge"}, now, 4)
	e.Latency().Update("api.example.com", 42*time.Millisecond, time.Minute)
	e.RecordCooldown(now.Add(time.Minute))
	snap := e.Snapshot("https://api.example.com/v1", now)
	if snap.Name != "edge" || snap.Protocol != "socks5" || !snap.Supported {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if !snap.HasLatency || snap.LatencyMS < 41 || snap.LatencyMS > 43 {
		t.Fatalf("latency snapshot: %+v", snap)
	}
	if snap.CooldownUntil == nil {
		t.Fatal("cooldown must be reported")
	}
	u, _ := url.Parse("https://api.example.com/v1")
	if u.Hostname() != "api.example.com" {
		t.Fatal("test fixture broken")
	}
}

func TestExpiredCooldownRestartsStreak(t *testing.T) {
	entry := NewEntry(Hash{2}, nil, Options{Type: HTTP}, time.Now(), 8)
	entry.RecordCooldown(time.Now().Add(-time.Minute))
	entry.RestoreHealth(true, time.Now().Add(-time.Hour).UnixNano(), "", 0)
	time.Sleep(2 * time.Millisecond)
	entry.RecordCooldown(time.Now().Add(time.Minute))
	if since := entry.CooldownSince(); since < time.Now().Add(-time.Second).UnixNano() {
		t.Fatalf("an expired cooldown must restart the streak, since=%d", since)
	}
}

func TestCooldownSinceHealthAndEgressIP(t *testing.T) {
	entry := NewEntry(Hash{1}, nil, Options{Type: HTTP}, time.Now(), 8)
	if entry.EverHealthy() {
		t.Fatal("a new proxy exit must start unroutable")
	}
	entry.RecordCooldown(time.Now().Add(time.Minute))
	since := entry.CooldownSince()
	if since == 0 {
		t.Fatal("cooldown must stamp its start")
	}
	time.Sleep(2 * time.Millisecond)
	entry.RecordCooldown(time.Now().Add(2 * time.Minute))
	if entry.CooldownSince() != since {
		t.Fatal("extending a cooldown must keep the original start")
	}
	entry.RecordProbeResult(true, time.Now())
	if !entry.EverHealthy() {
		t.Fatal("a successful probe must admit the node")
	}
	if entry.LastSuccessAt() == 0 {
		t.Fatal("a successful probe must stamp the last success time")
	}
	entry.RecordSuccess()
	if entry.CooldownSince() != 0 {
		t.Fatal("success must clear the cooldown start")
	}
	entry.RecordEgressAttempt("203.0.113.9", time.Now())
	if entry.EgressIP() != "203.0.113.9" || entry.LastEgressAttempt() == 0 {
		t.Fatal("egress attempt must persist the address and timestamp")
	}
	snap := entry.Snapshot("", time.Now())
	if snap.EgressIP != "203.0.113.9" || !snap.EverHealthy || snap.CooldownSince != nil || snap.LastSuccessAt == nil {
		t.Fatalf("snapshot = %+v", snap)
	}
}
