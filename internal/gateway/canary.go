package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/probe"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
)

// canaryExecutor runs one minimal inference through a specific exit. It only
// uses the anonymous free tier and a single "Reply OK" message.
func (g *Gateway) canaryExecutor(ctx context.Context, request probe.CanaryRequest) probe.CanaryResult {
	if g.egress == nil || request.Entry == nil {
		return probe.CanaryResult{Err: context.Canceled}
	}
	protocol := wire.Protocol(request.Key.Protocol)
	stream := request.Mode == "stream"
	payload := map[string]any{
		"model":      request.Key.Model,
		"stream":     stream,
		"max_tokens": 1,
		"messages":   []any{map[string]any{"role": "user", "content": "Reply OK"}},
	}
	var body []byte
	if protocol == wire.SystemOne {
		built, err := probePayload(request.Key.Model, protocol, "")
		if err != nil {
			return probe.CanaryResult{Err: err}
		}
		body = built
	} else {
		converted, err := wire.ConvertRequest(wire.Chat, protocol, payload)
		if err != nil {
			return probe.CanaryResult{Err: err}
		}
		encoded, err := json.Marshal(converted)
		if err != nil {
			return probe.CanaryResult{Err: err}
		}
		body = prepareAnonymousBody(encoded, protocol)
	}
	session := identity.CanonicalSessionID("canary:" + request.Entry.ID())
	ids := identity.RequestIDs{Request: session, Session: session, Project: "mesh:default-project"}
	httpRequest, err := newUpstreamRequest(ctx, request.Key.Target, protocol, body, ids, anonymousZenKey)
	if err != nil {
		return probe.CanaryResult{Err: err}
	}
	client, err := g.egress.OutboundClient(request.Entry)
	if err != nil {
		return probe.CanaryResult{Err: err}
	}
	firstEvent := time.Duration(g.cfg.Performance.FirstEventTimeoutSeconds) * time.Second
	if firstEvent <= 0 {
		firstEvent = 60 * time.Second
	}
	started := time.Now()
	resp, verdict, err := doInferenceAttempt(client, httpRequest, firstEvent, g.attemptTimeout,
		protocol, request.Mode, g.healthMaxPeek(), nil)
	latency := time.Since(started)
	result := probe.CanaryResult{Latency: latency, Valid: verdict.Valid, ValidKnown: verdict.Known, Err: err}
	if resp != nil {
		result.Status = resp.StatusCode
		if resp.StatusCode == http.StatusForbidden && geoBlockedResponse(resp) {
			result.RegionBlocked = true
		}
		httpx.DrainAndClose(resp.Body)
	}
	return result
}
