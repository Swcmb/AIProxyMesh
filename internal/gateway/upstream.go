package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/health"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/jsonutil"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

// DebugKeyOverride pins one inference request to a single configured upstream
// key so the Playground can exercise that key in isolation.
type DebugKeyOverride struct {
	Tier  config.Tier
	KeyID string
}

type debugKeyOverrideContextKey struct{}

type diagnosticContextKey struct{}

func WithDiagnosticRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, diagnosticContextKey{}, true)
}

func isDiagnosticRequest(ctx context.Context) bool {
	diagnostic, _ := ctx.Value(diagnosticContextKey{}).(bool)
	return diagnostic
}

func WithDebugKeyOverride(ctx context.Context, override DebugKeyOverride) context.Context {
	return context.WithValue(WithDiagnosticRequest(ctx), debugKeyOverrideContextKey{}, override)
}

func debugKeyOverrideFrom(ctx context.Context) (DebugKeyOverride, bool) {
	override, ok := ctx.Value(debugKeyOverrideContextKey{}).(DebugKeyOverride)
	return override, ok && override.KeyID != ""
}

func (g *Gateway) doUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs) (*http.Response, models.Route, error) {
	resp, effectiveRoute, attempts, err := g.doUpstreamTiers(ctx, route, bodies, ids, 0)
	if _, selected := debugKeyOverrideFrom(ctx); selected {
		return resp, effectiveRoute, err
	}
	if err != nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, effectiveRoute, err
	}
	origBody := resp.Body
	errBody, readErr := io.ReadAll(io.LimitReader(origBody, 1<<20))
	// The original network body is always closed here: the retry path below
	// reuses the connection, otherwise downstream receives a fresh in-memory
	// reader over the cached bytes.
	httpx.DrainAndClose(origBody)
	if readErr != nil {
		resp.Body = io.NopCloser(bytes.NewReader(errBody))
		return resp, effectiveRoute, nil
	}
	// Restore the body so downstream error handling still sees the original
	// payload when no retry happens below.
	resp.Body = io.NopCloser(bytes.NewReader(errBody))
	// Log a capped snippet of the upstream error body so the 400 class is
	// visible without dumping full payloads. Bodies stay out of attempt
	// records; the hub redactor scrubs configured secrets from log output.
	if len(errBody) > 0 {
		redactor := config.NewSecretRedactor()
		redactor.Replace(g.cfg)
		snippet := redactor.String(string(errBody))
		if len(snippet) > 240 {
			snippet = snippet[:240]
		}
		g.logger.Info("upstream error returned or retried", "component", "upstream", "event", "upstream_error_body", "request_id", ids.Request, "model", route.ID, "status", resp.StatusCode, "snippet", snippet)
	}
	if !isStaleReasoningReference(errBody) {
		return resp, effectiveRoute, nil
	}
	stripped, changed := stripStaleReasoningInputs(effectiveRoute, bodies)
	if !changed {
		return resp, effectiveRoute, nil
	}
	// The referenced reasoning items belong to an upstream chain this session
	// can no longer address (e.g. an interrupted stream). Replay once without
	// them instead of failing the client request outright. Attempt numbering
	// continues from the first round so monitoring never shows duplicate attempt
	// numbers for one request.
	//
	// The client's session is deliberately preserved: it carries key/proxy
	// affinity and upstream session correlation. Minting a fresh session made
	// the replay land on a different node with a cold prompt cache and detached
	// the reply from the chain the client still holds; the stale references are
	// already removed from the payload itself.
	g.logger.Info("retrying upstream without stale reasoning references", "component", "upstream", "event", "reasoning_reference_retry", "request_id", ids.Request, "model", route.ID, "tier", effectiveRoute.Tier, "attempt_offset", attempts)
	retryResp, retryRoute, _, retryErr := g.doUpstreamTiers(ctx, effectiveRoute, stripped, ids, attempts)
	if retryErr != nil || retryResp == nil || retryResp.StatusCode/100 != 2 {
		retryStatus := 0
		if retryResp != nil {
			retryStatus = retryResp.StatusCode
			httpx.DrainAndClose(retryResp.Body)
		}
		g.logger.Warn("reasoning reference retry failed; returning original error", "component", "upstream", "event", "reasoning_reference_retry_failed", "request_id", ids.Request, "model", route.ID, "tier", effectiveRoute.Tier, "error", retryErr, "retry_status", retryStatus)
		fallback := *resp
		fallback.Body = io.NopCloser(bytes.NewReader(errBody))
		return &fallback, effectiveRoute, nil
	}
	return retryResp, retryRoute, nil
}

// isStaleReasoningReference reports whether an upstream 400 body describes a
// reasoning item/reference the server no longer recognizes, such as
// "Referenced reasoning item 'rs_...' was not found or has expired".
// Generic validation errors that merely mention reasoning (e.g. "unknown
// reasoning field") must NOT match, so both the target phrase and the
// gone/expired marker are required.
func isStaleReasoningReference(body []byte) bool {
	text := strings.ToLower(string(body))
	if !strings.Contains(text, "reasoning item") && !strings.Contains(text, "reasoning reference") {
		return false
	}
	for _, marker := range []string{"not found", "expir", "does not exist", "no longer"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// stripStaleReasoningInputs removes server-issued reasoning references from
// Responses-protocol upstream payloads: replayed "reasoning" input items and
// any previous_response_id chain link. Other tiers/protocols are passed
// through untouched. It reports whether any payload actually changed.
func stripStaleReasoningInputs(route models.Route, bodies map[config.Tier][]byte) (map[config.Tier][]byte, bool) {
	changed := false
	out := make(map[config.Tier][]byte, len(bodies))
	for tier, body := range bodies {
		if len(body) == 0 || route.ProtocolFor(tier) != wire.Responses {
			out[tier] = body
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			out[tier] = body
			continue
		}
		tierChanged := false
		if _, ok := payload["previous_response_id"]; ok {
			delete(payload, "previous_response_id")
			tierChanged = true
		}
		if raw, ok := payload["input"].([]any); ok {
			kept := make([]any, 0, len(raw))
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok && jsonutil.StringAt(m, "type") == "reasoning" {
					tierChanged = true
					continue
				}
				kept = append(kept, item)
			}
			if tierChanged {
				payload["input"] = kept
			}
		}
		if !tierChanged {
			out[tier] = body
			continue
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			out[tier] = body
			continue
		}
		out[tier] = encoded
		changed = true
	}
	return out, changed
}

func (g *Gateway) doUpstreamTiers(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, models.Route, int, error) {
	var lastResponse *http.Response
	var lastErr error
	effectiveRoute := route
	attempts := attemptOffset
	g.dumpOutboundBodies(route, bodies, ids, attemptOffset)
	if override, selected := debugKeyOverrideFrom(ctx); selected {
		resp, err, used := g.doSelectedKeyUpstream(ctx, route, bodies, ids, override, attemptOffset)
		return resp, route, used, err
	}
	if route.Anonymous {
		resp, err, used := g.doAnonymousUpstream(ctx, route, bodies, ids, attempts)
		attempts += used
		if err == nil && resp != nil && resp.StatusCode/100 == 2 {
			return resp, route, attempts, nil
		}
		lastResponse, lastErr = resp, err
		if peekDeterministic(resp) {
			// The request itself was rejected. Another exit or tier gets the
			// same answer, and retrying only buries it under the next error, so
			// hand the upstream 4xx back unchanged.
			g.logger.Warn("upstream rejected the request; not retrying on other tiers", "component", "upstream", "event", "deterministic_rejection", "request_id", ids.Request, "attempts", attempts, "model", route.ID)
			return resp, route, attempts, nil
		}
		if len(route.KeyTiers) > 0 {
			g.logger.Debug("anonymous request did not succeed; entering preferred key tiers", "component", "upstream", "event", "anonymous_fallback", "request_id", ids.Request, "attempts", attempts, "key_tiers", route.KeyTiers)
		}
	}
	if ctx.Err() != nil {
		// The anonymous phase already consumed the request budget. Entering the
		// authenticated tiers here would only fire instant attempts against a
		// dead context, cooling keys that were never really tried.
		if lastResponse != nil {
			return lastResponse, effectiveRoute, attempts, nil
		}
		if lastErr == nil {
			lastErr = ctx.Err()
		}
		return nil, effectiveRoute, attempts, lastErr
	}

	keyTiers := route.KeyTiers
	if !route.Anonymous && len(keyTiers) == 0 && (route.Tier == config.TierZen || route.Tier == config.TierGo) {
		keyTiers = []config.Tier{route.Tier}
	}
	for _, tier := range keyTiers {
		if attempts >= g.cfg.Retry.MaxAttempts {
			// The single request budget is spent; keep the last tier's response
			// instead of replacing it with a budget-exhausted error.
			break
		}
		if lastResponse != nil {
			httpx.DrainAndClose(lastResponse.Body)
			lastResponse = nil
		}
		keyRoute := route
		keyRoute.Tier = tier
		keyRoute.Anonymous = false
		keyRoute.Protocol = route.ProtocolFor(tier)
		effectiveRoute = keyRoute
		resp, err, used := g.doKeyUpstream(ctx, keyRoute, bodies, ids, attempts)
		attempts += used
		if err == nil && resp != nil && resp.StatusCode/100 == 2 {
			return resp, keyRoute, attempts, nil
		}
		if peekDeterministic(resp) {
			g.logger.Warn("upstream rejected the request; not trying other tiers", "component", "upstream", "event", "deterministic_rejection", "request_id", ids.Request, "attempts", attempts, "tier", keyRoute.Tier, "status", resp.StatusCode)
			return resp, keyRoute, attempts, nil
		}
		lastResponse, lastErr = resp, err
	}
	if lastResponse != nil {
		return lastResponse, effectiveRoute, attempts, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no usable upstream route")
	}
	return nil, effectiveRoute, attempts, lastErr
}

// doAnonymousUpstream tries every currently available proxy at most once. Any
// failure, including an HTTP error response, advances to the next proxy. Only a
// successful response ends the anonymous phase; exhausting the proxy cursor
// returns control to the preferred authenticated tiers.
func (g *Gateway) doAnonymousUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, error, int) {
	var lastResponse *http.Response
	var lastErr error
	attempts := 0
	remaining := g.cfg.Retry.MaxAttempts - attemptOffset
	if remaining < 1 {
		return nil, errors.New("request attempt budget exhausted"), 0
	}
	limit := g.anonymous.Len()
	if g.egressEnabled() {
		limit = remaining
	} else if limit > remaining {
		limit = remaining
	}
	// The breaker holds the lane shut for an hour-scale window after repeated
	// failures: poking a flagged upstream again would only extend the block.
	if g.anonymous.Paused() {
		return nil, errAnonymousLanePaused, 0
	}
	body := bodies[config.TierZen]
	if len(body) == 0 {
		return nil, errors.New("no prepared Zen request body"), 0
	}
	// The anonymous free tier only serves agent-shaped streaming requests
	// carrying the official core tool definitions (anything else is rejected
	// with 403 FreeTierError). Normalize the wire body here; the gateway
	// collapses the stream back when the downstream client asked for a plain
	// JSON reply. Key tiers keep their original bodies.
	body = prepareAnonymousBody(body, route.ProtocolFor(config.TierZen))
	anonymousKey := healthKeyFor(g.cfg.Upstream.Anonymous, route, config.TierZen, body)

	var cursor *anonymousCursor
	if !g.egressEnabled() {
		c := g.anonymous.CursorFor(ids.Session)
		cursor = &c
	}
	exclude := make(map[string]struct{})
	var retryWait time.Duration
	for attempts < limit {
		if ctx.Err() != nil {
			// The request-level budget is already gone. Stop scanning instead
			// of firing attempts that cannot succeed and then recording them
			// as failures against proxies that were never really tried.
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			break
		}

		var client *http.Client
		var node *anonymousNode
		var egressAttempt *routing.Attempt
		var proxy *proxyTransport
		if g.egressEnabled() {
			target, err := g.egressTarget(g.cfg.Upstream.Anonymous)
			if err != nil {
				return nil, err, attempts
			}
			egressAttempt, err = g.selectEgress(ctx, target, anonymousKey, exclude, ids.Session)
			if err != nil {
				if retryWait > 0 && waitForRetryAfter(ctx, retryWait) {
					retryWait = 0
					continue
				}
				lastErr = err
				break
			}
			retryWait = 0
			exclude[egressAttempt.NodeID] = struct{}{}
			client = egressAttempt.Client()
			setRequestCredentialEgress(ctx, config.TierZen, "anonymous", "anonymous", true, egressAttempt)
		} else {
			node = cursor.Next()
			if node == nil {
				break
			}
			proxy = node.proxy
			client = proxy.client
			setRequestCredential(ctx, config.TierZen, "anonymous", "anonymous", true, proxy)
		}

		attempts++
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Attempts = attemptOffset + attempts
			meta.Tier = string(config.TierZen)
		}
		if lastResponse != nil {
			httpx.DrainAndClose(lastResponse.Body)
			lastResponse = nil
		}
		req, err := newUpstreamRequest(ctx, g.cfg.Upstream.Anonymous, route.Protocol, body, ids, anonymousZenKey)
		if err != nil {
			if egressAttempt != nil {
				egressAttempt.Release()
			}
			return nil, err, attempts
		}
		started := time.Now()
		var streamCallback func(bool)
		if egressAttempt != nil {
			attemptRef := egressAttempt
			streamCallback = func(clean bool) {
				if clean {
					attemptRef.ReportStream(health.StreamSuccess)
				} else {
					attemptRef.ReportStream(health.StreamFailure)
				}
			}
		}
		resp, verdict, err := doInferenceAttempt(client, req, time.Duration(g.cfg.Performance.FirstEventTimeoutSeconds)*time.Second, g.attemptTimeout,
			route.ProtocolFor(config.TierZen), requestMode(body), g.healthMaxPeek(), streamCallback)
		duration := time.Since(started)
		if ctx.Err() != nil {
			// The parent budget expired while this attempt was in flight. Its
			// outcome says nothing about this exit, so record nothing and stop
			// scanning.
			if egressAttempt != nil {
				egressAttempt.Release()
			}
			lastResponse, lastErr = resp, err
			if lastErr == nil && lastResponse == nil {
				lastErr = ctx.Err()
			}
			break
		}

		if peekDeterministic(resp) {
			// A request-level rejection is identical on every exit; stop the
			// walk without cooling the exit for the client's mistake.
			if egressAttempt != nil {
				egressAttempt.Release()
			}
			g.logger.Warn("upstream rejected the request; ending the anonymous walk", "component", "upstream", "event", "deterministic_rejection", "request_id", ids.Request, "attempt", attempts, "status", resp.StatusCode)
			lastResponse, lastErr = resp, nil
			break
		}

		if egressAttempt != nil {
			classification := g.reportEgressAttempt(ctx, route, ids, attemptOffset+attempts, "anonymous", "anonymous", true, egressAttempt, resp, err, verdict, duration)
			status := upstreamStatus(resp)
			switch classification.Kind {
			case routing.Success, routing.Neutral:
				g.anonymous.NoteSuccess()
				g.logger.Debug("anonymous upstream accepted request", "component", "upstream", "event", "anonymous_attempt_succeeded", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "node", egressAttempt.NodeName, "node_id", egressAttempt.NodeID, "status", status, "duration_ms", duration.Milliseconds())
				return resp, nil, attempts
			case routing.UpstreamFailure:
				g.noteAnonymousFailure(ids, attempts)
				lastResponse, lastErr = resp, err
				if classification.UpstreamWide {
					return resp, nil, attempts
				}
				continue
			case routing.ClientCancel:
				lastResponse, lastErr = resp, err
				return resp, lastErr, attempts
			default:
				g.noteAnonymousFailure(ids, attempts)
				lastResponse, lastErr = resp, egressProxyError(egressAttempt, classification, err)
				if classification.Kind == routing.ExitFailure && resp != nil && g.honorRetryAfter() {
					retryWait = parseRetryAfter(resp.Header.Get("Retry-After"))
				}
				if err != nil {
					g.logger.Debug("anonymous egress attempt failed", "component", "upstream", "event", "anonymous_attempt_transport_failed", "request_id", ids.Request, "attempt", attempts, "node", egressAttempt.NodeName, "node_id", egressAttempt.NodeID, "duration_ms", duration.Milliseconds(), "error", err)
				} else {
					g.logger.Debug("anonymous upstream returned an error response; trying the next exit", "component", "upstream", "event", "anonymous_attempt_response_failed", "request_id", ids.Request, "attempt", attempts, "node", egressAttempt.NodeName, "node_id", egressAttempt.NodeID, "status", status, "duration_ms", duration.Milliseconds())
				}
				continue
			}
		}

		g.observeAnonymousResult(ctx, node, resp, err)
		g.recordUpstreamAttempt(ctx, route, ids, attemptOffset+attempts, "anonymous", "anonymous", true, proxyTrace(proxy), resp, err, duration)
		if err == nil && resp.StatusCode/100 == 2 {
			g.anonymous.NoteSuccess()
			g.logger.Debug("anonymous upstream accepted request", "component", "upstream", "event", "anonymous_attempt_succeeded", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "proxy", config.RedactURL(proxy.name), "status", resp.StatusCode, "duration_ms", duration.Milliseconds())
			return resp, nil, attempts
		}
		g.noteAnonymousFailure(ids, attempts)
		lastResponse = resp
		lastErr = asProxyUnavailable(proxy, err)
		if err != nil {
			g.logger.Debug("anonymous transport attempt failed", "component", "upstream", "event", "anonymous_attempt_transport_failed", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "proxy", config.RedactURL(proxy.name), "duration_ms", duration.Milliseconds(), "error", err)
		} else {
			g.logger.Debug("anonymous upstream returned an error response; trying the next proxy", "component", "upstream", "event", "anonymous_attempt_response_failed", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "proxy", config.RedactURL(proxy.name), "status", resp.StatusCode, "duration_ms", duration.Milliseconds())
		}
	}
	if lastResponse != nil {
		return lastResponse, nil, attempts
	}
	if lastErr == nil {
		lastErr = errors.New("no healthy anonymous proxies available")
		if !g.egressEnabled() && !g.transports.hasHealthy() {
			lastErr = fmt.Errorf("no healthy anonymous proxies available: %w", errProxyUnavailable)
		}
	}
	return nil, lastErr, attempts
}

// anonymousCoreTools are the tool names the anonymous free tier expects on
// an agent-shaped request. Requests without a shell tool and a read tool are
// rejected with 403 FreeTierError.
//
// "shell" is the name OpenCode 2.x ships; the pre-2.x "bash" spelling is
// deliberately not injected because the free tier validates the definition of
// every tool it recognises, and a "bash" definition the gateway synthesises
// does not match the one upstream expects. Since 2026-10-14 name presence is
// not enough either: the upstream compares each recognised tool's description
// and parameter schema against the official CLI's definitions byte-for-byte
// (minimal stand-ins 403), which is why the genuine definitions below are
// embedded. Tool order is irrelevant and extra client tools are allowed.
var anonymousCoreTools = []string{"shell", "edit", "glob", "grep", "read"}

// opencodeToolsJSON holds the genuine OpenCode tool definitions (description
// and JSON schema) captured from the official client. The free tier checks the
// definition of every tool whose name it recognises, so a synthesised stub —
// even one carrying the right name — is rejected with 403 FreeTierError. The
// gateway therefore ships the real definitions and uses them both for the tools
// it injects and for any same-named tool the downstream client declared.
//
//go:embed opencode_tools.json
var opencodeToolsJSON []byte

type opencodeToolDefinition struct {
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

var (
	opencodeToolsOnce sync.Once
	opencodeTools     map[string]opencodeToolDefinition
)

// knownOpencodeTool returns the genuine definition for an OpenCode tool name.
func knownOpencodeTool(name string) (opencodeToolDefinition, bool) {
	opencodeToolsOnce.Do(func() {
		definitions := map[string]opencodeToolDefinition{}
		if err := json.Unmarshal(opencodeToolsJSON, &definitions); err != nil {
			definitions = map[string]opencodeToolDefinition{}
		}
		opencodeTools = definitions
	})
	definition, ok := opencodeTools[name]
	return definition, ok
}

// prepareAnonymousBody returns a copy of body normalized for the anonymous
// free tier: streaming enabled plus the core agent tools present with genuine
// definitions. Bodies that already satisfy all of that (or are not JSON
// objects) are returned unchanged. System One payloads are decision requests,
// not agent traffic, so they are forwarded verbatim; injecting streaming or
// tool definitions would make the upstream reject them.
func prepareAnonymousBody(body []byte, protocol wire.Protocol) []byte {
	if protocol == wire.SystemOne {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	changed := false
	if streaming, ok := payload["stream"].(bool); !ok || !streaming {
		payload["stream"] = true
		changed = true
	}
	if ensureAnonymousChatUsage(payload, protocol) {
		changed = true
	}
	if ensureAnonymousTools(payload, protocol) {
		changed = true
	}
	if !changed {
		return body
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

// ensureAnonymousChatUsage keeps token usage available when a non-streaming
// request is forced onto the Chat SSE path. OpenAI-compatible Chat streams
// require stream_options.include_usage for the final usage event.
func ensureAnonymousChatUsage(payload map[string]any, protocol wire.Protocol) bool {
	if protocol != wire.Chat {
		return false
	}
	options, ok := payload["stream_options"].(map[string]any)
	if !ok {
		payload["stream_options"] = map[string]any{"include_usage": true}
		return true
	}
	includeUsage, ok := options["include_usage"].(bool)
	if ok && includeUsage {
		return false
	}
	options["include_usage"] = true
	return true
}

// ensureAnonymousTools makes the tool list read as an agent session: every
// recognised OpenCode tool carries its genuine definition, and the missing
// core tools are appended. It reports whether the payload changed. Tools whose
// names upstream does not recognise are left exactly as the client declared
// them, so downstream function calling keeps working.
func ensureAnonymousTools(payload map[string]any, protocol wire.Protocol) bool {
	raw, exists := payload["tools"]
	if !exists {
		payload["tools"] = anonymousToolset(protocol, nil)
		return true
	}
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	changed := false
	present := make(map[string]bool, len(items))
	for index, item := range items {
		name := anonymousToolName(protocol, item)
		if name == "" {
			continue
		}
		present[name] = true
		// A declared definition for a recognised tool name is replaced with the
		// genuine one: the free tier rejects a stub, and a client that happens to
		// use the same name with its own schema would otherwise fail the request.
		if _, known := knownOpencodeTool(name); known {
			items[index] = anonymousTool(protocol, name)
			changed = true
		}
	}
	missing := make([]string, 0, len(anonymousCoreTools))
	for _, name := range anonymousCoreTools {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		items = append(items, anonymousToolset(protocol, missing)...)
		changed = true
	}
	if !changed {
		return false
	}
	payload["tools"] = items
	return true
}

func anonymousToolName(protocol wire.Protocol, item any) string {
	entry, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	if protocol == wire.Chat {
		return jsonutil.StringAt(entry, "function", "name")
	}
	return jsonutil.StringAt(entry, "name")
}

// anonymousToolset returns the official wire-shaped definitions for the
// given tool names (all core tools when names is nil).
func anonymousToolset(protocol wire.Protocol, names []string) []any {
	if names == nil {
		names = anonymousCoreTools
	}
	tools := make([]any, 0, len(names))
	for _, name := range names {
		if tool := anonymousTool(protocol, name); tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

// anonymousTool renders one core tool in the shape the given protocol expects,
// using the genuine OpenCode definition whenever one is known. SystemOne
// payloads never reach this path (prepareAnonymousBody forwards them
// verbatim), so it falls back to the Chat shape.
func anonymousTool(protocol wire.Protocol, name string) map[string]any {
	description := "Agent tool " + name
	parameters := map[string]any{"type": "object", "properties": map[string]any{}}
	if definition, ok := knownOpencodeTool(name); ok {
		description = definition.Description
		if definition.Parameters != nil {
			parameters = definition.Parameters
		}
	}
	switch protocol {
	case wire.Chat:
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
				"parameters":  parameters,
			},
		}
	case wire.Anthropic:
		return map[string]any{
			"name":         name,
			"description":  description,
			"input_schema": parameters,
		}
	default:
		return map[string]any{
			"type":        "function",
			"name":        name,
			"description": description,
			"parameters":  parameters,
		}
	}
}

// requestBodyDumpLimit caps how much of an outbound body is written to the log.
const requestBodyDumpLimit = 64 << 10

// dumpOutboundBodies records the exact bytes about to be sent upstream when
// logging.dump_request_bodies is enabled. It exists so retry and translation
// problems (stale rs_* references, re-encoded history, tier re-encoding) can be
// diagnosed without patching the binary. Credentials travel in headers and never
// in the body. The logger redacts configured secrets, but conversation content
// can still be sensitive. Reference IDs are retained for replay diagnostics.
func (g *Gateway) dumpOutboundBodies(route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) {
	if !g.cfg.Logging.DumpRequestBodies || len(bodies) == 0 {
		return
	}
	phase := "initial"
	if attemptOffset > 0 {
		phase = "retry"
	}
	tiers := make([]string, 0, len(bodies))
	for tier := range bodies {
		tiers = append(tiers, string(tier))
	}
	sort.Strings(tiers)
	for _, name := range tiers {
		tier := config.Tier(name)
		body := bodies[tier]
		if len(body) == 0 {
			continue
		}
		sum := sha256.Sum256(body)
		truncated := len(body) > requestBodyDumpLimit
		payload := body
		if truncated {
			payload = body[:requestBodyDumpLimit]
		}
		g.logger.Debug("outbound upstream request body", "component", "upstream", "event", "upstream_request_body",
			"request_id", ids.Request, "model", route.ID, "tier", tier, "protocol", route.ProtocolFor(tier),
			"phase", phase, "bytes", len(body), "sha256", hex.EncodeToString(sum[:8]),
			"truncated", truncated, "body", string(payload))
	}
}

// shapeKeyBody normalizes key-tier free-model wire bodies to agent shape
// (stream + core tools), mirroring the anonymous lane. Upstream now
// rejects non-agent-shaped free-tier requests on every lane with 403
// FreeTierError; paid models keep their original bodies. It reports
// whether the body changed so the gateway can collapse the SSE stream a
// non-streaming client receives back.
func (g *Gateway) shapeKeyBody(body []byte, route models.Route, tier config.Tier) ([]byte, bool) {
	if !g.catalog.IsFreeModel(route.ID) {
		return body, false
	}
	shaped := prepareAnonymousBody(body, route.ProtocolFor(tier))
	return shaped, !bytes.Equal(shaped, body)
}

// key. It performs no failover at all — no anonymous lane, no other key and no
// other tier — so the outcome describes that one key. It also leaves pool state
// untouched on purpose: a diagnostic request must never cool a production key or
// evict a proxy, which is exactly what made a bad Playground request degrade
// live traffic before.
func (g *Gateway) doSelectedKeyUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, override DebugKeyOverride, attemptOffset int) (*http.Response, error, int) {
	nodes := g.zenNodes
	baseURL := g.cfg.Upstream.Zen
	if override.Tier == config.TierGo {
		nodes = g.goNodes
		baseURL = g.cfg.Upstream.Go
	}
	node := nodes.NodeByID(override.KeyID)
	if node == nil {
		return nil, fmt.Errorf("selected %s key is no longer configured", override.Tier), 0
	}
	body := bodies[override.Tier]
	if len(body) == 0 {
		return nil, fmt.Errorf("no prepared %s request body", override.Tier), 0
	}
	if shaped, changed := g.shapeKeyBody(body, route, override.Tier); changed {
		body = shaped
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Shaped = true
		}
	}
	if meta := telemetry.MetaFromContext(ctx); meta != nil {
		meta.Attempts = attemptOffset + 1
		meta.Tier = string(override.Tier)
	}
	keyID := config.KeyDisplayID(node.key)
	req, err := newUpstreamRequest(ctx, baseURL, route.ProtocolFor(override.Tier), body, ids, node.key)
	if err != nil {
		return nil, err, 0
	}

	var client *http.Client
	var egressAttempt *routing.Attempt
	var proxy *proxyTransport
	if g.egressEnabled() {
		target, targetErr := g.egressTarget(baseURL)
		if targetErr != nil {
			return nil, targetErr, 0
		}
		egressAttempt, err = g.selectEgress(ctx, target, healthKeyFor(baseURL, route, override.Tier, body), nil, keyID)
		if err != nil {
			return nil, err, 0
		}
		client = egressAttempt.Client()
		setRequestCredentialEgress(ctx, override.Tier, keyID, "key", false, egressAttempt)
	} else {
		proxy = nodes.Proxy(node)
		if proxy == nil {
			return nil, errors.New("selected upstream key has no proxy binding"), 0
		}
		client = proxy.client
		setRequestCredential(ctx, override.Tier, keyID, "key", false, proxy)
	}

	started := time.Now()
	resp, _, err := doInferenceAttempt(client, req, time.Duration(g.cfg.Performance.FirstEventTimeoutSeconds)*time.Second, g.attemptTimeout,
		route.ProtocolFor(override.Tier), requestMode(body), 0, nil)
	duration := time.Since(started)
	if egressAttempt != nil {
		// Diagnostics must not change pool state; release the in-flight slot
		// and record only telemetry.
		egressAttempt.Release()
		g.recordUpstreamAttempt(ctx, route, ids, attemptOffset+1, keyID, "key", false,
			egressTrace{Proxy: "egress", ExitNode: egressAttempt.NodeName, ExitID: egressAttempt.NodeID}, resp, err, duration)
	} else {
		g.recordUpstreamAttempt(ctx, route, ids, attemptOffset+1, keyID, "key", false, proxyTrace(proxy), resp, err, duration)
	}
	if err != nil {
		if egressAttempt != nil && egressAttempt.ProxyURL != nil {
			return nil, &proxyUnavailableError{proxy: egressAttempt.NodeName, err: err}, 1
		}
		if proxy != nil {
			return nil, asProxyUnavailable(proxy, err), 1
		}
		return nil, err, 1
	}
	return resp, nil, 1
}

func (g *Gateway) doKeyUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, error, int) {
	var lastResponse *http.Response
	var lastErr error
	nodes := g.zenNodes
	baseURL := g.cfg.Upstream.Zen
	if route.Tier == config.TierGo {
		nodes = g.goNodes
		baseURL = g.cfg.Upstream.Go
	}
	if nodes.Len() == 0 {
		return nil, fmt.Errorf("no %s nodes configured", route.Tier), 0
	}
	remaining := g.cfg.Retry.MaxAttempts - attemptOffset
	if remaining < 1 {
		return nil, errors.New("request attempt budget exhausted"), 0
	}
	cursor := nodes.CursorFor(ids.Session)
	body := bodies[route.Tier]
	if len(body) == 0 {
		return nil, fmt.Errorf("no prepared %s request body", route.Tier), 0
	}
	if shaped, changed := g.shapeKeyBody(body, route, route.Tier); changed {
		body = shaped
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Shaped = true
		}
	}
	attempts := 0
	var current *upstreamNode
	exclude := make(map[string]struct{})
	var retryWait time.Duration
	attemptKey := healthKeyFor(baseURL, route, route.Tier, body)
	for attempts < remaining {
		if ctx.Err() != nil {
			// Same guard as the anonymous loop: an exhausted request budget
			// must not be spent rotating keys that were never really tried.
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			break
		}
		if current == nil {
			current = cursor.Next()
			if current == nil {
				break
			}
		}
		keyID := config.KeyDisplayID(current.key)
		req, err := newUpstreamRequest(ctx, baseURL, route.Protocol, body, ids, current.key)
		if err != nil {
			return nil, err, attempts
		}

		var client *http.Client
		var egressAttempt *routing.Attempt
		var proxy *proxyTransport
		if g.egressEnabled() {
			target, err := g.egressTarget(baseURL)
			if err != nil {
				return nil, err, attempts
			}
			egressAttempt, err = g.selectEgress(ctx, target, attemptKey, exclude, keyID)
			if err != nil {
				if retryWait > 0 && waitForRetryAfter(ctx, retryWait) {
					retryWait = 0
					continue
				}
				lastErr = err
				break
			}
			retryWait = 0
			exclude[egressAttempt.NodeID] = struct{}{}
			client = egressAttempt.Client()
			setRequestCredentialEgress(ctx, route.Tier, keyID, "key", false, egressAttempt)
		} else {
			proxy = nodes.Proxy(current)
			if proxy == nil {
				lastErr = errors.New("upstream key has no proxy binding")
				break
			}
			client = proxy.client
			setRequestCredential(ctx, route.Tier, keyID, "key", false, proxy)
		}
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Attempts = attemptOffset + attempts + 1
			meta.Tier = string(route.Tier)
		}
		if lastResponse != nil {
			httpx.DrainAndClose(lastResponse.Body)
			lastResponse = nil
		}
		attempts++
		attemptStarted := time.Now()
		var streamCallback func(bool)
		if egressAttempt != nil {
			attemptRef := egressAttempt
			streamCallback = func(clean bool) {
				if clean {
					attemptRef.ReportStream(health.StreamSuccess)
				} else {
					attemptRef.ReportStream(health.StreamFailure)
				}
			}
		}
		resp, verdict, err := doInferenceAttempt(client, req, time.Duration(g.cfg.Performance.FirstEventTimeoutSeconds)*time.Second, g.attemptTimeout,
			route.ProtocolFor(route.Tier), requestMode(body), g.healthMaxPeek(), streamCallback)
		attemptDuration := time.Since(attemptStarted)
		if ctx.Err() != nil {
			// The request budget expired while this attempt was in flight. A
			// cancelled context says nothing about the key or the exit.
			if egressAttempt != nil {
				egressAttempt.Release()
			}
			lastResponse, lastErr = resp, err
			if lastErr == nil && lastResponse == nil {
				lastErr = ctx.Err()
			}
			break
		}

		if peekDeterministic(resp) {
			// Request-level rejection: another key or tier would answer the
			// same, so stop rotating and forward this response.
			if egressAttempt != nil {
				egressAttempt.Release()
			}
			g.logger.Warn("upstream rejected the request; ending key rotation", "component", "upstream", "event", "deterministic_rejection", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "status", resp.StatusCode)
			return resp, nil, attempts
		}

		if egressAttempt != nil {
			classification := g.reportEgressAttempt(ctx, route, ids, attemptOffset+attempts, keyID, "key", false, egressAttempt, resp, err, verdict, attemptDuration)
			status := upstreamStatus(resp)
			switch classification.Kind {
			case routing.Success, routing.Neutral:
				nodes.MarkSuccess(current)
				g.logger.Debug("upstream accepted request", "component", "upstream", "event", "attempt_succeeded", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "node", egressAttempt.NodeName, "node_id", egressAttempt.NodeID, "status", status, "duration_ms", attemptDuration.Milliseconds())
				return resp, nil, attempts
			case routing.KeyFailure:
				nodes.MarkFailure(current, resp, err)
				lastResponse, lastErr = resp, err
				current = nil
				continue
			case routing.UpstreamFailure:
				lastResponse, lastErr = resp, err
				if classification.UpstreamWide {
					nodes.MarkFailure(current, resp, err)
					return resp, nil, attempts
				}
				continue
			case routing.ClientCancel:
				lastResponse, lastErr = resp, err
				return resp, lastErr, attempts
			default:
				lastResponse, lastErr = resp, egressProxyError(egressAttempt, classification, err)
				if classification.Kind == routing.ExitFailure && resp != nil && g.honorRetryAfter() {
					retryWait = parseRetryAfter(resp.Header.Get("Retry-After"))
				}
				if err != nil {
					g.logger.Debug("upstream egress attempt failed", "component", "upstream", "event", "attempt_transport_failed", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "node", egressAttempt.NodeName, "node_id", egressAttempt.NodeID, "duration_ms", attemptDuration.Milliseconds(), "error", err)
				} else {
					g.logger.Debug("upstream returned a retryable response", "component", "upstream", "event", "attempt_retryable_response", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "node", egressAttempt.NodeName, "node_id", egressAttempt.NodeID, "status", status, "duration_ms", attemptDuration.Milliseconds())
				}
				continue
			}
		}

		g.observeKeyResult(ctx, nodes, current, proxy, resp, err)
		g.recordUpstreamAttempt(ctx, route, ids, attemptOffset+attempts, keyID, "key", false, proxyTrace(proxy), resp, err, attemptDuration)
		if err == nil && resp.StatusCode/100 == 2 {
			g.logger.Debug("upstream accepted request", "component", "upstream", "event", "attempt_succeeded", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "proxy", config.RedactURL(proxy.name), "status", resp.StatusCode, "duration_ms", attemptDuration.Milliseconds())
			return resp, nil, attempts
		}
		// Request-shape errors are deterministic and must leave this tier without
		// rotating through unrelated keys. The outer route may still try the next
		// tier in prefer order. Authentication, throttling, server, and transport
		// failures remain retryable inside this tier.
		if isNonRetryableClientResponse(resp, err) {
			g.logger.Debug("upstream rejected a non-retryable request", "component", "upstream", "event", "attempt_rejected", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "status", resp.StatusCode, "proxy", config.RedactURL(proxy.name), "duration_ms", attemptDuration.Milliseconds())
			return resp, nil, attempts
		}
		lastResponse = resp
		lastErr = asProxyUnavailable(proxy, err)
		if err != nil {
			g.logger.Debug("upstream transport attempt failed", "component", "upstream", "event", "attempt_transport_failed", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "proxy", config.RedactURL(proxy.name), "duration_ms", attemptDuration.Milliseconds(), "error", err)
		} else {
			g.logger.Debug("upstream returned a retryable response", "component", "upstream", "event", "attempt_retryable_response", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "status", resp.StatusCode, "proxy", config.RedactURL(proxy.name), "duration_ms", attemptDuration.Milliseconds())
		}
		current = nil
	}
	if lastResponse != nil {
		return lastResponse, nil, attempts
	}
	return nil, lastErr, attempts
}

// Diagnostics use the same routing and transports as production, but must not
// update cooldowns, health, or bindings, including after successful attempts.
func (g *Gateway) observeKeyResult(ctx context.Context, nodes *nodePool, node *upstreamNode, proxy *proxyTransport, resp *http.Response, err error) {
	if isDiagnosticRequest(ctx) {
		return
	}
	status := upstreamStatus(resp)
	proxyFailed := g.syncProxyResult(ctx, proxy, status, err)
	if err == nil && status/100 == 2 || isNonRetryableClientResponse(resp, err) {
		nodes.MarkSuccess(node)
		return
	}
	if !proxyFailed || nodes.Proxy(node) == proxy {
		nodes.MarkFailure(node, resp, err)
	}
}

func (g *Gateway) observeAnonymousResult(ctx context.Context, node *anonymousNode, resp *http.Response, err error) {
	if isDiagnosticRequest(ctx) {
		return
	}
	status := upstreamStatus(resp)
	g.syncProxyResult(ctx, node.proxy, status, err)
	if err == nil && status/100 == 2 {
		g.anonymous.MarkSuccess(node)
	} else {
		g.anonymous.MarkFailure(node, resp, err)
	}
}

func upstreamStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func setRequestCredential(ctx context.Context, tier config.Tier, keyID, channel string, anonymous bool, proxy *proxyTransport) {
	meta := telemetry.MetaFromContext(ctx)
	if meta == nil {
		return
	}
	meta.Tier = string(tier)
	meta.KeyID = keyID
	meta.Channel = channel
	meta.Anonymous = anonymous
	meta.Proxy = ""
	meta.ExitNode = ""
	meta.ExitID = ""
	if proxy != nil {
		meta.Proxy = config.RedactURL(proxy.name)
	}
}

func requestCredential(ctx context.Context) (string, string, bool) {
	meta := telemetry.MetaFromContext(ctx)
	if meta == nil {
		return "", "", false
	}
	return meta.KeyID, meta.Channel, meta.Anonymous
}

func (g *Gateway) recordUpstreamAttempt(ctx context.Context, route models.Route, ids identity.RequestIDs, attempt int, keyID, channel string, anonymous bool, trace egressTrace, resp *http.Response, err error, duration time.Duration) {
	status := upstreamStatus(resp)
	success := err == nil && status >= 200 && status < 300
	outcome := "retryable_failure"
	if success {
		outcome = "success"
	} else if err != nil {
		outcome = "transport_error"
	} else if isNonRetryableClientResponse(resp, nil) {
		outcome = "rejected"
	}
	if meta := telemetry.MetaFromContext(ctx); meta != nil {
		meta.AttemptOutcome = outcome
		meta.Protocol = route.Protocol
		meta.Proxy = trace.Proxy
		meta.ExitNode = trace.ExitNode
		meta.ExitID = trace.ExitID
		// Append a per-attempt leg (key:status:ms, anonymized for the
		// public lane) so the request log shows the whole attempt chain
		// at a glance. Only the redacted key suffix is retained.
		label := keyID
		if anonymous {
			label = "anon"
		}
		leg := label + ":"
		if err != nil {
			leg += classifyAttemptError(err)
		} else {
			leg += strconv.Itoa(status)
		}
		leg += ":" + strconv.FormatInt(max(duration.Milliseconds(), 0), 10) + "ms"
		meta.Legs = append(meta.Legs, leg)
	}
	if g.monitor == nil {
		return
	}
	proxyName := trace.Proxy
	if proxyName == "" {
		proxyName = "unavailable"
	}
	g.monitor.RecordAttempt(telemetry.UpstreamAttempt{
		Time: time.Now().UTC(), RequestID: ids.Request, Model: route.ID, Tier: string(route.Tier), Attempt: attempt,
		KeyID: keyID, Channel: channel, Anonymous: anonymous, Proxy: proxyName,
		ExitNode: trace.ExitNode, ExitID: trace.ExitID, Status: status,
		DurationMS: max(duration.Milliseconds(), 0), Success: success, Outcome: outcome,
	})
}

<<<<<<< ours
func proxyTrace(proxy *proxyTransport) egressTrace {
	if proxy == nil {
		return egressTrace{Proxy: "unavailable"}
=======
// upstreamRequest builds the POST for one attempt: baseURL plus the protocol's
// /v1/... route, carrying body. The header set follows the lane: setV1Headers
// when models.IsV1Upstream(baseURL), setV2Headers otherwise. Anthropic
// endpoints on a v2 lane gain beta=true, matching the 2.x client; the 1.x lane
// keeps its shape. key is sent as x-api-key (with anthropic-version) on the
// Anthropic protocol and as a Bearer token on every other one; the anonymous
// lane passes anonymousZenKey.
func upstreamRequest(ctx context.Context, baseURL string, protocol wire.Protocol, body []byte, ids identity.RequestIDs, key string) (*http.Request, error) {
	endpoint := strings.TrimRight(baseURL, "/") + wire.Path(protocol)
	if !models.IsV1Upstream(baseURL) {
		endpoint = strings.TrimRight(baseURL, "/") + v2EndpointPath(baseURL, protocol)
	}
	if protocol == wire.Anthropic && !models.IsV1Upstream(baseURL) {
		endpoint = withAnthropicBeta(endpoint)
>>>>>>> theirs
	}
	return egressTrace{Proxy: config.RedactURL(proxy.name)}
}

func newUpstreamRequest(ctx context.Context, baseURL string, protocol wire.Protocol, body []byte, ids identity.RequestIDs, key string) (*http.Request, error) {
	endpoint := strings.TrimRight(baseURL, "/") + wire.Path(protocol)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Upstream fingerprints the client on every tier: it wants the OpenCode
	// User-Agent shape, x-opencode-session-id, and a bare 40-character hex
	// x-opencode-project, and it rejects the gateway's own x-opencode-request
	// header. Dropping or changing any one of them turns the request into a 403
	// FreeTierError, so the client's header set is mirrored exactly.
	req.Header.Set("User-Agent", httpx.UserAgent())
	req.Header.Set("x-opencode-client", "cli")
	// The free tier requires both session headers to carry the same
	// descending-encoded id. The x-opencode-request correlation header and
	// canonical prj_* project ids are rejected with 403 since 2026-10-14, so
	// neither is forwarded; tracing stays local to this gateway.
	req.Header.Set("x-opencode-session", ids.Session)
	req.Header.Set("x-opencode-session-id", ids.Session)
	req.Header.Set("x-session-affinity", ids.Session)
	req.Header.Set("X-Session-Id", ids.Session)
	// A canonical prj_* project id is rejected with 403 since 2026-10-14; the
	// client's own bare 40-hex shape (or any non-canonical value, e.g.
	// "global") passes. Hash the local project signal into the accepted shape
	// so one conversation stays pinned to one upstream project.
	req.Header.Set("x-opencode-project", clientProjectID(ids.Project))
	if ids.ParentSession != "" {
		req.Header.Set("x-parent-session-id", ids.ParentSession)
	}
	if protocol == wire.Anthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14")
	} else {
		// The anonymous credential is the literal token "public". The 2026-10-14
		// gate rejects keyless requests (an absent header used to pass) and is
		// case-sensitive: "Bearer PUBLIC" 401s.
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

<<<<<<< ours
// clientProjectID renders the project header the way the client does: a bare
// 40-character lowercase hex id rather than the gateway's "prj_" prefixed
// stable id. The value itself is not checked upstream, only its shape, but
// hashing keeps one conversation pinned to one project.
func clientProjectID(project string) string {
=======
// v2EndpointPath returns the route a 2.1 client posts to on a v2 origin. The
// 2.1 client groups /inference routes by provider family, so the family
// segment sits between the origin and the protocol path (a 2.0.32 client
// posted the protocol path directly, and that route still answers). Origins
// that are not the /inference gateway — a custom base, or /zen/go — keep the
// flat route, because the family grouping is specific to /inference.
func v2EndpointPath(baseURL string, protocol wire.Protocol) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/inference") {
		return wire.Path(protocol)
	}
	family := "openai"
	if protocol == wire.Anthropic {
		family = "anthropic"
	}
	return "/" + family + wire.Path(protocol)
}

// withAnthropicBeta marks an Anthropic endpoint as beta the way the OpenCode
// 2.x client does, preserving any query a configured base URL already carries.
func withAnthropicBeta(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	query := parsed.Query()
	if query.Get("beta") == "true" {
		return endpoint
	}
	query.Set("beta", "true")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// setV1Headers renders the v1 lane's header set, that of an OpenCode 1.19.3
// model request on its opencode provider, as captured from the official
// release: the AI SDK User-Agent of the package serving the protocol, both
// session headers, the user message id as x-opencode-request (a fresh
// ascending msg_ id when none was derived), the git root commit as the
// project, the canonical parent session under both parent headers, and only
// the anthropic-beta flags the AI SDK derives from the body. The v1 client
// sends no x-session-affinity or X-Session-Id to this provider.
func setV1Headers(header http.Header, protocol wire.Protocol, body []byte, ids identity.RequestIDs) {
	header.Set("Accept", "*/*")
	header.Set("User-Agent", httpx.V1ModelUserAgent(v1ProviderUtils(protocol)))
	header.Set("x-opencode-client", "cli")
	header.Set("x-opencode-session", ids.Session)
	header.Set("x-opencode-session-id", ids.Session)
	message := ids.Message
	if message == "" {
		message = identity.AscendingID("msg", time.Now())
	}
	header.Set("x-opencode-request", message)
	header.Set("x-opencode-project", projectID(ids.Project))
	if ids.ParentSession != "" {
		parent := identity.CanonicalSessionID(ids.ParentSession)
		header.Set("x-opencode-parent-session-id", parent)
		header.Set("x-parent-session-id", parent)
	}
	if protocol == wire.Anthropic {
		if betas := v1AnthropicBetas(body); betas != "" {
			header.Set("anthropic-beta", betas)
		}
	}
}

// setV2Headers renders the OpenCode 2.x CLI header set the v2 gates
// fingerprint: the "opencode/<channel>/<version>/cli" User-Agent, the
// client's child session as x-opencode-session-id, the conversation affinity
// (the canonical parent when one is present) as x-opencode-session,
// x-session-affinity and X-Session-Id, and a bare 40-character hex
// x-opencode-project. The v2 gate rejects x-opencode-request and canonical
// prj_* project ids with 403 FreeTierError, so neither is sent. Anthropic
// requests carry a fixed anthropic-beta set; upstreamRequest adds
// anthropic-version and the beta query.
func setV2Headers(header http.Header, protocol wire.Protocol, ids identity.RequestIDs) {
	header.Set("Accept", "application/json, text/event-stream")
	header.Set("x-opencode-client", "cli")
	rootSession := ids.Session
	if ids.ParentSession != "" {
		parent := identity.CanonicalSessionID(ids.ParentSession)
		rootSession = parent
		header.Set("x-opencode-parent-session-id", parent)
		header.Set("x-parent-session-id", parent)
	}
	childSession := ids.ChildSession
	if childSession == "" {
		childSession = ids.Session
	}
	header.Set("x-session-affinity", rootSession)
	header.Set("X-Session-Id", rootSession)
	header.Set("x-opencode-session", rootSession)
	header.Set("x-opencode-session-id", childSession)
	header.Set("User-Agent", httpx.V2UserAgent())
	header.Set("x-opencode-project", projectID(ids.Project))
	if protocol == wire.Anthropic {
		header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	}
}

// v1ProviderUtils names the provider-utils version of the AI SDK package the
// OpenCode 1.x client routes a protocol through, for the v1 User-Agent. System
// One has no v1 counterpart and reports the chat package.
func v1ProviderUtils(protocol wire.Protocol) string {
	switch protocol {
	case wire.Responses:
		return httpx.V1ResponsesSDK
	case wire.Anthropic:
		return httpx.V1AnthropicSDK
	default:
		return httpx.V1ChatSDK
	}
}

// gitCommitPattern matches the git root commit hash OpenCode uses as its
// project id on both lanes.
var gitCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// v1AnthropicBetaRequest holds the Messages body fields the AI SDK
// anthropic provider derives beta flags from. Fields whose JSON type varies
// stay raw so one unexpected value cannot hide the others.
type v1AnthropicBetaRequest struct {
	Model             string            `json:"model"`
	MCPServers        []json.RawMessage `json:"mcp_servers"`
	ContextManagement *struct {
		Edits []struct {
			Type string `json:"type"`
		} `json:"edits"`
	} `json:"context_management"`
	Container    json.RawMessage `json:"container"`
	OutputConfig struct {
		TaskBudget json.RawMessage `json:"task_budget"`
	} `json:"output_config"`
	Speed     json.RawMessage `json:"speed"`
	Fallbacks json.RawMessage `json:"fallbacks"`
	Thinking  struct {
		BlockBinding json.RawMessage `json:"block_binding"`
	} `json:"thinking"`
	Tools []struct {
		Type           string          `json:"type"`
		InputExamples  json.RawMessage `json:"input_examples"`
		AllowedCallers json.RawMessage `json:"allowed_callers"`
	} `json:"tools"`
}

// v1AnthropicToolBetas maps the server tool types of a Messages body to the
// beta flag the AI SDK anthropic provider sends with each.
var v1AnthropicToolBetas = map[string]string{
	"code_execution_20250522": "code-execution-2025-05-22",
	"code_execution_20250825": "code-execution-2025-08-25",
	"computer_20241022":       "computer-use-2024-10-22",
	"computer_20250124":       "computer-use-2025-01-24",
	"computer_20251124":       "computer-use-2025-11-24",
	"text_editor_20241022":    "computer-use-2024-10-22",
	"text_editor_20250124":    "computer-use-2025-01-24",
	"text_editor_20250429":    "computer-use-2025-01-24",
	"bash_20241022":           "computer-use-2024-10-22",
	"bash_20250124":           "computer-use-2025-01-24",
	"memory_20250818":         "context-management-2025-06-27",
	"web_fetch_20250910":      "web-fetch-2025-09-10",
	"web_fetch_20260209":      "code-execution-web-tools-2026-02-09",
	"web_search_20260209":     "code-execution-web-tools-2026-02-09",
	"advisor_20260301":        "advisor-tool-2026-03-01",
}

// v1AnthropicBetas returns the anthropic-beta value OpenCode 1.19.3 sends
// with a Messages body on the v1 lane, or "" when no flag applies. Its
// opencode provider sets no fixed betas: the AI SDK derives them from the
// request features (body features first, then tools, in the SDK's order), and
// per-tool eager_input_streaming has replaced the old
// fine-grained-tool-streaming flag.
func v1AnthropicBetas(body []byte) string {
	var request v1AnthropicBetaRequest
	// A field of an unexpected type fails alone; the rest still decode.
	_ = json.Unmarshal(body, &request)
	var betas []string
	add := func(beta string) {
		if !slices.Contains(betas, beta) {
			betas = append(betas, beta)
		}
	}
	if len(request.MCPServers) > 0 {
		add("mcp-client-2025-04-04")
	}
	if request.ContextManagement != nil {
		add("context-management-2025-06-27")
		for _, edit := range request.ContextManagement.Edits {
			if edit.Type == "compact_20260112" {
				add("compact-2026-01-12")
			}
		}
	}
	var container struct {
		Skills []json.RawMessage `json:"skills"`
	}
	if json.Unmarshal(request.Container, &container) == nil && len(container.Skills) > 0 {
		add("code-execution-2025-08-25")
		add("skills-2025-10-02")
		add("files-api-2025-04-14")
	}
	if isPresentJSON(request.OutputConfig.TaskBudget) {
		add("task-budgets-2026-03-13")
	}
	if string(request.Speed) == `"fast"` {
		add("fast-mode-2026-02-01")
	}
	var fallbacks []json.RawMessage
	if string(request.Fallbacks) == `"default"` {
		add("server-side-fallback-2026-07-01")
	} else if json.Unmarshal(request.Fallbacks, &fallbacks) == nil && len(fallbacks) > 0 {
		add("server-side-fallback-2026-06-01")
	}
	if isPresentJSON(request.Thinking.BlockBinding) {
		add("thinking-binding-controls-2026-08-01")
	}
	for _, tool := range request.Tools {
		if tool.Type == "" || tool.Type == "custom" {
			if anthropicSupportsStructuredOutput(request.Model) {
				add("structured-outputs-2025-11-13")
			}
			if isPresentJSON(tool.InputExamples) || isPresentJSON(tool.AllowedCallers) {
				add("advanced-tool-use-2025-11-20")
			}
		} else if beta := v1AnthropicToolBetas[tool.Type]; beta != "" {
			add(beta)
		}
	}
	return strings.Join(betas, ",")
}

func isPresentJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// claude3OrOlderPattern matches the Claude model families older than Claude 4
// (Claude Instant, Claude 2 and every Claude 3.x model) that the AI SDK treats
// as lacking structured output.
var claude3OrOlderPattern = regexp.MustCompile(`claude-(?:instant(?:-|$)|v?2(?:$|[-.:])|3(?:$|[-.]))`)

// anthropicSupportsStructuredOutput mirrors the AI SDK anthropic provider's
// model capability table, checked in the same order: the dated Claude 4.0
// releases and Claude 3 and older lack structured output, every other Claude
// model has it, and non-Claude models do not.
func anthropicSupportsStructuredOutput(model string) bool {
	for _, supported := range []string{
		"claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-fable-5", "claude-sonnet-5",
		"claude-sonnet-4-6", "claude-opus-4-6", "claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5",
		"claude-opus-4-1",
	} {
		if strings.Contains(model, supported) {
			return true
		}
	}
	if strings.Contains(model, "claude-sonnet-4-") || strings.Contains(model, "claude-opus-4-") ||
		strings.Contains(model, "claude-3-haiku") || claude3OrOlderPattern.MatchString(model) {
		return false
	}
	return strings.Contains(model, "claude-")
}

// projectID renders the project header in the shape both lanes use: the
// genuine git root commit hash or "global" is kept verbatim, and any other
// project is hashed into the same 40-character lowercase hex shape. The value
// itself is not checked upstream, only its shape, but hashing keeps one
// project mapped to one stable upstream project id and masks a caller's
// prj_*-style project, which the v2 gate rejects.
func projectID(project string) string {
	if project == "global" || gitCommitPattern.MatchString(project) {
		return project
	}
>>>>>>> theirs
	sum := sha256.Sum256([]byte("opencode-project\x00" + project))
	return hex.EncodeToString(sum[:20])
}

func isNonRetryableClientResponse(resp *http.Response, err error) bool {
	return err == nil && resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests
}
