package routing

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

type indexBucket struct {
	active    []node.Hash
	verifying []node.Hash
	activePos map[node.Hash]int
	verifyPos map[node.Hash]int
}

func newIndexBucket() *indexBucket {
	return &indexBucket{activePos: map[node.Hash]int{}, verifyPos: map[node.Hash]int{}}
}

// Index is the stable-pool index. It tracks only health keys that have a
// recorded state, so the request path samples instead of scanning the pool.
type Index struct {
	mu        sync.RWMutex
	buckets   map[health.HealthKey]*indexBucket
	byNode    map[node.Hash]map[health.HealthKey]struct{}
	reachable map[string]map[node.Hash]time.Time
}

// NewIndex creates an empty index.
func NewIndex() *Index {
	return &Index{
		buckets:   make(map[health.HealthKey]*indexBucket),
		byNode:    make(map[node.Hash]map[health.HealthKey]struct{}),
		reachable: make(map[string]map[node.Hash]time.Time),
	}
}

// Upsert moves a key to its state bucket. CANDIDATE/DEAD/OPEN are removed from
// routing buckets. A reachable timestamp feeds the target-level fallback.
func (idx *Index) Upsert(key health.HealthKey, hash node.Hash, state health.State, reachable time.Time) {
	if !key.Valid() {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	bucket := idx.buckets[key]
	if bucket == nil {
		bucket = newIndexBucket()
		idx.buckets[key] = bucket
	}
	bucket.active = removeHash(bucket.active, bucket.activePos, hash)
	bucket.verifying = removeHash(bucket.verifying, bucket.verifyPos, hash)
	switch state {
	case health.Active, health.Suspect, health.HalfOpen:
		bucket.active = addHash(bucket.active, bucket.activePos, hash)
	case health.Verifying:
		bucket.verifying = addHash(bucket.verifying, bucket.verifyPos, hash)
	}
	nodeKeys := idx.byNode[hash]
	if nodeKeys == nil {
		nodeKeys = make(map[health.HealthKey]struct{})
		idx.byNode[hash] = nodeKeys
	}
	nodeKeys[key] = struct{}{}
	if !reachable.IsZero() {
		targets := idx.reachable[key.Target]
		if targets == nil {
			targets = make(map[node.Hash]time.Time)
			idx.reachable[key.Target] = targets
		}
		targets[hash] = reachable
	}
}

// RemoveAll drops every key of one node.
func (idx *Index) RemoveAll(hash node.Hash) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for key := range idx.byNode[hash] {
		if bucket := idx.buckets[key]; bucket != nil {
			bucket.active = removeHash(bucket.active, bucket.activePos, hash)
			bucket.verifying = removeHash(bucket.verifying, bucket.verifyPos, hash)
		}
	}
	delete(idx.byNode, hash)
	for target, targets := range idx.reachable {
		delete(targets, hash)
		if len(targets) == 0 {
			delete(idx.reachable, target)
		}
	}
}

// Sample returns up to k hashes from one state bucket.
func (idx *Index) Sample(key health.HealthKey, state string, k int) []node.Hash {
	if k < 1 {
		k = 1
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	bucket := idx.buckets[key]
	if bucket == nil {
		return nil
	}
	source := bucket.active
	if state == "verifying" {
		source = bucket.verifying
	}
	return sampleHashes(source, k)
}

// ReachableSample returns recently reachable hashes for a target, refreshing
// the recorded timestamp from the entry when it is newer.
func (idx *Index) ReachableSample(target string, window time.Duration, k int, now time.Time) []node.Hash {
	if k < 1 {
		k = 1
	}
	idx.mu.RLock()
	targets := idx.reachable[target]
	out := make([]node.Hash, 0, len(targets))
	for hash, at := range targets {
		if window <= 0 || now.Sub(at) <= window {
			out = append(out, hash)
		}
	}
	idx.mu.RUnlock()
	if len(out) == 0 {
		return nil
	}
	if len(out) > k {
		rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		out = out[:k]
	}
	return out
}

// Count returns active and verifying sizes for one key.
func (idx *Index) Count(key health.HealthKey) (active int, verifying int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	bucket := idx.buckets[key]
	if bucket == nil {
		return 0, 0
	}
	return len(bucket.active), len(bucket.verifying)
}

// MarkReachable records a target-level reachable hint without creating a key.
func (idx *Index) MarkReachable(target string, hash node.Hash, at time.Time) {
	if target == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	targets := idx.reachable[target]
	if targets == nil {
		targets = make(map[node.Hash]time.Time)
		idx.reachable[target] = targets
	}
	targets[hash] = at
}

// Rebuild replaces the index from restored health tables and reachable hints.
type RebuildEntry struct {
	Hash        node.Hash
	State       health.State
	ReachableAt time.Time
}

// Rebuild seeds the index in one pass.
func (idx *Index) Rebuild(entries map[health.HealthKey][]RebuildEntry) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.buckets = make(map[health.HealthKey]*indexBucket)
	idx.byNode = make(map[node.Hash]map[health.HealthKey]struct{})
	idx.reachable = make(map[string]map[node.Hash]time.Time)
	for key, list := range entries {
		for _, entry := range list {
			bucket := idx.buckets[key]
			if bucket == nil {
				bucket = newIndexBucket()
				idx.buckets[key] = bucket
			}
			switch entry.State {
			case health.Active, health.Suspect, health.HalfOpen:
				addHash(bucket.active, bucket.activePos, entry.Hash)
			case health.Verifying:
				addHash(bucket.verifying, bucket.verifyPos, entry.Hash)
			}
			nodeKeys := idx.byNode[entry.Hash]
			if nodeKeys == nil {
				nodeKeys = make(map[health.HealthKey]struct{})
				idx.byNode[entry.Hash] = nodeKeys
			}
			nodeKeys[key] = struct{}{}
			if !entry.ReachableAt.IsZero() {
				targets := idx.reachable[key.Target]
				if targets == nil {
					targets = make(map[node.Hash]time.Time)
					idx.reachable[key.Target] = targets
				}
				targets[entry.Hash] = entry.ReachableAt
			}
		}
	}
}

func addHash(list []node.Hash, pos map[node.Hash]int, hash node.Hash) []node.Hash {
	if _, exists := pos[hash]; exists {
		return list
	}
	pos[hash] = len(list)
	return append(list, hash)
}

func removeHash(list []node.Hash, pos map[node.Hash]int, hash node.Hash) []node.Hash {
	index, exists := pos[hash]
	if !exists {
		return list
	}
	last := len(list) - 1
	list[index] = list[last]
	pos[list[index]] = index
	list = list[:last]
	delete(pos, hash)
	return list
}

func sampleHashes(source []node.Hash, k int) []node.Hash {
	if len(source) == 0 {
		return nil
	}
	if k >= len(source) {
		out := make([]node.Hash, len(source))
		copy(out, source)
		rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	out := make([]node.Hash, 0, k)
	for len(out) < k {
		out = append(out, source[rand.IntN(len(source))])
	}
	return out
}
