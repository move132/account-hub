package token

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
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

type importRow struct {
	Line   int         `json:"line"`
	Input  CreateInput `json:"input"`
	Errors []string    `json:"errors"`
}

func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", "无法解析上传文件", nil)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_FILE_REQUIRED", "请选择 CSV 文件", nil)
		return
	}
	defer file.Close()
	rows, err := parseImport(file)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_INVALID", err.Error(), nil)
		return
	}
	valid, invalid := 0, 0
	for _, row := range rows {
		if len(row.Errors) == 0 {
			valid++
		} else {
			invalid++
		}
	}
	if r.URL.Query().Get("dry_run") == "true" {
		httpserver.Write(w, http.StatusOK, map[string]any{"rows": importPreview(rows), "total": len(rows), "valid": valid, "invalid": invalid}, "预检完成")
		return
	}
	if invalid > 0 {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_HAS_ERRORS", "导入文件存在错误，请先修正", map[string]any{"rows": importPreview(rows), "invalid": invalid})
		return
	}
	targets := make([]job.Target, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, job.Target{Label: row.Input.Email, Payload: row.Input})
	}
	session, _ := auth.SessionFromContext(r.Context())
	createdJob, _, err := h.jobs.Create(r.Context(), "token", "import", session.AdminID, r.URL.Path, "", job.RequestHash(rows), targets)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "IMPORT_JOB_FAILED", err.Error(), nil)
		return
	}
	h.log(r, "token_import", createdJob.ID, fmt.Sprintf("创建 %d 行 Token 导入任务", len(rows)))
	httpserver.Write(w, http.StatusAccepted, map[string]any{"job": createdJob}, "导入任务已创建")
}

func importPreview(rows []importRow) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]any{
			"line": row.Line, "name": row.Input.Name, "email": row.Input.Email, "status": row.Input.Status,
			"access_token_masked":  security.MaskSecret(row.Input.AccessToken),
			"session_token_masked": security.MaskSecret(row.Input.SessionToken), "errors": row.Errors,
		})
	}
	return result
}

func parseImport(reader io.Reader) ([]importRow, error) {
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	header, err := csvReader.Read()
	if err != nil {
		return nil, errors.New("CSV 文件为空")
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	want := []string{"name", "email", "access_token", "session_token", "access_expires_at", "status", "note"}
	indexes := map[string]int{}
	for index, value := range header {
		indexes[strings.TrimSpace(value)] = index
	}
	for _, column := range want {
		if _, found := indexes[column]; !found {
			return nil, fmt.Errorf("CSV 缺少列: %s", column)
		}
	}
	rows := make([]importRow, 0)
	seen := map[string]int{}
	for line := 2; ; line++ {
		values, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("第 %d 行无法解析: %w", line, err)
		}
		value := func(column string) string {
			index := indexes[column]
			if index >= len(values) {
				return ""
			}
			return strings.TrimSpace(values[index])
		}
		input := CreateInput{Name: value("name"), Email: value("email"), AccessToken: value("access_token"), SessionToken: value("session_token"), AccessExpiresAt: value("access_expires_at"), Status: value("status"), Note: value("note")}
		row := importRow{Line: line, Input: input, Errors: []string{}}
		if input.AccessToken == "" {
			row.Errors = append(row.Errors, "access_token 不能为空")
		}
		if first, exists := seen[input.AccessToken]; input.AccessToken != "" && exists {
			row.Errors = append(row.Errors, fmt.Sprintf("与第 %d 行 access_token 重复", first))
		} else if input.AccessToken != "" {
			seen[input.AccessToken] = line
		}
		if input.Status != "" && input.Status != "enabled" && input.Status != "disabled" {
			row.Errors = append(row.Errors, "status 只能是 enabled 或 disabled")
		}
		if err := validateLengths(input.Name, input.Email, input.AccessToken, input.SessionToken, input.Note); err != nil {
			row.Errors = append(row.Errors, err.Error())
		}
		rows = append(rows, row)
		if len(rows) > 10000 {
			return nil, errors.New("CSV 最多允许 10000 行")
		}
	}
	return rows, nil
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	var builder strings.Builder
	writer := csv.NewWriter(&builder)
	_ = writer.Write([]string{"id", "name", "email", "status", "check_state", "access_expires_at", "note", "created_at", "updated_at"})
	page := 1
	total := 0
	for {
		items, count, err := h.service.List(r.Context(), ListFilter{Page: page, PageSize: 100, Sort: "created_at", Order: "asc"})
		if err != nil {
			httpserver.Error(w, r, http.StatusInternalServerError, "TOKEN_EXPORT_FAILED", "导出失败", nil)
			return
		}
		for _, item := range items {
			_ = writer.Write([]string{item.ID, item.Name, item.Email, item.Status, item.CheckState, item.AccessExpiresAt, item.Note, strconv.FormatInt(item.CreatedAt, 10), strconv.FormatInt(item.UpdatedAt, 10)})
		}
		total = count
		if page*100 >= count {
			break
		}
		page++
	}
	writer.Flush()
	h.log(r, "token_export", "", fmt.Sprintf("导出 %d 个 Token 的元数据", total))
	httpserver.Write(w, http.StatusOK, map[string]any{"filename": fmt.Sprintf("tokens-%s.csv", time.Now().Format("20060102-150405")), "content": builder.String(), "count": total}, "导出成功")
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
