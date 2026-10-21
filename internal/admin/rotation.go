package admin

import (
	"net/http"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

func (a *Server) handleRotation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 200, a.manager.RotationSnapshot())
}
func (a *Server) handleSaveRotation(w http.ResponseWriter, r *http.Request) {
	var input config.RotationConfig
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, 400, "invalid_request", err.Error())
		return
	}
	_, err := a.manager.Update(func(cfg *config.Config) error {
		if err := input.Validate(nil); err != nil {
			return err
		}
		cfg.Rotation = input
		return nil
	})
	if err != nil {
		writeAdminError(w, 400, "rotation_rejected", a.manager.Redact(err.Error()))
		return
	}
	a.handleRotation(w, r)
}
func (a *Server) handleSelectRotation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Group string `json:"group"`
		Model string `json:"model"`
	}
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, 400, "invalid_request", err.Error())
		return
	}
	if err := a.manager.SelectRotation(input.Group, input.Model); err != nil {
		writeAdminError(w, 400, "rotation_rejected", a.manager.Redact(err.Error()))
		return
	}
	a.handleRotation(w, r)
}
