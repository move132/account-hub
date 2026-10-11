package token

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/idempotency"
	"account-hub/internal/job"
	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/upstream"
)

type Handler struct {
	service *Service
	audit   *audit.Repository
	imports *ImportUseCase
}

func NewHandler(service *Service, auditRepository *audit.Repository, jobs *job.Manager) *Handler {
	return &Handler{service: service, audit: auditRepository, imports: NewImportUseCase(service, jobs)}
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
	item, err := h.create(r.Context(), input)
	if err != nil {
		if IsUniqueError(err) {
			httpserver.Error(w, r, http.StatusConflict, "TOKEN_DUPLICATE", "Token 或 Session Token 已存在", nil)
		} else {
			handleError(w, r, err, "创建 Token 失败")
		}
		return
	}
	h.log(r, "token_create", item.ID, "创建 Token")
	httpserver.Write(w, http.StatusCreated, map[string]any{"token": item}, "创建成功")
}

// create is the HTTP application command. The repository receives a transaction
// callback and remains independent of response envelopes and idempotency records.
func (h *Handler) create(ctx context.Context, input CreateInput) (View, error) {
	if _, ok := idempotency.RecordFromContext(ctx); !ok {
		return h.service.Create(ctx, input)
	}
	return h.service.createWithCommit(ctx, input, func(ctx context.Context, tx *sql.Tx, view View) error {
		body, err := json.Marshal(httpserver.Response{Code: http.StatusCreated, Success: true, Data: map[string]any{"token": view}, Msg: "创建成功", TS: h.service.now().UTC().UnixMilli()})
		if err != nil {
			return err
		}
		return idempotency.CompleteResultTx(ctx, tx, idempotency.Result{
			Status: http.StatusCreated, Body: string(body) + "\n", Headers: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			ResourceType: "token", ResourceID: view.ID,
		})
	})
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
	reader, closeReader, err := importReader(r)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", err.Error(), nil)
		return
	}
	defer closeReader()
	plan, err := h.imports.Preview(r.Context(), reader)
	if err != nil {
		if apperror.KindOf(err) == apperror.Validation {
			httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", err.Error(), nil)
		} else {
			httpserver.ReportError(w, r, err, "IMPORT_CHECK_FAILED", "检查重复内容失败，请重试")
		}
		return
	}
	if r.URL.Query().Get("dry_run") == "true" {
		httpserver.Write(w, http.StatusOK, plan.Report, "预览完成")
		return
	}
	if plan.Report.Invalid > 0 {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_HAS_ERRORS", "导入内容存在错误，请先修正", map[string]any{"rows": plan.Report.Rows, "invalid": plan.Report.Invalid})
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	result, err := h.imports.Submit(r.Context(), plan, session.AdminID, r.URL.Path)
	if err != nil {
		httpserver.ReportError(w, r, err, "IMPORT_JOB_FAILED", "创建导入任务失败")
		return
	}
	if result.Job == nil {
		httpserver.Write(w, http.StatusOK, result, "所有 Token均已存在，已跳过")
		return
	}
	h.log(r, "token_import", result.Job.ID, fmt.Sprintf("创建 %d 条导入任务，跳过 %d 条重复记录", result.Accepted, result.Skipped))
	httpserver.Write(w, http.StatusAccepted, result, "导入任务已创建")
}

// importReader belongs to the HTTP adapter; the import use case only sees io.Reader.
func importReader(r *http.Request) (io.Reader, func(), error) {
	file, _, err := r.FormFile("file")
	if err == nil {
		return file, func() { _ = file.Close() }, nil
	}
	if !errors.Is(err, http.ErrMissingFile) {
		return nil, func() {}, errors.New("无法读取上传文件")
	}
	return strings.NewReader(r.FormValue("content")), func() {}, nil
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
		httpserver.ReportError(w, r, err, ErrorCode(err), message)
	}
}

func handleUpstreamError(w http.ResponseWriter, r *http.Request, err error, message string) {
	var providerError *upstream.Error
	if !errors.As(err, &providerError) {
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
		details["remote_completed"] = refreshErr.stage == refreshStageProfile
		details["credentials_persisted"] = false
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
	if h.audit == nil {
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	if err := h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: action, TargetKind: "token", TargetID: targetID, Summary: summary, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr}); err != nil {
		httpserver.Logger(r.Context()).Error("record account audit", "action", action, "target_id", targetID, "error", err)
	}
}
