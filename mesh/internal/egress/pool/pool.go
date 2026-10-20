// Package pool owns the global set of egress nodes and the subscription
// references that keep each node alive.
package pool

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// Input is one parsed node ready to enter the pool.
type Input struct {
	Options node.Options
	Raw     json.RawMessage
}

// Diff describes the effect of one subscription reconciliation.
type Diff struct {
	Added       int `json:"added"`
	Updated     int `json:"updated"`
	Removed     int `json:"removed"`
	Total       int `json:"total"`
	Unsupported int `json:"unsupported"`
}

// Pool is the concurrency-safe node registry.
type Pool struct {
	mu         sync.RWMutex
	entries    map[node.Hash]*node.Entry
	refs       map[node.Hash]map[string]struct{}
	subEnabled map[string]bool
	maxLatency int
	now        func() time.Time
}

// New creates an empty pool. maxLatencyEntries bounds each node's per-host
// latency table.
func New(maxLatencyEntries int) *Pool {
	if maxLatencyEntries <= 0 {
		maxLatencyEntries = 64
	}
	return &Pool{
		entries:    make(map[node.Hash]*node.Entry),
		refs:       make(map[node.Hash]map[string]struct{}),
		subEnabled: make(map[string]bool),
		maxLatency: maxLatencyEntries,
		now:        time.Now,
	}
}

// SetClock replaces the clock used for creation timestamps (tests).
func (p *Pool) SetClock(now func() time.Time) {
	p.mu.Lock()
	p.now = now
	p.mu.Unlock()
}

// Add inserts or reuses a node and records the subscription reference.
func (p *Pool) Add(subID string, input Input) (*node.Entry, bool) {
	h := node.HashOptions(input.Options)
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, exists := p.entries[h]
	if !exists {
		entry = node.NewEntry(h, input.Raw, input.Options, p.now(), p.maxLatency)
		p.entries[h] = entry
	}
	entry.AddSubscriptionID(subID)
	p.refs[h] = addRef(p.refs[h], subID)
	return entry, !exists
}

// ReplaceSubscription reconciles one subscription's node set atomically with
// respect to other pool readers. Nodes still referenced elsewhere survive.
func (p *Pool) ReplaceSubscription(subID string, inputs []Input) Diff {
	p.mu.Lock()
	defer p.mu.Unlock()

	diff := Diff{Unsupported: 0}
	next := make(map[node.Hash]struct{}, len(inputs))
	for _, input := range inputs {
		h := node.HashOptions(input.Options)
		if _, duplicate := next[h]; duplicate {
			continue
		}
		next[h] = struct{}{}
		entry, exists := p.entries[h]
		if !exists {
			entry = node.NewEntry(h, input.Raw, input.Options, p.now(), p.maxLatency)
			p.entries[h] = entry
			diff.Added++
		} else {
			diff.Updated++
		}
		entry.AddSubscriptionID(subID)
		p.refs[h] = addRef(p.refs[h], subID)
		if !input.Options.Supported() {
			diff.Unsupported++
		}
	}

	for h, subscriptions := range p.refs {
		if _, keep := next[h]; keep {
			continue
		}
		if _, referenced := subscriptions[subID]; !referenced {
			continue
		}
		delete(subscriptions, subID)
		if len(subscriptions) == 0 {
			delete(p.refs, h)
			delete(p.entries, h)
			diff.Removed++
			continue
		}
		p.refs[h] = subscriptions
		if entry := p.entries[h]; entry != nil {
			entry.RemoveSubscriptionID(subID)
		}
	}
	diff.Total = len(next)
	return diff
}

// RemoveSubscription drops every reference from one subscription.
func (p *Pool) RemoveSubscription(subID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	removed := 0
	for h, subscriptions := range p.refs {
		if _, ok := subscriptions[subID]; !ok {
			continue
		}
		delete(subscriptions, subID)
		if len(subscriptions) == 0 {
			delete(p.refs, h)
			delete(p.entries, h)
			removed++
			continue
		}
		p.refs[h] = subscriptions
		if entry := p.entries[h]; entry != nil {
			entry.RemoveSubscriptionID(subID)
		}
	}
	delete(p.subEnabled, subID)
	return removed
}

// SetSubscriptionEnabled controls whether nodes referenced only by this
// subscription remain eligible.
func (p *Pool) SetSubscriptionEnabled(subID string, enabled bool) {
	p.mu.Lock()
	p.subEnabled[subID] = enabled
	p.mu.Unlock()
}

// Active reports whether at least one referencing subscription is enabled.
func (p *Pool) Active(entry *node.Entry) bool {
	if entry == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for subID := range p.refs[entry.Hash] {
		if enabled, known := p.subEnabled[subID]; !known || enabled {
			return true
		}
	}
	return false
}

// Get returns an entry by hash.
func (p *Pool) Get(h node.Hash) *node.Entry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.entries[h]
}

// List returns a snapshot slice of entries.
func (p *Pool) List() []*node.Entry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*node.Entry, 0, len(p.entries))
	for _, entry := range p.entries {
		out = append(out, entry)
	}
	return out
}

// SetEnabled flips the manual switch on one node.
func (p *Pool) SetEnabled(h node.Hash, enabled bool) error {
	p.mu.RLock()
	entry := p.entries[h]
	p.mu.RUnlock()
	if entry == nil {
		return fmt.Errorf("node %s not found", h)
	}
	entry.SetEnabled(enabled)
	return nil
}

// Len returns the node count.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

// SubscriptionIDs returns the subscriptions currently known to the pool.
func (p *Pool) SubscriptionIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, subscriptions := range p.refs {
		for subID := range subscriptions {
			seen[subID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for subID := range seen {
		out = append(out, subID)
	}
	return out
}

// Restore re-inserts a persisted entry and its references without counting a
// diff. Callers use it during startup before the first subscription refresh.
func (p *Pool) Restore(entry *node.Entry, subscriptionIDs []string, enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry.SetEnabled(enabled)
	p.entries[entry.Hash] = entry
	for _, subID := range subscriptionIDs {
		if subID == "" {
			continue
		}
		entry.AddSubscriptionID(subID)
		p.refs[entry.Hash] = addRef(p.refs[entry.Hash], subID)
	}
}

// Snapshot returns the management view of every node.
func (p *Pool) Snapshot(target string, now time.Time) []node.Snapshot {
	entries := p.List()
	out := make([]node.Snapshot, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Snapshot(target, now))
	}
	return out
}

func addRef(set map[string]struct{}, id string) map[string]struct{} {
	if set == nil {
		set = make(map[string]struct{}, 1)
	}
	if id != "" {
		set[id] = struct{}{}
	}
	return set
}
