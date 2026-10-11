package httpserver

import (
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
const loggerKey contextKey = "logger"

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func Logger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := security.RandomID("req")
		if err != nil {
			requestID = "req_unavailable"
		}
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		ctx = context.WithValue(ctx, loggerKey, logger)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		started := time.Now()
		tracked := &responseWriter{ResponseWriter: w}
		var target http.ResponseWriter = tracked
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/") {
			w.Header().Set("Cache-Control", "no-store")
			target = &jsonErrorWriter{ResponseWriter: tracked, request: r}
		}
		defer func() {
			recovered := recover()
			committed := tracked.status != 0
			if recovered != nil {
				logger.Error("panic serving request", "request_id", requestID, "panic", recovered, "stack", string(debug.Stack()))
				if tracked.status == 0 {
					clearBodyHeaders(tracked.Header())
					Error(tracked, r, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误", nil)
				}
			}
			status := tracked.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("http request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr, "status", status, "bytes", tracked.bytes, "elapsed_ms", time.Since(started).Milliseconds())
			// A started stream cannot be replaced with a second JSON response. Let net/http abort it.
			if recovered != nil && committed {
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(target, r)
	})
}

// responseWriter forwards each write immediately and records the actual response status and size.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}
func (w *responseWriter) Flush() { _ = w.FlushError() }
func (w *responseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// jsonErrorWriter normalizes net/http's plain-text 404/405 errors without buffering successful JSON or attachments.
type jsonErrorWriter struct {
	http.ResponseWriter
	request  *http.Request
	wrote    bool
	replaced bool
}

func (w *jsonErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *jsonErrorWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wrote = true
	if status >= 400 && !strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "application/json") {
		w.replaced = true
		clearBodyHeaders(w.Header())
		code, message := "HTTP_ERROR", "请求失败"
		switch status {
		case http.StatusNotFound:
			code, message = "ROUTE_NOT_FOUND", "接口不存在"
		case http.StatusMethodNotAllowed:
			code, message = "METHOD_NOT_ALLOWED", "请求方法不允许"
		}
		Error(w.ResponseWriter, w.request, status, code, message, nil)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *jsonErrorWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.replaced {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}
func (w *jsonErrorWriter) Flush() { _ = w.FlushError() }
func (w *jsonErrorWriter) FlushError() error {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Headers describing an abandoned representation must not describe the replacement JSON error.
func clearBodyHeaders(header http.Header) {
	header.Del("Content-Length")
	header.Del("Content-Disposition")
	header.Del("Content-Encoding")
}
