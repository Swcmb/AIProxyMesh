package gateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGeoBlockedResponseRestoresBody(t *testing.T) {
	body := `{"error":{"message":"This model is not available in your country"}}`
	resp := &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(body))}
	if !geoBlockedResponse(resp) {
		t.Fatal("country rejection must be detected")
	}
	restored, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != body {
		t.Fatalf("body must be relayable after peeking, got %q", restored)
	}
}

func TestGeoBlockedResponseIgnoresOtherForbidden(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"error":"project id rejected"}`))}
	if geoBlockedResponse(resp) {
		t.Fatal("an unrelated 403 must not trigger a region block")
	}
}
