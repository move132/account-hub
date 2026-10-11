package token

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/httpserver"
)

type ContentHandler struct {
	service *ContentService
	audit   *audit.Repository
}

// NewContentHandler is retained for existing callers; composition roots can
// explicitly assemble the application service with NewContentHandlerWithService.
func NewContentHandler(repository *Repository, client *Client, auditRepository *audit.Repository) *ContentHandler {
	return NewContentHandlerWithService(NewContentService(repository, client), auditRepository)
}

func NewContentHandlerWithService(service *ContentService, auditRepository *audit.Repository) *ContentHandler {
	return &ContentHandler{service: service, audit: auditRepository}
}

func (h *ContentHandler) Storage(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Storage(r.Context(), r.PathValue("id"))
	if err != nil {
		contentError(w, r, err, "获取存储用量失败", "INVALID_CONTENT")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"usage": result}, "获取存储用量成功")
}

func (h *ContentHandler) Files(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Files(r.Context(), r.PathValue("id"), strings.TrimSpace(r.URL.Query().Get("cursor")))
	if err != nil {
		contentError(w, r, err, "获取图片列表失败", "INVALID_CURSOR")
		return
	}
	httpserver.Write(w, http.StatusOK, result, "获取图片列表成功")
}

func (h *ContentHandler) DeleteFiles(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Files []DeleteLibraryFile `json:"files"`
	}
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	result, err := h.service.DeleteFiles(r.Context(), id, input.Files)
	if err != nil {
		contentError(w, r, err, "删除图片失败", "INVALID_LIBRARY_FILES")
		return
	}
	message := fmt.Sprintf("已删除 %d 张图片，%d 张未删除或待确认", result.DeletedCount, result.FailureCount)
	if h.audit != nil {
		session, _ := auth.SessionFromContext(r.Context())
		if err := h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "token_library_delete", TargetKind: "token", TargetID: id, Summary: message, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr}); err != nil {
			httpserver.Logger(r.Context()).Error("record content deletion audit", "token_id", id, "error", err)
		}
	}
	httpserver.Write(w, http.StatusOK, result, message)
}

func (h *ContentHandler) Conversations(w http.ResponseWriter, r *http.Request) {
	offset, err := contentQueryInt(r, "offset", 0, 0, 1000000)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "INVALID_PAGINATION", err.Error(), nil)
		return
	}
	limit, err := contentQueryInt(r, "limit", 30, 1, 100)
	if err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "INVALID_PAGINATION", err.Error(), nil)
		return
	}
	result, err := h.service.Conversations(r.Context(), r.PathValue("id"), offset, limit)
	if err != nil {
		contentError(w, r, err, "获取聊天记录失败", "INVALID_PAGINATION")
		return
	}
	httpserver.Write(w, http.StatusOK, result, "获取聊天记录成功")
}

func (h *ContentHandler) Conversation(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Conversation(r.Context(), r.PathValue("id"), r.PathValue("conversation_id"))
	if err != nil {
		contentError(w, r, err, "获取聊天内容失败", "INVALID_CONTENT")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"conversation": result}, "获取聊天内容成功")
}

func (h *ContentHandler) Image(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Image(r.Context(), r.PathValue("id"), r.PathValue("file_id"))
	if err != nil {
		contentError(w, r, err, "获取图片失败", "INVALID_CONTENT")
		return
	}
	httpserver.Write(w, http.StatusOK, map[string]any{"image": result}, "获取图片成功")
}

func contentError(w http.ResponseWriter, r *http.Request, err error, message, validationCode string) {
	if isMissingAccessToken(err) {
		httpserver.Error(w, r, http.StatusUnprocessableEntity, "MISSING_ACCESS_TOKEN", err.Error(), nil)
		return
	}
	if apperror.KindOf(err) == apperror.Validation {
		httpserver.Error(w, r, http.StatusBadRequest, validationCode, err.Error(), nil)
		return
	}
	handleUpstreamError(w, r, err, message)
}

func contentQueryInt(r *http.Request, key string, fallback, minimum, maximum int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("分页参数超出允许范围")
	}
	return value, nil
}
