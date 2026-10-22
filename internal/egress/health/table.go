package health

import (
	"container/list"
	"sync"
	"time"
)

type tableEntry struct {
	stats *KeyStats
	elem  *list.Element
}

// Table is a node's bounded health-key table. Stable states are pinned;
// candidates, verifying keys and dead keys are evicted LRU-first.
type Table struct {
	mu         sync.Mutex
	max        int
	pendingMax int
	data       map[HealthKey]*tableEntry
	lru        *list.List
	pending    []HealthKey
	pendingSet map[HealthKey]struct{}
}

// NewTable creates a bounded table.
func NewTable(maxKeys, pendingMax int) *Table {
	if maxKeys < 1 {
		maxKeys = 4
	}
	if pendingMax < 1 {
		pendingMax = 10000
	}
	return &Table{
		max:        maxKeys,
		pendingMax: pendingMax,
		data:       make(map[HealthKey]*tableEntry),
		lru:        list.New(),
		pendingSet: make(map[HealthKey]struct{}),
	}
}

// Max returns the per-node key capacity.
func (t *Table) Max() int { return t.max }

// Get returns a tracked key without creating it.
func (t *Table) Get(key HealthKey) *KeyStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.data[key]
	if entry == nil {
		return nil
	}
	t.lru.MoveToFront(entry.elem)
	return entry.stats
}

// GetOrCreate returns the key record, creating it when capacity allows. A
// false second result means the key was queued as pending because every slot
// is pinned.
func (t *Table) GetOrCreate(key HealthKey, cfg Config, now time.Time) (*KeyStats, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.getOrCreateLocked(key, cfg, now)
}

func (t *Table) getOrCreateLocked(key HealthKey, cfg Config, now time.Time) (*KeyStats, bool) {
	if entry := t.data[key]; entry != nil {
		t.lru.MoveToFront(entry.elem)
		return entry.stats, true
	}
	if len(t.data) >= t.max {
		if !t.evictLocked() {
			t.queuePendingLocked(key)
			return nil, false
		}
	}
	stats := NewKeyStats(key, cfg, now)
	entry := &tableEntry{stats: stats, elem: t.lru.PushFront(key)}
	t.data[key] = entry
	delete(t.pendingSet, key)
	t.removePendingLocked(key)
	return stats, true
}

// Apply folds one observation into the key, creating it when possible.
func (t *Table) Apply(key HealthKey, obs Observation, cfg Config, now time.Time) (Transition, *KeyStats, bool) {
	stats, ok := t.GetOrCreate(key, cfg, now)
	if !ok {
		return Transition{From: Candidate, To: Candidate}, nil, false
	}
	return stats.Apply(obs, cfg, now), stats, true
}

// Upsert inserts or replaces a restored record.
func (t *Table) Upsert(stats *KeyStats) {
	if stats == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := stats.Key
	if entry := t.data[key]; entry != nil {
		entry.stats = stats
		t.lru.MoveToFront(entry.elem)
		return
	}
	if len(t.data) >= t.max && !t.evictLocked() {
		t.queuePendingLocked(key)
		return
	}
	entry := &tableEntry{stats: stats, elem: t.lru.PushFront(key)}
	t.data[key] = entry
}

// ProbeSnapshots returns the scheduler view of every tracked key.
func (t *Table) ProbeSnapshots(now time.Time) []ProbeSnapshot {
	t.mu.Lock()
	stats := make([]*KeyStats, 0, len(t.data))
	for _, entry := range t.data {
		stats = append(stats, entry.stats)
	}
	t.mu.Unlock()
	out := make([]ProbeSnapshot, 0, len(stats))
	for _, record := range stats {
		out = append(out, record.ProbeSnapshot(now))
	}
	return out
}

// MarkProbeEnqueued records that a key has been scheduled at least once.
func (t *Table) MarkProbeEnqueued(key HealthKey) {
	t.mu.Lock()
	entry := t.data[key]
	t.mu.Unlock()
	if entry == nil {
		return
	}
	entry.stats.Acquire()
	entry.stats.ProbeEnqueued = true
	entry.stats.Release()
}

// Snapshot copies every tracked record.
func (t *Table) Snapshot() []KeyStats {
	t.mu.Lock()
	stats := make([]*KeyStats, 0, len(t.data))
	for _, entry := range t.data {
		stats = append(stats, entry.stats)
	}
	t.mu.Unlock()
	out := make([]KeyStats, 0, len(stats))
	for _, record := range stats {
		out = append(out, record.Snapshot())
	}
	return out
}

// Keys returns the tracked keys.
func (t *Table) Keys() []HealthKey {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]HealthKey, 0, len(t.data))
	for key := range t.data {
		out = append(out, key)
	}
	return out
}

// Len returns the tracked key count.
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.data)
}

// Remove drops one key.
func (t *Table) Remove(key HealthKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry := t.data[key]; entry != nil {
		t.lru.Remove(entry.elem)
		delete(t.data, key)
	}
	t.removePendingLocked(key)
}

// evictLocked removes one evictable record (DEAD, then CANDIDATE, then
// VERIFYING) in LRU order.
func (t *Table) evictLocked() bool {
	var fallback *list.Element
	for elem := t.lru.Back(); elem != nil; elem = elem.Prev() {
		key := elem.Value.(HealthKey)
		entry := t.data[key]
		if entry == nil {
			continue
		}
		state := entry.stats.StateOf()
		if state.Pinned() {
			continue
		}
		if state == Dead || state == Candidate {
			t.lru.Remove(elem)
			delete(t.data, key)
			return true
		}
		if state == Verifying && fallback == nil {
			fallback = elem
		}
	}
	if fallback != nil {
		key := fallback.Value.(HealthKey)
		t.lru.Remove(fallback)
		delete(t.data, key)
		return true
	}
	return false
}

func (t *Table) queuePendingLocked(key HealthKey) {
	if _, exists := t.pendingSet[key]; exists {
		return
	}
	if len(t.pending) >= t.pendingMax {
		dropped := t.pending[0]
		t.pending = t.pending[1:]
		delete(t.pendingSet, dropped)
	}
	t.pending = append(t.pending, key)
	t.pendingSet[key] = struct{}{}
}

func (t *Table) removePendingLocked(key HealthKey) {
	if _, exists := t.pendingSet[key]; !exists {
		return
	}
	delete(t.pendingSet, key)
	for i, pending := range t.pending {
		if pending == key {
			t.pending = append(t.pending[:i], t.pending[i+1:]...)
			break
		}
	}
}

// PendingLen reports the queued key count.
func (t *Table) PendingLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}
