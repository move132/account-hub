package token

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/platform/httpserver"
)

type ContentHandler struct {
	repository *Repository
	client     *Client
	audit      *audit.Repository
}

func NewContentHandler(repository *Repository, client *Client, auditRepository *audit.Repository) *ContentHandler {
	return &ContentHandler{repository: repository, client: client, audit: auditRepository}
}

func (h *ContentHandler) account(w http.ResponseWriter, r *http.Request) (Record, bool) {
	record, err := h.repository.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		handleError(w, r, err, "查询账号失败")
		return Record{}, false
	}
	if strings.TrimSpace(record.AccessToken) == "" {
		httpserver.Error(w, r, http.StatusUnprocessableEntity, "MISSING_ACCESS_TOKEN", "该账号没有 Access Token", nil)
		return Record{}, false
	}
	return record, true
}

func (h *ContentHandler) Storage(w http.ResponseWriter, r *http.Request) {
	if record, ok := h.account(w, r); ok {
		result, err := h.client.StorageUsage(r.Context(), record.AccessToken)
		if err != nil {
			handleUpstreamError(w, r, err, "获取存储用量失败")
			return
		}
		httpserver.Write(w, http.StatusOK, map[string]any{"usage": result}, "获取存储用量成功")
	}
}

func (h *ContentHandler) Files(w http.ResponseWriter, r *http.Request) {
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if len(cursor) > 8192 {
		httpserver.Error(w, r, http.StatusBadRequest, "INVALID_CURSOR", "分页游标过长", nil)
		return
	}
	if record, ok := h.account(w, r); ok {
		result, err := h.client.LibraryFiles(r.Context(), record.AccessToken, cursor)
		if err != nil {
			handleUpstreamError(w, r, err, "获取图片列表失败")
			return
		}
		httpserver.Write(w, http.StatusOK, result, "获取图片列表成功")
	}
}

func (h *ContentHandler) DeleteFiles(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Files []DeleteLibraryFile `json:"files"`
	}
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	if err := validateLibraryDeletion(input.Files); err != nil {
		httpserver.Error(w, r, http.StatusBadRequest, "INVALID_LIBRARY_FILES", err.Error(), nil)
		return
	}
	if record, ok := h.account(w, r); ok {
		result, err := h.client.DeleteLibraryFiles(r.Context(), record.AccessToken, input.Files)
		if err != nil {
			handleUpstreamError(w, r, err, "删除图片失败")
			return
		}
		message := fmt.Sprintf("已删除 %d 张图片，%d 张未删除或待确认", result.DeletedCount, result.FailureCount)
		if h.audit != nil {
			session, _ := auth.SessionFromContext(r.Context())
			_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "token_library_delete", TargetKind: "token", TargetID: record.ID, Summary: message, RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
		}
		httpserver.Write(w, http.StatusOK, result, message)
	}
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
	if record, ok := h.account(w, r); ok {
		result, err := h.client.Conversations(r.Context(), record.AccessToken, offset, limit)
		if err != nil {
			handleUpstreamError(w, r, err, "获取聊天记录失败")
			return
		}
		httpserver.Write(w, http.StatusOK, result, "获取聊天记录成功")
	}
}

func (h *ContentHandler) Conversation(w http.ResponseWriter, r *http.Request) {
	if record, ok := h.account(w, r); ok {
		result, err := h.client.Conversation(r.Context(), record.AccessToken, r.PathValue("conversation_id"))
		if err != nil {
			handleUpstreamError(w, r, err, "获取聊天内容失败")
			return
		}
		httpserver.Write(w, http.StatusOK, map[string]any{"conversation": result}, "获取聊天内容成功")
	}
}

func (h *ContentHandler) Image(w http.ResponseWriter, r *http.Request) {
	if record, ok := h.account(w, r); ok {
		result, err := h.client.ContentImage(r.Context(), record.AccessToken, r.PathValue("file_id"))
		if err != nil {
			handleUpstreamError(w, r, err, "获取图片失败")
			return
		}
		httpserver.Write(w, http.StatusOK, map[string]any{"image": result}, "获取图片成功")
	}
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
