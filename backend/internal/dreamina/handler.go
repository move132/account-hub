package dreamina

import (
	"archive/zip"
	"bytes"
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
	"account-hub/internal/platform/security"
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
	item, err := h.create(r.Context(), input)
	if err != nil {
		if IsUniqueError(err) {
			httpserver.Error(w, r, http.StatusConflict, "DREAMINA_DUPLICATE", "Session ID 已存在", nil)
		} else {
			handleError(w, r, err, "创建即梦账号失败")
		}
		return
	}
	h.log(r, "dreamina_create", item.ID, "创建即梦账号")
	httpserver.Write(w, http.StatusCreated, map[string]any{"account": item}, "创建成功")
}

// create is the HTTP application command. The repository receives a transaction
// callback and remains independent of response envelopes and idempotency records.
func (h *Handler) create(ctx context.Context, input CreateInput) (View, error) {
	if _, ok := idempotency.RecordFromContext(ctx); !ok {
		return h.service.Create(ctx, input)
	}
	return h.service.createWithCommit(ctx, input, func(ctx context.Context, tx *sql.Tx, view View) error {
		body, err := json.Marshal(httpserver.Response{Code: http.StatusCreated, Success: true, Data: map[string]any{"account": view}, Msg: "创建成功", TS: h.service.now().UTC().UnixMilli()})
		if err != nil {
			return err
		}
		return idempotency.CompleteResultTx(ctx, tx, idempotency.Result{
			Status: http.StatusCreated, Body: string(body) + "\n", Headers: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			ResourceType: "dreamina", ResourceID: view.ID,
		})
	})
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
	if err != nil && result.RemoteCompleted {
		switch result.Status {
		case ClaimRejected:
			httpserver.Error(w, r, http.StatusUnprocessableEntity, "CLAIM_REJECTED", result.outcomeMessage(), map[string]any{"operation": result})
			return
		case ClaimUnconfirmed:
			httpserver.Error(w, r, http.StatusBadGateway, "CLAIM_OUTCOME_UNCONFIRMED", result.outcomeMessage(), map[string]any{"operation": result})
			return
		}
	}
	if err != nil {
		handleUpstreamError(w, r, err, "领取积分失败")
		return
	}
	h.log(r, "dreamina_credit_claim", id, "领取即梦积分")
	message := result.outcomeMessage()
	if result.Warning != nil {
		message = result.Warning.Message
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"upstream": security.Redact(result.Upstream), "operation": result}, message)
}

func (h *Handler) Assets(w http.ResponseWriter, r *http.Request) {
	data, err := h.service.Assets(r.Context(), r.PathValue("id"), intQuery(r, "offset", 0), intQuery(r, "count", 50))
	if err != nil {
		handleUpstreamError(w, r, err, "查询历史作品失败")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"assets": security.Redact(data.Legacy), "page": data}, "查询成功")
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
		httpserver.Write(w, http.StatusOK, result, "所有 Session均已存在，已跳过")
		return
	}
	h.log(r, "dreamina_import", result.Job.ID, fmt.Sprintf("创建 %d 条导入任务，跳过 %d 条重复记录", result.Accepted, result.Skipped))
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
	if h.audit == nil {
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	if err := h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: action, TargetKind: "dreamina", TargetID: targetID, Summary: summary, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr}); err != nil {
		httpserver.Logger(r.Context()).Error("record account audit", "action", action, "target_id", targetID, "error", err)
	}
}
