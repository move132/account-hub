package dreamina

import (
	"archive/zip"
	"bytes"
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
	"account-hub/internal/platform/security"
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
	filter := ListFilter{Page: intQuery(r, "page", 1), PageSize: intQuery(r, "page_size", 20), Search: strings.TrimSpace(r.URL.Query().Get("search")), Status: r.URL.Query().Get("status"), Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order")}
	items, total, err := h.service.List(r.Context(), filter)
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "DREAMINA_LIST_FAILED", "查询即梦账号失败", nil)
		return
	}
	if filter.PageSize < 1 || filter.PageSize > 500 {
		filter.PageSize = 20
	}
	totalPages := 0
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
			httpserver.Error(w, r, http.StatusConflict, "DREAMINA_DUPLICATE", "Session ID 已存在", nil)
		} else {
			httpserver.Error(w, r, http.StatusBadRequest, "DREAMINA_CREATE_FAILED", err.Error(), nil)
		}
		return
	}
	h.log(r, "dreamina_create", item.ID, "创建即梦账号")
	httpserver.Write(w, http.StatusCreated, map[string]any{"account": item}, "创建成功")
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		handleError(w, r, err, "查询即梦账号失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"account": item}, "查询成功")
}

func (h *Handler) GetForEdit(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetForEdit(r.Context(), r.PathValue("id"))
	if err != nil {
		handleError(w, r, err, "查询即梦账号失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"account": item}, "查询成功")
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var input UpdateInput
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		handleError(w, r, err, "更新即梦账号失败")
		return
	}
	h.log(r, "dreamina_update", item.ID, "更新即梦账号")
	httpserver.Write(w, http.StatusOK, map[string]any{"account": item}, "更新成功")
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.service.Delete(r.Context(), id); err != nil {
		handleError(w, r, err, "删除即梦账号失败")
		return
	}
	h.log(r, "dreamina_delete", id, "删除即梦账号")
	httpserver.Write(w, http.StatusOK, nil, "删除成功")
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, credit, err := h.service.Check(r.Context(), id)
	if err != nil {
		handleUpstreamError(w, r, err, "即梦账号检查失败")
		return
	}
	h.log(r, "dreamina_check", id, "检查即梦账号")
	httpserver.Write(w, http.StatusOK, map[string]any{"account": item, "credit": security.Redact(credit.Raw)}, "检查成功")
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := h.service.Refresh(r.Context(), id)
	if err != nil {
		handleUpstreamError(w, r, err, "刷新即梦 Session 失败")
		return
	}
	h.log(r, "dreamina_refresh", id, "刷新即梦 Session")
	httpserver.Write(w, http.StatusOK, map[string]any{"account": item}, "刷新成功")
}

func (h *Handler) Credit(w http.ResponseWriter, r *http.Request) {
	credit, err := h.service.Credit(r.Context(), r.PathValue("id"))
	if err != nil {
		handleUpstreamError(w, r, err, "查询积分失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"balance": credit.Balance, "upstream": security.Redact(credit.Raw)}, "查询成功")
}

func (h *Handler) ClaimCredit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result, err := h.service.ClaimCredit(r.Context(), id)
	if err != nil {
		handleUpstreamError(w, r, err, "领取积分失败")
		return
	}
	h.log(r, "dreamina_credit_claim", id, "领取即梦积分")
	httpserver.Write(w, http.StatusOK, map[string]any{"upstream": security.Redact(result)}, "积分领取成功")
}

func (h *Handler) Assets(w http.ResponseWriter, r *http.Request) {
	data, err := h.service.Assets(r.Context(), r.PathValue("id"), intQuery(r, "offset", 0), intQuery(r, "count", 50))
	if err != nil {
		handleUpstreamError(w, r, err, "查询历史作品失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"assets": security.Redact(data)}, "查询成功")
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
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", "无法解析上传文件", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	rows, err := readImport(r)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", err.Error(), nil)
		return
	}
	if err := h.service.markImportDuplicates(r.Context(), rows); err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "IMPORT_CHECK_FAILED", "检查重复 Session 失败，请重试", nil)
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
		httpserver.Write(w, http.StatusOK, map[string]any{"rows": importPreview(rows), "total": len(rows), "valid": valid, "invalid": invalid, "duplicates": duplicates}, "预览完成")
		return
	}
	if invalid > 0 {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_HAS_ERRORS", "导入内容存在错误，请先修正", map[string]any{"rows": importPreview(rows), "invalid": invalid})
		return
	}
	if valid == 0 {
		httpserver.Write(w, http.StatusOK, map[string]any{"job": nil, "accepted": 0, "skipped": duplicates}, "所有 Session 均已存在，已跳过")
		return
	}
	targets := make([]job.Target, 0, valid)
	for _, row := range rows {
		if !row.Duplicate {
			targets = append(targets, job.Target{Label: row.Input.Email, Payload: row.Input})
		}
	}
	session, _ := auth.SessionFromContext(r.Context())
	createdJob, _, err := h.jobs.Create(r.Context(), "dreamina", "import", session.AdminID, r.URL.Path, "", job.RequestHash(rows), targets)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_JOB_FAILED", err.Error(), nil)
		return
	}
	h.log(r, "dreamina_import", createdJob.ID, fmt.Sprintf("创建 %d 行即梦导入任务，跳过 %d 行重复 Session", valid, duplicates))
	httpserver.Write(w, http.StatusAccepted, map[string]any{"job": createdJob, "accepted": valid, "skipped": duplicates}, "导入任务已创建")
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	sessionIDs, err := h.service.ActiveSessionIDs(r.Context())
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "DREAMINA_EXPORT_FAILED", "导出失败", nil)
		return
	}
	if len(sessionIDs) == 0 {
		httpserver.Error(w, r, http.StatusNotFound, "DREAMINA_EXPORT_EMPTY", "没有找到启用的 Session", nil)
		return
	}

	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	const batchSize = 300
	for start := 0; start < len(sessionIDs); start += batchSize {
		end := min(start+batchSize, len(sessionIDs))
		entry, createErr := archive.Create(fmt.Sprintf("%d-%d.txt", start+1, end))
		if createErr != nil {
			_ = archive.Close()
			httpserver.Error(w, r, http.StatusInternalServerError, "DREAMINA_EXPORT_FAILED", "创建导出文件失败", nil)
			return
		}
		if _, writeErr := entry.Write([]byte(strings.Join(sessionIDs[start:end], "|"))); writeErr != nil {
			_ = archive.Close()
			httpserver.Error(w, r, http.StatusInternalServerError, "DREAMINA_EXPORT_FAILED", "写入导出文件失败", nil)
			return
		}
	}
	allEntry, err := archive.Create("session-all.txt")
	if err == nil {
		_, err = allEntry.Write([]byte(strings.Join(sessionIDs, "|")))
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "DREAMINA_EXPORT_FAILED", "创建导出文件失败", nil)
		return
	}

	filename := fmt.Sprintf("dreamina_sessions_export_%s.zip", time.Now().Format("2006-01-02_15-04-05"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.Itoa(buffer.Len()))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
	h.log(r, "dreamina_export", "", fmt.Sprintf("导出 %d 个启用的即梦 Session", len(sessionIDs)))
}

func handleError(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case IsNotFound(err):
		httpserver.Error(w, r, http.StatusNotFound, "DREAMINA_NOT_FOUND", "即梦账号不存在", nil)
	case errors.Is(err, ErrVersionConflict):
		httpserver.Error(w, r, http.StatusConflict, "VERSION_CONFLICT", "记录已被其他操作修改，请刷新后重试", nil)
	case IsUniqueError(err):
		httpserver.Error(w, r, http.StatusConflict, "DREAMINA_DUPLICATE", "Session ID 已存在", nil)
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
	httpserver.Error(w, r, status, code, message+": "+err.Error(), nil)
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
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: action, TargetKind: "dreamina", TargetID: targetID, Summary: summary, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
}
