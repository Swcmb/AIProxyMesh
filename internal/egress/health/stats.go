package health

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"time"
)

// Phi is the standard normal CDF.
func Phi(x float64) float64 { return 0.5 * (1 + math.Erf(x/math.Sqrt2)) }

// PhiInv is the standard normal quantile function.
func PhiInv(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	return math.Sqrt2 * math.Erfinv(2*p-1)
}

// WilsonLower returns the one-sided Wilson score lower bound. z is the
// checkpoint-specific critical value, not a fixed 1.96.
func WilsonLower(success, failure uint32, z float64) float64 {
	n := float64(success) + float64(failure)
	if n == 0 {
		return 0
	}
	p := float64(success) / n
	z2 := z * z
	denom := 1 + z2/n
	center := p + z2/(2*n)
	margin := z * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	lower := (center - margin) / denom
	if lower < 0 {
		return 0
	}
	if lower > 1 {
		return 1
	}
	return lower
}

// AlphaTotal is the two-sided error rate implied by the nominal confidence z.
func AlphaTotal(confidenceZ float64) float64 {
	alpha := 2 * (1 - Phi(confidenceZ))
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.05
	}
	return alpha
}

// CheckpointZ returns the critical value reserved for the j-th look at one
// metric. HTTP 200 and valid-inference each receive half of alpha_total.
func CheckpointZ(confidenceZ float64, round int) float64 {
	if round < 1 {
		round = 1
	}
	alphaMetric := AlphaTotal(confidenceZ) / 2
	alpha := alphaMetric / (float64(round) * float64(round+1))
	return PhiInv(1 - alpha)
}

// MinSamplesForTarget returns the all-success sample count required at the
// first checkpoint, used to validate configuration.
func MinSamplesForTarget(target, z float64) int {
	if target <= 0 || target >= 1 {
		return 1 << 30
	}
	return int(math.Ceil(target * z * z / (1 - target)))
}

// BetaState is a time-decayed Beta posterior. Only the part above the prior 1
// is decayed, so an idle key returns to a uniform prior.
type BetaState struct {
	Alpha     float64   `json:"alpha"`
	Beta      float64   `json:"beta"`
	DecayedAt time.Time `json:"decayed_at"`
}

// EmptyBeta returns the uniform prior.
func EmptyBeta(now time.Time) BetaState {
	return BetaState{Alpha: 1, Beta: 1, DecayedAt: now}
}

// decay applies exponential forgetting up to now.
func (b *BetaState) decay(now time.Time, halfLife time.Duration) {
	if b.DecayedAt.IsZero() {
		b.DecayedAt = now
		return
	}
	if halfLife <= 0 {
		halfLife = time.Hour
	}
	elapsed := now.Sub(b.DecayedAt)
	if elapsed <= 0 {
		return
	}
	weight := math.Exp(-math.Ln2 * elapsed.Seconds() / halfLife.Seconds())
	b.Alpha = 1 + (b.Alpha-1)*weight
	b.Beta = 1 + (b.Beta-1)*weight
	b.DecayedAt = now
}

// Observe decays to now and adds one weighted observation.
func (b *BetaState) Observe(success bool, now time.Time, halfLife time.Duration) {
	b.decay(now, halfLife)
	if success {
		b.Alpha++
	} else {
		b.Beta++
	}
}

// DecayTo applies time decay up to now without adding an observation.
func (b *BetaState) DecayTo(now time.Time, halfLife time.Duration) {
	b.decay(now, halfLife)
}

// Quantile returns the posterior p-quantile.
func (b BetaState) Quantile(p float64) float64 {
	alpha, beta := b.Alpha, b.Beta
	if alpha < 1 {
		alpha = 1
	}
	if beta < 1 {
		beta = 1
	}
	return BetaQuantile(alpha, beta, p)
}

// RecentWeight is the posterior observation weight (alpha+beta-2).
func (b BetaState) RecentWeight() float64 {
	weight := b.Alpha + b.Beta - 2
	if weight < 0 {
		return 0
	}
	return weight
}

// BetaQuantile inverts the regularized incomplete beta function by bisection.
func BetaQuantile(a, b, p float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 80; i++ {
		mid := (lo + hi) / 2
		if regularizedIncompleteBeta(a, b, mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// regularizedIncompleteBeta evaluates I_x(a,b).
func regularizedIncompleteBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lgAB, _ := math.Lgamma(a + b)
	lgA, _ := math.Lgamma(a)
	lgB, _ := math.Lgamma(b)
	logTerm := lgAB - lgA - lgB + a*math.Log(x) + b*math.Log1p(-x)
	front := math.Exp(logTerm)
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(a, b, x) / a
	}
	return 1 - front*betaContinuedFraction(b, a, 1-x)/b
}

func betaContinuedFraction(a, b, x float64) float64 {
	const (
		maxIter = 200
		eps     = 3e-12
		tiny    = 1e-30
	)
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		mf := float64(m)
		m2 := 2 * mf
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < eps {
			break
		}
	}
	return h
}

// LatencyHist is a 16-bucket log2 histogram (1ms..64s).
type LatencyHist [16]uint32

// Observe records one duration.
func (h *LatencyHist) Observe(d time.Duration) {
	if d <= 0 {
		return
	}
	ms := float64(d) / float64(time.Millisecond)
	bucket := int(math.Floor(math.Log2(ms)))
	if bucket < 0 {
		bucket = 0
	}
	if bucket > 15 {
		bucket = 15
	}
	h[bucket]++
}

// Samples returns the total observation count.
func (h LatencyHist) Samples() uint64 {
	var total uint64
	for _, count := range h {
		total += uint64(count)
	}
	return total
}

// Quantile returns the interpolated latency at p (0..1). Bucket i covers
// [2^i, 2^(i+1)) milliseconds.
func (h LatencyHist) Quantile(p float64) time.Duration {
	total := h.Samples()
	if total == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	target := p * float64(total)
	if target < 1 {
		target = 1
	}
	var cumulative uint64
	for i, count := range h {
		if count == 0 {
			continue
		}
		next := cumulative + uint64(count)
		if float64(next) < target {
			cumulative = next
			continue
		}
		lower := math.Pow(2, float64(i))
		upper := math.Pow(2, float64(i+1))
		rank := (target - float64(cumulative)) / float64(count)
		if rank < 0 {
			rank = 0
		}
		if rank > 1 {
			rank = 1
		}
		ms := lower + rank*(upper-lower)
		return time.Duration(ms * float64(time.Millisecond))
	}
	return 64 * time.Second
}

// Encode serializes the histogram as 16 little-endian uint32 values.
func (h LatencyHist) Encode() []byte {
	out := make([]byte, 16*4)
	for i, count := range h {
		binary.LittleEndian.PutUint32(out[i*4:], count)
	}
	return out
}

// DecodeLatencyHist parses the fixed-width histogram blob.
func DecodeLatencyHist(raw []byte) (LatencyHist, error) {
	var hist LatencyHist
	if len(raw) == 0 {
		return hist, nil
	}
	if len(raw) != 16*4 {
		return hist, errors.New("health: invalid latency histogram length")
	}
	for i := range hist {
		hist[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	return hist, nil
}

// Reservoir is a bounded uniform sample of one bucket's observations. It is
// used for admission counting so one busy burst cannot dominate the estimate.
type Reservoir struct {
	K       int    `json:"k"`
	Seen    uint32 `json:"seen"`
	Samples []bool `json:"samples"`
}

// NewReservoir creates a reservoir with capacity k.
func NewReservoir(k int) Reservoir {
	if k < 1 {
		k = 1
	}
	return Reservoir{K: k}
}

// Add inserts one observation using Algorithm R.
func (r *Reservoir) Add(success bool) {
	if r.K < 1 {
		r.K = 1
	}
	r.Seen++
	if len(r.Samples) < r.K {
		r.Samples = append(r.Samples, success)
		return
	}
	j := rand.IntN(int(r.Seen))
	if j < r.K {
		r.Samples[j] = success
	}
}

// Counts returns the success/failure counts in the reservoir.
func (r Reservoir) Counts() (uint32, uint32) {
	var success, failure uint32
	for _, sample := range r.Samples {
		if sample {
			success++
		} else {
			failure++
		}
	}
	return success, failure
}

// Snapshot returns a copy of the sample set.
func (r Reservoir) Snapshot() []bool {
	return append([]bool(nil), r.Samples...)
}

// Encode serializes the reservoir: version byte 1 then one byte per sample.
func (r Reservoir) Encode() []byte {
	out := make([]byte, 1+len(r.Samples))
	out[0] = 1
	for i, sample := range r.Samples {
		if sample {
			out[i+1] = 1
		}
	}
	return out
}

// DecodeReservoir parses a reservoir blob.
func DecodeReservoir(raw []byte, k int) (Reservoir, error) {
	reservoir := NewReservoir(k)
	if len(raw) == 0 {
		return reservoir, nil
	}
	if raw[0] != 1 {
		return reservoir, errors.New("health: unsupported reservoir version")
	}
	if len(raw)-1 > k {
		return reservoir, errors.New("health: reservoir exceeds capacity")
	}
	for _, value := range raw[1:] {
		reservoir.Samples = append(reservoir.Samples, value&1 == 1)
	}
	return reservoir, nil
}

// WindowBucket is one rolling failure-rate bucket.
type WindowBucket struct {
	Start    int64
	Samples  uint16
	Failures uint16
}

// Window is a true rolling failure-rate window of ten equal buckets.
type Window struct {
	Buckets         [10]WindowBucket `json:"buckets"`
	Index           uint8            `json:"index"`
	BucketSeconds   int64            `json:"bucket_seconds"`
	LastAdvanceUnix int64            `json:"last_advance"`
}

// NewWindow creates an empty rolling window.
func NewWindow(bucketSeconds int) Window {
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	return Window{BucketSeconds: int64(bucketSeconds)}
}

// advance rotates expired buckets before a sample is written.
func (w *Window) advance(now time.Time) {
	if w.BucketSeconds <= 0 {
		w.BucketSeconds = 60
	}
	bucketStart := now.Unix() / w.BucketSeconds * w.BucketSeconds
	current := &w.Buckets[w.Index]
	if current.Start == 0 {
		current.Start = bucketStart
		w.LastAdvanceUnix = now.Unix()
		return
	}
	for current.Start+w.BucketSeconds <= bucketStart {
		next := (w.Index + 1) % uint8(len(w.Buckets))
		w.Index = next
		w.Buckets[next] = WindowBucket{Start: current.Start + w.BucketSeconds}
		current = &w.Buckets[next]
	}
	w.LastAdvanceUnix = now.Unix()
}

// Add writes one primary observation.
func (w *Window) Add(success bool, now time.Time) {
	w.advance(now)
	bucket := &w.Buckets[w.Index]
	if bucket.Samples < math.MaxUint16 {
		bucket.Samples++
	}
	if !success && bucket.Failures < math.MaxUint16 {
		bucket.Failures++
	}
}

// FailureRate aggregates the live buckets.
func (w Window) FailureRate(now time.Time) (rate float64, samples int) {
	cutoff := now.Unix() - int64(len(w.Buckets))*w.BucketSeconds
	for _, bucket := range w.Buckets {
		if bucket.Start == 0 || bucket.Start < cutoff {
			continue
		}
		samples += int(bucket.Samples)
	}
	if samples == 0 {
		return 0, 0
	}
	var failures int
	for _, bucket := range w.Buckets {
		if bucket.Start == 0 || bucket.Start < cutoff {
			continue
		}
		failures += int(bucket.Failures)
	}
	return float64(failures) / float64(samples), samples
}

// Encode serializes ten 12-byte records.
func (w Window) Encode() []byte {
	out := make([]byte, 0, 10*12)
	for _, bucket := range w.Buckets {
		var record [12]byte
		binary.LittleEndian.PutUint64(record[0:], uint64(bucket.Start))
		binary.LittleEndian.PutUint16(record[8:], bucket.Samples)
		binary.LittleEndian.PutUint16(record[10:], bucket.Failures)
		out = append(out, record[:]...)
	}
	return out
}

// DecodeWindow parses the fixed-width window blob.
func DecodeWindow(raw []byte, bucketSeconds int) (Window, error) {
	window := NewWindow(bucketSeconds)
	if len(raw) == 0 {
		return window, nil
	}
	if len(raw) != 10*12 {
		return window, errors.New("health: invalid window length")
	}
	for i := range window.Buckets {
		record := raw[i*12:]
		window.Buckets[i] = WindowBucket{
			Start:    int64(binary.LittleEndian.Uint64(record[0:])),
			Samples:  binary.LittleEndian.Uint16(record[8:]),
			Failures: binary.LittleEndian.Uint16(record[10:]),
		}
	}
	return window, nil
}
