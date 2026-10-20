package admin

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
)

// EgressSubscriptionView hides the secret-bearing parts of a subscription URL.
// The ID lets the client round-trip the existing URL without ever seeing it.
// Inline subscriptions report only a line count; the full content is served by
// the single-subscription endpoint.
type EgressSubscriptionView struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	URL            string `json:"url,omitempty"`
	Inline         bool   `json:"inline,omitempty"`
	ContentLines   int    `json:"content_lines,omitempty"`
	Enabled        bool   `json:"enabled"`
	RefreshSeconds int    `json:"refresh_seconds,omitempty"`
}

// EgressView is the management representation of the exit-pool section.
type EgressView struct {
	Enabled             bool                     `json:"enabled"`
	Subscriptions       []EgressSubscriptionView `json:"subscriptions,omitempty"`
	ImportLegacyProxies *bool                    `json:"import_legacy_proxies,omitempty"`
	Schedule            config.EgressSchedule    `json:"schedule"`
	Protocols           config.EgressProtocols   `json:"protocols"`
	Cooldown            config.EgressCooldown    `json:"cooldown"`
	Failover            config.EgressFailover    `json:"failover"`
	Probe               config.EgressProbe       `json:"probe"`
	StatePath           string                   `json:"state_path,omitempty"`
	MaxLatencyEntries   int                      `json:"max_latency_entries,omitempty"`
}

// EgressSubscriptionUpdate accepts either a new URL, inline content, or an
// empty source that keeps whatever is already stored under the same ID.
type EgressSubscriptionUpdate struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	URL            string `json:"url,omitempty"`
	Content        string `json:"content,omitempty"`
	Enabled        bool   `json:"enabled"`
	RefreshSeconds int    `json:"refresh_seconds,omitempty"`
}

// EgressUpdate is the writable form of the egress section.
type EgressUpdate struct {
	Enabled             bool                       `json:"enabled"`
	Subscriptions       []EgressSubscriptionUpdate `json:"subscriptions,omitempty"`
	ImportLegacyProxies *bool                      `json:"import_legacy_proxies,omitempty"`
	Schedule            config.EgressSchedule      `json:"schedule"`
	Protocols           config.EgressProtocols     `json:"protocols"`
	Cooldown            config.EgressCooldown      `json:"cooldown"`
	Failover            config.EgressFailover      `json:"failover"`
	Probe               config.EgressProbe         `json:"probe"`
	StatePath           string                     `json:"state_path,omitempty"`
	MaxLatencyEntries   int                        `json:"max_latency_entries,omitempty"`
}

func egressView(cfg *config.EgressConfig) *EgressView {
	if cfg == nil {
		return nil
	}
	view := &EgressView{
		Enabled:             cfg.Enabled,
		ImportLegacyProxies: cfg.ImportLegacyProxies,
		Schedule:            cfg.Schedule,
		Protocols:           cfg.Protocols,
		Cooldown:            cfg.Cooldown,
		Failover:            cfg.Failover,
		Probe:               cfg.Probe,
		StatePath:           cfg.StatePath,
		MaxLatencyEntries:   cfg.MaxLatencyEntries,
	}
	for _, sub := range cfg.Subscriptions {
		view.Subscriptions = append(view.Subscriptions, EgressSubscriptionView{
			ID: sub.ID, Name: sub.Name, URL: maskSubscriptionURL(sub.URL),
			Inline: sub.Content != "", ContentLines: lineCount(sub.Content),
			Enabled: sub.Enabled, RefreshSeconds: sub.RefreshSeconds,
		})
	}
	return view
}

// lineCount reports how many non-empty entries an inline subscription holds.
func lineCount(content string) int {
	if content == "" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// maskSubscriptionURL keeps only the origin; feed URLs frequently embed
// tokens in the path or query.
func maskSubscriptionURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "***"
	}
	return parsed.Scheme + "://" + parsed.Host + "/***"
}

func resolveEgressUpdate(update *EgressUpdate, current *config.EgressConfig) (*config.EgressConfig, error) {
	if update == nil {
		return current, nil
	}
	known := make(map[string]string, len(currentSubscriptions(current)))
	masked := make(map[string]string, len(currentSubscriptions(current)))
	stored := make(map[string]config.EgressSubscription, len(currentSubscriptions(current)))
	for _, sub := range currentSubscriptions(current) {
		known[sub.ID] = sub.URL
		masked[sub.ID] = maskSubscriptionURL(sub.URL)
		stored[sub.ID] = sub
	}
	result := &config.EgressConfig{
		Enabled:             update.Enabled,
		ImportLegacyProxies: update.ImportLegacyProxies,
		Schedule:            update.Schedule,
		Protocols:           update.Protocols,
		Cooldown:            update.Cooldown,
		Failover:            update.Failover,
		Probe:               update.Probe,
		StatePath:           update.StatePath,
		MaxLatencyEntries:   update.MaxLatencyEntries,
	}
	seen := make(map[string]struct{}, len(update.Subscriptions))
	for _, sub := range update.Subscriptions {
		id := strings.TrimSpace(sub.ID)
		if id == "" {
			return nil, errors.New("egress subscription id must not be empty")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate egress subscription id %q", id)
		}
		seen[id] = struct{}{}
		resolved := strings.TrimSpace(sub.URL)
		content := sub.Content
		switch {
		case resolved != "" && content != "":
			return nil, fmt.Errorf("egress subscription %q must have either url or content", id)
		case resolved != "":
			if resolved == masked[id] {
				existing, ok := known[id]
				if !ok {
					return nil, fmt.Errorf("egress subscription %q needs a url", id)
				}
				resolved = existing
			}
		case content != "":
			// Inline source wins; any stored URL is dropped on purpose.
		default:
			existing, ok := stored[id]
			if !ok {
				return nil, fmt.Errorf("egress subscription %q needs a url", id)
			}
			resolved, content = existing.URL, existing.Content
		}
		result.Subscriptions = append(result.Subscriptions, config.EgressSubscription{
			ID: id, Name: sub.Name, URL: resolved, Content: content,
			Enabled: sub.Enabled, RefreshSeconds: sub.RefreshSeconds,
		})
	}
	return result, nil
}

func currentSubscriptions(cfg *config.EgressConfig) []config.EgressSubscription {
	if cfg == nil {
		return nil
	}
	return cfg.Subscriptions
}
