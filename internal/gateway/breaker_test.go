package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

func TestAnonymousBreakerTripsAndCloses(t *testing.T) {
	pool := newAnonymousPool(false, nil, 0, anonymousBackoff{threshold: 2, base: time.Hour, max: 4 * time.Hour})
	if pool.Paused() {
		t.Fatal("a fresh lane must not be paused")
	}
	if _, tripped := pool.NoteFailure(); tripped {
		t.Fatal("the first failure is below the threshold")
	}
	pause, tripped := pool.NoteFailure()
	if !tripped || pause != time.Hour || !pool.Paused() {
		t.Fatalf("second failure must trip for an hour: pause=%v tripped=%v paused=%v", pause, tripped, pool.Paused())
	}
	pool.NoteSuccess()
	if pool.Paused() {
		t.Fatal("a success must close the breaker")
	}
	// A success forgets the strikes: the next trip starts from the base.
	pool.NoteFailure()
	pause, _ = pool.NoteFailure()
	if pause != time.Hour {
		t.Fatalf("trip after a success = %v", pause)
	}
	// Consecutive trips double, up to max.
	pool.NoteFailure()
	pause, _ = pool.NoteFailure()
	if pause != 2*time.Hour {
		t.Fatalf("second trip = %v", pause)
	}
	pool.NoteFailure()
	pause, _ = pool.NoteFailure()
	if pause != 4*time.Hour {
		t.Fatalf("capped pause = %v", pause)
	}
}

func TestPausedLaneSkipsTheUpstream(t *testing.T) {
	fake := newFakeUpstream(t)
	cfg := testConfig(fake.server.URL, nil)
	cfg.Anonymous = ptrBool(true)
	cfg.Models.AnonymousBackoff = &config.AnonymousBackoffConfig{Threshold: 1, BaseHours: 1, MaxHours: 2}
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	g.anonymous.NoteFailure() // trips: threshold is 1
	if !g.anonymous.Paused() {
		t.Fatal("the lane must be paused")
	}
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, Anonymous: true}
	resp, err, used := g.doAnonymousUpstream(context.Background(), route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-paused", Session: "ses-paused"}, 0)
	if resp != nil || used != 0 || err == nil {
		t.Fatalf("paused lane must not attempt: resp=%v used=%d err=%v", resp, used, err)
	}
	if err.Error() != errAnonymousLanePaused.Error() {
		t.Fatalf("err = %v", err)
	}
	if bodies, _, _ := fake.snapshot(); len(bodies) != 0 {
		t.Fatalf("the upstream must not be contacted, saw %d requests", len(bodies))
	}
}

func TestAnonymousFailureFeedsTheBreaker(t *testing.T) {
	fake := newFakeUpstream(t)
	fake.reply = func(w http.ResponseWriter, _ *http.Request, _ []byte) bool {
		w.WriteHeader(500)
		return true
	}
	cfg := testConfig(fake.server.URL, nil)
	cfg.Anonymous = ptrBool(true)
	cfg.Models.AnonymousBackoff = &config.AnonymousBackoffConfig{Threshold: 1, BaseHours: 1, MaxHours: 2}
	g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), telemetry.NewMonitor())
	if err != nil {
		t.Fatal(err)
	}
	route := models.Route{ID: "test-model", Tier: config.TierZen, Protocol: wire.Chat, Anonymous: true}
	_, _, _ = g.doAnonymousUpstream(context.Background(), route,
		map[config.Tier][]byte{config.TierZen: []byte(`{"model":"test-model","messages":[]}`)},
		identity.RequestIDs{Request: "req-fail", Session: "ses-fail"}, 0)
	if !g.anonymous.Paused() {
		t.Fatal("a failed anonymous attempt must trip a threshold of one")
	}
}
