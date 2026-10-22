// Package health implements the egress health model: statistical admission,
// recent-health decay, the key-level state machine and environment epochs.
//
// A health key is (upstream target, model, protocol, mode). Reachability is a
// separate, coarser record and never feeds admission.
package health

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Protocol names the wire protocol of one request shape.
const (
	ProtocolChat      = "chat"
	ProtocolResponses = "responses"
	ProtocolAnthropic = "anthropic"
	ProtocolSystemone = "systemone"
)

// Mode is the request framing.
const (
	ModeStream = "stream"
	ModeJSON   = "json"
)

// HealthKey is the smallest unit of health accounting. Node identity is
// implied by the owning entry.
type HealthKey struct {
	Target   string `json:"target"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
	Mode     string `json:"mode"`
}

// ReachabilityKey records network reachability for a target only. It is not a
// HealthKey and never enters an admission bucket.
type ReachabilityKey struct {
	Target string `json:"target"`
}

// NewKey normalizes a raw key. Wildcards are rejected: only reachability may
// be recorded without a model/protocol/mode.
func NewKey(target, model, protocol, mode string) (HealthKey, error) {
	normalizedTarget, err := NormalizeTarget(target)
	if err != nil {
		return HealthKey{}, err
	}
	key := HealthKey{
		Target:   normalizedTarget,
		Model:    strings.TrimSpace(model),
		Protocol: strings.ToLower(strings.TrimSpace(protocol)),
		Mode:     strings.ToLower(strings.TrimSpace(mode)),
	}
	if key.Model == "" || key.Model == "*" {
		return HealthKey{}, errors.New("health: model must be a concrete id")
	}
	switch key.Protocol {
	case ProtocolChat, ProtocolResponses, ProtocolAnthropic, ProtocolSystemone:
	default:
		return HealthKey{}, fmt.Errorf("health: unsupported protocol %q", key.Protocol)
	}
	switch key.Mode {
	case ModeStream, ModeJSON:
	default:
		return HealthKey{}, fmt.Errorf("health: unsupported mode %q", key.Mode)
	}
	return key, nil
}

// Valid reports whether the key may enter admission buckets.
func (k HealthKey) Valid() bool {
	if k.Target == "" || k.Model == "" || k.Model == "*" {
		return false
	}
	if k.Protocol == "" || k.Protocol == "*" || k.Mode == "" || k.Mode == "*" {
		return false
	}
	return true
}

// ValidRequired reports whether the protocol can be checked for a valid
// inference payload. systemone and unknown shapes only track HTTP 200.
func (k HealthKey) ValidRequired() bool {
	switch k.Protocol {
	case ProtocolChat, ProtocolResponses, ProtocolAnthropic:
		return true
	default:
		return false
	}
}

// NormalizeTarget canonicalizes an upstream base URL for use in a health key:
// lowercase scheme and host, no default port, no trailing dot, no trailing
// slash, and no userinfo, query or fragment.
func NormalizeTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("health: empty target")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("health: invalid target %q", raw)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("health: target scheme must be http(s): %q", raw)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return "", fmt.Errorf("health: target has no host: %q", raw)
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	authority := host
	if port != "" {
		authority += ":" + port
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "/" {
		path = ""
	}
	return scheme + "://" + authority + path, nil
}

// TargetHost extracts the canonical host[:port] of a target for upstream
// attribution, which stays host-scoped.
func TargetHost(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	return host
}
