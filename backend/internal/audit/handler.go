package audit

import (
	"net/http"
	"strconv"

	"account-hub/internal/platform/httpserver"
)

type Handler struct{ repository *Repository }

func NewHandler(repository *Repository) *Handler { return &Handler{repository: repository} }

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	page := parseInt(r.URL.Query().Get("page"), 1)
	pageSize := parseInt(r.URL.Query().Get("page_size"), 20)
	items, total, err := h.repository.List(r.Context(), page, pageSize)
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "AUDIT_LIST_FAILED", "查询审计日志失败", nil)
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

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
