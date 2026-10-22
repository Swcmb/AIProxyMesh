package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/bootstrap"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/state"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

// egressTrace carries the routing labels recorded for one upstream attempt.
type egressTrace struct {
	Proxy    string
	ExitNode string
	ExitID   string
}

func (g *Gateway) egressEnabled() bool { return g.egress != nil && g.egress.Router() != nil }

func (g *Gateway) egressTarget(baseURL string) (*url.URL, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid upstream URL %q", baseURL)
	}
	return parsed, nil
}

// requestMode derives the health-key mode from the actual upstream body.
func requestMode(body []byte) string {
	var probe struct {
		Stream *bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Stream != nil {
		if *probe.Stream {
			return health.ModeStream
		}
		return health.ModeJSON
	}
	return health.ModeJSON
}

// healthKeyFor builds the concrete key one attempt will observe.
func healthKeyFor(baseURL string, route models.Route, tier config.Tier, body []byte) health.HealthKey {
	key, err := health.NewKey(baseURL, route.ID, string(route.ProtocolFor(tier)), requestMode(body))
	if err != nil {
		return health.HealthKey{}
	}
	return key
}

func (g *Gateway) healthMaxPeek() int {
	if g.cfg.Egress != nil && g.cfg.Egress.Health.MaxValidPeekBytes > 0 {
		return g.cfg.Egress.Health.MaxValidPeekBytes
	}
	return 8192
}

func (g *Gateway) selectEgress(ctx context.Context, target *url.URL, key health.HealthKey, exclude map[string]struct{}, affinity string) (*routing.Attempt, error) {
	var attempt *routing.Attempt
	var err error
	if key.Valid() {
		attempt, err = g.egress.Router().SelectKey(ctx, target, key, exclude, affinity)
	} else {
		attempt, err = g.egress.Router().SelectAffinity(ctx, target, exclude, affinity)
	}
	if err != nil {
		if errors.Is(err, routing.ErrNoEligible) {
			return nil, fmt.Errorf("%w: %v", errProxyUnavailable, err)
		}
		return nil, err
	}
	return attempt, nil
}

// classifyEgressOutcome maps a wire result to the router's outcome taxonomy.
// Direct exits never cool on transport errors: without a proxy hop, a dial
// failure says the upstream is unreachable, not that the exit is broken.
func classifyEgressOutcome(ctx context.Context, resp *http.Response, err error, direct bool, honorRetryAfter bool) routing.Outcome {
	if ctx.Err() != nil {
		return routing.Outcome{Kind: routing.ClientCancel, Err: ctx.Err()}
	}
	if err != nil {
		if errors.Is(err, bootstrap.ErrUnavailable) {
			return routing.Outcome{Kind: routing.BootstrapFailure, Err: err}
		}
		if errors.Is(err, errFirstEventTimeout) || upstreamAccepted(err) {
			// The upstream accepted the request; a slow first event is not a
			// broken proxy and must not cool the node.
			return routing.Outcome{Kind: routing.InferenceTimeout, StatusCode: http.StatusOK, HeadersReceived: true, Err: err}
		}
		kind := routing.ProxyFailure
		if direct {
			kind = routing.UpstreamFailure
		}
		return routing.Outcome{Kind: kind, Err: err}
	}
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		return routing.Outcome{Kind: routing.Success, StatusCode: status}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return routing.Outcome{Kind: routing.KeyFailure, StatusCode: status}
	case status == http.StatusTooManyRequests:
		outcome := routing.Outcome{Kind: routing.ExitFailure, StatusCode: status}
		if honorRetryAfter {
			outcome.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return outcome
	case status >= 500:
		return routing.Outcome{Kind: routing.UpstreamFailure, StatusCode: status}
	case status >= 400:
		return routing.Outcome{Kind: routing.Neutral, StatusCode: status}
	default:
		return routing.Outcome{Kind: routing.UpstreamFailure, StatusCode: status}
	}
}

func (g *Gateway) honorRetryAfter() bool {
	if g.cfg.Egress == nil || g.cfg.Egress.Failover.HonorRetryAfter == nil {
		return true
	}
	return *g.cfg.Egress.Failover.HonorRetryAfter
}

// reportEgressAttempt records the attempt in the router, telemetry and the
// persistent failover log.
func (g *Gateway) reportEgressAttempt(
	ctx context.Context,
	route models.Route,
	ids identity.RequestIDs,
	attemptNumber int,
	keyID, channel string,
	anonymous bool,
	attempt *routing.Attempt,
	resp *http.Response,
	attemptErr error,
	verdict inferenceVerdict,
	duration time.Duration,
) routing.Classification {
	attempt.KeyID = keyID
	outcome := classifyEgressOutcome(ctx, resp, attemptErr, attempt.ProxyURL == nil, g.honorRetryAfter())
	outcome.ValidInference = verdict.Valid
	outcome.ValidKnown = verdict.Known
	if resp != nil {
		outcome.HeadersReceived = true
	}
	if resp != nil && resp.StatusCode == http.StatusForbidden && attempt.ProxyURL != nil && geoBlockedResponse(resp) {
		// A country rejection is not a dead exit: rotate to another region and
		// hide this country for the target instead of cooling the node.
		outcome = routing.Outcome{Kind: routing.RegionBlocked, StatusCode: http.StatusForbidden}
		if g.egress != nil {
			outcome.Region = g.egress.NodeRegion(attempt.NodeID)
		}
	}
	outcome.Latency = duration
	classification := attempt.Report(outcome)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	g.recordUpstreamAttempt(ctx, route, ids, attemptNumber, keyID, channel, anonymous,
		egressTrace{Proxy: "egress", ExitNode: attempt.NodeName, ExitID: attempt.NodeID},
		resp, attemptErr, duration)
	g.egress.RecordEvent(state.Event{
		At:        time.Now().UTC(),
		RequestID: ids.Request,
		NodeHash:  attempt.NodeID,
		NodeName:  attempt.NodeName,
		Target:    attempt.Target,
		Kind:      string(classification.Kind),
		Status:    status,
		Attempt:   attemptNumber,
		Reason:    classification.Reason,
		Detail:    errorText(attemptErr),
	})
	if g.egress != nil && attempt.Key.Valid() && !outcome.Diagnostic && !outcome.Direct {
		switch classification.Kind {
		case routing.Success, routing.ExitFailure, routing.ProxyFailure, routing.InferenceTimeout:
			g.egress.RecordAccuracy(attempt, classification.Kind == routing.Success, ids.Request)
		}
	}
	if classification.UpstreamWide {
		g.logger.Warn("upstream failure consensus reached; stopping failover",
			"component", "egress", "event", "upstream_wide", "request_id", ids.Request,
			"model", route.ID, "tier", route.Tier, "node", attempt.NodeName, "node_id", attempt.NodeID,
			"status", status, "reason", classification.Reason)
	}
	return classification
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 240 {
		text = text[:240]
	}
	return text
}

// setRequestCredentialEgress mirrors setRequestCredential for pool exits.
func setRequestCredentialEgress(ctx context.Context, tier config.Tier, keyID, channel string, anonymous bool, attempt *routing.Attempt) {
	meta := telemetry.MetaFromContext(ctx)
	if meta == nil {
		return
	}
	meta.Tier = string(tier)
	meta.KeyID = keyID
	meta.Channel = channel
	meta.Anonymous = anonymous
	meta.Proxy = "egress"
	meta.ExitNode = attempt.NodeName
	meta.ExitID = attempt.NodeID
}

// egressProxyError tags a transport failure that happened through a real proxy
// hop so the client sees proxy semantics instead of a generic upstream error.
func egressProxyError(attempt *routing.Attempt, classification routing.Classification, err error) error {
	if err == nil || classification.Kind != routing.ProxyFailure || attempt.ProxyURL == nil {
		return err
	}
	return &proxyUnavailableError{proxy: attempt.NodeName, err: err}
}

// waitForRetryAfter sleeps when every exit is cooling and the upstream asked
// for a delay that still fits inside the request budget.
func waitForRetryAfter(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return false
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
