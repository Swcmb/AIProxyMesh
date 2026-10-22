package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
)

// inferenceVerdict reports whether the first upstream payload proved that the
// exit can produce inference output. Known=false means the payload could not
// be judged, which is recorded as HTTP 200 evidence only.
type inferenceVerdict struct {
	Valid bool
	Known bool
}

// readFirstOutput consumes SSE event blocks until the first output event, an
// error, a terminal event or EOF. It returns every consumed byte so the body
// can be replayed to the client unchanged.
func readFirstOutput(reader *bufio.Reader, protocol wire.Protocol, maxBytes int) ([]byte, inferenceVerdict, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	var prefix, eventBytes []byte
	terminal := false
	for {
		line, err := reader.ReadSlice('\n')
		if len(prefix)+len(line) > maxBytes {
			return nil, inferenceVerdict{}, errors.New("upstream SSE prelude exceeds limit")
		}
		prefix = append(prefix, line...)
		eventBytes = append(eventBytes, line...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(bytes.TrimSpace(eventBytes)) > 0 {
					output, done, failure := parseSSEEvent(eventBytes, protocol)
					if failure {
						return prefix, inferenceVerdict{Known: true}, nil
					}
					if output {
						return prefix, inferenceVerdict{Valid: true, Known: true}, nil
					}
					if done {
						terminal = true
					}
				}
				if terminal {
					return prefix, inferenceVerdict{Known: true}, nil
				}
				return nil, inferenceVerdict{}, io.ErrUnexpectedEOF
			}
			return nil, inferenceVerdict{}, err
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if text == "" {
			output, done, failure := parseSSEEvent(eventBytes, protocol)
			eventBytes = eventBytes[:0]
			if failure {
				return prefix, inferenceVerdict{Known: true}, nil
			}
			if output {
				return prefix, inferenceVerdict{Valid: true, Known: true}, nil
			}
			if done {
				terminal = true
				return prefix, inferenceVerdict{Known: true}, nil
			}
		}
	}
}

// parseSSEEvent interprets one event block. It returns whether the event
// carries real output, whether it is terminal, and whether it is an error.
func parseSSEEvent(event []byte, protocol wire.Protocol) (output bool, terminal bool, failure bool) {
	var dataLines []string
	for _, raw := range strings.Split(string(event), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(line, "event:") {
			if strings.TrimSpace(strings.TrimPrefix(line, "event:")) == "error" {
				return false, false, true
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	if len(dataLines) == 0 {
		return false, false, false
	}
	data := strings.Join(dataLines, "\n")
	if data == "[DONE]" {
		return false, true, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return false, false, false
	}
	if value, ok := payload["error"]; ok && value != nil {
		return false, false, true
	}
	kind, _ := payload["type"].(string)
	switch protocol {
	case wire.Chat:
		if kind == "error" {
			return false, false, true
		}
		choices, _ := payload["choices"].([]any)
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			if choice == nil {
				continue
			}
			if delta, _ := choice["delta"].(map[string]any); delta != nil {
				if content, _ := delta["content"].(string); content != "" {
					return true, false, false
				}
				if tools, _ := delta["tool_calls"].([]any); len(tools) > 0 {
					return true, false, false
				}
			}
			if message, _ := choice["message"].(map[string]any); message != nil {
				if content, _ := message["content"].(string); content != "" {
					return true, false, false
				}
				if tools, _ := message["tool_calls"].([]any); len(tools) > 0 {
					return true, false, false
				}
			}
		}
		return false, false, false
	case wire.Anthropic:
		switch kind {
		case "message_stop":
			return false, true, false
		case "error":
			return false, false, true
		case "content_block_delta":
			delta, _ := payload["delta"].(map[string]any)
			if delta == nil {
				return false, false, false
			}
			for _, key := range []string{"text", "partial_json", "thinking"} {
				if value, _ := delta[key].(string); value != "" {
					return true, false, false
				}
			}
		}
		return false, false, false
	case wire.Responses:
		switch kind {
		case "response.output_text.delta":
			delta, _ := payload["delta"].(string)
			return delta != "", false, false
		case "response.function_call_arguments.delta":
			delta, _ := payload["delta"].(string)
			return delta != "", false, false
		case "response.content_part.added":
			part, _ := payload["part"].(map[string]any)
			if part != nil {
				if text, _ := part["text"].(string); text != "" {
					return true, false, false
				}
			}
		case "response.completed":
			return false, true, false
		case "response.failed", "error":
			return false, false, true
		}
	}
	return false, false, false
}

// jsonVerdict judges a complete non-streaming JSON payload.
func jsonVerdict(payload []byte, protocol wire.Protocol) inferenceVerdict {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return inferenceVerdict{}
	}
	if value, ok := object["error"]; ok && value != nil {
		return inferenceVerdict{Known: true}
	}
	switch protocol {
	case wire.Chat:
		if choices, _ := object["choices"].([]any); len(choices) > 0 {
			return inferenceVerdict{Valid: true, Known: true}
		}
	case wire.Anthropic:
		if content, _ := object["content"].([]any); len(content) > 0 {
			return inferenceVerdict{Valid: true, Known: true}
		}
	case wire.Responses:
		if output, _ := object["output"].([]any); len(output) > 0 {
			return inferenceVerdict{Valid: true, Known: true}
		}
	}
	return inferenceVerdict{Known: true}
}

// peekJSONVerdict reads at most limit bytes and judges a complete JSON object.
// The prefix is returned so the body can be replayed.
func peekJSONVerdict(body io.Reader, protocol wire.Protocol, limit int) ([]byte, inferenceVerdict) {
	if limit <= 0 {
		limit = 8192
	}
	prefix, err := io.ReadAll(io.LimitReader(body, int64(limit)))
	if err != nil && len(prefix) == 0 {
		return nil, inferenceVerdict{}
	}
	if !json.Valid(prefix) {
		return prefix, inferenceVerdict{}
	}
	return prefix, jsonVerdict(prefix, protocol)
}

// streamWatcher scans a streaming response for terminal and error events and
// reports one clean/failed conclusion when the body ends.
type streamWatcher struct {
	body     io.ReadCloser
	protocol wire.Protocol
	mu       sync.Mutex
	tail     []byte
	output   bool
	terminal bool
	failed   bool
	ended    bool
	onEnd    func(clean bool)
}

func newStreamWatcher(body io.ReadCloser, protocol wire.Protocol, onEnd func(bool)) *streamWatcher {
	return &streamWatcher{body: body, protocol: protocol, onEnd: onEnd}
}

func (w *streamWatcher) Read(p []byte) (int, error) {
	n, err := w.body.Read(p)
	if n > 0 {
		w.scan(p[:n])
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			w.finish()
		} else {
			w.finishFailed()
		}
	}
	return n, err
}

func (w *streamWatcher) Close() error { return w.body.Close() }

func (w *streamWatcher) scan(chunk []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tail = append(w.tail, chunk...)
	if len(w.tail) > 64<<10 {
		w.tail = w.tail[len(w.tail)-(64<<10):]
	}
	text := string(w.tail)
	if value, ok := terminalMarker(w.protocol); ok && strings.Contains(text, value) {
		w.terminal = true
	}
	if output, ok := outputMarker(w.protocol); ok && strings.Contains(text, output) {
		w.output = true
	}
	if strings.Contains(text, "event: error") || strings.Contains(text, `"type":"error"`) ||
		strings.Contains(text, `"type": "error"`) {
		w.failed = true
	}
	// Keep only a small overlap so markers split across reads still match.
	if len(w.tail) > 8<<10 {
		w.tail = w.tail[len(w.tail)-(8<<10):]
	}
}

func terminalMarker(protocol wire.Protocol) (string, bool) {
	switch protocol {
	case wire.Chat:
		return "[DONE]", true
	case wire.Anthropic:
		return `"message_stop"`, true
	case wire.Responses:
		return `"response.completed"`, true
	}
	return "", false
}

func outputMarker(protocol wire.Protocol) (string, bool) {
	switch protocol {
	case wire.Chat:
		return `"content":"`, true
	case wire.Anthropic:
		return `"content_block_delta"`, true
	case wire.Responses:
		return `"response.output_text.delta"`, true
	}
	return "", false
}

func (w *streamWatcher) finish() {
	w.mu.Lock()
	if w.ended {
		w.mu.Unlock()
		return
	}
	w.ended = true
	clean := !w.failed && (w.terminal || w.output)
	w.mu.Unlock()
	if w.onEnd != nil {
		w.onEnd(clean)
	}
}

func (w *streamWatcher) finishFailed() {
	w.mu.Lock()
	if w.ended {
		w.mu.Unlock()
		return
	}
	w.ended = true
	w.mu.Unlock()
	if w.onEnd != nil {
		w.onEnd(false)
	}
}
