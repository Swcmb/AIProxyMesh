package pool

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

func input(opts node.Options) Input {
	raw, _ := json.Marshal(opts)
	return Input{Options: opts, Raw: raw}
}

func httpNode(server string, port uint16, tag string) node.Options {
	return node.Options{Type: node.HTTP, Server: server, Port: port, Tag: tag}
}

func TestDedupSharesHealthState(t *testing.T) {
	p := New(8)
	a, created := p.Add("sub-a", input(httpNode("1.1.1.1", 8080, "one")))
	if !created {
		t.Fatal("first add must create")
	}
	b, created := p.Add("sub-b", input(httpNode("1.1.1.1", 8080, "two")))
	if created {
		t.Fatal("same exit must reuse the entry")
	}
	if a != b {
		t.Fatal("deduplicated nodes must share state")
	}
	a.RecordNetworkResult(false)
	if b.HealthScore() >= 0.5 {
		t.Fatal("health state must be shared")
	}
	if p.Len() != 1 {
		t.Fatalf("pool size = %d", p.Len())
	}
}

func TestReplaceSubscriptionKeepsSharedNodes(t *testing.T) {
	p := New(8)
	shared := httpNode("1.1.1.1", 8080, "shared")
	p.Add("sub-a", input(shared))
	p.Add("sub-b", input(shared))
	p.Add("sub-a", input(httpNode("2.2.2.2", 8081, "only-a")))

	diff := p.ReplaceSubscription("sub-a", []Input{input(shared)})
	if diff.Added != 0 || diff.Removed != 1 || diff.Total != 1 {
		t.Fatalf("diff = %+v", diff)
	}
	if p.Len() != 1 {
		t.Fatalf("shared node must survive, size = %d", p.Len())
	}
	diff = p.ReplaceSubscription("sub-b", nil)
	if diff.Removed != 0 || p.Len() != 1 {
		t.Fatalf("shared node must survive the second subscription: %+v size=%d", diff, p.Len())
	}
	if removed := p.RemoveSubscription("sub-a"); removed != 1 || p.Len() != 0 {
		t.Fatalf("last reference removal must delete node: removed=%d size=%d", removed, p.Len())
	}
}

func TestSubscriptionEnabledGate(t *testing.T) {
	p := New(8)
	entry, _ := p.Add("sub-a", input(httpNode("1.1.1.1", 8080, "one")))
	if !p.Active(entry) {
		t.Fatal("unknown subscription defaults to enabled")
	}
	p.SetSubscriptionEnabled("sub-a", false)
	if p.Active(entry) {
		t.Fatal("disabled subscription must gate eligibility")
	}
	p.Add("sub-b", input(httpNode("1.1.1.1", 8080, "two")))
	if !p.Active(entry) {
		t.Fatal("a second enabled reference must restore eligibility")
	}
}

func TestManualSwitch(t *testing.T) {
	p := New(8)
	entry, _ := p.Add("sub-a", input(httpNode("1.1.1.1", 8080, "one")))
	if err := p.SetEnabled(entry.Hash, false); err != nil {
		t.Fatal(err)
	}
	if entry.Enabled() {
		t.Fatal("manual switch must apply")
	}
	if err := p.SetEnabled(node.HashOptions(httpNode("9.9.9.9", 1, "")), false); err == nil {
		t.Fatal("missing node must error")
	}
}

func TestRestore(t *testing.T) {
	p := New(8)
	opts := httpNode("1.1.1.1", 8080, "restored")
	h := node.HashOptions(opts)
	raw, _ := json.Marshal(opts)
	entry := node.NewEntry(h, raw, opts, time.Now(), 8)
	entry.RestoreCooldown(2, time.Now().Add(time.Minute).UnixNano())
	p.Restore(entry, []string{"sub-a"}, true)
	if p.Len() != 1 || p.Get(h) != entry || !entry.Cooling(time.Now()) {
		t.Fatal("restore must reinsert state")
	}
	if got := entry.SubscriptionIDs(); len(got) != 1 || got[0] != "sub-a" {
		t.Fatalf("subscriptions = %v", got)
	}
}

func TestUnsupportedVisibleButNotSupported(t *testing.T) {
	p := New(8)
	entry, _ := p.Add("sub-a", input(node.Options{Type: "ss", RawType: "ss", Server: "1.1.1.1", Port: 8388, Tag: "ss"}))
	if entry.Options.Supported() {
		t.Fatal("ss must be unsupported")
	}
	if p.Len() != 1 {
		t.Fatal("unsupported node must remain visible in the pool")
	}
}
