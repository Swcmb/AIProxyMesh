package health

import (
	"net/http"
	"testing"
	"time"
)

func TestQuotaFreshnessFollowsRateLimits(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Unix(1_800_000_000, 0).UTC()
	key, err := NewKey("https://opencode.ai/zen", "m", ProtocolChat, ModeJSON)
	if err != nil {
		t.Fatal(err)
	}
	stats := NewKeyStats(key, cfg, now)
	freshness := func() float64 { return QuotaFreshness(stats.Snapshot().RateWindow, now) }

	if freshness() != 1 {
		t.Fatalf("freshness without evidence = %v, want 1 so an unprobed exit is not penalised", freshness())
	}
	stats.Apply(Observation{Key: key, Kind: OutcomeTransportFailure}, cfg, now)
	if freshness() != 1 {
		t.Fatalf("freshness after a transport failure = %v, want 1 because it says nothing about quota", freshness())
	}

	// A separate record keeps the attribution check free of the transport
	// failure above, which legitimately counts against the primary metric.
	rateLimited := NewKeyStats(key, cfg, now)
	for i := 0; i < 4; i++ {
		rateLimited.Apply(Observation{Key: key, Kind: OutcomeRateLimited, Status: http.StatusTooManyRequests}, cfg, now)
	}
	if got := QuotaFreshness(rateLimited.Snapshot().RateWindow, now); got > 0.01 {
		t.Fatalf("freshness after four 429s = %v, want ~0", got)
	}
	// Attribution discipline: rate limiting stays neutral for the primary
	// metrics and only feeds the quota window.
	snapshot := rateLimited.Snapshot()
	if snapshot.HTTP200Success+snapshot.HTTP200Failure != 0 || snapshot.PrimaryFailure != 0 {
		t.Fatalf("429 must not move the primary metrics: %+v", snapshot)
	}
	if snapshot.RateLimited != 4 {
		t.Fatalf("RateLimited = %d, want 4", snapshot.RateLimited)
	}

	for i := 0; i < 4; i++ {
		stats.Apply(Observation{Key: key, Kind: OutcomeSuccess, Status: http.StatusOK}, cfg, now)
	}
	if freshness() < 0.4 {
		t.Fatalf("freshness after four 200s = %v, want >= 0.4", freshness())
	}
}

func TestQuotaWindowSurvivesSnapshotRebuild(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Unix(1_800_000_000, 0).UTC()
	key, err := NewKey("https://opencode.ai/zen", "m", ProtocolChat, ModeJSON)
	if err != nil {
		t.Fatal(err)
	}
	stats := NewKeyStats(key, cfg, now)
	stats.Apply(Observation{Key: key, Kind: OutcomeRateLimited, Status: http.StatusTooManyRequests}, cfg, now)

	rebuilt := FromSnapshot(stats.Snapshot(), cfg, now)
	if rebuilt.Snapshot().RateWindow.BucketSeconds != int64(cfg.QuotaBucketSeconds) {
		t.Fatalf("rebuilt bucket seconds = %d, want %d",
			rebuilt.Snapshot().RateWindow.BucketSeconds, cfg.QuotaBucketSeconds)
	}
	if got := QuotaFreshness(rebuilt.Snapshot().RateWindow, now); got >= 1 {
		t.Fatalf("rebuilt freshness = %v, want the rate-limited sample preserved", got)
	}
}
