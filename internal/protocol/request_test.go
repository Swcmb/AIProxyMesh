package protocol

import "testing"

func TestDecodeResponsesSkipsBuiltinTools(t *testing.T) {
	input := map[string]any{
		"model": "test-model",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "hello"}},
		"tools": []any{
			map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}},
			map[string]any{"type": "web_search"},
			map[string]any{"type": "file_search"},
		},
	}
	request, err := decodeBridgeRequest(Responses, input)
	if err != nil {
		t.Fatalf("a built-in tool must not fail the bridge: %v", err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "shell" {
		t.Fatalf("expected only the function tool to survive, got %+v", request.Tools)
	}
}

func TestDecodeResponsesRejectsNonObjectTool(t *testing.T) {
	input := map[string]any{
		"model": "test-model",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "hello"}},
		"tools": []any{"web_search"},
	}
	if _, err := decodeBridgeRequest(Responses, input); err == nil {
		t.Fatal("a non-object tool entry is malformed and must be rejected")
	}
}
