package health

import (
	"math"
	"testing"
	"time"
)

func TestNormalizeTarget(t *testing.T) {
	cases := map[string]string{
		"HTTPS://OpenCode.AI./zen/go/":               "https://opencode.ai/zen/go",
		"https://opencode.ai:443/inference":          "https://opencode.ai/inference",
		"http://opencode.ai:80":                      "http://opencode.ai",
		"https://user:pass@Opencode.ai/zen?x=1#frag": "https://opencode.ai/zen",
	}
	for raw, want := range cases {
		got, err := NormalizeTarget(raw)
		if err != nil {
			t.Fatalf("NormalizeTarget(%q): %v", raw, err)
		}
		if got != want {
			t.Errorf("NormalizeTarget(%q) = %q, want %q", raw, got, want)
		}
	}
	zen, _ := NormalizeTarget("https://opencode.ai/zen")
	goPath, _ := NormalizeTarget("https://opencode.ai/zen/go")
	if zen == goPath {
		t.Fatal("base path must distinguish health keys")
	}
}

func TestNewKeyRejectsWildcards(t *testing.T) {
	if _, err := NewKey("https://opencode.ai/zen", "*", "chat", "stream"); err == nil {
		t.Fatal("wildcard model must be rejected")
	}
	key, err := NewKey("https://opencode.ai/zen", "claude-sonnet", "chat", "stream")
	if err != nil {
		t.Fatal(err)
	}
	if !key.Valid() || !key.ValidRequired() {
		t.Fatalf("unexpected key validity: %+v", key)
	}
	system, _ := NewKey("https://opencode.ai/zen", "m", ProtocolSystemone, ModeJSON)
	if system.ValidRequired() {
		t.Fatal("systemone must not require payload validation")
	}
}

func TestWilsonAndCheckpoints(t *testing.T) {
	if WilsonLower(0, 0, 2) != 0 {
		t.Fatal("empty sample must have zero lower bound")
	}
	z1 := CheckpointZ(1.96, 1)
	if math.Abs(z1-2.241) > 0.01 {
		t.Fatalf("first checkpoint z = %v, want ~2.241", z1)
	}
	// alpha_j shrinks with each look, so the critical value grows.
	if z2 := CheckpointZ(1.96, 2); z2 <= z1 {
		t.Fatalf("later checkpoint z must be stricter: %v <= %v", z2, z1)
	}
	if got := WilsonLower(96, 0, z1); got < 0.95 {
		t.Fatalf("96 all-success HTTP lower = %v, want >= 0.95", got)
	}
	if got := WilsonLower(46, 0, z1); got < 0.90 {
		t.Fatalf("46 all-success valid lower = %v, want >= 0.90", got)
	}
	if got := WilsonLower(45, 0, z1); got >= 0.90 {
		t.Fatalf("45 all-success valid lower = %v, want < 0.90", got)
	}
}

func TestBetaQuantile(t *testing.T) {
	if got := BetaQuantile(1, 1, 0.3); math.Abs(got-0.3) > 0.01 {
		t.Fatalf("uniform quantile = %v, want 0.3", got)
	}
	if got := BetaQuantile(3, 1, 0.5); math.Abs(got-math.Cbrt(0.5)) > 0.01 {
		t.Fatalf("beta(3,1) median = %v, want %v", got, math.Cbrt(0.5))
	}
	if got := BetaQuantile(2, 2, 0.5); math.Abs(got-0.5) > 0.01 {
		t.Fatalf("beta(2,2) median = %v, want 0.5", got)
	}
}

func TestBetaDecay(t *testing.T) {
	now := time.Now()
	beta := EmptyBeta(now)
	for i := 0; i < 10; i++ {
		beta.Observe(true, now, time.Hour)
	}
	before := beta.RecentWeight()
	decayed := beta
	decayed.DecayTo(now.Add(3*time.Hour), time.Hour)
	if decayed.RecentWeight() >= before {
		t.Fatalf("decay did not reduce weight: %v -> %v", before, decayed.RecentWeight())
	}
	if decayed.Alpha < 1 || decayed.Beta < 1 {
		t.Fatal("decayed posterior must stay above the prior")
	}
}

func TestReservoirRoundTrip(t *testing.T) {
	reservoir := NewReservoir(10)
	for i := 0; i < 100; i++ {
		reservoir.Add(i%2 == 0)
	}
	if len(reservoir.Samples) != 10 || reservoir.Seen != 100 {
		t.Fatalf("reservoir size %d seen %d", len(reservoir.Samples), reservoir.Seen)
	}
	decoded, err := DecodeReservoir(reservoir.Encode(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Samples) != len(reservoir.Samples) {
		t.Fatalf("decoded %d samples", len(decoded.Samples))
	}
	if _, err := DecodeReservoir([]byte{9, 0}, 10); err == nil {
		t.Fatal("unsupported version must fail")
	}
}

func TestWindowRollsOver(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	window := NewWindow(60)
	for i := 0; i < 5; i++ {
		window.Add(false, now.Add(time.Duration(i)*time.Minute))
	}
	rate, samples := window.FailureRate(now.Add(4 * time.Minute))
	if samples != 5 || rate != 1 {
		t.Fatalf("rate=%v samples=%d", rate, samples)
	}
	rate, samples = window.FailureRate(now.Add(20 * time.Minute))
	if samples != 0 || rate != 0 {
		t.Fatalf("expired window: rate=%v samples=%d", rate, samples)
	}
}

func TestLatencyHistogram(t *testing.T) {
	var hist LatencyHist
	for i := 0; i < 100; i++ {
		hist.Observe(time.Duration(i+1) * time.Millisecond)
	}
	// The 64-128ms bucket interpolates the 95th percentile; the result must
	// stay inside the bucket, not at its upper bound.
	p95 := hist.Quantile(0.95)
	if p95 < 90*time.Millisecond || p95 >= 128*time.Millisecond {
		t.Fatalf("p95 = %v", p95)
	}
	var slow LatencyHist
	for i := 0; i < 100; i++ {
		slow.Observe(7 * time.Second)
	}
	if slow.Quantile(0.95) > 8*time.Second {
		t.Fatalf("p95 inside the 4096-8192ms bucket must not exceed the budget: %v", slow.Quantile(0.95))
	}
	decoded, err := DecodeLatencyHist(hist.Encode())
	if err != nil || decoded != hist {
		t.Fatalf("round trip: %v %v", err, decoded)
	}
}

func admit(t *testing.T, stats *KeyStats, cfg Config, now time.Time) time.Time {
	t.Helper()
	for i := 0; i < 11; i++ {
		now = now.Add(61 * time.Second)
		for j := 0; j < 10; j++ {
			stats.Apply(Observation{
				Kind:           OutcomeSuccess,
				Status:         200,
				ValidKnown:     true,
				ValidInference: true,
				TTFB:           100 * time.Millisecond,
				Total:          200 * time.Millisecond,
			}, cfg, now)
			if stats.StateOf() == Active {
				return now
			}
		}
	}
	return now
}

func TestStateMachineAdmissionAndRecovery(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Unix(2_000_000, 0)
	key, _ := NewKey("https://opencode.ai/zen", "m", ProtocolChat, ModeStream)
	stats := NewKeyStats(key, cfg, now)

	stats.Apply(Observation{Kind: OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true}, cfg, now)
	if stats.StateOf() != Verifying {
		t.Fatalf("first 200 should move candidate to verifying, got %s", stats.StateOf())
	}
	now = admit(t, stats, cfg, now)
	if stats.StateOf() != Active {
		t.Fatalf("admission did not reach active: %s", stats.StateOf())
	}

	now = now.Add(time.Second)
	stats.Apply(Observation{Kind: OutcomeExitFailure, Status: 503}, cfg, now)
	stats.Apply(Observation{Kind: OutcomeExitFailure, Status: 503}, cfg, now.Add(time.Second))
	if stats.StateOf() != Suspect {
		t.Fatalf("two primary failures should be suspect, got %s", stats.StateOf())
	}
	stats.Apply(Observation{Kind: OutcomeExitFailure, Status: 503}, cfg, now.Add(2*time.Second))
	if stats.StateOf() != Open {
		t.Fatalf("third primary failure should open, got %s", stats.StateOf())
	}
	transition := stats.Tick(now.Add(2*time.Minute), cfg)
	if transition.To != HalfOpen {
		t.Fatalf("open should expire to half-open, got %s", transition.To)
	}
	for i := 0; i < 3; i++ {
		stats.Apply(Observation{Kind: OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true}, cfg, now.Add(time.Duration(3+i)*time.Second))
	}
	// The key went ACTIVE -> SUSPECT -> OPEN, so half-open returns to SUSPECT;
	// one more success satisfies the SUSPECT recovery streak.
	if stats.StateOf() != Suspect {
		t.Fatalf("half-open recovery should return to the previous state, got %s", stats.StateOf())
	}
	stats.Apply(Observation{Kind: OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true}, cfg, now.Add(10*time.Second))
	if stats.StateOf() != Active {
		t.Fatalf("suspect recovery should restore active, got %s", stats.StateOf())
	}
}

func TestStreamFailureDemotesActive(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Unix(3_000_000, 0)
	key, _ := NewKey("https://opencode.ai/zen", "m", ProtocolChat, ModeStream)
	stats := NewKeyStats(key, cfg, now)
	stats.Apply(Observation{Kind: OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true}, cfg, now)
	now = admit(t, stats, cfg, now)
	if stats.StateOf() != Active {
		t.Fatalf("not active: %s", stats.StateOf())
	}
	stats.Apply(Observation{Kind: OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true, Stream: StreamFailure}, cfg, now)
	stats.Apply(Observation{Kind: OutcomeSuccess, Status: 200, ValidKnown: true, ValidInference: true, Stream: StreamFailure}, cfg, now.Add(time.Second))
	if stats.StateOf() != Suspect {
		t.Fatalf("two stream failures should demote, got %s", stats.StateOf())
	}
}

func TestTableEvictionAndPinning(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	table := NewTable(1, 10)
	a, _ := NewKey("https://opencode.ai/zen", "a", ProtocolChat, ModeStream)
	b, _ := NewKey("https://opencode.ai/zen", "b", ProtocolChat, ModeStream)
	first, ok := table.GetOrCreate(a, cfg, now)
	if !ok {
		t.Fatal("first key should fit")
	}
	if _, ok := table.GetOrCreate(b, cfg, now); !ok {
		t.Fatal("candidate should be evictable")
	}
	if table.Get(a) != nil {
		t.Fatal("evicted key still present")
	}
	pinned := NewKeyStats(a, cfg, now)
	table.Upsert(pinned)
	pinned.Acquire()
	pinned.State = Active
	pinned.Release()
	if _, ok := table.GetOrCreate(b, cfg, now); ok {
		t.Fatal("pinned table must not evict an active key")
	}
	if table.PendingLen() != 1 {
		t.Fatalf("pending queue length = %d", table.PendingLen())
	}
	if got := table.Get(first.Key); got == nil {
		t.Fatal("active record lost")
	}
}

func TestEnvironmentFreezesAndRecovers(t *testing.T) {
	cfg := DefaultEnvironmentConfig()
	cfg.FailureGrace = 0
	now := time.Now()
	env := NewEnvironment(cfg, now)
	env.SetActiveKeys(1)
	env.ReportTransportFailure("n1", "https://a/zen", now)
	env.ReportTransportFailure("n2", "https://b/zen", now)
	env.ReportTransportFailure("n3", "https://a/zen", now)
	if !env.Frozen() {
		t.Fatal("three nodes across two targets should freeze")
	}
	env.ReportBootstrapProbe(true, 10*time.Millisecond, now.Add(time.Minute))
	if env.Frozen() {
		t.Fatal("probe success should recover")
	}
	if env.Epoch() != 1 {
		t.Fatalf("recovery epoch = %d", env.Epoch())
	}
}
