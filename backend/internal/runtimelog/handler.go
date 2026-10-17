package runtimelog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"account-hub/internal/auth"
	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/logging"
)

type SessionStore interface {
	Session(context.Context, string) (auth.Session, error)
}

type Handler struct {
	buffer            *logging.Buffer
	sessions          SessionStore
	heartbeatInterval time.Duration
	writeTimeout      time.Duration
}

func NewHandler(buffer *logging.Buffer, sessions SessionStore) *Handler {
	return &Handler{buffer: buffer, sessions: sessions, heartbeatInterval: 15 * time.Second, writeTimeout: 30 * time.Second}
}

func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	session, ok := auth.SessionFromContext(r.Context())
	if !ok {
		httpserver.Error(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录", nil)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil {
		httpserver.Error(w, r, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "暂时无法订阅运行日志", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("after")
	}
	ticker := time.NewTicker(h.heartbeatInterval)
	defer ticker.Stop()
	expiry := time.NewTimer(max(time.Until(time.UnixMilli(session.ExpiresAt)), 0))
	defer expiry.Stop()

	first := true
	for {
		if r.Context().Err() != nil {
			return
		}
		if time.Now().UnixMilli() >= session.ExpiresAt {
			h.writeEvent(w, controller, "auth-expired", "", nil)
			return
		}
		snapshot := h.buffer.Snapshot(cursor)
		if snapshot.Closed {
			return
		}
		if first || len(snapshot.Items) > 0 || snapshot.Reset || snapshot.Truncated {
			if !h.writeEvent(w, controller, "logs", snapshot.Cursor, snapshot) {
				return
			}
			cursor = snapshot.Cursor
			first = false
		}
		select {
		case <-r.Context().Done():
			return
		case <-expiry.C:
			h.writeEvent(w, controller, "auth-expired", "", nil)
			return
		case <-snapshot.Changed:
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			_, err := h.sessions.Session(ctx, session.IDHash)
			cancel()
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) || apperror.KindOf(err) == apperror.Unauthenticated {
					h.writeEvent(w, controller, "auth-expired", "", nil)
				}
				return
			}
			if err := controller.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil {
				return
			}
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

func (h *Handler) writeEvent(w http.ResponseWriter, controller *http.ResponseController, event, id string, value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if err := controller.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil {
		return false
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return false
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	return controller.Flush() == nil
}
