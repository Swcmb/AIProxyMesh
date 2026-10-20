package egress

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
)

// Node status classes used by the pool filters.
const (
	StatusHealthy   = "healthy"
	StatusCooling   = "cooling"
	StatusDisabled  = "disabled"
	StatusUnsupport = "unsupported"
	StatusUnprobed  = "unprobed"
	StatusError     = "error"
)

// NodeQuery is the server-side filter, sort and page request for the pool.
type NodeQuery struct {
	Keyword      string
	Status       string
	Subscription string
	Protocol     string
	SortBy       string
	SortDesc     bool
	Offset       int
	Limit        int
}

// NodeView is one pool row: the node snapshot plus its scheduling verdict for
// the primary upstream target.
type NodeView struct {
	node.Snapshot
	Status   string        `json:"status"`
	Score    routing.Score `json:"score"`
	Eligible bool          `json:"eligible"`
}

// NodePage is a filtered view of the whole pool. Total counts the filtered
// rows, not the pool, so the pager reflects the active query.
type NodePage struct {
	Enabled           bool              `json:"enabled"`
	Nodes             []NodeView        `json:"nodes"`
	Total             int               `json:"total"`
	Pool              int               `json:"pool"`
	Counts            Counts            `json:"counts"`
	Facets            NodeFacets        `json:"facets"`
	SubscriptionNames map[string]string `json:"subscription_names"`
}

// NodeFacets lists the values each filter accepts, so the UI never hardcodes
// protocol or subscription names.
type NodeFacets struct {
	Protocols     []string `json:"protocols"`
	Subscriptions []string `json:"subscriptions"`
	Statuses      []string `json:"statuses"`
}

const (
	defaultNodePageSize = 50
	maxNodePageSize     = 5000
)

// nodeMatch is the cheap per-node data needed to filter and sort. A full
// snapshot is only built for the nodes that actually make the page, because a
// pool can hold tens of thousands of exits.
type nodeMatch struct {
	entry      *node.Entry
	id         string
	name       string
	protocol   string
	status     string
	latency    float64
	hasLatency bool
	health     float64
	failures   int
	createdAt  time.Time
	lastProbe  time.Time
	score      routing.Score
	eligible   bool
}

// Nodes returns the pool after filtering, sorting and paging. Scores come from
// the router for the primary target; nodes the router cannot score keep a zero
// score and stay listed.
func (e *Engine) Nodes(query NodeQuery) NodePage {
	target := e.PrimaryTarget()
	now := time.Now()
	host := ""
	if target != nil {
		host = node.NormalizeHost(target.Hostname())
	}
	candidates := map[string]routing.Candidate{}
	if target != nil {
		for _, candidate := range e.router.Diagnose(target, nil) {
			candidates[candidate.ID] = candidate
		}
	}

	page := NodePage{
		Enabled:           true,
		SubscriptionNames: map[string]string{},
		Facets: NodeFacets{
			Statuses: []string{StatusHealthy, StatusCooling, StatusError, StatusUnprobed, StatusDisabled, StatusUnsupport},
		},
	}
	for _, status := range e.subs.Statuses() {
		page.SubscriptionNames[status.ID] = status.Name
	}

	protocols := map[string]struct{}{}
	subscriptions := map[string]struct{}{}
	keyword := strings.ToLower(strings.TrimSpace(query.Keyword))
	entries := e.pool.List()
	page.Pool = len(entries)
	matches := make([]nodeMatch, 0, len(entries))
	for _, entry := range entries {
		status := entryStatus(entry, now)
		match := nodeMatch{
			entry:     entry,
			id:        entry.ID(),
			name:      entry.Name(),
			protocol:  string(entry.Options.Type),
			status:    status,
			health:    entry.HealthScore(),
			failures:  int(entry.Failures()),
			createdAt: entry.CreatedAt,
		}
		if at := entry.LastProbeAt(); at > 0 {
			match.lastProbe = time.Unix(0, at).UTC()
		}
		if latency, ok := entry.LatencyFor(host); ok {
			match.latency = float64(latency.Microseconds()) / 1000
			match.hasLatency = true
		}
		if candidate, ok := candidates[match.id]; ok {
			match.score = candidate.Score
			match.eligible = candidate.Eligible
		}

		protocols[match.protocol] = struct{}{}
		for _, id := range entry.SubscriptionIDs() {
			subscriptions[id] = struct{}{}
		}
		switch status {
		case StatusCooling:
			page.Counts.Cooling++
		case StatusUnsupport:
			page.Counts.Unsupported++
		case StatusDisabled:
			page.Counts.Disabled++
		}
		if match.eligible {
			page.Counts.Eligible++
		}
		if !matchesQuery(match, entry, query, keyword, page.SubscriptionNames) {
			continue
		}
		matches = append(matches, match)
	}

	page.Facets.Protocols = sortedKeys(protocols)
	page.Facets.Subscriptions = sortedKeys(subscriptions)
	page.Counts.Total = len(matches)
	sortMatches(matches, query)
	page.Total = len(matches)

	limit := query.Limit
	switch {
	case limit <= 0:
		limit = defaultNodePageSize
	case limit > maxNodePageSize:
		limit = maxNodePageSize
	}
	offset := min(max(query.Offset, 0), len(matches))
	end := min(offset+limit, len(matches))
	page.Nodes = make([]NodeView, 0, end-offset)
	targetString := targetURLString(target)
	for _, match := range matches[offset:end] {
		page.Nodes = append(page.Nodes, NodeView{
			Snapshot: match.entry.Snapshot(targetString, now),
			Status:   match.status,
			Score:    match.score,
			Eligible: match.eligible,
		})
	}
	return page
}

// entryStatus classifies one entry for the status filter and badge.
func entryStatus(entry *node.Entry, now time.Time) string {
	switch {
	case !entry.Options.Supported():
		return StatusUnsupport
	case !entry.Enabled():
		return StatusDisabled
	case entry.CooldownUntil() > 0 && time.Unix(0, entry.CooldownUntil()).After(now):
		return StatusCooling
	case entry.LastError() != "":
		return StatusError
	case entry.LastProbeAt() == 0:
		return StatusUnprobed
	default:
		return StatusHealthy
	}
}

func matchesQuery(match nodeMatch, entry *node.Entry, query NodeQuery, keyword string, names map[string]string) bool {
	if query.Status != "" && query.Status != "all" && match.status != query.Status {
		return false
	}
	if query.Protocol != "" && query.Protocol != "all" && match.protocol != query.Protocol {
		return false
	}
	subscriptions := entry.SubscriptionIDs()
	if query.Subscription != "" && query.Subscription != "all" {
		found := false
		for _, id := range subscriptions {
			if id == query.Subscription {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if keyword == "" {
		return true
	}
	for _, candidate := range []string{match.name, match.id, entry.Options.Server, entry.Options.RawType, match.protocol} {
		if strings.Contains(strings.ToLower(candidate), keyword) {
			return true
		}
	}
	for _, alias := range entry.Aliases() {
		if strings.Contains(strings.ToLower(alias.Tag), keyword) ||
			strings.Contains(strings.ToLower(alias.Subscription), keyword) ||
			strings.Contains(strings.ToLower(names[alias.Subscription]), keyword) {
			return true
		}
	}
	for _, id := range subscriptions {
		if strings.Contains(strings.ToLower(id), keyword) || strings.Contains(strings.ToLower(names[id]), keyword) {
			return true
		}
	}
	return false
}

func sortMatches(matches []nodeMatch, query NodeQuery) {
	less := func(i, j int) bool {
		left, right := matches[i], matches[j]
		switch query.SortBy {
		case "name":
			return strings.ToLower(left.name) < strings.ToLower(right.name)
		case "latency":
			if !left.hasLatency {
				return false
			}
			if !right.hasLatency {
				return true
			}
			return left.latency < right.latency
		case "health":
			return left.health > right.health
		case "failures":
			return left.failures > right.failures
		case "created_at":
			return left.createdAt.After(right.createdAt)
		case "last_probe_at":
			return left.lastProbe.After(right.lastProbe)
		default:
			if left.score.Total != right.score.Total {
				return left.score.Total > right.score.Total
			}
			return strings.ToLower(left.name) < strings.ToLower(right.name)
		}
	}
	sort.SliceStable(matches, less)
	if query.SortDesc {
		sort.SliceStable(matches, func(i, j int) bool { return less(j, i) })
	}
}

func targetURLString(target *url.URL) string {
	if target == nil {
		return ""
	}
	return target.String()
}

// NodeDetail returns one node with the same scheduling view as the list.
func (e *Engine) NodeDetail(id string) (NodeView, bool) {
	hash, err := node.ParseHex(id)
	if err != nil {
		return NodeView{}, false
	}
	entry := e.pool.Get(hash)
	if entry == nil {
		return NodeView{}, false
	}
	target := e.PrimaryTarget()
	now := time.Now()
	view := NodeView{Snapshot: entry.Snapshot(targetURLString(target), now), Status: entryStatus(entry, now)}
	if target != nil {
		for _, candidate := range e.router.Diagnose(target, nil) {
			if candidate.ID == view.ID {
				view.Score = candidate.Score
				view.Eligible = candidate.Eligible
				break
			}
		}
	}
	return view, true
}

func sortedKeys(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
