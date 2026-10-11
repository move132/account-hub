package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/principal"
)

// Middleware adapts HTTP to Store. Application commands that own local writes
// should use InTransaction or AttachResourceTx so replay is committed atomically.
type Middleware struct{ store *Store }

func New(db *sql.DB) *Middleware { return &Middleware{store: NewStore(db)} }
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := NormalizeKey(r.Header.Get("Idempotency-Key"))
		if err != nil {
			httpserver.Error(w, r, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", err.Error(), nil)
			return
		}
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		caller, ok := principal.FromContext(r.Context())
		if !ok {
			httpserver.Error(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先完成身份验证", nil)
			return
		}
		const limit = 10 << 20
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			httpserver.Error(w, r, http.StatusBadRequest, "REQUEST_READ_FAILED", "无法读取请求", nil)
			return
		}
		if len(body) > limit {
			httpserver.Error(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "请求内容过大", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hash := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"), body...))
		route := r.Pattern
		if route == "" {
			route = r.Method + " " + r.URL.Path
		}
		record, created, err := m.store.Reserve(r.Context(), Request{ActorID: caller.ActorID(), Route: route, Key: key, Hash: hex.EncodeToString(hash[:])})
		if err != nil {
			if errors.Is(err, ErrConflict) {
				httpserver.Error(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key 已用于不同请求", nil)
			} else {
				httpserver.Error(w, r, http.StatusInternalServerError, "IDEMPOTENCY_STORE_FAILED", "幂等记录读取失败", nil)
			}
			return
		}
		if !created {
			switch record.State {
			case "processing":
				httpserver.Error(w, r, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", "相同请求正在处理中", nil)
			case "completed":
				copyHeaders(w.Header(), record.Headers)
				if w.Header().Get("Content-Type") == "" {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
				}
				w.Header().Set("Idempotent-Replay", "true")
				w.WriteHeader(record.Status)
				_, _ = w.Write([]byte(record.Body))
			default:
				httpserver.Error(w, r, http.StatusConflict, "IDEMPOTENCY_OUTCOME_UNKNOWN", "先前请求的执行结果尚待确认，请检查资源状态", map[string]any{"resource_type": record.ResourceType, "resource_id": record.ResourceID})
			}
			return
		}
		recorder := newRecorder()
		r = r.WithContext(WithRecord(r.Context(), record))
		defer func() {
			if recovered := recover(); recovered != nil {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
				defer cancel()
				// A panic may follow a successful side effect. Keep its key so
				// retry cannot silently repeat the operation.
				_ = m.store.MarkUncertain(ctx, record)
				panic(recovered)
			}
		}()
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
		defer cancel()
		saveErr := m.store.Complete(ctx, record, Result{Status: recorder.status, Body: recorder.body.String(), Headers: recorder.header})
		if saveErr != nil || recorder.status >= 400 {
			// The application may already have committed both its mutation and a
			// success response. Prefer that durable result over a later reload or
			// rendering error; do not turn a completed command into a cached 500.
			saved, found, lookupErr := m.store.Lookup(ctx, record.Request)
			if lookupErr == nil && found && saved.State == "completed" && saved.Status >= 200 && saved.Status < 400 {
				recorder.status = saved.Status
				recorder.header = saved.Headers
				recorder.body.Reset()
				recorder.body.WriteString(saved.Body)
				saveErr = nil
			}
		}
		if saveErr != nil {
			_ = m.store.MarkUncertain(ctx, record)
			httpserver.Error(w, r, http.StatusInternalServerError, "IDEMPOTENCY_STORE_FAILED", "业务可能已完成，幂等结果保存失败，请检查资源状态", nil)
			return
		}

		copyHeaders(w.Header(), recorder.header)
		w.WriteHeader(recorder.status)
		_, _ = w.Write(recorder.body.Bytes())
	})
}
func copyHeaders(to, from http.Header) {
	for name, values := range from {
		to[name] = append([]string(nil), values...)
	}
}

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newRecorder() *recorder            { return &recorder{header: make(http.Header)} }
func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *recorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.body.Write(body)
}

var _ http.ResponseWriter = (*recorder)(nil)
