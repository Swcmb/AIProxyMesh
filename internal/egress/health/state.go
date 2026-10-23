package health

import (
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// State is one key-level lifecycle state.
type State string

const (
	Candidate State = "candidate"
	Verifying State = "verifying"
	Active    State = "active"
	Suspect   State = "suspect"
	Open      State = "open"
	HalfOpen  State = "half_open"
	Dead      State = "dead"
)

// Routable reports whether the state may serve real traffic.
func (s State) Routable() bool {
	switch s {
	case Active, Suspect, HalfOpen:
		return true
	}
	return false
}

// Pinned reports whether the state is protected from table eviction.
func (s State) Pinned() bool {
	switch s {
	case Active, Suspect, Open, HalfOpen:
		return true
	}
	return false
}

// StreamResult is the result of the complete stream, separate from first-event
// validity.
type StreamResult string

const (
	StreamNone    StreamResult = ""
	StreamSuccess StreamResult = "success"
	StreamFailure StreamResult = "failure"
)

// OutcomeKind classifies one observation for health accounting. It mirrors the
// gateway's attribution taxonomy but stays independent of the router package.
type OutcomeKind string

const (
	OutcomeSuccess            OutcomeKind = "success"
	OutcomeInvalidInference   OutcomeKind = "invalid_inference"
	OutcomeNonTarget2xx       OutcomeKind = "non_target_2xx"
	OutcomeTransportFailure   OutcomeKind = "transport_failure"
	OutcomeExitFailure        OutcomeKind = "exit_failure"
	OutcomeRateLimited        OutcomeKind = "rate_limited"
	OutcomeNotFound           OutcomeKind = "not_found"
	OutcomeKeyFailure         OutcomeKind = "key_failure"
	OutcomeRegionBlocked      OutcomeKind = "region_blocked"
	OutcomeClientCancel       OutcomeKind = "client_cancel"
	OutcomeUpstreamWide       OutcomeKind = "upstream_wide"
	OutcomeNeutral            OutcomeKind = "neutral"
	OutcomeBootstrapFailure   OutcomeKind = "bootstrap_failure"
	OutcomeEnvironmentFailure OutcomeKind = "environment_failure"
	OutcomeEnvironmentSlow    OutcomeKind = "environment_degraded"
)

// Observation is one classified attempt result.
type Observation struct {
	Key             HealthKey
	ValidRequired   bool
	Kind            OutcomeKind
	Status          int
	HeadersReceived bool
	ValidInference  bool
	ValidKnown      bool
	Stream          StreamResult
	TTFB            time.Duration
	Total           time.Duration
	RetryAfter      time.Duration
	Diagnostic      bool
	Direct          bool
}

// Config is the health engine tuning. Zero values fall back to the approved
// defaults through Normalize.
type Config struct {
	ConfidenceZ            float64
	MinSamples             int
	ValidMinSamples        int
	EvidenceBucketSeconds  int
	QuotaBucketSeconds     int
	MaxSamplesPerBucket    int
	MinEvidenceBuckets     int
	HTTP200Target          float64
	ValidTarget            float64
	DecayHalfLife          time.Duration
	RecentQuantile         float64
	SuspectRecentLower     float64
	MinRecentWeight        float64
	SuspectFailures        int
	SuspectLatencySamples  int
	SuspectRecoverySuccess int
	ReverifySuccesses      int
	UnknownStreakSuspect   int
	StreamFailureSuspect   int
	OpenFailures           int
	OpenRateWindow         time.Duration
	OpenRateThreshold      float64
	HalfOpenSuccesses      int
	HalfOpenMaxInflight    int
	DeadConfirmations      int
	DeadRecheck            time.Duration
	LatencyBudget          time.Duration
	BackoffBase            time.Duration
	BackoffMax             time.Duration
	JitterRatio            float64
}

// DefaultConfig returns the approved defaults.
func DefaultConfig() Config {
	return Config{
		ConfidenceZ:            1.96,
		MinSamples:             96,
		ValidMinSamples:        46,
		EvidenceBucketSeconds:  60,
		QuotaBucketSeconds:     60,
		MaxSamplesPerBucket:    10,
		MinEvidenceBuckets:     5,
		HTTP200Target:          0.95,
		ValidTarget:            0.90,
		DecayHalfLife:          time.Hour,
		RecentQuantile:         0.10,
		SuspectRecentLower:     0.50,
		MinRecentWeight:        10,
		SuspectFailures:        2,
		SuspectLatencySamples:  5,
		SuspectRecoverySuccess: 3,
		ReverifySuccesses:      3,
		UnknownStreakSuspect:   5,
		StreamFailureSuspect:   2,
		OpenFailures:           3,
		OpenRateWindow:         10 * time.Minute,
		OpenRateThreshold:      0.50,
		HalfOpenSuccesses:      3,
		HalfOpenMaxInflight:    1,
		DeadConfirmations:      2,
		DeadRecheck:            6 * time.Hour,
		LatencyBudget:          8 * time.Second,
		BackoffBase:            30 * time.Second,
		BackoffMax:             30 * time.Minute,
		JitterRatio:            0.5,
	}
}

// Normalize clamps nonsensical values to the approved defaults.
func (c Config) Normalize() Config {
	def := DefaultConfig()
	positive := func(value *float64, target float64) {
		if *value <= 0 {
			*value = target
		}
	}
	if c.ConfidenceZ <= 0 {
		c.ConfidenceZ = def.ConfidenceZ
	}
	if c.MinSamples <= 0 {
		c.MinSamples = def.MinSamples
	}
	if c.ValidMinSamples <= 0 {
		c.ValidMinSamples = def.ValidMinSamples
	}
	if c.EvidenceBucketSeconds <= 0 {
		c.EvidenceBucketSeconds = def.EvidenceBucketSeconds
	}
	if c.QuotaBucketSeconds <= 0 {
		c.QuotaBucketSeconds = def.QuotaBucketSeconds
	}
	if c.MaxSamplesPerBucket <= 0 {
		c.MaxSamplesPerBucket = def.MaxSamplesPerBucket
	}
	if c.MinEvidenceBuckets <= 0 {
		c.MinEvidenceBuckets = def.MinEvidenceBuckets
	}
	positive(&c.HTTP200Target, def.HTTP200Target)
	positive(&c.ValidTarget, def.ValidTarget)
	if c.DecayHalfLife <= 0 {
		c.DecayHalfLife = def.DecayHalfLife
	}
	positive(&c.RecentQuantile, def.RecentQuantile)
	positive(&c.SuspectRecentLower, def.SuspectRecentLower)
	if c.MinRecentWeight <= 0 {
		c.MinRecentWeight = def.MinRecentWeight
	}
	if c.SuspectFailures <= 0 {
		c.SuspectFailures = def.SuspectFailures
	}
	if c.SuspectLatencySamples <= 0 {
		c.SuspectLatencySamples = def.SuspectLatencySamples
	}
	if c.SuspectRecoverySuccess <= 0 {
		c.SuspectRecoverySuccess = def.SuspectRecoverySuccess
	}
	if c.ReverifySuccesses <= 0 {
		c.ReverifySuccesses = def.ReverifySuccesses
	}
	if c.UnknownStreakSuspect <= 0 {
		c.UnknownStreakSuspect = def.UnknownStreakSuspect
	}
	if c.StreamFailureSuspect <= 0 {
		c.StreamFailureSuspect = def.StreamFailureSuspect
	}
	if c.OpenFailures <= 0 {
		c.OpenFailures = def.OpenFailures
	}
	if c.OpenRateWindow <= 0 {
		c.OpenRateWindow = def.OpenRateWindow
	}
	positive(&c.OpenRateThreshold, def.OpenRateThreshold)
	if c.HalfOpenSuccesses <= 0 {
		c.HalfOpenSuccesses = def.HalfOpenSuccesses
	}
	if c.HalfOpenMaxInflight <= 0 {
		c.HalfOpenMaxInflight = def.HalfOpenMaxInflight
	}
	if c.DeadConfirmations <= 0 {
		c.DeadConfirmations = def.DeadConfirmations
	}
	if c.DeadRecheck <= 0 {
		c.DeadRecheck = def.DeadRecheck
	}
	if c.LatencyBudget <= 0 {
		c.LatencyBudget = def.LatencyBudget
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = def.BackoffBase
	}
	if c.BackoffMax < c.BackoffBase {
		c.BackoffMax = def.BackoffMax
	}
	if c.JitterRatio < 0 {
		c.JitterRatio = def.JitterRatio
	}
	return c
}

// Transition describes a state change produced by one observation.
type Transition struct {
	From    State
	To      State
	Reason  string
	Changed bool
}

// stateMutex hides sync.Mutex behind enter/leave so KeyStats does not itself
// implement sync.Locker and can still be copied into a snapshot value.
type stateMutex struct {
	mu sync.Mutex
}

func newStateMutex() *stateMutex { return &stateMutex{} }

func (m *stateMutex) enter() { m.mu.Lock() }
func (m *stateMutex) leave() { m.mu.Unlock() }

// KeyStats is the complete in-memory health record of one key.
//
// mu is a pointer so the record can be copied into an immutable snapshot
// without copying a lock.
type KeyStats struct {
	mu *stateMutex

	Key           HealthKey
	ValidRequired bool

	HTTP200Success         uint32
	HTTP200Failure         uint32
	ValidSuccess           uint32
	ValidFailure           uint32
	PrimarySuccess         uint32
	PrimaryFailure         uint32
	StreamSuccess          uint32
	StreamFailure          uint32
	ConsecutiveStreamFails uint8
	RateLimited            uint32
	NotFoundCount          uint8
	LastNotFoundAt         time.Time
	UnknownStreak          uint8
	LastUnknownAt          time.Time

	AdmissionHTTP200Success uint32
	AdmissionHTTP200Failure uint32
	AdmissionValidSuccess   uint32
	AdmissionValidFailure   uint32
	AdmissionHTTPBuckets    uint16
	AdmissionValidBuckets   uint16
	BucketStart             time.Time
	HTTPSeen                uint32
	ValidSeen               uint32
	HTTPReservoir           Reservoir
	ValidReservoir          Reservoir
	HTTPCheckRound          uint8
	ValidCheckRound         uint8

	Window Window
	// RateWindow tracks how often this key's upstream answered at all versus
	// answering with 429. It is the quota-freshness evidence used for ranking;
	// the primary metrics stay neutral about rate limiting.
	RateWindow            Window
	LastCheckSamples      uint32
	LastCheckValidSamples uint32

	LastReachableAt time.Time

	Beta200   BetaState
	BetaValid BetaState

	TTFBEWMA     time.Duration
	TotalEWMA    time.Duration
	LatencyHist  LatencyHist
	TTFBSamples  uint32
	LastSampleAt time.Time

	State              State
	PrevState          State
	EnvironmentEpoch   uint64
	PendingReverify    bool
	ReverifySuccess    uint8
	ActiveEpoch        uint32
	ConsecutiveFails   uint8
	ConsecutiveSuccess uint16
	OpenCount          uint8
	OpenUntil          time.Time
	RecoveryStart      time.Time
	EpochSuccess       uint8
	EpochFailure       uint8
	HalfOpenSuccess    uint8
	LastSuccessAt      time.Time
	LastFailureAt      time.Time
	LastProbeAt        time.Time
	NextProbeAt        time.Time
	// ProbeEnqueued marks the key has entered the scheduler heap at least once;
	// it drives the anti-starvation bonus.
	ProbeEnqueued bool
	LastStatus    int
	LastError     string
	StateVersion  uint64
}

// NewKeyStats creates a fresh candidate record.
func NewKeyStats(key HealthKey, cfg Config, now time.Time) *KeyStats {
	cfg = cfg.Normalize()
	return &KeyStats{
		mu:             newStateMutex(),
		Key:            key,
		ValidRequired:  key.ValidRequired(),
		State:          Candidate,
		HTTPReservoir:  NewReservoir(cfg.MaxSamplesPerBucket),
		ValidReservoir: NewReservoir(cfg.MaxSamplesPerBucket),
		Beta200:        EmptyBeta(now),
		BetaValid:      EmptyBeta(now),
		Window:         NewWindow(cfg.EvidenceBucketSeconds),
		RateWindow:     NewWindow(cfg.QuotaBucketSeconds),
		BucketStart:    now.Truncate(time.Duration(cfg.EvidenceBucketSeconds) * time.Second),
	}
}

// FromSnapshot rebuilds a record from persisted state, re-decaying the Beta
// posteriors to now.
func FromSnapshot(snapshot KeyStats, cfg Config, now time.Time) *KeyStats {
	cfg = cfg.Normalize()
	out := snapshot
	out.mu = newStateMutex()
	if out.HTTPReservoir.K == 0 {
		out.HTTPReservoir.K = cfg.MaxSamplesPerBucket
	}
	if out.ValidReservoir.K == 0 {
		out.ValidReservoir.K = cfg.MaxSamplesPerBucket
	}
	if out.Window.BucketSeconds == 0 {
		out.Window = NewWindow(cfg.EvidenceBucketSeconds)
	}
	if out.RateWindow.BucketSeconds == 0 {
		out.RateWindow = NewWindow(cfg.QuotaBucketSeconds)
	}
	out.Beta200.DecayTo(now, cfg.DecayHalfLife)
	out.BetaValid.DecayTo(now, cfg.DecayHalfLife)
	if out.State == "" {
		out.State = Candidate
	}
	return &out
}

// Acquire locks the record for atomic multi-field updates.
func (s *KeyStats) Acquire() {
	if s.mu == nil {
		s.mu = newStateMutex()
	}
	s.mu.enter()
}

// Release unlocks the record.
func (s *KeyStats) Release() {
	if s.mu != nil {
		s.mu.leave()
	}
}

// StateOf returns the current state under lock.
func (s *KeyStats) StateOf() State {
	s.Acquire()
	defer s.Release()
	return s.State
}

// ProbeSnapshot is the small view the scheduler needs. It avoids copying
// reservoirs, histograms and counters that a scan never reads.
type ProbeSnapshot struct {
	Key             HealthKey
	State           State
	LastProbeAt     time.Time
	NextProbeAt     time.Time
	LastReachableAt time.Time
	OpenUntil       time.Time
	PrimarySuccess  uint32
	PrimaryFailure  uint32
	ProbeEnqueued   bool
	BetaWeight      float64
	FailureRate     float64
}

// ProbeSnapshot returns the scheduler view under the key lock.
func (s *KeyStats) ProbeSnapshot(now time.Time) ProbeSnapshot {
	s.Acquire()
	defer s.Release()
	rate, _ := s.Window.FailureRate(now)
	return ProbeSnapshot{
		Key: s.Key, State: s.State,
		LastProbeAt: s.LastProbeAt, NextProbeAt: s.NextProbeAt,
		LastReachableAt: s.LastReachableAt, OpenUntil: s.OpenUntil,
		PrimarySuccess: s.PrimarySuccess, PrimaryFailure: s.PrimaryFailure, ProbeEnqueued: s.ProbeEnqueued,
		BetaWeight: s.Beta200.RecentWeight(), FailureRate: rate,
	}
}

// Snapshot copies the record for persistence and API rendering.
func (s *KeyStats) Snapshot() KeyStats {
	s.Acquire()
	defer s.Release()
	out := *s
	out.mu = nil
	out.HTTPReservoir = Reservoir{K: s.HTTPReservoir.K, Seen: s.HTTPReservoir.Seen, Samples: s.HTTPReservoir.Snapshot()}
	out.ValidReservoir = Reservoir{K: s.ValidReservoir.K, Seen: s.ValidReservoir.Seen, Samples: s.ValidReservoir.Snapshot()}
	return out
}

// QuotaFreshness reports the share of recently answered requests on this key
// that were not rate limited. A key nobody has answered yet counts as fresh, so
// an unprobed exit is never penalised for evidence it could not have produced.
func QuotaFreshness(window Window, now time.Time) float64 {
	rate, samples := window.FailureRate(now)
	if samples == 0 {
		return 1
	}
	return 1 - rate
}

// Rate returns the raw HTTP 200 success ratio for diagnostics.
func (s *KeyStats) Rate() float64 {
	s.Acquire()
	defer s.Release()
	return s.rateLocked()
}

func (s *KeyStats) rateLocked() float64 {
	total := s.HTTP200Success + s.HTTP200Failure
	if total == 0 {
		return 0
	}
	return float64(s.HTTP200Success) / float64(total)
}

// AdmissionSamples returns the closed-bucket admission counts.
func (s *KeyStats) AdmissionSamples() (http int, valid int) {
	s.Acquire()
	defer s.Release()
	return s.admissionSamplesLocked()
}

func (s *KeyStats) admissionSamplesLocked() (int, int) {
	return int(s.AdmissionHTTP200Success + s.AdmissionHTTP200Failure),
		int(s.AdmissionValidSuccess + s.AdmissionValidFailure)
}

// LowerBounds returns the current admission Wilson lower bounds. A negative
// value means the evidence is below the checkpoint requirement.
func (s *KeyStats) LowerBounds(cfg Config) (httpLower, validLower float64) {
	cfg = cfg.Normalize()
	s.Acquire()
	defer s.Release()
	httpSamples, validSamples := s.admissionSamplesLocked()
	httpLower, validLower = -1, -1
	if httpSamples >= cfg.MinSamples {
		z := CheckpointZ(cfg.ConfidenceZ, int(s.HTTPCheckRound)+1)
		httpLower = WilsonLower(s.AdmissionHTTP200Success, s.AdmissionHTTP200Failure, z)
	}
	if s.ValidRequired && validSamples >= cfg.ValidMinSamples {
		z := CheckpointZ(cfg.ConfidenceZ, int(s.ValidCheckRound)+1)
		validLower = WilsonLower(s.AdmissionValidSuccess, s.AdmissionValidFailure, z)
	}
	return httpLower, validLower
}

// Tick advances clock-driven transitions (bucket folding and OPEN to
// HALF_OPEN) without a new observation.
func (s *KeyStats) Tick(now time.Time, cfg Config) Transition {
	cfg = cfg.Normalize()
	s.Acquire()
	defer s.Release()
	s.foldBucket(now, cfg)
	if s.State == Open && !s.OpenUntil.IsZero() && !now.Before(s.OpenUntil) {
		return s.transition(HalfOpen, "open_expired")
	}
	return Transition{From: s.State, To: s.State}
}

// Apply folds one observation into the record and evaluates transitions.
func (s *KeyStats) Apply(obs Observation, cfg Config, now time.Time) Transition {
	cfg = cfg.Normalize()
	s.Acquire()
	defer s.Release()
	if obs.Diagnostic || obs.Direct {
		return Transition{From: s.State, To: s.State}
	}
	s.foldBucket(now, cfg)
	s.LastStatus = obs.Status
	s.applyEvidence(obs, cfg, now)
	s.applyPrimary(obs, cfg, now)
	s.applyStream(obs, cfg)
	transition := s.evaluateState(obs, cfg, now)
	s.checkAdmission(cfg, now, &transition)
	return transition
}

func (s *KeyStats) applyEvidence(obs Observation, cfg Config, now time.Time) {
	if obs.Kind != OutcomeNotFound {
		s.NotFoundCount = 0
	}
	if obs.ValidRequired {
		s.ValidRequired = true
	}
	if httpSample, counts := httpResult(obs); counts {
		if httpSample {
			s.HTTP200Success++
		} else {
			s.HTTP200Failure++
		}
		s.admissionHTTPAdd(httpSample, cfg)
		s.Beta200.Observe(httpSample, now, cfg.DecayHalfLife)
		s.LastReachableAt = now
	}
	if s.ValidRequired && obs.ValidKnown {
		if obs.ValidInference {
			s.ValidSuccess++
		} else {
			s.ValidFailure++
		}
		s.admissionValidAdd(obs.ValidInference, cfg)
		s.BetaValid.Observe(obs.ValidInference, now, cfg.DecayHalfLife)
	}
	if obs.Kind == OutcomeSuccess && obs.Status == 200 && s.ValidRequired && !obs.ValidKnown {
		if s.UnknownStreak < 255 {
			s.UnknownStreak++
		}
		s.LastUnknownAt = now
	}
	if obs.TTFB > 0 {
		s.TTFBEWMA = ewma(s.TTFBEWMA, obs.TTFB, 10*time.Minute, now, s.LastSampleAt)
		s.TTFBSamples++
		s.LatencyHist.Observe(obs.TTFB)
	}
	if obs.Total > 0 {
		s.TotalEWMA = ewma(s.TotalEWMA, obs.Total, 10*time.Minute, now, s.LastSampleAt)
	}
	if obs.TTFB > 0 || obs.Total > 0 {
		s.LastSampleAt = now
	}
	switch obs.Kind {
	case OutcomeRateLimited:
		s.RateLimited++
	case OutcomeNotFound:
		s.NotFoundCount++
		s.LastNotFoundAt = now
	}
	// Any answered request proves the exit's source IP still reaches the
	// upstream; a 429 proves it has spent its quota. Transport failures carry
	// no quota information and stay out of this window.
	if obs.Status > 0 {
		s.RateWindow.Add(obs.Kind != OutcomeRateLimited, now)
	}
}

// httpResult maps one observation to HTTP 200 success (true/false) and whether
// it counts at all.
func httpResult(obs Observation) (bool, bool) {
	switch obs.Kind {
	case OutcomeSuccess, OutcomeInvalidInference:
		return obs.Status == 200, obs.Status > 0
	case OutcomeNonTarget2xx:
		return false, true
	case OutcomeTransportFailure, OutcomeExitFailure:
		return false, true
	}
	return false, false
}

// primaryResult maps one observation to the key-level primary metric.
func primaryResult(obs Observation, validRequired bool) (success bool, counts bool) {
	switch obs.Kind {
	case OutcomeSuccess:
		if obs.Status != 200 {
			return false, true
		}
		if validRequired && !obs.ValidKnown {
			// Unknown payloads only count as HTTP 200 evidence; they must not
			// advance the primary metric.
			return false, false
		}
		if validRequired && !obs.ValidInference {
			return false, true
		}
		return true, true
	case OutcomeInvalidInference:
		return false, true
	case OutcomeNonTarget2xx, OutcomeTransportFailure, OutcomeExitFailure:
		return false, true
	}
	return false, false
}

func (s *KeyStats) applyPrimary(obs Observation, cfg Config, now time.Time) {
	success, counts := primaryResult(obs, s.ValidRequired)
	if !counts {
		if isNeutralForStreak(obs.Kind) {
			s.UnknownStreak = 0
		}
		return
	}
	if success {
		s.PrimarySuccess++
		s.ConsecutiveSuccess++
		s.ConsecutiveFails = 0
		s.UnknownStreak = 0
		s.LastSuccessAt = now
		s.Window.Add(true, now)
		if s.PendingReverify {
			s.ReverifySuccess++
		}
		if s.State == HalfOpen {
			s.EpochSuccess++
			s.HalfOpenSuccess++
		}
		return
	}
	s.PrimaryFailure++
	s.ConsecutiveFails++
	s.ConsecutiveSuccess = 0
	s.LastFailureAt = now
	s.Window.Add(false, now)
	if s.State == HalfOpen {
		s.EpochFailure++
	}
}

func isNeutralForStreak(kind OutcomeKind) bool {
	switch kind {
	case OutcomeClientCancel, OutcomeKeyFailure, OutcomeRegionBlocked, OutcomeNeutral,
		OutcomeUpstreamWide, OutcomeRateLimited, OutcomeNotFound, OutcomeBootstrapFailure,
		OutcomeEnvironmentFailure, OutcomeEnvironmentSlow:
		return true
	}
	return false
}

func (s *KeyStats) applyStream(obs Observation, cfg Config) {
	switch obs.Stream {
	case StreamSuccess:
		s.StreamSuccess++
		s.ConsecutiveStreamFails = 0
	case StreamFailure:
		s.StreamFailure++
		if s.ConsecutiveStreamFails < 255 {
			s.ConsecutiveStreamFails++
		}
	}
}

func (s *KeyStats) evaluateState(obs Observation, cfg Config, now time.Time) Transition {
	switch s.State {
	case Candidate:
		if (obs.Kind == OutcomeSuccess || obs.Kind == OutcomeInvalidInference) && obs.Status == 200 {
			return s.transition(Verifying, "first_http200")
		}
	case Verifying:
		if s.failureBreaker(cfg, now) {
			return s.enterOpen("verifying_failure", cfg, now)
		}
	case Active:
		if s.PendingReverify && s.ReverifySuccess >= uint8(cfg.ReverifySuccesses) {
			s.PendingReverify = false
			s.ReverifySuccess = 0
		}
		if s.failureBreaker(cfg, now) {
			return s.enterOpen("active_failure", cfg, now)
		}
		if s.ConsecutiveFails >= uint8(cfg.SuspectFailures) {
			return s.transition(Suspect, "consecutive_failures")
		}
		if s.unknownSuspect(cfg) {
			return s.transition(Suspect, "unknown_streak")
		}
		if s.streamSuspect(cfg) {
			return s.transition(Suspect, "stream_failures")
		}
		if s.latencySuspect(cfg) {
			return s.transition(Suspect, "latency_budget")
		}
		if s.recentSuspect(cfg) {
			return s.transition(Suspect, "recent_health")
		}
	case Suspect:
		if s.failureBreaker(cfg, now) {
			return s.enterOpen("suspect_failure", cfg, now)
		}
		if s.couldRecover(cfg) {
			return s.transition(Active, "recovered")
		}
	case HalfOpen:
		if s.EpochFailure > 0 || s.ConsecutiveFails > 0 {
			return s.enterOpen("half_open_failure", cfg, now)
		}
		if s.EpochSuccess >= uint8(cfg.HalfOpenSuccesses) || s.HalfOpenSuccess >= uint8(cfg.HalfOpenSuccesses) {
			target := s.PrevState
			if target != Active && target != Verifying && target != Suspect {
				target = Verifying
			}
			return s.transition(target, "half_open_recovered")
		}
		if s.streamSuspect(cfg) {
			return s.transition(Suspect, "half_open_stream_failure")
		}
	case Open:
		if !s.OpenUntil.IsZero() && !now.Before(s.OpenUntil) {
			return s.transition(HalfOpen, "open_expired")
		}
	}
	return Transition{From: s.State, To: s.State}
}

func (s *KeyStats) failureBreaker(cfg Config, now time.Time) bool {
	if s.ConsecutiveFails >= uint8(cfg.OpenFailures) {
		return true
	}
	rate, samples := s.Window.FailureRate(now)
	return samples >= 10 && rate >= cfg.OpenRateThreshold
}

func (s *KeyStats) couldRecover(cfg Config) bool {
	if s.ConsecutiveSuccess < uint16(cfg.SuspectRecoverySuccess) {
		return false
	}
	if s.ConsecutiveStreamFails >= uint8(cfg.StreamFailureSuspect) {
		return false
	}
	if s.TTFBSamples >= uint32(cfg.SuspectLatencySamples) && s.LatencyHist.Quantile(0.95) > cfg.LatencyBudget {
		return false
	}
	return true
}

func (s *KeyStats) unknownSuspect(cfg Config) bool {
	return s.ValidRequired && s.UnknownStreak >= uint8(cfg.UnknownStreakSuspect)
}

func (s *KeyStats) streamSuspect(cfg Config) bool {
	return s.ConsecutiveStreamFails >= uint8(cfg.StreamFailureSuspect)
}

func (s *KeyStats) latencySuspect(cfg Config) bool {
	return s.TTFBSamples >= uint32(cfg.SuspectLatencySamples) &&
		s.LatencyHist.Quantile(0.95) > cfg.LatencyBudget
}

func (s *KeyStats) recentSuspect(cfg Config) bool {
	beta := s.Beta200
	if s.ValidRequired {
		beta = s.BetaValid
	}
	if beta.RecentWeight() < cfg.MinRecentWeight {
		return false
	}
	return beta.Quantile(cfg.RecentQuantile) < cfg.SuspectRecentLower
}

func (s *KeyStats) enterOpen(reason string, cfg Config, now time.Time) Transition {
	if s.State != HalfOpen {
		s.PrevState = s.State
	}
	s.OpenCount++
	delay := backoff(cfg, s.OpenCount)
	if delay > cfg.BackoffMax {
		delay = cfg.BackoffMax
	}
	s.OpenUntil = now.Add(delay)
	s.RecoveryStart = now
	s.EpochSuccess = 0
	s.EpochFailure = 0
	s.HalfOpenSuccess = 0
	s.ConsecutiveSuccess = 0
	return s.transition(Open, reason)
}

func backoff(cfg Config, openCount uint8) time.Duration {
	if openCount < 1 {
		openCount = 1
	}
	shift := min(int(openCount)-1, 10)
	delay := cfg.BackoffBase * time.Duration(1<<shift)
	jitter := 1 + (rand.Float64()*2-1)*cfg.JitterRatio
	return time.Duration(float64(delay) * jitter)
}

func (s *KeyStats) transition(to State, reason string) Transition {
	from := s.State
	if from == to {
		return Transition{From: from, To: to}
	}
	switch to {
	case Active:
		s.ActiveEpoch++
		s.OpenCount = 0
		s.HalfOpenSuccess = 0
		s.EpochSuccess = 0
		s.EpochFailure = 0
		s.PendingReverify = false
		s.ReverifySuccess = 0
	case Open:
		s.PendingReverify = false
		s.ReverifySuccess = 0
	case HalfOpen:
		s.EpochSuccess = 0
		s.EpochFailure = 0
		s.HalfOpenSuccess = 0
		s.ConsecutiveSuccess = 0
	case Verifying:
		s.OpenCount = 0
		s.PendingReverify = false
		s.ReverifySuccess = 0
	case Dead:
		s.PendingReverify = false
	}
	s.State = to
	s.StateVersion++
	return Transition{From: from, To: to, Reason: reason, Changed: true}
}

// MarkNotFound promotes the key to DEAD once the same 404 repeated enough on
// this exit while another exit served the same key. The caller owns the
// cross-exit signal.
func (s *KeyStats) MarkNotFound(otherExitSucceeded bool, cfg Config) bool {
	cfg = cfg.Normalize()
	s.Acquire()
	defer s.Release()
	if otherExitSucceeded && s.NotFoundCount >= uint8(cfg.DeadConfirmations) {
		s.transition(Dead, "configuration_error")
		return true
	}
	return false
}

// MarkUnknown raises the unknown streak for a 200 that could not be parsed.
func (s *KeyStats) MarkUnknown(now time.Time) {
	s.Acquire()
	defer s.Release()
	if s.UnknownStreak < 255 {
		s.UnknownStreak++
	}
	s.LastUnknownAt = now
}

// MarkEnvironmentEpoch resets admission evidence for a new environment epoch.
// Raw diagnostic counts stay; the state survives with PendingReverify.
func (s *KeyStats) MarkEnvironmentEpoch(epoch uint64, cfg Config, now time.Time) {
	cfg = cfg.Normalize()
	s.Acquire()
	defer s.Release()
	s.EnvironmentEpoch = epoch
	s.AdmissionHTTP200Success = 0
	s.AdmissionHTTP200Failure = 0
	s.AdmissionValidSuccess = 0
	s.AdmissionValidFailure = 0
	s.AdmissionHTTPBuckets = 0
	s.AdmissionValidBuckets = 0
	s.HTTPReservoir = NewReservoir(cfg.MaxSamplesPerBucket)
	s.ValidReservoir = NewReservoir(cfg.MaxSamplesPerBucket)
	s.HTTPSeen = 0
	s.ValidSeen = 0
	s.BucketStart = now.Truncate(time.Duration(cfg.EvidenceBucketSeconds) * time.Second)
	s.Window = NewWindow(cfg.EvidenceBucketSeconds)
	s.Beta200 = EmptyBeta(now)
	s.BetaValid = EmptyBeta(now)
	s.PrimarySuccess = 0
	s.PrimaryFailure = 0
	s.ConsecutiveFails = 0
	s.ConsecutiveSuccess = 0
	s.ConsecutiveStreamFails = 0
	s.StreamSuccess = 0
	s.StreamFailure = 0
	s.UnknownStreak = 0
	s.TTFBSamples = 0
	s.LatencyHist = LatencyHist{}
	s.TTFBEWMA = 0
	s.TotalEWMA = 0
	s.HTTPCheckRound = 0
	s.ValidCheckRound = 0
	s.LastCheckSamples = 0
	s.LastCheckValidSamples = 0
	s.ReverifySuccess = 0
	if s.State == Active {
		s.PendingReverify = true
	}
	s.StateVersion++
}

// foldBucket closes the current admission bucket when the wall clock crossed
// its boundary and folds both reservoirs into the closed-bucket counters.
func (s *KeyStats) foldBucket(now time.Time, cfg Config) {
	duration := time.Duration(cfg.EvidenceBucketSeconds) * time.Second
	bucketStart := now.Truncate(duration)
	if s.BucketStart.IsZero() {
		s.BucketStart = bucketStart
		return
	}
	if !bucketStart.After(s.BucketStart) {
		return
	}
	httpSuccess, httpFailure := s.HTTPReservoir.Counts()
	if httpSuccess+httpFailure > 0 {
		s.AdmissionHTTP200Success += httpSuccess
		s.AdmissionHTTP200Failure += httpFailure
		s.AdmissionHTTPBuckets++
	}
	validSuccess, validFailure := s.ValidReservoir.Counts()
	if validSuccess+validFailure > 0 {
		s.AdmissionValidSuccess += validSuccess
		s.AdmissionValidFailure += validFailure
		s.AdmissionValidBuckets++
	}
	s.HTTPReservoir = NewReservoir(cfg.MaxSamplesPerBucket)
	s.ValidReservoir = NewReservoir(cfg.MaxSamplesPerBucket)
	s.HTTPSeen = 0
	s.ValidSeen = 0
	s.BucketStart = bucketStart
}

func (s *KeyStats) admissionHTTPAdd(success bool, cfg Config) {
	if s.HTTPReservoir.K != cfg.MaxSamplesPerBucket {
		s.HTTPReservoir = NewReservoir(cfg.MaxSamplesPerBucket)
	}
	s.HTTPReservoir.Add(success)
	s.HTTPSeen++
}

func (s *KeyStats) admissionValidAdd(success bool, cfg Config) {
	if s.ValidReservoir.K != cfg.MaxSamplesPerBucket {
		s.ValidReservoir = NewReservoir(cfg.MaxSamplesPerBucket)
	}
	s.ValidReservoir.Add(success)
	s.ValidSeen++
}

func (s *KeyStats) checkAdmission(cfg Config, now time.Time, transition *Transition) {
	if s.State != Verifying {
		return
	}
	httpSamples, validSamples := s.admissionSamplesLocked()
	minHTTPBuckets := max(cfg.MinEvidenceBuckets, (cfg.MinSamples+cfg.MaxSamplesPerBucket-1)/cfg.MaxSamplesPerBucket)
	minValidBuckets := max(cfg.MinEvidenceBuckets, (cfg.ValidMinSamples+cfg.MaxSamplesPerBucket-1)/cfg.MaxSamplesPerBucket)
	httpDue := httpSamples >= cfg.MinSamples &&
		int(s.AdmissionHTTPBuckets) >= minHTTPBuckets &&
		(s.LastCheckSamples == 0 || httpSamples-int(s.LastCheckSamples) >= cfg.MinSamples)
	validDue := true
	if s.ValidRequired {
		validDue = validSamples >= cfg.ValidMinSamples &&
			int(s.AdmissionValidBuckets) >= minValidBuckets &&
			(s.LastCheckValidSamples == 0 || validSamples-int(s.LastCheckValidSamples) >= cfg.ValidMinSamples)
	}
	if !httpDue || !validDue {
		return
	}
	httpZ := CheckpointZ(cfg.ConfidenceZ, int(s.HTTPCheckRound)+1)
	httpOK := WilsonLower(s.AdmissionHTTP200Success, s.AdmissionHTTP200Failure, httpZ) >= cfg.HTTP200Target
	validOK := true
	if s.ValidRequired {
		validZ := CheckpointZ(cfg.ConfidenceZ, int(s.ValidCheckRound)+1)
		validOK = WilsonLower(s.AdmissionValidSuccess, s.AdmissionValidFailure, validZ) >= cfg.ValidTarget
	}
	latencyOK := s.TTFBSamples == 0 || s.LatencyHist.Quantile(0.95) <= cfg.LatencyBudget
	if httpOK && validOK && latencyOK {
		*transition = s.transition(Active, "admission_passed")
		return
	}
	if !httpOK {
		s.HTTPCheckRound++
		s.LastCheckSamples = uint32(httpSamples)
	}
	if !validOK {
		s.ValidCheckRound++
		s.LastCheckValidSamples = uint32(validSamples)
	}
}

func ewma(current, sample, tau time.Duration, now, last time.Time) time.Duration {
	if current <= 0 || last.IsZero() {
		return sample
	}
	elapsed := now.Sub(last)
	if elapsed <= 0 {
		return current
	}
	weight := 1 - math.Exp(-float64(elapsed)/float64(tau))
	return time.Duration(float64(current) + weight*(float64(sample)-float64(current)))
}
