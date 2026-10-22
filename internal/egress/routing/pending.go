package routing

import (
	"sync"
	"time"
)

// pendingObservation is one attributable transport failure waiting out the
// environment grace window before it may cool its exit.
type pendingObservation struct {
	attempt  *Attempt
	outcome  Outcome
	deadline time.Time
	id       string
}

// pendingQueue is a bounded FIFO, deduped per node+health key. It exists so a
// short local network glitch cannot instantly cool a large slice of the pool.
type pendingQueue struct {
	mu      sync.Mutex
	max     int
	items   map[string]*pendingObservation
	order   []string
	dropped uint64
}

func newPendingQueue(max int) *pendingQueue {
	if max < 1 {
		max = 1
	}
	return &pendingQueue{max: max, items: make(map[string]*pendingObservation)}
}

func (q *pendingQueue) setMax(max int) {
	q.mu.Lock()
	if max > 0 {
		q.max = max
	}
	q.mu.Unlock()
}

func (q *pendingQueue) add(item *pendingObservation) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if existing, ok := q.items[item.id]; ok {
		existing.deadline = item.deadline
		existing.outcome = item.outcome
		existing.attempt = item.attempt
		return
	}
	if len(q.items) >= q.max {
		q.evictOldestLocked()
	}
	q.items[item.id] = item
	q.order = append(q.order, item.id)
}

func (q *pendingQueue) evictOldestLocked() {
	for len(q.order) > 0 {
		id := q.order[0]
		q.order = q.order[1:]
		if _, ok := q.items[id]; !ok {
			continue
		}
		delete(q.items, id)
		q.dropped++
		return
	}
}

// due removes and returns observations whose grace window expired.
func (q *pendingQueue) due(now time.Time) []*pendingObservation {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	out := make([]*pendingObservation, 0, len(q.items))
	order := q.order[:0]
	for _, id := range q.order {
		item, ok := q.items[id]
		if !ok {
			continue
		}
		if !now.Before(item.deadline) {
			out = append(out, item)
			delete(q.items, id)
			continue
		}
		order = append(order, id)
	}
	q.order = order
	return out
}

// drain removes every pending observation (shutdown path).
func (q *pendingQueue) drain() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := len(q.items)
	q.items = make(map[string]*pendingObservation)
	q.order = nil
	return count
}

func (q *pendingQueue) stats() (pending int, dropped uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items), q.dropped
}

func (q *pendingQueue) markDropped(count int) {
	if count <= 0 {
		return
	}
	q.mu.Lock()
	q.dropped += uint64(count)
	q.mu.Unlock()
}
