package node

import (
	"math"
	"sort"
	"sync"
	"time"
)

// DomainStats holds the TD-EWMA latency for one target host.
type DomainStats struct {
	EWMA        time.Duration
	LastUpdated time.Time
}

type latencyEntry struct {
	stats DomainStats
	seq   uint64
}

// LatencyTable keeps a bounded, concurrent per-host latency table. Hosts are
// keyed by lowercase hostname; the entry updated least recently is evicted
// when the table is full.
type LatencyTable struct {
	mu      sync.Mutex
	max     int
	seq     uint64
	entries map[string]latencyEntry
}

// NewLatencyTable creates a table bounded to max entries.
func NewLatencyTable(max int) *LatencyTable {
	if max <= 0 {
		max = 1
	}
	return &LatencyTable{max: max, entries: make(map[string]latencyEntry, max)}
}

// NormalizeHost lowercases a host and strips a trailing dot.
func NormalizeHost(host string) string {
	for len(host) > 0 && host[len(host)-1] == '.' {
		host = host[:len(host)-1]
	}
	lower := make([]byte, len(host))
	for i := 0; i < len(host); i++ {
		c := host[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower[i] = c
	}
	return string(lower)
}

// Update records a latency observation using time-decayed EWMA.
func (t *LatencyTable) Update(host string, latency, decayWindow time.Duration) {
	host = NormalizeHost(host)
	if host == "" || latency < 0 {
		return
	}
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.seq++
	old, found := t.entries[host]
	if !found {
		if len(t.entries) >= t.max {
			t.evictLocked()
		}
		t.entries[host] = latencyEntry{stats: DomainStats{EWMA: latency, LastUpdated: now}, seq: t.seq}
		return
	}
	t.entries[host] = latencyEntry{
		stats: DomainStats{EWMA: tdEWMA(old.stats, latency, decayWindow, now), LastUpdated: now},
		seq:   t.seq,
	}
}

// Load restores a persisted observation without applying EWMA.
func (t *LatencyTable) Load(host string, stats DomainStats) {
	host = NormalizeHost(host)
	if host == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.entries[host]; !exists && len(t.entries) >= t.max {
		t.evictLocked()
	}
	t.seq++
	t.entries[host] = latencyEntry{stats: stats, seq: t.seq}
}

// Get returns the stats for a host.
func (t *LatencyTable) Get(host string) (DomainStats, bool) {
	host = NormalizeHost(host)
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[host]
	return entry.stats, ok
}

// Average returns the mean EWMA across all tracked hosts.
func (t *LatencyTable) Average() (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.entries) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, entry := range t.entries {
		total += entry.stats.EWMA
	}
	return total / time.Duration(len(t.entries)), true
}

// Size returns the number of tracked hosts.
func (t *LatencyTable) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Snapshot copies the table for persistence.
func (t *LatencyTable) Snapshot() map[string]DomainStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]DomainStats, len(t.entries))
	for host, entry := range t.entries {
		out[host] = entry.stats
	}
	return out
}

func (t *LatencyTable) evictLocked() {
	var oldestHost string
	var oldestSeq uint64
	first := true
	for host, entry := range t.entries {
		if first || entry.seq < oldestSeq {
			oldestHost, oldestSeq, first = host, entry.seq, false
		}
	}
	if oldestHost != "" {
		delete(t.entries, oldestHost)
	}
}

func tdEWMA(old DomainStats, latency, decayWindow time.Duration, now time.Time) time.Duration {
	decay := decayWindow.Seconds()
	if decay <= 0 {
		decay = 1
	}
	weight := math.Exp(-now.Sub(old.LastUpdated).Seconds() / decay)
	return time.Duration(float64(old.EWMA)*weight + float64(latency)*(1-weight))
}

// SortedHosts returns tracked hosts in ascending order (diagnostics/tests).
func (t *LatencyTable) SortedHosts() []string {
	snapshot := t.Snapshot()
	hosts := make([]string, 0, len(snapshot))
	for host := range snapshot {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}
