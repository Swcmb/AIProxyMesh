// Package node defines the egress node model, identity, health counters and
// per-target latency statistics. It intentionally depends only on the standard
// library so that Tier A exits stay dependency-free.
package node

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Protocol is the outbound scheme of a node. Tier A protocols are directly
// usable; any other scheme is kept visible but excluded from selection.
type Protocol string

const (
	Direct  Protocol = "direct"
	HTTP    Protocol = "http"
	HTTPS   Protocol = "https"
	SOCKS5  Protocol = "socks5"
	SOCKS5H Protocol = "socks5h"
	// Unsupported marks a parsed node whose protocol has no v1 outbound.
	Unsupported Protocol = "unsupported"
)

// Options is the canonical, hashable description of a node.
type Options struct {
	Type     Protocol `json:"type"`
	Server   string   `json:"server,omitempty"`
	Port     uint16   `json:"server_port,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	TLS      bool     `json:"tls,omitempty"`
	// Tag is display-only; Hash ignores it so the same exit from two
	// subscriptions collapses into one pool entry.
	Tag string `json:"tag,omitempty"`
	// RawType keeps the original protocol name for unsupported nodes (ss,
	// vmess, ...), so the UI can explain why a node is not pooled.
	RawType string `json:"raw_type,omitempty"`
}

// Supported reports whether the protocol has a v1 (Tier A) outbound.
func (p Protocol) Supported() bool {
	switch p {
	case Direct, HTTP, HTTPS, SOCKS5, SOCKS5H:
		return true
	default:
		return false
	}
}

// Supported reports whether the node can be used as a v1 (Tier A) exit.
func (o Options) Supported() bool {
	return o.Type.Supported()
}

// Addr returns host:port, or empty for direct/unsupported entries.
func (o Options) Addr() string {
	if o.Server == "" || o.Port == 0 {
		return ""
	}
	return net.JoinHostPort(o.Server, strconv.Itoa(int(o.Port)))
}

// ProxyURL builds the URL http.Transport.Proxy consumes. Direct yields nil.
func (o Options) ProxyURL() (*url.URL, error) {
	if o.Type == Direct {
		return nil, nil
	}
	if !o.Supported() {
		return nil, fmt.Errorf("unsupported protocol %q", o.Type)
	}
	scheme := string(o.Type)
	u := &url.URL{Scheme: scheme, Host: o.Addr()}
	if o.Username != "" || o.Password != "" {
		u.User = url.UserPassword(o.Username, o.Password)
	}
	return u, nil
}

// Hash is a 128-bit node identity derived from canonical JSON with Tag removed.
type Hash [16]byte

// Zero is the zero-value Hash.
var Zero Hash

// Hex returns the lowercase hex encoding of the hash.
func (h Hash) Hex() string { return hex.EncodeToString(h[:]) }

// String implements fmt.Stringer.
func (h Hash) String() string { return h.Hex() }

// IsZero reports whether h is the zero hash.
func (h Hash) IsZero() bool { return h == Zero }

// ParseHex decodes a 32-character hex string into a Hash.
func ParseHex(s string) (Hash, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return Zero, fmt.Errorf("parse node hash: %w", err)
	}
	if len(b) != 16 {
		return Zero, fmt.Errorf("parse node hash: expected 16 bytes, got %d", len(b))
	}
	var h Hash
	copy(h[:], b)
	return h, nil
}

// HashOptions derives the identity of a node from its options. Tag and
// RawType are excluded because they carry no routing meaning.
func HashOptions(o Options) Hash {
	o.Tag = ""
	o.RawType = ""
	raw, err := json.Marshal(o)
	if err != nil {
		// Options contains only plain scalars, so marshalling cannot fail.
		panic("node: marshal options: " + err.Error())
	}
	sum := sha256.Sum256(raw)
	var h Hash
	copy(h[:], sum[:16])
	return h
}

// Entry is one node in the global pool. Static fields are immutable; the rest
// is guarded or atomic because request and probe goroutines touch it
// concurrently.
type Entry struct {
	Hash      Hash
	Raw       json.RawMessage
	Options   Options
	CreatedAt time.Time

	mu              sync.RWMutex
	subscriptionIDs []string
	aliases         map[string]string
	lastError       string
	cooldownReason  string

	enabled       atomic.Bool
	failures      atomic.Int32
	cooldownUntil atomic.Int64
	// cooldownSince is when the current uninterrupted cooling streak began.
	// Ephemeral cleanup evicts nodes that stay circuit-open too long.
	cooldownSince atomic.Int64
	// everHealthy turns true after the first successful probe or passthrough
	// request. New nodes stay unroutable until then, mirroring Resin's
	// default-circuit-open behaviour.
	everHealthy atomic.Bool
	// lastSuccess is when the node last proved it can carry traffic. The router
	// prefers recently verified exits over the unverified mass of the pool.
	lastSuccess atomic.Int64

	egressIP         atomic.Pointer[string]
	lastEgressUpdate atomic.Int64
	// region caches the GeoIP country of the exit address so region filters do
	// not re-run an mmdb lookup per candidate per request.
	region atomic.Pointer[string]

	netSuccess atomic.Uint64
	netFailure atomic.Uint64
	probeOK    atomic.Uint64
	probeFail  atomic.Uint64
	lastProbe  atomic.Int64

	latency  *LatencyTable
	upstream sync.Map // target host -> *UpstreamStats
}

// UpstreamStats counts attempts that reached the upstream through this exit,
// split by whether the upstream accepted the request.
type UpstreamStats struct {
	Success atomic.Uint64
	Failure atomic.Uint64
}

// NewEntry creates an entry. maxLatencyEntries <= 0 disables latency tracking.
func NewEntry(h Hash, raw json.RawMessage, o Options, now time.Time, maxLatencyEntries int) *Entry {
	e := &Entry{Hash: h, Raw: raw, Options: o, CreatedAt: now}
	e.enabled.Store(true)
	// The direct exit is the local connection itself; there is no proxy hop to
	// validate, so it starts trusted instead of waiting for a probe.
	if o.Type == Direct {
		e.everHealthy.Store(true)
	}
	if maxLatencyEntries > 0 {
		e.latency = NewLatencyTable(maxLatencyEntries)
	}
	return e
}

// ID returns the stable hex identity.
func (e *Entry) ID() string { return e.Hash.Hex() }

// Name returns the display tag, falling back to the endpoint.
func (e *Entry) Name() string {
	if e.Options.Tag != "" {
		return e.Options.Tag
	}
	if e.Options.Type == Direct {
		return "direct"
	}
	if addr := e.Options.Addr(); addr != "" {
		return addr
	}
	return e.ID()[:12]
}

// SubscriptionIDs returns a copy of the referencing subscription IDs.
func (e *Entry) SubscriptionIDs() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]string(nil), e.subscriptionIDs...)
}

// AddSubscriptionID records a reference, idempotently.
func (e *Entry) AddSubscriptionID(id string) {
	if id == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, existing := range e.subscriptionIDs {
		if existing == id {
			return
		}
	}
	e.subscriptionIDs = append(e.subscriptionIDs, id)
}

// SetAlias records the name one subscription gives this node. One exit shared
// by several subscriptions keeps every name, so the pool view can show them
// all instead of only the first one seen.
func (e *Entry) SetAlias(subscription, tag string) {
	if subscription == "" || tag == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.aliases == nil {
		e.aliases = map[string]string{}
	}
	e.aliases[subscription] = tag
}

// Aliases returns every subscription-scoped name, ordered by subscription ID.
func (e *Entry) Aliases() []Alias {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Alias, 0, len(e.aliases))
	for subscription, tag := range e.aliases {
		result = append(result, Alias{Subscription: subscription, Tag: tag})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Subscription < result[j].Subscription })
	return result
}

// RemoveSubscriptionID drops a reference and reports whether none remain.
func (e *Entry) RemoveSubscriptionID(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, existing := range e.subscriptionIDs {
		if existing == id {
			e.subscriptionIDs = append(e.subscriptionIDs[:i], e.subscriptionIDs[i+1:]...)
			break
		}
	}
	delete(e.aliases, id)
	return len(e.subscriptionIDs) == 0
}

// SetLastError stores the most recent failure reason for display.
func (e *Entry) SetLastError(msg string) {
	e.mu.Lock()
	e.lastError = msg
	e.mu.Unlock()
}

// LastError returns the most recent failure reason.
func (e *Entry) LastError() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastError
}

// Enabled reports the manual switch state.
func (e *Entry) Enabled() bool { return e.enabled.Load() }

// SetEnabled flips the manual switch.
func (e *Entry) SetEnabled(v bool) { e.enabled.Store(v) }

// Failures returns the consecutive node-level failure count.
func (e *Entry) Failures() int { return int(e.failures.Load()) }

// Cooling reports whether the node is inside its cooldown window.
func (e *Entry) Cooling(now time.Time) bool {
	until := e.cooldownUntil.Load()
	return until != 0 && now.UnixNano() < until
}

// CooldownUntil returns the cooldown deadline in Unix nanoseconds (0 = none).
func (e *Entry) CooldownUntil() int64 { return e.cooldownUntil.Load() }

// CooldownSince returns when the current cooling streak started in Unix
// nanoseconds (0 = not cooling).
func (e *Entry) CooldownSince() int64 { return e.cooldownSince.Load() }

// RestoreCooldown reloads a persisted cooldown on startup.
func (e *Entry) RestoreCooldown(failures int32, untilNanos int64) {
	e.failures.Store(failures)
	e.cooldownUntil.Store(untilNanos)
}

// RestoreHealth reloads persisted health metadata. everHealthy defaults to
// true for legacy records so an upgrade does not empty the routing pool while
// the first probe sweep runs.
func (e *Entry) RestoreHealth(everHealthy bool, cooldownSinceNanos int64, egressIP string, lastEgressNanos int64) {
	e.everHealthy.Store(everHealthy)
	e.cooldownSince.Store(cooldownSinceNanos)
	if egressIP != "" {
		value := egressIP
		e.egressIP.Store(&value)
	}
	e.lastEgressUpdate.Store(lastEgressNanos)
}

// MarkHealthy records the first positive reachability signal.
func (e *Entry) MarkHealthy() {
	e.everHealthy.Store(true)
	e.lastSuccess.Store(time.Now().UnixNano())
}

// EverHealthy reports whether the node has ever produced a successful
// connection. A node that is still false is kept out of routing.
func (e *Entry) EverHealthy() bool { return e.everHealthy.Load() }

// LastSuccessAt returns the last successful traffic signal in Unix
// nanoseconds (0 = never).
func (e *Entry) LastSuccessAt() int64 { return e.lastSuccess.Load() }

// RestoreLastSuccess reloads the persisted success timestamp.
func (e *Entry) RestoreLastSuccess(at int64) { e.lastSuccess.Store(at) }

// Region returns the cached GeoIP country code (lowercase), if resolved.
func (e *Entry) Region() string {
	if value := e.region.Load(); value != nil {
		return *value
	}
	return ""
}

// SetRegion caches the GeoIP country code.
func (e *Entry) SetRegion(country string) {
	if country == "" {
		return
	}
	value := country
	e.region.Store(&value)
}

// EgressIP returns the last observed public exit address, if any.
func (e *Entry) EgressIP() string {
	if value := e.egressIP.Load(); value != nil {
		return *value
	}
	return ""
}

// LastEgressAttempt returns the last egress-IP probe attempt in Unix
// nanoseconds (0 = never).
func (e *Entry) LastEgressAttempt() int64 { return e.lastEgressUpdate.Load() }

// RecordEgressAttempt stores the attempt time and, when ip is non-empty, the
// observed exit address.
func (e *Entry) RecordEgressAttempt(ip string, at time.Time) {
	if ip != "" {
		value := ip
		e.egressIP.Store(&value)
	}
	e.lastEgressUpdate.Store(at.UnixNano())
}

// RecordSuccess clears the failure streak, cooldown and its reason.
func (e *Entry) RecordSuccess() {
	e.failures.Store(0)
	e.cooldownUntil.Store(0)
	e.cooldownSince.Store(0)
	e.everHealthy.Store(true)
	e.lastSuccess.Store(time.Now().UnixNano())
	e.mu.Lock()
	e.cooldownReason = ""
	e.mu.Unlock()
}

// RecordCooldown starts a cooldown until the given deadline.
func (e *Entry) RecordCooldown(until time.Time) {
	e.RecordCooldownReason(until, "")
}

// RecordCooldownReason starts a cooldown and records why it was applied, so a
// later upstream-consensus verdict can clear only the cooldowns it invalidates.
// The first cooldown of an uninterrupted streak stamps CooldownSince.
func (e *Entry) RecordCooldownReason(until time.Time, reason string) {
	e.failures.Add(1)
	if until.IsZero() {
		until = time.Now()
	}
	now := time.Now()
	// A cooldown that already expired means the previous cooling streak ended
	// without a recorded success; the new failure starts a fresh streak.
	if prev := e.cooldownUntil.Load(); prev == 0 || now.UnixNano() >= prev {
		e.cooldownSince.Store(now.UnixNano())
	}
	e.cooldownUntil.Store(until.UnixNano())
	e.mu.Lock()
	e.cooldownReason = reason
	e.mu.Unlock()
}

// ClearCooldownFor resets the node penalty only when the active cooldown was
// created for the given reason. It returns whether anything was cleared.
func (e *Entry) ClearCooldownFor(reason string) bool {
	if reason == "" {
		return false
	}
	e.mu.Lock()
	if e.cooldownReason != reason {
		e.mu.Unlock()
		return false
	}
	e.cooldownReason = ""
	e.mu.Unlock()
	e.failures.Store(0)
	e.cooldownUntil.Store(0)
	e.cooldownSince.Store(0)
	return true
}

// RecordNetworkResult feeds the health score. ok means the exit produced an
// HTTP response (any status) rather than a transport failure.
func (e *Entry) RecordNetworkResult(ok bool) {
	if ok {
		e.netSuccess.Add(1)
	} else {
		e.netFailure.Add(1)
	}
}

// RecordProbeResult stores an active-probe outcome and its timestamp.
func (e *Entry) RecordProbeResult(ok bool, at time.Time) {
	if ok {
		e.probeOK.Add(1)
		e.everHealthy.Store(true)
		e.lastSuccess.Store(at.UnixNano())
	} else {
		e.probeFail.Add(1)
	}
	e.lastProbe.Store(at.UnixNano())
}

// LastProbeAt returns the last probe attempt time in Unix nanoseconds.
func (e *Entry) LastProbeAt() int64 { return e.lastProbe.Load() }

// HealthScore is a smoothed estimate in [0,1] of the exit being reachable.
// Successful HTTP responses count even when the upstream later rejects them.
func (e *Entry) HealthScore() float64 {
	ok := float64(e.netSuccess.Load() + e.probeOK.Load())
	fail := float64(e.netFailure.Load() + e.probeFail.Load())
	return (ok + 1) / (ok + fail + 2)
}

// UpstreamScore returns the smoothed upstream success rate for one target.
// Targets without samples start at the 0.5 prior to encourage exploration.
func (e *Entry) UpstreamScore(target string) float64 {
	stats := e.upstreamStats(target)
	if stats == nil {
		return 0.5
	}
	ok := float64(stats.Success.Load())
	fail := float64(stats.Failure.Load())
	return (ok + 1) / (ok + fail + 2)
}

func (e *Entry) upstreamStats(target string) *UpstreamStats {
	if target == "" {
		return nil
	}
	if value, ok := e.upstream.Load(target); ok {
		return value.(*UpstreamStats)
	}
	return nil
}

// RecordUpstreamResult attributes an upstream outcome to this exit.
func (e *Entry) RecordUpstreamResult(target string, ok bool) {
	if target == "" {
		return
	}
	value, _ := e.upstream.LoadOrStore(target, &UpstreamStats{})
	stats := value.(*UpstreamStats)
	if ok {
		stats.Success.Add(1)
		e.everHealthy.Store(true)
		e.lastSuccess.Store(time.Now().UnixNano())
	} else {
		stats.Failure.Add(1)
	}
}

// UpstreamSnapshot exposes per-target counts for persistence and diagnostics.
func (e *Entry) UpstreamSnapshot() map[string][2]uint64 {
	out := make(map[string][2]uint64)
	e.upstream.Range(func(key, value any) bool {
		stats := value.(*UpstreamStats)
		out[key.(string)] = [2]uint64{stats.Success.Load(), stats.Failure.Load()}
		return true
	})
	return out
}

// RestoreUpstreamStats reloads persisted attribution counters.
func (e *Entry) RestoreUpstreamStats(target string, success, failure uint64) {
	if target == "" {
		return
	}
	value, _ := e.upstream.LoadOrStore(target, &UpstreamStats{})
	stats := value.(*UpstreamStats)
	stats.Success.Store(success)
	stats.Failure.Store(failure)
}

// Latency returns the per-target latency table, or nil when disabled.
func (e *Entry) Latency() *LatencyTable { return e.latency }

// LatencyFor returns the smoothed latency for a target host.
func (e *Entry) LatencyFor(host string) (time.Duration, bool) {
	if e.latency == nil {
		return 0, false
	}
	if stats, ok := e.latency.Get(host); ok {
		return stats.EWMA, true
	}
	return e.latency.Average()
}

// Snapshot is the read-only view used by the management API.
type Snapshot struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Protocol      string     `json:"protocol"`
	RawType       string     `json:"raw_type,omitempty"`
	Server        string     `json:"server,omitempty"`
	Port          uint16     `json:"port,omitempty"`
	TLS           bool       `json:"tls,omitempty"`
	Enabled       bool       `json:"enabled"`
	Supported     bool       `json:"supported"`
	Subscriptions []string   `json:"subscriptions"`
	Aliases       []Alias    `json:"aliases,omitempty"`
	Failures      int        `json:"failures"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	CooldownSince *time.Time `json:"cooldown_since,omitempty"`
	EverHealthy   bool       `json:"ever_healthy"`
	EgressIP      string     `json:"egress_ip,omitempty"`
	HealthScore   float64    `json:"health_score"`
	LatencyMS     float64    `json:"latency_ms,omitempty"`
	HasLatency    bool       `json:"has_latency"`
	LastError     string     `json:"last_error,omitempty"`
	LastProbeAt   *time.Time `json:"last_probe_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Alias is the name one subscription gives to a shared exit.
type Alias struct {
	Subscription string `json:"subscription"`
	Tag          string `json:"tag,omitempty"`
}

// Snapshot builds a management view. target supplies the upstream host whose
// latency and success rate are reported.
func (e *Entry) Snapshot(target string, now time.Time) Snapshot {
	snap := Snapshot{
		ID:            e.ID(),
		Name:          e.Name(),
		Protocol:      string(e.Options.Type),
		RawType:       e.Options.RawType,
		Server:        e.Options.Server,
		Port:          e.Options.Port,
		TLS:           e.Options.TLS,
		Enabled:       e.Enabled(),
		Supported:     e.Options.Supported(),
		Subscriptions: e.SubscriptionIDs(),
		Aliases:       e.Aliases(),
		Failures:      e.Failures(),
		EverHealthy:   e.EverHealthy(),
		EgressIP:      e.EgressIP(),
		HealthScore:   e.HealthScore(),
		LastError:     e.LastError(),
		CreatedAt:     e.CreatedAt,
	}
	if until := e.CooldownUntil(); until > 0 {
		value := time.Unix(0, until).UTC()
		snap.CooldownUntil = &value
	}
	if since := e.CooldownSince(); since > 0 {
		value := time.Unix(0, since).UTC()
		snap.CooldownSince = &value
	}
	if host := targetHost(target); host != "" {
		if latency, ok := e.LatencyFor(host); ok {
			snap.LatencyMS = float64(latency.Microseconds()) / 1000
			snap.HasLatency = true
		}
	}
	if at := e.LastProbeAt(); at > 0 {
		value := time.Unix(0, at).UTC()
		snap.LastProbeAt = &value
	}
	if at := e.LastSuccessAt(); at > 0 {
		value := time.Unix(0, at).UTC()
		snap.LastSuccessAt = &value
	}
	return snap
}

func targetHost(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return strings.ToLower(raw)
}
