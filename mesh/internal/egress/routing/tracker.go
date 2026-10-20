package routing

import (
	"sync"
	"time"
)

// UpstreamTracker decides whether repeated failures belong to one exit or to
// the upstream as a whole. Consensus means at least two distinct exits reported
// the same status class for the same target and no exit succeeded in between.
type UpstreamTracker struct {
	mu          sync.Mutex
	window      time.Duration
	failures    map[string][]failureSample
	lastSuccess map[string]successSample
}

type failureSample struct {
	nodeID string
	at     time.Time
}

type successSample struct {
	nodeID string
	at     time.Time
}

// NewUpstreamTracker creates a tracker with the given observation window.
func NewUpstreamTracker(window time.Duration) *UpstreamTracker {
	if window <= 0 {
		window = time.Minute
	}
	return &UpstreamTracker{
		window:      window,
		failures:    make(map[string][]failureSample),
		lastSuccess: make(map[string]successSample),
	}
}

// SetWindow updates the observation window and drops stale samples.
func (t *UpstreamTracker) SetWindow(window time.Duration) {
	if window <= 0 {
		window = time.Minute
	}
	t.mu.Lock()
	t.window = window
	t.mu.Unlock()
}

// Record stores a failure and reports whether the bucket has reached
// consensus. statusClass is the status code's hundreds digit (4 or 5).
func (t *UpstreamTracker) Record(target, keyID string, statusClass int, nodeID string, at time.Time) bool {
	if target == "" {
		return false
	}
	bucket := target + "|" + keyID + "|" + statusKey(statusClass)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(at)
	t.failures[bucket] = append(t.failures[bucket], failureSample{nodeID: nodeID, at: at})

	distinct := make(map[string]struct{})
	for _, sample := range t.failures[bucket] {
		distinct[sample.nodeID] = struct{}{}
	}
	if len(distinct) < 2 {
		return false
	}
	if success, ok := t.lastSuccess[target]; ok && at.Sub(success.at) <= t.window {
		return false
	}
	return true
}

// RecordSuccess clears failure history for a target.
func (t *UpstreamTracker) RecordSuccess(target, nodeID string, at time.Time) {
	if target == "" {
		return
	}
	t.mu.Lock()
	for bucket := range t.failures {
		if hasPrefixTarget(bucket, target) {
			delete(t.failures, bucket)
		}
	}
	t.lastSuccess[target] = successSample{nodeID: nodeID, at: at}
	t.mu.Unlock()
}

// RecentSuccessByOther reports a success for the target by a different exit
// inside the window. A repeat success from the same exit does not make a later
// 5xx exit-specific.
func (t *UpstreamTracker) RecentSuccessByOther(target, nodeID string, at time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	success, ok := t.lastSuccess[target]
	return ok && success.nodeID != nodeID && at.Sub(success.at) <= t.window
}

func (t *UpstreamTracker) pruneLocked(now time.Time) {
	cutoff := now.Add(-t.window)
	for bucket, samples := range t.failures {
		kept := samples[:0]
		for _, sample := range samples {
			if sample.at.After(cutoff) {
				kept = append(kept, sample)
			}
		}
		if len(kept) == 0 {
			delete(t.failures, bucket)
			continue
		}
		t.failures[bucket] = kept
	}
	for target, at := range t.lastSuccess {
		if at.at.Before(cutoff) {
			delete(t.lastSuccess, target)
		}
	}
}

func statusKey(class int) string {
	if class <= 0 {
		return "err"
	}
	return string(rune('0' + class))
}

func hasPrefixTarget(bucket, target string) bool {
	return len(bucket) > len(target) && bucket[:len(target)] == target && bucket[len(target)] == '|'
}
