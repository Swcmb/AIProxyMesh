package gateway

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

// geoBlockMarkers are upstream wordings for an IP-region rejection. They are
// deliberately specific so an ordinary model error cannot trigger a region
// block.
var geoBlockMarkers = []string{
	"not available in your country",
	"not available in your region",
	"not supported in your country",
	"not supported in your region",
	"country is not supported",
	"region is not supported",
	"unsupported country",
	"unsupported region",
}

// geoBlockedResponse peeks a 403 body for a regional rejection and restores the
// body, so the response can still be relayed untouched.
func geoBlockedResponse(resp *http.Response) bool {
	if resp == nil || resp.Body == nil {
		return false
	}
	peek, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return false
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(peek), resp.Body), resp.Body}
	text := strings.ToLower(string(peek))
	for _, marker := range geoBlockMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
