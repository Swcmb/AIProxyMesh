package routing

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestQuotaStaleExitLeavesVerifiedSet(t *testing.T) {
	r, p := testRouter(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	r.SetClock(func() time.Time { return now })
	u := mustURL(t, "https://api.example.com/v1")

	spent := addNode(p, "sub", httpExit("127.0.0.1", 18091, "spent"))
	fresh := addNode(p, "sub", httpExit("127.0.0.1", 18092, "fresh"))
	// Both recently proved they carry traffic, so both sit in the verified set.
	// The timestamp is written against the fake clock, not the wall clock.
	spent.RestoreLastSuccess(now.UnixNano())
	fresh.RestoreLastSuccess(now.UnixNano())

	// Force the spent exit to be chosen, then have the upstream tell it that its
	// source IP has spent the free quota.
	attempt, err := r.Select(context.Background(), u, map[string]struct{}{fresh.ID(): {}})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.NodeID != spent.ID() {
		t.Fatalf("expected the spent exit under test, got %s", attempt.NodeID)
	}
	attempt.Report(Outcome{Kind: ExitFailure, StatusCode: http.StatusTooManyRequests})
	if !spent.QuotaStale(now) {
		t.Fatal("a 429 must mark the exit's quota as spent")
	}

	// Past the node cooldown but inside both the verified window and the quota
	// window: the exit must no longer be chosen from the verified set.
	now = now.Add(time.Minute)
	for i := 0; i < 20; i++ {
		attempt, err := r.Select(context.Background(), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.NodeID != fresh.ID() {
			t.Fatalf("pick %d = %s, want %s: a quota-spent exit leaves the verified set",
				i, attempt.NodeID, fresh.ID())
		}
		// The attempt is released, not reported: a success would rewrite the
		// verified timestamp against the wall clock and defeat the fake clock.
		attempt.Release()
	}

	// Once the quota window closes the earlier success counts again.
	now = now.Add(5 * time.Minute)
	if spent.QuotaStale(now) {
		t.Fatal("the quota window must expire")
	}
	returned := false
	for i := 0; i < 40 && !returned; i++ {
		attempt, err := r.Select(context.Background(), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.NodeID == spent.ID() {
			returned = true
		}
		attempt.Release()
	}
	if !returned {
		t.Fatal("the exit stays excluded after its quota window closed")
	}
}

func TestQuotaDimensionLowersScore(t *testing.T) {
	_, p := testRouter(t)
	entry := addNode(p, "sub", httpExit("127.0.0.1", 18093, "node"))
	weights := DefaultWeights()

	freshScore := computeScore(entry, "api.example.com", 0, weights, 1)
	if freshScore.Quota != 1 {
		t.Fatalf("fresh quota component = %v, want 1", freshScore.Quota)
	}
	spentScore := computeScore(entry, "api.example.com", 0, weights, 0.2)
	if spentScore.Total >= freshScore.Total {
		t.Fatalf("a spent quota must lower the total: %v vs %v", spentScore.Total, freshScore.Total)
	}
	// A zero weight keeps the published behaviour for operators who opt out.
	unweighted := weights
	unweighted.Quota = 0
	if computeScore(entry, "api.example.com", 0, unweighted, 0.2).Total !=
		computeScore(entry, "api.example.com", 0, unweighted, 1).Total {
		t.Fatal("quota must not move the score when its weight is zero")
	}

}
