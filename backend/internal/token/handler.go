package token

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/job"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/upstream"
)

type Handler struct {
	service *Service
	audit   *audit.Repository
	jobs    *job.Manager
}

func NewHandler(service *Service, auditRepository *audit.Repository, jobs *job.Manager) *Handler {
	return &Handler{service: service, audit: auditRepository, jobs: jobs}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	filter := ListFilter{
		Page: intQuery(r, "page", 1), PageSize: intQuery(r, "page_size", 20),
		Search: strings.TrimSpace(r.URL.Query().Get("search")), Status: r.URL.Query().Get("status"),
		Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order"),
	}
	items, total, err := h.service.List(r.Context(), filter)
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "TOKEN_LIST_FAILED", "查询 Token 失败", nil)
		return
	}
	totalPages := 0
	if filter.PageSize < 1 || filter.PageSize > 500 {
		filter.PageSize = 20
	}
	if total > 0 {
		totalPages = (total + filter.PageSize - 1) / filter.PageSize
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"items": items, "page": max(filter.Page, 1), "page_size": filter.PageSize, "total": total, "total_pages": totalPages}, "查询成功")
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var input CreateInput
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Create(r.Context(), input)
	if err != nil {
		if IsUniqueError(err) {
			httpserver.Error(w, r, http.StatusConflict, "TOKEN_DUPLICATE", "Token 或 Session Token 已存在", nil)
		} else {
			httpserver.Error(w, r, http.StatusBadRequest, "TOKEN_CREATE_FAILED", err.Error(), nil)
		}
		return
	}
	h.log(r, "token_create", item.ID, "创建 Token")
	httpserver.Write(w, http.StatusCreated, map[string]any{"token": item}, "创建成功")
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		handleError(w, r, err, "查询 Token 失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"token": item}, "查询成功")
}

func (h *Handler) GetForEdit(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetForEdit(r.Context(), r.PathValue("id"))
	if err != nil {
		handleError(w, r, err, "查询 Token 失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"token": item}, "查询成功")
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var input UpdateInput
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		handleError(w, r, err, "更新 Token 失败")
		return
	}
	h.log(r, "token_update", item.ID, "更新 Token")
	httpserver.Write(w, http.StatusOK, map[string]any{"token": item}, "更新成功")
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.service.Delete(r.Context(), id); err != nil {
		handleError(w, r, err, "删除 Token 失败")
		return
	}
	h.log(r, "token_delete", id, "删除 Token")
	httpserver.Write(w, http.StatusOK, nil, "删除成功")
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := h.service.Check(r.Context(), id)
	if err != nil {
		handleUpstreamError(w, r, err, "检查失败")
		return
	}
	h.log(r, "token_check", id, "检查 Token")
	httpserver.Write(w, http.StatusOK, map[string]any{"token": item}, "检查成功")
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := h.service.Refresh(r.Context(), id)
	if err != nil {
		handleUpstreamError(w, r, err, "刷新失败")
		return
	}
	h.log(r, "token_refresh", id, "刷新 Token")
	httpserver.Write(w, http.StatusOK, map[string]any{"token": item}, "刷新成功")
}

func (h *Handler) Attempts(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.Attempts(r.Context(), r.PathValue("id"), intQuery(r, "limit", 50))
	if err != nil {
		handleError(w, r, err, "查询执行记录失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"items": items}, "查询成功")
}

func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	const maxImportBytes = 8 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", "无法解析导入内容", nil)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	rows, err := readImport(r)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", err.Error(), nil)
		return
	}
	if err := h.service.markImportDuplicates(r.Context(), rows); err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "IMPORT_CHECK_FAILED", "检查重复 Token 失败，请重试", nil)
		return
	}
	valid, invalid, duplicates := 0, 0, 0
	for _, row := range rows {
		if len(row.Errors) > 0 {
			invalid++
		} else if row.Duplicate {
			duplicates++
		} else {
			valid++
		}
	}
	if r.URL.Query().Get("dry_run") == "true" {
		httpserver.Write(w, http.StatusOK, map[string]any{
			"rows": importPreview(rows), "total": len(rows), "valid": valid, "invalid": invalid,
			"duplicates": duplicates,
		}, "预览完成")
		return
	}
	if invalid > 0 {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_HAS_ERRORS", "导入内容存在错误，请先修正", map[string]any{"rows": importPreview(rows), "invalid": invalid})
		return
	}
	if valid == 0 {
		httpserver.Write(w, http.StatusOK, map[string]any{"job": nil, "accepted": 0, "skipped": duplicates}, "所有 Token 均已存在，已跳过")
		return
	}
	targets := make([]job.Target, 0, valid)
	for _, row := range rows {
		if row.Duplicate {
			continue
		}
		targets = append(targets, job.Target{Label: row.Input.Name, Payload: row.Input})
	}
	session, _ := auth.SessionFromContext(r.Context())
	createdJob, _, err := h.jobs.Create(r.Context(), "token", "import", session.AdminID, r.URL.Path, "", job.RequestHash(rows), targets)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_JOB_FAILED", err.Error(), nil)
		return
	}
	h.log(r, "token_import", createdJob.ID, fmt.Sprintf("创建 %d 条 Token 导入任务，跳过 %d 条重复 Token", valid, duplicates))
	httpserver.Write(w, http.StatusAccepted, map[string]any{
		"job": createdJob, "accepted": valid, "skipped": duplicates,
	}, "导入任务已创建")
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs []string `json:"ids"`
	}
	if !httpserver.DecodeJSON(w, r, &request) {
		return
	}
	if len(request.IDs) == 0 {
		httpserver.Error(w, r, http.StatusBadRequest, "EXPORT_TARGET_REQUIRED", "请先选择要导出的 Token", nil)
		return
	}
	if len(request.IDs) > maxImportRows {
		httpserver.Error(w, r, http.StatusBadRequest, "EXPORT_TOO_MANY_TARGETS", fmt.Sprintf("每次最多导出 %d 个 Token", maxImportRows), nil)
		return
	}
	ids := make([]string, 0, len(request.IDs))
	seen := make(map[string]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, found := seen[id]; !found {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		httpserver.Error(w, r, http.StatusBadRequest, "EXPORT_TARGET_REQUIRED", "请先选择要导出的 Token", nil)
		return
	}
	tokensByID, err := h.service.AccessTokensByIDs(r.Context(), ids)
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "TOKEN_EXPORT_FAILED", "导出失败", nil)
		return
	}
	tokens := make([]string, 0, len(ids))
	for _, id := range ids {
		if token, found := tokensByID[id]; found {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) != len(ids) {
		httpserver.Error(w, r, http.StatusNotFound, "TOKEN_NOT_FOUND", "部分选中的 Token 已不存在，请刷新后重试", nil)
		return
	}
	content := []byte(strings.Join(tokens, "\r\n"))
	filename := fmt.Sprintf("access_tokens_%s.txt", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
	h.log(r, "token_export", "", fmt.Sprintf("导出 %d 个选中 Token 的 Access Token", len(tokens)))
}

func handleError(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case IsNotFound(err):
		httpserver.Error(w, r, http.StatusNotFound, "TOKEN_NOT_FOUND", "Token 不存在", nil)
	case errors.Is(err, ErrVersionConflict):
		httpserver.Error(w, r, http.StatusConflict, "VERSION_CONFLICT", "记录已被其他操作修改，请刷新后重试", nil)
	case IsUniqueError(err):
		httpserver.Error(w, r, http.StatusConflict, "TOKEN_DUPLICATE", "Token 或 Session Token 已存在", nil)
	default:
		httpserver.Error(w, r, http.StatusBadRequest, ErrorCode(err), message+": "+err.Error(), nil)
	}
}

func handleUpstreamError(w http.ResponseWriter, r *http.Request, err error, message string) {
	if IsNotFound(err) {
		handleError(w, r, err, message)
		return
	}
	code := ErrorCode(err)
	status := http.StatusBadGateway
	switch code {
	case upstream.InvalidCredentials:
		status = http.StatusUnprocessableEntity
	case upstream.RateLimited:
		status = http.StatusTooManyRequests
	case upstream.Timeout:
		status = http.StatusGatewayTimeout
	case upstream.Unknown:
		status = http.StatusBadRequest
	}
	details := map[string]any{}
	var upstreamErr *upstream.Error
	if errors.As(err, &upstreamErr) && upstreamErr.HTTPStatus > 0 {
		details["upstream_status"] = upstreamErr.HTTPStatus
	}
	var refreshErr *refreshError
	if errors.As(err, &refreshErr) {
		details["stage"] = refreshErr.stage
	}
	httpserver.Error(w, r, status, code, message+": "+err.Error(), details)
}

func intQuery(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return fallback
	}
	return value
}

func (h *Handler) log(r *http.Request, action, targetID, summary string) {
	session, _ := auth.SessionFromContext(r.Context())
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: action, TargetKind: "token", TargetID: targetID, Summary: summary, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
}
