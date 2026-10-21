package admin

import (
	"net/http"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/ranking"
)

type rankingsResponse struct {
	TaskType    ranking.Task     `json:"task_type"`
	Preset      ranking.Preset   `json:"preset"`
	GeneratedAt time.Time        `json:"generated_at"`
	SampleHours int              `json:"sample_hours"`
	Models      []ranking.Result `json:"models"`
}

func (a *Server) handleRankings(w http.ResponseWriter, r *http.Request) {
	task := r.URL.Query().Get("task")
	if task == "" {
		task = string(ranking.AgentCoding)
	}
	preset, results, err := a.manager.Rankings(task)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, rankingsResponse{
		TaskType:    preset.Task,
		Preset:      preset,
		GeneratedAt: time.Now().UTC(),
		SampleHours: 24,
		Models:      results,
	})
}

func (a *Server) handleRankingTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": a.manager.RankingTasks()})
}
