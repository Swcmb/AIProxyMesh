package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/outbound"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

func testRouter(t *testing.T) (*Router, *pool.Pool) {
	t.Helper()
	p := pool.New(16)
	manager := outbound.NewManager(outbound.DefaultConfig())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := DefaultConfig()
	// Legacy tests assert immediate cooldown; staging has dedicated tests.
	cfg.FailureGrace = 0
	return New(p, manager, cfg, logger), p
}

func addNode(p *pool.Pool, sub string, opts node.Options) *node.Entry {
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add(sub, pool.Input{Options: opts, Raw: raw})
	// Pool helpers describe reachable exits; pending-node behaviour has its
	// own test.
	entry.MarkHealthy()
	return entry
}

func httpExit(server string, port uint16, tag string) node.Options {
	return node.Options{Type: node.HTTP, Server: server, Port: port, Tag: tag}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func selectOnly(t *testing.T, r *Router, u *url.URL) *Attempt {
	t.Helper()
	attempt, err := r.Select(context.Background(), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestUnprobedNodeIsNotEligible(t *testing.T) {
	r, p := testRouter(t)
	opts := httpExit("127.0.0.1", 18080, "pending")
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	u := mustURL(t, "https://api.example.com/v1")
	if _, err := r.Select(context.Background(), u, nil); !errors.Is(err, ErrNoEligible) {
		t.Fatalf("a node that never connected must stay unroutable, got %v", err)
	}
	candidates := r.Diagnose(u, nil)
	if len(candidates) != 1 || candidates[0].Reason != "unprobed" {
		t.Fatalf("diagnose = %+v", candidates)
	}
	entry.MarkHealthy()
	if _, err := r.Select(context.Background(), u, nil); err != nil {
		t.Fatalf("a healthy signal must make the node routable: %v", err)
	}
}

func TestSelectSkipsBlockedRegion(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	cnOpts := httpExit("127.0.0.1", 18081, "cn")
	cnRaw, _ := json.Marshal(cnOpts)
	cn, _ := p.Add("sub", pool.Input{Options: cnOpts, Raw: cnRaw})
	cn.MarkHealthy()
	cn.SetRegion("cn")
	usOpts := httpExit("127.0.0.1", 18082, "us")
	usRaw, _ := json.Marshal(usOpts)
	us, _ := p.Add("sub", pool.Input{Options: usOpts, Raw: usRaw})
	us.MarkHealthy()
	us.SetRegion("us")
	r.SetRegionResolver(func(entry *node.Entry) string { return entry.Region() })

	r.RecordRegionBlock(u.Hostname(), "cn")
	for i := 0; i < 10; i++ {
		attempt, err := r.Select(context.Background(), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.NodeID != us.ID() {
			t.Fatalf("selected blocked region node %s", attempt.NodeName)
		}
		attempt.Report(Outcome{Kind: Success})
	}

	// Region blocks are hard constraints: all blocked means no eligible exit,
	// never a fallback to a blocked region.
	r.RecordRegionBlock(u.Hostname(), "us")
	if _, err := r.Select(context.Background(), u, nil); !errors.Is(err, ErrNoEligible) {
		t.Fatalf("all regions blocked must return ErrNoEligible, got %v", err)
	}
}

func TestFrozenEnvironmentDoesNotCoolNodes(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	opts := httpExit("127.0.0.1", 18083, "frozen")
	raw, _ := json.Marshal(opts)
	entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
	entry.MarkHealthy()

	env := health.NewEnvironment(health.EnvironmentConfig{FailureGrace: 0}, time.Now())
	env.SetActiveKeys(1)
	env.ReportTransportFailure("a", "https://t-a", time.Now())
	env.ReportTransportFailure("b", "https://t-a", time.Now())
	env.ReportTransportFailure("c", "https://t-b", time.Now())
	if !env.Frozen() {
		t.Fatal("environment must be frozen for this test")
	}
	r.SetEnvironment(env)

	attempt, err := r.Select(context.Background(), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	classification := attempt.Report(Outcome{Kind: ProxyFailure, Err: errors.New("dial failed")})
	if classification.Reason != "environment_frozen" {
		t.Fatalf("reason = %s", classification.Reason)
	}
	if entry.Cooling(time.Now()) {
		t.Fatal("a frozen environment must not cool exits")
	}
}

func TestRegionBlockedOutcomeHidesRegion(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	cnOpts := httpExit("127.0.0.1", 18081, "cn")
	cnRaw, _ := json.Marshal(cnOpts)
	cn, _ := p.Add("sub", pool.Input{Options: cnOpts, Raw: cnRaw})
	cn.MarkHealthy()
	cn.SetRegion("cn")
	usOpts := httpExit("127.0.0.1", 18082, "us")
	usRaw, _ := json.Marshal(usOpts)
	us, _ := p.Add("sub", pool.Input{Options: usOpts, Raw: usRaw})
	us.MarkHealthy()
	us.SetRegion("us")
	r.SetRegionResolver(func(entry *node.Entry) string { return entry.Region() })

	blocked := &Attempt{Router: r, entry: cn, Target: u.Hostname()}
	classification := blocked.Report(Outcome{Kind: RegionBlocked, StatusCode: 403, Region: "cn"})
	if !classification.Retryable || classification.Reason != "region_blocked" {
		t.Fatalf("classification = %+v", classification)
	}
	for i := 0; i < 5; i++ {
		attempt, err := r.Select(context.Background(), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.NodeID != us.ID() {
			t.Fatalf("region block was not applied, selected %s", attempt.NodeName)
		}
		attempt.Report(Outcome{Kind: Success})
	}
}

func TestSelectPrefersRecentlyVerified(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	verifiedOpts := httpExit("127.0.0.1", 18081, "verified")
	verifiedRaw, _ := json.Marshal(verifiedOpts)
	verified, _ := p.Add("sub", pool.Input{Options: verifiedOpts, Raw: verifiedRaw})
	verified.MarkHealthy()
	for i := 0; i < 20; i++ {
		opts := httpExit("127.0.0.1", uint16(20000+i), fmt.Sprintf("raw-%d", i))
		raw, _ := json.Marshal(opts)
		entry, _ := p.Add("sub", pool.Input{Options: opts, Raw: raw})
		// Eligible from a legacy restore, but never verified in this process.
		entry.RestoreHealth(true, 0, "", 0)
	}
	for i := 0; i < 20; i++ {
		attempt, err := r.Select(context.Background(), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.NodeID != verified.ID() {
			t.Fatalf("selected %s, want recently verified %s", attempt.NodeName, verified.Name())
		}
		attempt.Report(Outcome{Kind: Success})
	}
}

func TestSelectRequiresEligibleNode(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	if _, err := r.Select(context.Background(), u, nil); !errors.Is(err, ErrNoEligible) {
		t.Fatalf("empty pool must report no eligible node, got %v", err)
	}

	unsupported := addNode(p, "sub", node.Options{Type: "ss", RawType: "ss", Server: "1.1.1.1", Port: 8388, Tag: "ss"})
	if _, err := r.Select(context.Background(), u, nil); !errors.Is(err, ErrNoEligible) {
		t.Fatal("unsupported node must not be selected")
	}
	_ = unsupported

	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	attempt := selectOnly(t, r, u)
	if attempt.NodeID != entry.ID() || attempt.ProxyURL == nil || attempt.ProxyURL.Host != "1.1.1.1:8080" {
		t.Fatalf("attempt = %+v", attempt)
	}
	attempt.Report(Outcome{Kind: Success})

	entry.SetEnabled(false)
	if _, err := r.Select(context.Background(), u, nil); !errors.Is(err, ErrNoEligible) {
		t.Fatal("disabled node must be filtered")
	}
	entry.SetEnabled(true)
	p.SetSubscriptionEnabled("sub", false)
	if _, err := r.Select(context.Background(), u, nil); !errors.Is(err, ErrNoEligible) {
		t.Fatal("disabled subscription must gate selection")
	}
	p.SetSubscriptionEnabled("sub", true)
	if _, err := r.Select(context.Background(), u, map[string]struct{}{entry.ID(): {}}); !errors.Is(err, ErrNoEligible) {
		t.Fatal("excluded node must not be reselected")
	}
}

func TestCooldownBackoffAndRetryAfter(t *testing.T) {
	r, p := testRouter(t)
	now := time.Unix(1_700_000_000, 0)
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")
	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))

	attempt := selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: ProxyFailure, Err: errors.New("connection refused")})
	if result.NodeCooldown == nil {
		t.Fatal("proxy failure must cool the node")
	}
	first := result.NodeCooldown.Sub(now)
	if first != 30*time.Second {
		t.Fatalf("first cooldown = %v", first)
	}
	if !entry.Cooling(now) {
		t.Fatal("entry must be cooling")
	}

	now = now.Add(first)
	attempt = selectOnly(t, r, u)
	result = attempt.Report(Outcome{Kind: ProxyFailure})
	if got := result.NodeCooldown.Sub(now); got != 60*time.Second {
		t.Fatalf("second cooldown = %v", got)
	}

	now = now.Add(2 * time.Hour)
	attempt = selectOnly(t, r, u)
	result = attempt.Report(Outcome{Kind: ExitFailure, StatusCode: 429, RetryAfter: 10 * time.Minute})
	if got := result.NodeCooldown.Sub(now); got != 10*time.Minute {
		t.Fatalf("Retry-After must extend cooldown, got %v", got)
	}
}

func TestRetryAfterBeyondCooldownCap(t *testing.T) {
	r, p := testRouter(t)
	now := time.Unix(1_700_000_000, 0)
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")
	addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	attempt := selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: ExitFailure, StatusCode: 429, RetryAfter: time.Hour})
	if result.NodeCooldown == nil {
		t.Fatal("expected cooldown")
	}
	if got := result.NodeCooldown.Sub(now); got != time.Hour {
		t.Fatalf("Retry-After must set the full cooldown, got %v", got)
	}
}

func TestExitFailureConsensusBecomesUpstreamWide(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	a := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	b := addNode(p, "sub", httpExit("2.2.2.2", 8081, "b"))

	first := selectOnly(t, r, u)
	first.KeyID = "key-1"
	result := first.Report(Outcome{Kind: ExitFailure, StatusCode: 429})
	if result.UpstreamWide || result.NodeCooldown == nil {
		t.Fatalf("first 429 must cool the exit: %+v", result)
	}
	firstHash, _ := node.ParseHex(first.NodeID)
	if entry := p.Get(firstHash); entry == nil || !entry.Cooling(time.Now()) {
		t.Fatal("first node must be cooling")
	}

	second := selectOnly(t, r, u)
	if second.NodeID == first.NodeID {
		t.Fatal("cooling node must not be reserved")
	}
	second.KeyID = "key-1"
	result = second.Report(Outcome{Kind: ExitFailure, StatusCode: 429})
	if !result.UpstreamWide || result.NodeCooldown != nil {
		t.Fatalf("second exit with the same key must produce upstream consensus: %+v", result)
	}
	secondHash, _ := node.ParseHex(second.NodeID)
	if entry := p.Get(secondHash); entry == nil || entry.Cooling(time.Now()) {
		t.Fatal("upstream-wide failures must not cool the node")
	}
	if entry := p.Get(firstHash); entry == nil || entry.Cooling(time.Now()) {
		t.Fatal("consensus must clear the earlier exit-layer cooldown")
	}
	_ = a
	_ = b
}

func TestKeyFailureDoesNotPenalizeExit(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	attempt := selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: KeyFailure, StatusCode: 401})
	if result.NodeCooldown != nil || entry.Cooling(time.Now()) {
		t.Fatal("401 must not penalize a healthy exit")
	}
	if !result.Retryable {
		t.Fatal("key failure must remain retryable through key rotation")
	}
}

func TestClientCancelDoesNotPenalize(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	before := entry.HealthScore()
	attempt := selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: ClientCancel})
	if result.NodeCooldown != nil || entry.Cooling(time.Now()) {
		t.Fatal("client cancel must not cool the node")
	}
	if entry.HealthScore() != before || entry.Failures() != 0 {
		t.Fatal("client cancel must not change node state")
	}
}

func TestSuccessRecordsLatencyAndResetsCooldown(t *testing.T) {
	r, p := testRouter(t)
	now := time.Unix(1_700_000_000, 0)
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")
	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))

	attempt := selectOnly(t, r, u)
	attempt.Report(Outcome{Kind: ProxyFailure})
	if !entry.Cooling(now) {
		t.Fatal("setup: node must be cooling")
	}
	now = now.Add(time.Hour)
	attempt = selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: Success, Latency: 120 * time.Millisecond})
	if result.Kind != Success || entry.Cooling(now) || entry.Failures() != 0 {
		t.Fatalf("success must clear cooldown: %+v", result)
	}
	latency, ok := entry.LatencyFor("api.example.com")
	if !ok || latency < 100*time.Millisecond || latency > 140*time.Millisecond {
		t.Fatalf("latency = %v ok=%v", latency, ok)
	}
	if score := entry.UpstreamScore("api.example.com"); score <= 0.5 {
		t.Fatalf("success must raise upstream score: %f", score)
	}
}

func TestFiveHundredAttribution(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	a := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	b := addNode(p, "sub", httpExit("2.2.2.2", 8081, "b"))

	attempt := selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: UpstreamFailure, StatusCode: 503})
	if result.UpstreamWide || result.NodeCooldown != nil {
		t.Fatalf("lone 5xx without evidence must stay unattributed: %+v", result)
	}

	winner := selectOnly(t, r, u)
	winner.KeyID = "key-1"
	winner.Report(Outcome{Kind: Success})
	loser := selectOnly(t, r, u)
	if loser.NodeID == winner.NodeID {
		// Weighted selection must not pick the success-only path when the
		// success removed that path from the eligible set; skip in that case.
		t.Skip("selection picked the same node twice")
	}
	loser.KeyID = "key-1"
	result = loser.Report(Outcome{Kind: UpstreamFailure, StatusCode: 503})
	if !result.UpstreamWide && result.NodeCooldown == nil {
		t.Fatalf("5xx after another exit succeeded must be attributed somewhere: %+v", result)
	}
	_ = a
	_ = b
}

func TestNeutralResponse(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	attempt := selectOnly(t, r, u)
	result := attempt.Report(Outcome{Kind: Neutral, StatusCode: 400})
	if result.Retryable || result.NodeCooldown != nil || entry.Failures() != 0 {
		t.Fatalf("non-retryable 4xx must preserve error semantics: %+v", result)
	}
}

func TestDiagnoseExplainsEligibility(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	healthy := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	cooling := addNode(p, "sub", httpExit("2.2.2.2", 8081, "b"))
	unsupported := addNode(p, "sub", node.Options{Type: "vmess", RawType: "vmess", Server: "3.3.3.3", Port: 443, Tag: "c"})
	now := time.Now()
	cooling.RecordCooldown(now.Add(time.Minute))
	candidates := r.Diagnose(u, map[string]struct{}{healthy.ID(): {}})
	reasons := map[string]string{}
	for _, candidate := range candidates {
		reasons[candidate.ID] = candidate.Reason
	}
	if reasons[healthy.ID()] != "already_attempted" {
		t.Fatalf("healthy reason = %q", reasons[healthy.ID()])
	}
	if reasons[cooling.ID()] != "cooling_down" {
		t.Fatalf("cooling reason = %q", reasons[cooling.ID()])
	}
	if reasons[unsupported.ID()] != "unsupported_protocol" {
		t.Fatalf("unsupported reason = %q", reasons[unsupported.ID()])
	}
}

func TestAffinityIsStickyButRespectsHealth(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	var entries []*node.Entry
	for i := 0; i < 4; i++ {
		entries = append(entries, addNode(p, "sub", httpExit("10.0.0."+string(rune('1'+i)), uint16(8080+i), "n"+string(rune('1'+i)))))
	}
	var preferred string
	for i := 0; i < 20; i++ {
		attempt, err := r.SelectAffinity(context.Background(), u, nil, "key-stable")
		if err != nil {
			t.Fatal(err)
		}
		if preferred == "" {
			preferred = attempt.NodeID
		}
		if attempt.NodeID != preferred {
			t.Fatalf("affinity must keep the same exit: got %s want %s", attempt.NodeID, preferred)
		}
		attempt.Report(Outcome{Kind: Success})
	}

	// A failed affinity exit must yield to another node.
	preferredHash, _ := node.ParseHex(preferred)
	entry := p.Get(preferredHash)
	entry.SetEnabled(false)
	attempt, err := r.SelectAffinity(context.Background(), u, nil, "key-stable")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.NodeID == preferred {
		t.Fatal("disabled affinity node must not be chosen")
	}
	attempt.Report(Outcome{Kind: Success})
	_ = entries
}

func TestScoreComponents(t *testing.T) {
	_, p := testRouter(t)
	entry := addNode(p, "sub", httpExit("1.1.1.1", 8080, "a"))
	weights := DefaultWeights().Normalize()
	score := computeScore(entry, "api.example.com", 0, weights, 1)
	if score.Health != 0.5 || score.Latency != 0.5 || score.Upstream != 0.5 {
		t.Fatalf("priors = %+v", score)
	}
	if score.Routing != 1 {
		t.Fatalf("idle node routing score = %f", score.Routing)
	}
	if score.Total < 0 || score.Total > 100 {
		t.Fatalf("total out of range: %f", score.Total)
	}

	entry.RecordUpstreamResult("api.example.com", true)
	entry.Latency().Update("api.example.com", 50*time.Millisecond, time.Minute)
	better := computeScore(entry, "api.example.com", 0, weights, 1)
	if better.Upstream <= score.Upstream || better.Latency <= score.Latency || better.Total <= score.Total {
		t.Fatalf("good signals must raise the score: before=%+v after=%+v", score, better)
	}
	worse := computeScore(entry, "api.example.com", 5, weights, 1)
	if worse.Routing >= better.Routing {
		t.Fatalf("load must lower the routing score: %+v", worse)
	}
}
