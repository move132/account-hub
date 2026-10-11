package settings

import (
	"context"
	"net/http"
	"time"

	"account-hub/internal/apikey"
	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/platform/httpserver"
)

type Store interface {
	List(context.Context) ([]Item, error)
	Update(context.Context, map[string]any) ([]Item, error)
}

type HistoryCleaner interface {
	CleanupHistory(context.Context, int64) (CleanupResult, error)
}

type APIKeyManager interface {
	List(context.Context) ([]apikey.View, error)
	Create(context.Context, string) (apikey.SecretView, error)
	Delete(context.Context, string) error
}

type apiKeyAction struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"`
	ID     string `json:"id,omitempty"`
}

type Handler struct {
	repository  Store
	maintenance HistoryCleaner
	apiKeys     APIKeyManager
}

func NewHandler(repository Store, maintenance HistoryCleaner) *Handler {
	return &Handler{repository: repository, maintenance: maintenance}
}

func (h *Handler) WithAPIKeys(manager APIKeyManager) *Handler {
	h.apiKeys = manager
	return h
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	items, err := h.repository.List(r.Context())
	if err != nil {
		httpserver.ReportError(w, r, err, "SETTINGS_READ_FAILED", "读取设置失败")
		return
	}
	keys := []apikey.View{}
	if h.apiKeys != nil {
		keys, err = h.apiKeys.List(r.Context())
		if err != nil {
			httpserver.ReportError(w, r, err, "API_KEYS_READ_FAILED", "读取 API Key 列表失败")
			return
		}
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"settings": items, "api_keys": keys}, "查询成功")
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Settings     map[string]any `json:"settings"`
		APIKeyAction *apiKeyAction  `json:"api_key_action"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	ctx := audit.WithEntry(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "settings_update", TargetKind: "settings", Summary: "更新系统设置", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	if body.APIKeyAction != nil {
		if body.Settings != nil {
			httpserver.Error(w, r, http.StatusBadRequest, "INVALID_SETTINGS", "API Key 操作需单独提交", nil)
			return
		}
		h.patchAPIKey(w, r.WithContext(ctx), *body.APIKeyAction)
		return
	}
	items, err := h.repository.Update(ctx, body.Settings)
	if err != nil {
		httpserver.ReportError(w, r, err, "INVALID_SETTINGS", "设置保存失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"settings": items}, "设置保存成功")
}

func (h *Handler) patchAPIKey(w http.ResponseWriter, r *http.Request, action apiKeyAction) {
	if h.apiKeys == nil {
		httpserver.Error(w, r, http.StatusServiceUnavailable, "API_KEYS_UNAVAILABLE", "API Key 管理暂不可用", nil)
		return
	}
	switch action.Action {
	case "create":
		if action.ID != "" {
			httpserver.Error(w, r, http.StatusBadRequest, "API_KEY_ACTION_INVALID", "新增 Key 只需提供名称", nil)
			return
		}
		created, err := h.apiKeys.Create(r.Context(), action.Name)
		if err != nil {
			httpserver.ReportError(w, r, err, "API_KEY_CREATE_FAILED", "新增 API Key 失败")
			return
		}
		// The settings route does not persist response bodies in the idempotency store.
		httpserver.Write(w, http.StatusCreated, created, "API Key 已生成")
	case "delete":
		if action.Name != "" {
			httpserver.Error(w, r, http.StatusBadRequest, "API_KEY_ACTION_INVALID", "删除 Key 只需提供 ID", nil)
			return
		}
		if err := h.apiKeys.Delete(r.Context(), action.ID); err != nil {
			httpserver.ReportError(w, r, err, "API_KEY_DELETE_FAILED", "删除 API Key 失败")
			return
		}
		httpserver.Write(w, http.StatusOK, map[string]any{"deleted_id": action.ID}, "API Key 已删除")
	default:
		httpserver.Error(w, r, http.StatusBadRequest, "API_KEY_ACTION_INVALID", "API Key 仅支持新增或删除", nil)
	}
}

func (h *Handler) CleanupHistory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Before int64 `json:"before"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	summary := "清理历史日志，截止时间：" + time.UnixMilli(body.Before).In(time.Local).Format("2006-01-02 15:04")
	ctx := audit.WithEntry(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "history_cleanup", TargetKind: "settings", Summary: summary, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	result, err := h.maintenance.CleanupHistory(ctx, body.Before)
	if err != nil {
		httpserver.ReportError(w, r, err, "HISTORY_CLEANUP_FAILED", "清理历史日志失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"result": result}, "清理成功")
}
