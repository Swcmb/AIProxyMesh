package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

func (a *Server) handleGeoIPStatus(w http.ResponseWriter, _ *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	status, ok := engine.GeoIPStatus()
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "status": status})
}

func (a *Server) handleGeoIPUpdate(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := engine.UpdateGeoIP(ctx); err != nil {
		writeAdminError(w, http.StatusBadGateway, "geoip_update_failed", a.manager.Redact(err.Error()))
		return
	}
	status, _ := engine.GeoIPStatus()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "status": status})
}

func (a *Server) handleGeoIPLookup(w http.ResponseWriter, r *http.Request) {
	engine := a.egressEngine()
	if engine == nil {
		writeEgressDisabled(w)
		return
	}
	address := r.URL.Query().Get("ip")
	result, ok := engine.GeoIPLookup(address)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ip":      address,
		"found":   ok,
		"country": result.Country,
		"name":    result.Name,
	})
}
