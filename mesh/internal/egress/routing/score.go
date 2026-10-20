package routing

import (
	"math"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// Weights are the scoring coefficients from the integration spec. They are
// normalized to sum to 1 before use.
type Weights struct {
	Health   float64 `json:"health_weight"`
	Latency  float64 `json:"latency_weight"`
	Routing  float64 `json:"routing_weight"`
	Upstream float64 `json:"upstream_success_weight"`
}

// DefaultWeights matches the initial parameters in the approved spec.
func DefaultWeights() Weights {
	return Weights{Health: 0.30, Latency: 0.15, Routing: 0.15, Upstream: 0.40}
}

// Normalize clamps negative weights and rescales the sum to 1.
func (w Weights) Normalize() Weights {
	w.Health = max(w.Health, 0)
	w.Latency = max(w.Latency, 0)
	w.Routing = max(w.Routing, 0)
	w.Upstream = max(w.Upstream, 0)
	sum := w.Health + w.Latency + w.Routing + w.Upstream
	if sum <= 0 {
		return DefaultWeights()
	}
	return Weights{
		Health:   w.Health / sum,
		Latency:  w.Latency / sum,
		Routing:  w.Routing / sum,
		Upstream: w.Upstream / sum,
	}
}

// Score is the decomposed S_i for one node.
type Score struct {
	Health   float64 `json:"health"`
	Latency  float64 `json:"latency"`
	Routing  float64 `json:"routing"`
	Upstream float64 `json:"upstream"`
	Total    float64 `json:"total"`
}

// latencyHalfLife is the latency at which the latency component reaches 0.5.
const latencyHalfLife = 300 * time.Millisecond

func latencyScore(latency time.Duration) float64 {
	if latency <= 0 {
		return 1
	}
	ms := float64(latency) / float64(time.Millisecond)
	return 1 / (1 + ms/float64(latencyHalfLife/time.Millisecond))
}

func healthScore(entry *node.Entry) float64 {
	return clamp01(entry.HealthScore())
}

func routingScore(inflight int) float64 {
	return 1 / (1 + float64(max(inflight, 0)))
}

func upstreamScore(entry *node.Entry, target string) float64 {
	return clamp01(entry.UpstreamScore(target))
}

func computeScore(entry *node.Entry, target string, inflight int, weights Weights) Score {
	latency := 0.5
	if value, ok := entry.LatencyFor(target); ok {
		latency = latencyScore(value)
	}
	score := Score{
		Health:   healthScore(entry),
		Latency:  latency,
		Routing:  routingScore(inflight),
		Upstream: upstreamScore(entry, target),
	}
	score.Total = 100 * (weights.Health*score.Health +
		weights.Latency*score.Latency +
		weights.Routing*score.Routing +
		weights.Upstream*score.Upstream)
	return score
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return min(max(value, 0), 1)
}
