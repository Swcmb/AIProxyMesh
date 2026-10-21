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
	if len(presets) != 2 {
		t.Fatalf("expected the hproxy and proxifly presets, got %+v", body["presets"])
	}
	wantedSuffix := map[string]string{
		"api-proxy-pool-hproxy":   "/live.csv",
		"api-proxy-pool-proxifly": "/proxies/all/data.txt",
	}
	for _, raw := range presets {
		preset, _ := raw.(map[string]any)
		id, _ := preset["id"].(string)
		url, _ := preset["url"].(string)
		suffix, ok := wantedSuffix[id]
		if !ok {
			t.Fatalf("unexpected preset id %q", id)
		}
		if !strings.HasPrefix(url, "https://") || !strings.HasSuffix(url, suffix) {
			t.Fatalf("preset %s url = %q", id, url)
		}
		protocols, _ := preset["protocols"].([]any)
		if len(protocols) != 2 || protocols[0] != "http" || protocols[1] != "https" {
			t.Fatalf("preset %s protocols = %+v", id, preset["protocols"])
		}
		for _, field := range []string{"name", "description", "risk"} {
			if text, _ := preset[field].(string); strings.TrimSpace(text) == "" {
				t.Fatalf("preset %s %s must be non-empty", id, field)
			}
		}
	}
}
