package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

// subscriptionPayload is the create/update body of one subscription. A field
// left out of an update keeps its stored value; URL and content are mutually
// exclusive, and sending neither keeps the stored source.
type subscriptionPayload struct {
	ID             string    `json:"id"`
	Name           *string   `json:"name,omitempty"`
	URL            *string   `json:"url,omitempty"`
	Content        *string   `json:"content,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
	RefreshSeconds *int      `json:"refresh_seconds,omitempty"`
	Protocols      *[]string `json:"protocols,omitempty"`
	Ephemeral      *bool     `json:"ephemeral,omitempty"`
	EvictDelaySecs *int      `json:"evict_delay_seconds,omitempty"`
}

func (a *Server) egressSubscriptions() []config.EgressSubscription {
	egress := a.manager.Config().Egress
	if egress == nil {
		return nil
	}
	return egress.Subscriptions
}

func writeEgressStatuses(w http.ResponseWriter, a *Server) {
	engine := a.egressEngine()
	if engine == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": nil})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": engine.Subscriptions().Statuses()})
}

func (a *Server) handleEgressSubscriptionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, sub := range a.egressSubscriptions() {
		if sub.ID != id {
			continue
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"subscription": sub,
			"masked_url":   maskSubscriptionURL(sub.URL),
		})
		return
	}
	writeAdminError(w, http.StatusNotFound, "subscription_not_found", "subscription not found")
}

func (a *Server) handleEgressSubscriptionCreate(w http.ResponseWriter, r *http.Request) {
	var input subscriptionPayload
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "订阅 ID 不能为空")
		return
	}
	if stringValue(input.URL) == "" && stringValue(input.Content) == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "订阅需要 url 或 content")
		return
	}
	_, err := a.manager.Update(func(cfg *config.Config) error {
		egress := ensureEgressConfig(cfg)
		for _, sub := range egress.Subscriptions {
			if sub.ID == id {
				return fmt.Errorf("订阅 %q 已存在", id)
			}
		}
		enabled := boolValue(input.Enabled, true)
		refreshSeconds := intValue(input.RefreshSeconds, 3600)
		content := stringValue(input.Content)
		if content != "" {
			// An inline feed is parsed once and never re-fetched on a timer.
			refreshSeconds = 0
		}
		egress.Subscriptions = append(egress.Subscriptions, config.EgressSubscription{
			ID:                id,
			Name:              stringValue(input.Name),
			URL:               stringValue(input.URL),
			Content:           content,
			Enabled:           enabled,
			RefreshSeconds:    refreshSeconds,
			Protocols:         stringSliceValue(input.Protocols),
			Ephemeral:         boolValue(input.Ephemeral, false),
			EvictDelaySeconds: intValue(input.EvictDelaySecs, 0),
		})
		return nil
	})
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "subscription_rejected", a.manager.Redact(err.Error()))
		return
	}
	writeEgressStatuses(w, a)
}

func (a *Server) handleEgressSubscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input subscriptionPayload
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.URL != nil && input.Content != nil && strings.TrimSpace(*input.URL) != "" && strings.TrimSpace(*input.Content) != "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "url 与 content 只能二选一")
		return
	}
	_, err := a.manager.Update(func(cfg *config.Config) error {
		egress := ensureEgressConfig(cfg)
		for i := range egress.Subscriptions {
			sub := &egress.Subscriptions[i]
			if sub.ID != id {
				continue
			}
			if input.Name != nil {
				sub.Name = *input.Name
			}
			if input.URL != nil {
				sub.URL = *input.URL
				if strings.TrimSpace(*input.URL) != "" {
					sub.Content = ""
				}
			}
			if input.Content != nil && strings.TrimSpace(*input.Content) != "" {
				sub.Content = *input.Content
				sub.URL = ""
				sub.RefreshSeconds = 0
			}
			if input.Enabled != nil {
				sub.Enabled = *input.Enabled
			}
			if input.RefreshSeconds != nil {
				sub.RefreshSeconds = *input.RefreshSeconds
			}
			if input.Protocols != nil {
				sub.Protocols = append([]string(nil), (*input.Protocols)...)
			}
			if input.Ephemeral != nil {
				sub.Ephemeral = *input.Ephemeral
			}
			if input.EvictDelaySecs != nil {
				sub.EvictDelaySeconds = *input.EvictDelaySecs
			}
			if strings.TrimSpace(sub.URL) == "" && strings.TrimSpace(sub.Content) == "" {
				return fmt.Errorf("订阅 %q 需要 url 或 content", id)
			}
			return nil
		}
		return fmt.Errorf("订阅 %q 不存在", id)
	})
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "subscription_rejected", a.manager.Redact(err.Error()))
		return
	}
	writeEgressStatuses(w, a)
}

func (a *Server) handleEgressSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := a.manager.Update(func(cfg *config.Config) error {
		egress := cfg.Egress
		if egress == nil {
			return fmt.Errorf("订阅 %q 不存在", id)
		}
		kept := egress.Subscriptions[:0]
		for _, sub := range egress.Subscriptions {
			if sub.ID != id {
				kept = append(kept, sub)
			}
		}
		if len(kept) == len(egress.Subscriptions) {
			return fmt.Errorf("订阅 %q 不存在", id)
		}
		egress.Subscriptions = kept
		return nil
	})
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "subscription_rejected", a.manager.Redact(err.Error()))
		return
	}
	writeEgressStatuses(w, a)
}

func (a *Server) handleEgressSubscriptionCleanup(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	removed, err := engine.CleanupSubscription(ctx, id)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "subscription_cleanup_failed", a.manager.Redact(err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed_nodes": removed})
}

// ensureEgressConfig returns the writable egress section, creating it when the
// operator adds the first subscription to a configuration without one.
func ensureEgressConfig(cfg *config.Config) *config.EgressConfig {
	if cfg.Egress == nil {
		cfg.Egress = &config.EgressConfig{Enabled: true}
	}
	if !cfg.Egress.Enabled {
		cfg.Egress.Enabled = true
	}
	return cfg.Egress
}

func stringSliceValue(value *[]string) []string {
	if value == nil {
		return nil
	}
	return append([]string(nil), (*value)...)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}
