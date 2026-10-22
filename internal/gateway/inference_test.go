package gateway

import (
	"bufio"
	"strings"
	"testing"

	wire "github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
)

func TestReadFirstOutputChatIgnoresRoleOnly(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: [DONE]\n\n"
	prefix, verdict, err := readFirstOutput(bufio.NewReader(strings.NewReader(stream)), wire.Chat, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Valid || !verdict.Known {
		t.Fatalf("verdict = %+v", verdict)
	}
	if !strings.Contains(string(prefix), "role") || !strings.Contains(string(prefix), "hello") {
		t.Fatalf("prefix lost bytes: %q", prefix)
	}
}

func TestReadFirstOutputAnthropicMetadata(t *testing.T) {
	stream := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
	_, verdict, err := readFirstOutput(bufio.NewReader(strings.NewReader(stream)), wire.Anthropic, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Valid {
		t.Fatalf("metadata must not count, output must: %+v", verdict)
	}
}

func TestReadFirstOutputResponses(t *testing.T) {
	stream := "data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	_, verdict, err := readFirstOutput(bufio.NewReader(strings.NewReader(stream)), wire.Responses, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Valid {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestReadFirstOutputErrorEvent(t *testing.T) {
	stream := "event: error\ndata: {\"error\":{\"message\":\"boom\"}}\n\n"
	_, verdict, err := readFirstOutput(bufio.NewReader(strings.NewReader(stream)), wire.Chat, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Valid || !verdict.Known {
		t.Fatalf("error event must be a known invalid inference: %+v", verdict)
	}
}

func TestReadFirstOutputTerminalOnlyIsInvalid(t *testing.T) {
	stream := "data: [DONE]\n\n"
	_, verdict, err := readFirstOutput(bufio.NewReader(strings.NewReader(stream)), wire.Chat, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Valid || !verdict.Known {
		t.Fatalf("terminal without output is invalid, got %+v", verdict)
	}
}

func TestJSONVerdict(t *testing.T) {
	if verdict := jsonVerdict([]byte(`{"choices":[{"message":{"content":"ok"}}]}`), wire.Chat); !verdict.Valid {
		t.Fatalf("valid chat verdict = %+v", verdict)
	}
	if verdict := jsonVerdict([]byte(`{"error":{"message":"bad"}}`), wire.Chat); verdict.Valid || !verdict.Known {
		t.Fatalf("error verdict = %+v", verdict)
	}
	if verdict := jsonVerdict([]byte(`{"id":"x"}`), wire.Chat); verdict.Valid || !verdict.Known {
		t.Fatalf("missing choices verdict = %+v", verdict)
	}
	if verdict := jsonVerdict([]byte(`{"choices":[`), wire.Chat); verdict.Known {
		t.Fatalf("incomplete JSON must be unknown: %+v", verdict)
	}
}

func TestPeekJSONVerdictRestoresPrefix(t *testing.T) {
	payload := `{"choices":[{"message":{"content":"ok"}}]}`
	prefix, verdict := peekJSONVerdict(strings.NewReader(payload), wire.Chat, 8192)
	if !verdict.Valid {
		t.Fatalf("verdict = %+v", verdict)
	}
	if string(prefix) != payload {
		t.Fatalf("prefix = %q", prefix)
	}
}

func TestStreamWatcherReportsCleanAndBroken(t *testing.T) {
	var clean []bool
	body := &readCloser{Reader: strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")}
	watcher := newStreamWatcher(body, wire.Chat, func(ok bool) { clean = append(clean, ok) })
	buf := make([]byte, 64)
	for {
		if _, err := watcher.Read(buf); err != nil {
			break
		}
	}
	if len(clean) != 1 || !clean[0] {
		t.Fatalf("clean stream callbacks = %v", clean)
	}

	var broken []bool
	body = &readCloser{Reader: strings.NewReader("data: {\"type\":\"error\"}\n\n")}
	watcher = newStreamWatcher(body, wire.Chat, func(ok bool) { broken = append(broken, ok) })
	for {
		if _, err := watcher.Read(buf); err != nil {
			break
		}
	}
	if len(broken) != 1 || broken[0] {
		t.Fatalf("error stream callbacks = %v", broken)
	}
}

type readCloser struct{ *strings.Reader }

func (r *readCloser) Close() error { return nil }
