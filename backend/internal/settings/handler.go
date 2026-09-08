package settings

import (
	"net/http"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/platform/httpserver"
)

type Handler struct {
	repository *Repository
	audit      *audit.Repository
}

func NewHandler(repository *Repository, auditRepository *audit.Repository) *Handler {
	return &Handler{repository: repository, audit: auditRepository}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	items, err := h.repository.List(r.Context(), true)
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "SETTINGS_READ_FAILED", "读取设置失败", nil)
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"settings": items}, "查询成功")
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Settings map[string]any `json:"settings"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	items, err := h.repository.Update(r.Context(), body.Settings)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "settings_update", TargetKind: "settings", Summary: "更新系统设置", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, map[string]any{"settings": items}, "设置保存成功")
}

func (h *Handler) CleanupHistory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Before int64 `json:"before"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	result, err := h.repository.CleanupHistory(r.Context(), body.Before)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "HISTORY_CLEANUP_FAILED", err.Error(), nil)
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	summary := "清理历史日志，截止时间：" + time.UnixMilli(body.Before).In(time.Local).Format("2006-01-02 15:04")
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "history_cleanup", TargetKind: "settings", Summary: summary, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, map[string]any{"result": result}, "清理成功")
}
