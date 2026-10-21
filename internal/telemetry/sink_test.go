package telemetry

import (
	"sync"
	"testing"
	"time"
)

// recordingSink captures what the monitor forwards for persistence.
type recordingSink struct {
	mu       sync.Mutex
	requests []UpstreamRequest
	attempts []UpstreamAttempt
}

func (s *recordingSink) PersistRequest(record UpstreamRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, record)
}

func (s *recordingSink) PersistAttempt(record UpstreamAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, record)
}

func TestMonitorFansOutToSink(t *testing.T) {
	monitor := NewMonitor()
	sink := &recordingSink{}
	monitor.SetSink(sink)

	monitor.Record("/v1/responses", 200, 25*time.Millisecond, &RequestMeta{
		Request: "req-1", Model: "model-a", Tier: "zen", Channel: "zen", KeyID: "key-1",
		Session: "ses_1", ServerKeyID: "local", Attempts: 2,
	})
	monitor.RecordAttempt(UpstreamAttempt{RequestID: "req-1", Attempt: 1, Model: "model-a", Channel: "zen", Status: 429, Outcome: "rate_limited"})

	if len(sink.requests) != 1 || sink.requests[0].RequestID != "req-1" {
		t.Fatalf("request not forwarded: %+v", sink.requests)
	}
	if sink.requests[0].Attempts != 2 || sink.requests[0].DurationMS != 25 {
		t.Fatalf("forwarded request lost fields: %+v", sink.requests[0])
	}
	if len(sink.attempts) != 1 || sink.attempts[0].Status != 429 {
		t.Fatalf("attempt not forwarded: %+v", sink.attempts)
	}
	if len(monitor.RequestSamples(time.Minute)) != 1 {
		t.Fatal("the in-memory ring must keep recording alongside the sink")
	}
}

func TestMonitorWithoutSink(t *testing.T) {
	monitor := NewMonitor()
	monitor.Record("/v1/responses", 500, 5*time.Millisecond, &RequestMeta{Request: "req-2", Model: "model-a", Channel: "zen"})
	monitor.RecordAttempt(UpstreamAttempt{RequestID: "req-2", Attempt: 1, Channel: "zen"})
	monitor.SetSink(nil)
}
