package job

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/platform/httpserver"
)

type Handler struct {
	manager   *Manager
	audit     *audit.Repository
	resolvers map[string]TargetResolver
}

type TargetResolver func(context.Context, []string) ([]Target, error)

func NewHandler(manager *Manager, auditRepository *audit.Repository) *Handler {
	return &Handler{manager: manager, audit: auditRepository, resolvers: map[string]TargetResolver{}}
}

func (h *Handler) WithTargetResolver(targetKind string, resolver TargetResolver) *Handler {
	if resolver != nil {
		h.resolvers[targetKind] = resolver
	}
	return h
}

type CreateRequest struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
}

type DeleteRequest struct {
	IDs []string `json:"ids"`
}

func (h *Handler) Create(targetKind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body CreateRequest
		if !httpserver.DecodeJSON(w, r, &body) {
			return
		}
		targets, err := h.targets(r.Context(), targetKind, body.IDs)
		if err != nil {
			httpserver.Error(w, r, http.StatusBadRequest, "JOB_TARGETS_INVALID", err.Error(), nil)
			return
		}
		session, _ := auth.SessionFromContext(r.Context())
		job, replay, err := h.manager.Create(r.Context(), targetKind, body.Action, session.AdminID,
			r.URL.Path, r.Header.Get("Idempotency-Key"), RequestHash(body), targets)
		if err != nil {
			if errors.Is(err, ErrIdempotencyConflict) {
				httpserver.Error(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key 已用于不同请求", nil)
			} else {
				httpserver.Error(w, r, http.StatusBadRequest, "JOB_CREATE_FAILED", err.Error(), nil)
			}
			return
		}
		_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: targetKind + "_batch_" + body.Action,
			TargetKind: "job", TargetID: job.ID, Summary: "创建批量任务", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
		message := "任务已创建"
		if replay {
			message = "返回已创建的任务"
		}
		httpserver.Write(w, http.StatusAccepted, map[string]any{"job": job, "idempotent_replay": replay}, message)
	}
}

func (h *Handler) targets(ctx context.Context, targetKind string, ids []string) ([]Target, error) {
	clean := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if resolver, found := h.resolvers[targetKind]; found {
		return resolver(ctx, clean)
	}
	targets := make([]Target, 0, len(clean))
	for _, id := range clean {
		targets = append(targets, Target{ID: id, Label: id})
	}
	return targets, nil
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	page, pageSize := queryInt(r, "page", 1), queryInt(r, "page_size", 20)
	items, total, err := h.manager.List(r.Context(), page, pageSize)
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "JOB_LIST_FAILED", "查询任务失败", nil)
		return
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 20
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"items": items, "page": max(page, 1), "page_size": pageSize, "total": total, "total_pages": totalPages}, "查询成功")
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	item, err := h.manager.Get(r.Context(), r.PathValue("id"), true)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		}
		httpserver.Error(w, r, status, "JOB_NOT_FOUND", "任务不存在", nil)
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"job": item}, "查询成功")
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	item, err := h.manager.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		httpserver.Error(w, r, http.StatusConflict, "JOB_CANCEL_FAILED", "任务不存在或无法取消", nil)
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "job_cancel", TargetKind: "job", TargetID: item.ID, Summary: "取消任务", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, map[string]any{"job": item}, "任务已取消")
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.manager.Delete(r.Context(), id); err != nil {
		writeDeleteError(w, r, err)
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "job_delete", TargetKind: "job", TargetID: id, Summary: "删除任务", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, nil, "删除成功")
}

func (h *Handler) DeleteBatch(w http.ResponseWriter, r *http.Request) {
	var body DeleteRequest
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	deleted, err := h.manager.DeleteMany(r.Context(), body.IDs)
	if err != nil {
		writeDeleteError(w, r, err)
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "job_batch_delete", TargetKind: "job", Summary: "批量删除任务", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, map[string]any{"deleted": deleted}, "批量删除成功")
}

func writeDeleteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNoSelection):
		httpserver.Error(w, r, http.StatusBadRequest, "JOB_IDS_REQUIRED", "请至少选择一个任务", nil)
	case errors.Is(err, ErrNotFound):
		httpserver.Error(w, r, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在", nil)
	case errors.Is(err, ErrActive):
		httpserver.Error(w, r, http.StatusConflict, "JOB_ACTIVE", "任务尚未结束，请先取消或等待执行完成", nil)
	default:
		httpserver.Error(w, r, http.StatusInternalServerError, "JOB_DELETE_FAILED", "删除任务失败", nil)
	}
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return value
}
