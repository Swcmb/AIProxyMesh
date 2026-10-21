package admin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

func (a *Server) egressEngine() *egress.Engine {
	if a == nil || a.egressProvider == nil {
		return nil
	}
	return a.egressProvider()
}

func writeEgressDisabled(w http.ResponseWriter) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

func (a *Server) handleEgressSummary(w http.ResponseWriter, _ *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, engine.Snapshot())
}

func (a *Server) handleEgressNodes(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))
	page := engine.Nodes(egress.NodeQuery{
		Keyword:      query.Get("keyword"),
		Status:       query.Get("status"),
		Subscription: query.Get("subscription"),
		Protocol:     query.Get("protocol"),
		Region:       query.Get("region"),
		SortBy:       query.Get("sort_by"),
		SortDesc:     query.Get("sort_order") == "desc",
		Offset:       max(offset, 0),
		Limit:        limit,
	})
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (a *Server) handleEgressSubscriptions(w http.ResponseWriter, _ *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": engine.Subscriptions().Statuses()})
}

func (a *Server) handleEgressRefresh(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := engine.RefreshSubscription(ctx, id); err != nil {
		writeAdminError(w, http.StatusBadGateway, "subscription_refresh_failed", err.Error())
		return
	}
	status, _ := engine.Subscriptions().Status(id)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscription": status})
}

func (a *Server) handleEgressToggle(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := engine.SetNodeEnabled(r.PathValue("id"), input.Enabled); err != nil {
		writeAdminError(w, http.StatusNotFound, "node_not_found", err.Error())
		return
	}
	view, ok := engine.NodeDetail(r.PathValue("id"))
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"node": view})
}

func (a *Server) handleEgressNodeDetail(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	view, ok := engine.NodeDetail(r.PathValue("id"))
	if !ok {
		writeAdminError(w, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"node": view})
}

func (a *Server) handleEgressProbe(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := engine.ProbeNode(ctx, r.PathValue("id")); err != nil {
		writeAdminError(w, http.StatusNotFound, "node_probe_failed", err.Error())
		return
	}
	view, ok := engine.NodeDetail(r.PathValue("id"))
	if !ok {
		writeAdminError(w, http.StatusNotFound, "node_not_found", "node disappeared during probe")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"node": view})
}

func (a *Server) handleEgressEvents(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := engine.Events(r.Context(), limit)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "events_unavailable", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *Server) handleEgressDiagnose(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	var target *url.URL
	if raw := r.URL.Query().Get("target"); raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			writeAdminError(w, http.StatusBadRequest, "invalid_target", "target must be an absolute URL")
			return
		}
		target = parsed
	} else {
		target = engine.PrimaryTarget()
	}
	if target == nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_target", "no upstream target is configured")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"target":     target.String(),
		"candidates": engine.Diagnose(target, nil),
	})
}
