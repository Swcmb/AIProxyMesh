package logstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "requests.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func requestRow(id string, at time.Time, model string, status int, success bool) Request {
	return Request{
		RequestID: id, At: at, Model: model, Channel: "zen", Tier: "zen", KeyID: "key-1",
		ServerKeyID: "local", Status: status, DurationMS: 12, Success: success,
		Outcome: map[bool]string{true: "success", false: "client_error"}[success],
	}
}

func TestQueryFiltersAndPagination(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	rows := []Request{
		requestRow("r1", base, "alpha", 200, true),
		requestRow("r2", base.Add(time.Minute), "beta", 429, false),
		requestRow("r3", base.Add(2*time.Minute), "alpha", 500, false),
	}
	if err := store.InsertRequests(ctx, rows); err != nil {
		t.Fatalf("insert: %v", err)
	}

	page, err := store.Requests(ctx, Query{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(page.Requests) != 3 || page.Requests[0].RequestID != "r3" {
		t.Fatalf("expected newest first, got %+v", page.Requests)
	}

	page, err = store.Requests(ctx, Query{Limit: 10, Model: "alpha"})
	if err != nil {
		t.Fatalf("filtered query: %v", err)
	}
	if len(page.Requests) != 2 {
		t.Fatalf("model filter returned %d rows", len(page.Requests))
	}

	unsuccessful := false
	page, err = store.Requests(ctx, Query{Limit: 10, FailedOnly: true, Success: &unsuccessful})
	if err != nil {
		t.Fatalf("failed query: %v", err)
	}
	if len(page.Requests) != 2 {
		t.Fatalf("failed filter returned %d rows", len(page.Requests))
	}

	page, err = store.Requests(ctx, Query{Limit: 2})
	if err != nil {
		t.Fatalf("paged query: %v", err)
	}
	if len(page.Requests) != 2 || !page.HasMore || page.NextCursor == 0 {
		t.Fatalf("expected a next cursor, got %+v", page)
	}
	second, err := store.Requests(ctx, Query{Limit: 2, BeforeID: page.NextCursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Requests) != 1 || second.Requests[0].RequestID != "r1" {
		t.Fatalf("cursor page returned %+v", second.Requests)
	}

	page, err = store.Requests(ctx, Query{Limit: 10, Since: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("time filter: %v", err)
	}
	if len(page.Requests) != 2 {
		t.Fatalf("since filter returned %d rows", len(page.Requests))
	}

	page, err = store.Requests(ctx, Query{Limit: 10, Keyword: "BETA"})
	if err != nil {
		t.Fatalf("keyword filter: %v", err)
	}
	if len(page.Requests) != 1 || page.Requests[0].RequestID != "r2" {
		t.Fatalf("keyword filter returned %+v", page.Requests)
	}
}

func TestRepeatedRequestIDKeepsFinalOutcome(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.InsertRequests(ctx, []Request{requestRow("same", now, "alpha", 500, false)}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := store.InsertRequests(ctx, []Request{requestRow("same", now.Add(time.Second), "alpha", 200, true)}); err != nil {
		t.Fatalf("second insert: %v", err)
	}
	record, found, err := store.RequestByExternalID(ctx, "same")
	if err != nil || !found {
		t.Fatalf("lookup: found=%v err=%v", found, err)
	}
	page, err := store.Requests(ctx, Query{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(page.Requests) != 1 {
		t.Fatalf("expected one row per request id, got %d", len(page.Requests))
	}
	if record.Status != 200 || !record.Success {
		t.Fatalf("expected the final outcome to win, got %+v", record)
	}
}

func TestDetailReturnsAttemptChain(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.InsertRequests(ctx, []Request{requestRow("chain", now, "alpha", 200, true)}); err != nil {
		t.Fatalf("insert request: %v", err)
	}
	if err := store.InsertAttempts(ctx, []Attempt{
		{RequestID: "chain", At: now, Attempt: 2, KeyID: "key-2", Status: 429, Outcome: "rate_limited"},
		{RequestID: "chain", At: now, Attempt: 1, KeyID: "key-1", Status: 500, Outcome: "server_error"},
	}); err != nil {
		t.Fatalf("insert attempts: %v", err)
	}
	attempts, err := store.AttemptsOf(ctx, "chain")
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0].Attempt != 1 || attempts[1].Attempt != 2 {
		t.Fatalf("expected attempts ordered by number, got %+v", attempts)
	}
}

func TestPruneByAgeAndRowCap(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-30 * 24 * time.Hour)
	rows := make([]Request, 0, 5)
	for index := range 5 {
		rows = append(rows, requestRow(string(rune('a'+index)), base.Add(time.Duration(index)*time.Hour), "alpha", 200, true))
	}
	if err := store.InsertRequests(ctx, rows); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.InsertAttempts(ctx, []Attempt{{RequestID: "a", At: base, Attempt: 1}}); err != nil {
		t.Fatalf("insert attempts: %v", err)
	}

	removed, err := store.Prune(ctx, time.Now().Add(-time.Hour), 0)
	if err != nil {
		t.Fatalf("prune by age: %v", err)
	}
	if removed != 5 {
		t.Fatalf("expected 5 aged rows removed, got %d", removed)
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Requests != 0 || stats.Attempts != 0 {
		t.Fatalf("attempts of pruned requests must go too, got %+v", stats)
	}

	fresh := time.Now().UTC()
	rows = rows[:0]
	for index := range 6 {
		rows = append(rows, requestRow(string(rune('a'+index)), fresh, "alpha", 200, true))
	}
	if err := store.InsertRequests(ctx, rows); err != nil {
		t.Fatalf("reinsert: %v", err)
	}
	if _, err := store.Prune(ctx, time.Time{}, 3); err != nil {
		t.Fatalf("prune by rows: %v", err)
	}
	page, err := store.Requests(ctx, Query{Limit: 20})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(page.Requests) != 3 || page.Requests[0].RequestID != "f" {
		t.Fatalf("row cap must keep the newest rows, got %+v", page.Requests)
	}
}

func TestWriterPersistsAndCountsDrops(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := NewWriter(store, 2, nil)
	writer.SetRetention(7, 1000)
	writer.runLoop(ctx)

	now := time.Now().UTC()
	// The buffer holds two records; the rest must be dropped rather than block.
	for index := range 10 {
		writer.EnqueueRequest(requestRow(string(rune('a'+index)), now, "alpha", 200, true))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if writer.dropped.Load() == 0 {
		t.Fatal("a full buffer must count dropped records")
	}
	page, err := store.Requests(context.Background(), Query{Limit: 20})
	if err != nil {
		t.Fatalf("query after close: %v", err)
	}
	if len(page.Requests) == 0 {
		t.Fatal("queued records must be flushed on close")
	}
}

func TestServiceNilIsInert(t *testing.T) {
	var service *Service
	service.PersistRequest(requestTelemetry())
	service.PersistAttempt(attemptTelemetry())
	page, err := service.Requests(context.Background(), Query{Limit: 1})
	if err != nil || len(page.Requests) != 0 {
		t.Fatalf("nil service query: page=%+v err=%v", page, err)
	}
	if stats := service.Stats(context.Background()); stats.Enabled {
		t.Fatalf("nil service must report disabled, got %+v", stats)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("nil service close: %v", err)
	}
}

func TestServiceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	service, err := Open(Options{Path: path, RetentionDays: 3, MaxRows: 500})
	if err != nil {
		t.Fatalf("open service: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	now := time.Now().UTC()
	service.PersistRequest(requestAt("live", now))
	service.PersistAttempt(attemptAt("live", now))
	if err := service.Close(); err != nil {
		t.Fatalf("close service: %v", err)
	}

	reopened, err := Open(Options{Path: path, RetentionDays: 3, MaxRows: 500})
	if err != nil {
		t.Fatalf("reopen service: %v", err)
	}
	defer reopened.Close()
	record, attempts, found, err := reopened.Detail(context.Background(), "live")
	if err != nil || !found {
		t.Fatalf("lookup after restart: found=%v err=%v", found, err)
	}
	if record.Model != "alpha" || len(attempts) != 1 || attempts[0].RequestID != "live" {
		t.Fatalf("unexpected detail: %+v %+v", record, attempts)
	}
	stats := reopened.Stats(context.Background())
	if stats.Requests != 1 || stats.Attempts != 1 || stats.RetentionDays != 3 || stats.MaxRows != 500 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if removed, err := reopened.Purge(context.Background()); err != nil || removed != 1 {
		t.Fatalf("purge: removed=%d err=%v", removed, err)
	}
}

func requestAt(id string, at time.Time) telemetry.UpstreamRequest {
	return telemetry.UpstreamRequest{
		Time: at, RequestID: id, Model: "alpha", Tier: "zen", Channel: "zen", KeyID: "key-1",
		Status: 200, DurationMS: 12, Success: true, Outcome: "success",
	}
}

func attemptAt(id string, at time.Time) telemetry.UpstreamAttempt {
	return telemetry.UpstreamAttempt{
		Time: at, RequestID: id, Model: "alpha", Tier: "zen", Attempt: 1, KeyID: "key-1",
		Channel: "zen", Status: 200, DurationMS: 12, Success: true, Outcome: "success",
	}
}

func requestTelemetry() telemetry.UpstreamRequest { return requestAt("t1", time.Now().UTC()) }

func attemptTelemetry() telemetry.UpstreamAttempt { return attemptAt("t1", time.Now().UTC()) }
