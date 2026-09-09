package httpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"account-hub/internal/platform/security"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := security.RandomID("req")
		if err != nil {
			requestID = "req_unavailable"
		}
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		started := time.Now()
		var guard *jsonGuard
		var target http.ResponseWriter = w
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/") {
			w.Header().Set("Cache-Control", "no-store")
			guard = newJSONGuard()
			target = guard
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic serving request", "request_id", requestID, "panic", recovered, "stack", string(debug.Stack()))
				Error(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误", nil)
			} else if guard != nil {
				guard.flush(w, r)
			}
			logger.Info("http request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr, "elapsed_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(target, r)
	})
}

type jsonGuard struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func newJSONGuard() *jsonGuard           { return &jsonGuard{header: make(http.Header), status: http.StatusOK} }
func (g *jsonGuard) Header() http.Header { return g.header }
func (g *jsonGuard) WriteHeader(status int) {
	if g.wrote {
		return
	}
	g.wrote = true
	g.status = status
}
func (g *jsonGuard) Write(body []byte) (int, error) {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	return g.body.Write(body)
}

func (g *jsonGuard) flush(w http.ResponseWriter, r *http.Request) {
	contentType := strings.ToLower(g.header.Get("Content-Type"))
	isJSON := strings.Contains(contentType, "application/json")
	isAttachment := g.status >= http.StatusOK && g.status < http.StatusMultipleChoices &&
		strings.HasPrefix(strings.ToLower(g.header.Get("Content-Disposition")), "attachment;")
	if !isJSON && !isAttachment {
		code, message := "HTTP_ERROR", "请求失败"
		switch g.status {
		case http.StatusNotFound:
			code, message = "ROUTE_NOT_FOUND", "接口不存在"
		case http.StatusMethodNotAllowed:
			code, message = "METHOD_NOT_ALLOWED", "请求方法不允许"
		}
		Error(w, r, g.status, code, message, nil)
		return
	}
	for name, values := range g.header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(g.status)
	_, _ = w.Write(g.body.Bytes())
}

var _ http.ResponseWriter = (*jsonGuard)(nil)
