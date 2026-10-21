package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/logstore"
)

// requestLogResponse is the page shape returned to the WebUI.
type requestLogResponse struct {
	Enabled    bool                `json:"enabled"`
	Requests   []logstore.Request  `json:"requests"`
	Facets     map[string][]string `json:"facets"`
	NextCursor int64               `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
	Stats      logstore.Stats      `json:"stats"`
}

// requestLogStore returns the persistent request log, or nil when persistence is
// switched off in configuration.
func (a *Server) requestLogStore() *logstore.Service {
	if a.requestLogProvider == nil {
		return nil
	}
	return a.requestLogProvider()
}

func (a *Server) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	store := a.requestLogStore()
	response := requestLogResponse{Enabled: store != nil, Requests: []logstore.Request{}, Facets: map[string][]string{}}
	if store == nil {
		httpx.WriteJSON(w, http.StatusOK, response)
		return
	}
	query, err := parseRequestLogQuery(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request_log_query", err.Error())
		return
	}
	page, err := store.Requests(r.Context(), query)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "request_log_read_failed", "读取请求日志失败")
		return
	}
	if facets, facetErr := store.Facets(r.Context()); facetErr == nil {
		response.Facets = facets
	}
	response.Requests = page.Requests
	response.NextCursor = page.NextCursor
	response.HasMore = page.HasMore
	response.Stats = store.Stats(r.Context())
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (a *Server) handleRequestLogDetail(w http.ResponseWriter, r *http.Request) {
	store := a.requestLogStore()
	if store == nil {
		writeAdminError(w, http.StatusNotFound, "request_log_disabled", "请求日志未启用")
		return
	}
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" {
		writeAdminError(w, http.StatusBadRequest, "missing_request_id", "缺少 request_id")
		return
	}
	record, attempts, found, err := store.Detail(r.Context(), requestID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "request_log_read_failed", "读取请求详情失败")
		return
	}
	if !found {
		writeAdminError(w, http.StatusNotFound, "request_log_not_found", "该请求不在持久日志中")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"request": record, "attempts": attempts})
}

func (a *Server) handlePurgeRequestLogs(w http.ResponseWriter, r *http.Request) {
	store := a.requestLogStore()
	if store == nil {
		writeAdminError(w, http.StatusNotFound, "request_log_disabled", "请求日志未启用")
		return
	}
	removed, err := store.Purge(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "request_log_purge_failed", "清空请求日志失败")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed, "stats": store.Stats(r.Context())})
}

// parseRequestLogQuery reads the filter set. An unparseable value is a client
// error rather than a silently ignored filter, so a typo cannot look like "no
// results".
func parseRequestLogQuery(r *http.Request) (logstore.Query, error) {
	values := r.URL.Query()
	query := logstore.Query{
		Model:       values.Get("model"),
		Channel:     values.Get("channel"),
		Tier:        values.Get("tier"),
		KeyID:       values.Get("key"),
		ServerKeyID: values.Get("server_key"),
		ExitID:      values.Get("exit_id"),
		RequestID:   values.Get("request_id"),
		Keyword:     values.Get("keyword"),
	}
	var err error
	if query.Limit, err = intParam(values, "limit", 50, 1, 1000); err != nil {
		return query, err
	}
	before, err := intParam(values, "before", 0, 0, 1<<62)
	if err != nil {
		return query, err
	}
	query.BeforeID = int64(before)
	if query.Status, err = intParam(values, "status", 0, 0, 599); err != nil {
		return query, err
	}
	if query.Success, err = boolParam(values, "success"); err != nil {
		return query, err
	}
	if query.Anonymous, err = boolParam(values, "anonymous"); err != nil {
		return query, err
	}
	if query.Since, err = timeParam(values, "since"); err != nil {
		return query, err
	}
	if query.Until, err = timeParam(values, "until"); err != nil {
		return query, err
	}
	switch strings.ToLower(values.Get("outcome")) {
	case "":
	case "failed", "error":
		query.FailedOnly = true
	default:
		return query, errors.New("outcome 只支持 failed")
	}
	return query, nil
}

func intParam(values map[string][]string, name string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(firstValue(values, name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(name + " 不是有效数值")
	}
	return parsed, nil
}

func boolParam(values map[string][]string, name string) (*bool, error) {
	raw := strings.ToLower(strings.TrimSpace(firstValue(values, name)))
	if raw == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errors.New(name + " 只支持 true 或 false")
	}
	return &parsed, nil
}

func timeParam(values map[string][]string, name string) (time.Time, error) {
	raw := strings.TrimSpace(firstValue(values, name))
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New(name + " 需为 RFC3339 时间")
	}
	return parsed.UTC(), nil
}

func firstValue(values map[string][]string, name string) string {
	list := values[name]
	if len(list) == 0 {
		return ""
	}
	return list[0]
}
