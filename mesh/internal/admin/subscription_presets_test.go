package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubscriptionPresetsAreOptIn(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.handleSubscriptionPresets(recorder, httptest.NewRequest(http.MethodGet, "/api/egress/subscription-presets", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d", recorder.Code)
	}
	body := decodeBody(t, recorder)
	presets, _ := body["presets"].([]any)
	if len(presets) != 1 {
		t.Fatalf("expected one built-in preset, got %+v", body["presets"])
	}
	preset, _ := presets[0].(map[string]any)
	if preset["id"] != "api-proxy-pool" {
		t.Fatalf("preset id = %v", preset["id"])
	}
	url, _ := preset["url"].(string)
	if !strings.HasPrefix(url, "https://") || !strings.HasSuffix(url, "/alive.txt") {
		t.Fatalf("preset url = %q", url)
	}
	for _, field := range []string{"name", "description", "risk"} {
		if text, _ := preset[field].(string); strings.TrimSpace(text) == "" {
			t.Fatalf("preset %s must be non-empty", field)
		}
	}
}
