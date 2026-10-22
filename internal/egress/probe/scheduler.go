package probe

import (
	"container/heap"
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

// CanaryRequest is one minimal inference probe scheduler task.
type CanaryRequest struct {
	Entry *node.Entry
	Key   health.HealthKey
	Mode  string
}

// CanaryResult is what the gateway executor observed.
type CanaryResult struct {
	Status        int
	Latency       time.Duration
	Valid         bool
	ValidKnown    bool
	RegionBlocked bool
	Err           error
}

// CanaryFunc executes one canary through the configured exit.
type CanaryFunc func(ctx context.Context, request CanaryRequest) CanaryResult

// ScheduleConfig tunes the canary scheduler.
type ScheduleConfig struct {
	Enabled          bool
	BudgetPerSecond  float64
	Burst            int
	DailyBudget      int
	MaxPerScan       int
	MaxKeys          int
	MaxInflight      int
	CanaryMaxTokens  int
	DefaultMode      string
	CanaryModel      string
	CanaryTarget     string
	VerifyingRecheck time.Duration
	ActiveRecheck    time.Duration
	DeadRecheck      time.Duration
	BackoffBase      time.Duration
	BackoffMax       time.Duration
	JitterRatio      float64
	TargetPause      time.Duration
	Health           health.Config
	MaxKeysPerNode   int
}

type scheduleTask struct {
	entry      *node.Entry
	key        health.HealthKey
	nextAt     time.Time
	priority   float64
	enqueuedAt time.Time
	index      int
	pIndex     int
}

// priorityHeap keeps the lowest-priority task at the root so a full heap can
// evict it in O(log n).
type priorityHeap []*scheduleTask

func (h priorityHeap) Len() int { return len(h) }
func (h priorityHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority < h[j].priority
	}
	return h[i].enqueuedAt.Before(h[j].enqueuedAt)
}
func (h priorityHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].pIndex, h[j].pIndex = i, j
}
func (h *priorityHeap) Push(value any) {
	task := value.(*scheduleTask)
	task.pIndex = len(*h)
	*h = append(*h, task)
}
func (h *priorityHeap) Pop() any {
	old := *h
	task := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return task
}

type taskHeap []*scheduleTask

func (h taskHeap) Len() int { return len(h) }
func (h taskHeap) Less(i, j int) bool {
	return h[i].nextAt.Before(h[j].nextAt) || (h[i].nextAt.Equal(h[j].nextAt) && h[i].priority > h[j].priority)
}
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *taskHeap) Push(value any) {
	task := value.(*scheduleTask)
	task.index = len(*h)
	*h = append(*h, task)
}
func (h *taskHeap) Pop() any {
	old := *h
	task := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return task
}

// Scheduler runs budgeted canary probes and feeds evidence back into the
// health tables.
type Scheduler struct {
	pool        *pool.Pool
	cfg         atomicConfig
	logger      *slog.Logger
	nowFn       func() time.Time
	hookMu      sync.RWMutex
	enqueueHook func(string)
	scans       atomic.Int64
	examined    atomic.Int64
	enqueued    atomic.Int64
	execMu      sync.RWMutex
	executor    CanaryFunc
	env         atomic.Pointer[health.Environment]

	mu         sync.Mutex
	tasks      taskHeap
	byPriority priorityHeap
	queued     map[string]struct{}
	inflight   map[string]struct{}

	budgetMu   sync.Mutex
	tokens     float64
	lastRefill time.Time
	dailyDay   string
	dailyCount int

	scanCursor string
}

type atomicConfig struct {
	mu  sync.RWMutex
	cfg ScheduleConfig
}

func (a *atomicConfig) load() ScheduleConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *atomicConfig) store(cfg ScheduleConfig) {
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
}

// NewScheduler creates an idle scheduler.
func NewScheduler(p *pool.Pool, cfg ScheduleConfig, logger *slog.Logger) *Scheduler {
	s := &Scheduler{
		pool:     p,
		logger:   logger,
		nowFn:    time.Now,
		queued:   make(map[string]struct{}),
		inflight: make(map[string]struct{}),
	}
	s.cfg.store(cfg)
	s.tokens = float64(max(cfg.Burst, 1))
	s.lastRefill = time.Now()
	return s
}

// SetExecutor installs the gateway-side canary implementation.
func (s *Scheduler) SetExecutor(executor CanaryFunc) {
	s.execMu.Lock()
	s.executor = executor
	s.execMu.Unlock()
}

func (s *Scheduler) executorFn() CanaryFunc {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	return s.executor
}

// SetEnvironment installs the local-link classifier.
func (s *Scheduler) SetEnvironment(env *health.Environment) { s.env.Store(env) }

// SetClock replaces the scheduler clock (tests and the fairness simulation).
func (s *Scheduler) SetClock(now func() time.Time) {
	if now != nil {
		s.nowFn = now
	}
}

func (s *Scheduler) clock() time.Time {
	if s.nowFn == nil {
		return time.Now()
	}
	return s.nowFn()
}

// SetEnqueueHook observes successfully enqueued task ids (coverage tests).
func (s *Scheduler) SetEnqueueHook(hook func(string)) {
	s.hookMu.Lock()
	s.enqueueHook = hook
	s.hookMu.Unlock()
}

// SchedulerStats is cumulative scan instrumentation.
type SchedulerStats struct {
	Scans    int64
	Examined int64
	Enqueued int64
	Queued   int
}

// Stats returns cumulative scheduler counters.
func (s *Scheduler) Stats() SchedulerStats {
	return SchedulerStats{
		Scans: s.scans.Load(), Examined: s.examined.Load(),
		Enqueued: s.enqueued.Load(), Queued: s.QueueDepth(),
	}
}

// ScanOnce runs one fair scan at the supplied time.
func (s *Scheduler) ScanOnce(now time.Time) { s.scan(now) }

func (s *Scheduler) frozen() bool {
	env := s.env.Load()
	return env != nil && env.Frozen()
}

func (s *Scheduler) targetPaused(target string, now time.Time) bool {
	env := s.env.Load()
	return env != nil && env.TargetPaused(target, now)
}

// Configure swaps scheduling parameters on hot reload.
func (s *Scheduler) Configure(cfg ScheduleConfig) { s.cfg.store(cfg) }

// Run scans for due keys and dispatches within budget.
func (s *Scheduler) Run(ctx context.Context) {
	scan := time.NewTicker(15 * time.Second)
	dispatch := time.NewTicker(250 * time.Millisecond)
	defer scan.Stop()
	defer dispatch.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			s.scan(s.clock())
		case <-dispatch.C:
			for i := 0; i < 4; i++ {
				if !s.dispatchOne(ctx, time.Now()) {
					break
				}
			}
		}
	}
}

// scan enqueues eligible keys in fair ring order, bounded per pass.
func (s *Scheduler) scan(now time.Time) {
	cfg := s.cfg.load()
	if !cfg.Enabled || s.executorFn() == nil {
		return
	}
	if s.frozen() {
		return
	}
	entries := s.pool.List()
	if len(entries) == 0 {
		return
	}
	// pool.List is map-ordered. Sort by stable node id so scanCursor is a real
	// ring position; otherwise the cursor would jump randomly and starve keys.
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID() < entries[j].ID() })
	start := 0
	if s.scanCursor != "" {
		start = sort.Search(len(entries), func(i int) bool { return entries[i].ID() > s.scanCursor })
		if start == len(entries) {
			start = 0
		}
	}
	s.scans.Add(1)
	budget := cfg.MaxPerScan
	if budget <= 0 {
		budget = 2000
	}
	for offset := 0; offset < len(entries) && budget > 0; offset++ {
		entry := entries[(start+offset)%len(entries)]
		table := entry.ExistingHealthTable()
		if table == nil {
			continue
		}
		tracked := 0
		for _, record := range table.ProbeSnapshots(now) {
			tracked++
			s.examined.Add(1)
			if budget <= 0 {
				break
			}
			next, priority, due := s.dueTime(entry, record, cfg, now)
			if !due {
				continue
			}
			if s.enqueue(entry, record.Key, next, priority, now, !record.ProbeEnqueued) {
				budget--
			}
		}
		if tracked == 0 && cfg.CanaryModel != "" && cfg.CanaryTarget != "" {
			s.examined.Add(1)
			// A reachable node without any key gets one synthetic candidate key,
			// so a configured canary model can discover exits before real traffic.
			if last := entry.LastSuccessAt(); last > 0 {
				if key, err := health.NewKey(cfg.CanaryTarget, cfg.CanaryModel, health.ProtocolChat, health.ModeStream); err == nil {
					record := health.ProbeSnapshot{Key: key, State: health.Candidate, LastReachableAt: time.Unix(0, last)}
					next, priority, due := s.dueTime(entry, record, cfg, now)
					if due && s.enqueue(entry, key, next, priority, now, true) {
						budget--
					}
				}
			}
		}
		s.scanCursor = entry.ID()
	}
}

func (s *Scheduler) dueTime(entry *node.Entry, record health.ProbeSnapshot, cfg ScheduleConfig, now time.Time) (time.Time, float64, bool) {
	if cfg.TargetPause > 0 && s.targetPaused(record.Key.Target, now) {
		return time.Time{}, 0, false
	}
	lastProbe := record.LastProbeAt
	switch record.State {
	case health.Candidate:
		if record.LastReachableAt.IsZero() {
			return time.Time{}, 0, false
		}
		if !lastProbe.IsZero() && now.Sub(lastProbe) < cfg.VerifyingRecheck {
			return time.Time{}, 0, false
		}
	case health.Verifying:
		interval := cfg.VerifyingRecheck
		if interval <= 0 {
			interval = 30 * time.Second
		}
		if !lastProbe.IsZero() && now.Sub(lastProbe) < interval {
			return time.Time{}, 0, false
		}
	case health.Active, health.Suspect:
		if record.State == health.Suspect {
			break
		}
		interval := cfg.ActiveRecheck
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		if !lastProbe.IsZero() && now.Sub(lastProbe) < interval {
			return time.Time{}, 0, false
		}
		if record.PrimarySuccess+record.PrimaryFailure >= uint32(cfg.Health.MinSamples) && record.BetaWeight >= cfg.Health.MinRecentWeight {
			return time.Time{}, 0, false
		}
	case health.Open, health.HalfOpen:
		if !record.OpenUntil.IsZero() && now.Before(record.OpenUntil) {
			return time.Time{}, 0, false
		}
	case health.Dead:
		if !lastProbe.IsZero() && now.Sub(lastProbe) < cfg.DeadRecheck {
			return time.Time{}, 0, false
		}
	default:
		return time.Time{}, 0, false
	}
	priority := s.priority(entry, record, cfg)
	return now, priority, true
}

func (s *Scheduler) priority(entry *node.Entry, record health.ProbeSnapshot, cfg ScheduleConfig) float64 {
	samples := record.PrimarySuccess + record.PrimaryFailure
	insufficient := 1 - math.Min(float64(samples)/float64(max(cfg.Health.MinSamples, 1)), 1)
	failureRate := record.FailureRate
	recovery := 0.0
	switch record.State {
	case health.HalfOpen:
		recovery = 1
	case health.Open:
		if record.OpenUntil.Sub(s.clock()) < time.Minute {
			recovery = 0.5
		}
	}
	gap := 0.0
	// A key that never entered the heap gets the maximum aging bonus, so
	// high-priority states cannot starve it indefinitely.
	aging := 0.0
	if !record.ProbeEnqueued {
		aging = 0.3
	}
	return 0.35*insufficient + 0.25*failureRate + 0.25*recovery + 0.15*gap + aging
}

func taskID(entry *node.Entry, key health.HealthKey) string {
	return entry.ID() + "|" + key.Target + "|" + key.Model + "|" + key.Protocol + "|" + key.Mode
}

func (s *Scheduler) enqueue(entry *node.Entry, key health.HealthKey, next time.Time, priority float64, now time.Time, newcomer bool) bool {
	id := taskID(entry, key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.queued[id]; exists {
		return false
	}
	if _, exists := s.inflight[id]; exists {
		return false
	}
	if maxKeys := s.cfg.load().MaxKeys; maxKeys > 0 && s.tasks.Len() >= maxKeys {
		if len(s.byPriority) == 0 {
			return false
		}
		lowest := s.byPriority[0]
		// A never-scheduled key may evict the lowest-priority task even when
		// that task ranks higher; first coverage is a hard fairness guarantee.
		if !newcomer {
			if lowest.priority > priority {
				return false
			}
			// Equal priority: evict the older task so every candidate eventually
			// enters the heap (the fairness guarantee relies on this rotation).
			if lowest.priority == priority && !lowest.enqueuedAt.Before(now) {
				return false
			}
		}
		evicted := lowest
		heap.Remove(&s.byPriority, evicted.pIndex)
		heap.Remove(&s.tasks, evicted.index)
		delete(s.queued, taskID(evicted.entry, evicted.key))
	}
	task := &scheduleTask{entry: entry, key: key, nextAt: next, priority: priority, enqueuedAt: now}
	heap.Push(&s.tasks, task)
	heap.Push(&s.byPriority, task)
	s.queued[id] = struct{}{}
	s.enqueued.Add(1)
	s.hookMu.RLock()
	hook := s.enqueueHook
	s.hookMu.RUnlock()
	if table := entry.ExistingHealthTable(); table != nil {
		table.MarkProbeEnqueued(key)
	}
	if hook != nil {
		hook(id)
	}
	return true
}

func (s *Scheduler) dispatchOne(ctx context.Context, now time.Time) bool {
	cfg := s.cfg.load()
	if !cfg.Enabled || s.executorFn() == nil {
		return false
	}
	if s.frozen() {
		return false
	}
	s.mu.Lock()
	if s.tasks.Len() == 0 {
		s.mu.Unlock()
		return false
	}
	task := s.tasks[0]
	if task.nextAt.After(now) {
		s.mu.Unlock()
		return false
	}
	heap.Remove(&s.byPriority, task.pIndex)
	heap.Pop(&s.tasks)
	delete(s.queued, taskID(task.entry, task.key))
	if len(s.inflight) >= max(cfg.MaxInflight, 1) {
		s.mu.Unlock()
		next := now.Add(5 * time.Second)
		requeued := &scheduleTask{entry: task.entry, key: task.key, nextAt: next, priority: task.priority, enqueuedAt: task.enqueuedAt}
		heap.Push(&s.tasks, requeued)
		heap.Push(&s.byPriority, requeued)
		s.queued[taskID(task.entry, task.key)] = struct{}{}
		return true
	}
	id := taskID(task.entry, task.key)
	s.inflight[id] = struct{}{}
	s.mu.Unlock()

	if !s.takeToken(cfg, time.Now()) {
		s.finishTask(task, id)
		s.enqueue(task.entry, task.key, now.Add(time.Second), task.priority, now, false)
		return true
	}

	result := s.executorFn()(ctx, CanaryRequest{Entry: task.entry, Key: task.key, Mode: cfg.DefaultMode})
	s.applyResult(task, cfg, result)
	s.finishTask(task, id)

	table := task.entry.HealthTable(cfg.MaxKeysPerNode, 10000)
	if stats := table.Get(task.key); stats != nil {
		stats.Acquire()
		stats.LastProbeAt = now
		stats.NextProbeAt = now.Add(cfg.ActiveRecheck)
		stats.Release()
	}
	return true
}

func (s *Scheduler) finishTask(task *scheduleTask, id string) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}

func (s *Scheduler) applyResult(task *scheduleTask, cfg ScheduleConfig, result CanaryResult) {
	kind := health.OutcomeSuccess
	switch {
	case result.RegionBlocked:
		kind = health.OutcomeRegionBlocked
	case result.Err != nil:
		kind = health.OutcomeTransportFailure
	case result.Status == 429:
		kind = health.OutcomeRateLimited
	case result.Status == 404:
		kind = health.OutcomeNotFound
	case result.Status == 401 || result.Status == 403:
		kind = health.OutcomeKeyFailure
	case result.Status >= 500:
		kind = health.OutcomeExitFailure
	case result.Status >= 400:
		kind = health.OutcomeNeutral
	case !result.Valid && result.ValidKnown:
		kind = health.OutcomeInvalidInference
	}
	observation := health.Observation{
		Key: task.key, Kind: kind, Status: result.Status,
		ValidInference: result.Valid, ValidKnown: result.ValidKnown,
		TTFB: result.Latency,
	}
	table := task.entry.HealthTable(cfg.MaxKeysPerNode, 10000)
	table.Apply(task.key, observation, cfg.Health, time.Now())
	if result.Status == 404 {
		if env := s.env.Load(); env != nil && cfg.TargetPause > 0 {
			env.PauseTarget(task.key.Target, time.Now().Add(cfg.TargetPause))
		}
	}
}

func (s *Scheduler) takeToken(cfg ScheduleConfig, now time.Time) bool {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	rate := cfg.BudgetPerSecond
	if rate <= 0 {
		rate = 2
	}
	if s.lastRefill.IsZero() {
		s.lastRefill = now
	}
	elapsed := now.Sub(s.lastRefill).Seconds()
	if elapsed > 0 {
		s.tokens = math.Min(float64(max(cfg.Burst, 1)), s.tokens+elapsed*rate)
		s.lastRefill = now
	}
	day := now.UTC().Format("2006-01-02")
	if s.dailyDay != day {
		s.dailyDay = day
		s.dailyCount = 0
	}
	if cfg.DailyBudget > 0 && s.dailyCount >= cfg.DailyBudget {
		return false
	}
	if s.tokens < 1 {
		return false
	}
	s.tokens--
	s.dailyCount++
	return true
}

// DailyCount reports canary executions for today.
func (s *Scheduler) DailyCount() int {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	if s.dailyDay != time.Now().UTC().Format("2006-01-02") {
		return 0
	}
	return s.dailyCount
}

// QueueDepth reports pending tasks.
func (s *Scheduler) QueueDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks.Len()
}

// DueCount reports due tasks.
func (s *Scheduler) DueCount(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, task := range s.tasks {
		if !task.nextAt.After(now) {
			count++
		}
	}
	return count
}

// jitter applies the configured jitter to a backoff.
func jitter(base time.Duration, ratio float64) time.Duration {
	if ratio <= 0 {
		return base
	}
	factor := 1 + (rand.Float64()*2-1)*ratio
	return time.Duration(float64(base) * factor)
}
