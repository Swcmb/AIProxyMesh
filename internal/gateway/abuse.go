package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/identity"
)

// errAnonymousLanePaused reports that the lane's circuit breaker has paused the
// whole lane. Another exit cannot succeed while it is held shut, so the request
// must use an authenticated tier or report the pause itself.
var errAnonymousLanePaused = errors.New("anonymous lane paused by its circuit breaker")

// anonymousBackoffFromConfig converts the configured hours into the breaker's
// thresholds and durations. A nil block disables the breaker.
func anonymousBackoffFromConfig(cfg *config.AnonymousBackoffConfig) anonymousBackoff {
	if cfg == nil {
		return anonymousBackoff{}
	}
	return anonymousBackoff{
		threshold: int32(cfg.Threshold),
		base:      time.Duration(cfg.BaseHours) * time.Hour,
		max:       time.Duration(cfg.MaxHours) * time.Hour,
	}
}

// noteAnonymousFailure feeds one failed attempt to the lane breaker and logs a
// trip once, so the pause is visible in the event log.
func (g *Gateway) noteAnonymousFailure(ids identity.RequestIDs, attempts int) {
	pause, tripped := g.anonymous.NoteFailure()
	if tripped {
		g.logger.Warn("anonymous lane paused after repeated failures", "component", "upstream", "event", "anonymous_lane_paused", "request_id", ids.Request, "attempt", attempts, "pause_seconds", int(pause.Seconds()))
	}
}

// deterministicProbeLimit bounds how much of an error body is read to classify
// it. Error payloads are small; a stream would already have been rejected.
const deterministicProbeLimit = 16 << 10

// errDeterministicClientRejection reports that the upstream rejected the
// request itself: the body is malformed or too long for the model, so every
// exit, key and tier would answer the same. Retrying replaces the real error
// with an unrelated one (typically a 401 from another credential), so the
// request must end with the original response.
var errDeterministicClientRejection = errors.New("upstream rejected the request deterministically")

// deterministicMarkers are the error types and codes an upstream uses for a
// request that fails identically everywhere. The status must be 400 or 422 as
// well: 401, 403 and 429 can be specific to the credential or the moment.
var deterministicMarkers = []string{
	"invalid_request_error",
	"invalid_request",
	"context_length_exceeded",
	"maximum context length",
	"string_above_max_length",
	"prompt is too long",
}

// isDeterministicClientResponse reports whether status and body describe a
// request-level rejection rather than a credential or availability problem.
func isDeterministicClientResponse(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	text := strings.ToLower(string(body))
	for _, marker := range deterministicMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// peekDeterministic classifies a response without consuming its body. The
// status is checked first: peeking a streaming 2xx body would block until the
// first bytes arrive.
func peekDeterministic(resp *http.Response) bool {
	if resp == nil || (resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity) {
		return false
	}
	return isDeterministicClientResponse(resp.StatusCode, peekResponseBody(resp))
}

// peekResponseBody reads the start of a response body and puts it back in front
// of the rest, so both the classification and the client see the same payload.
func peekResponseBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, deterministicProbeLimit))
	resp.Body = replayBody(prefix, resp.Body)
	if err != nil {
		return nil
	}
	return prefix
}

func replayBody(prefix []byte, original io.ReadCloser) io.ReadCloser {
	return &prefixBody{Reader: io.MultiReader(bytes.NewReader(prefix), original), closer: original}
}

type prefixBody struct {
	io.Reader
	closer io.Closer
}

func (b *prefixBody) Close() error { return b.closer.Close() }
