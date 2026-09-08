package idempotency

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"account-hub/internal/auth"
	"account-hub/internal/platform/httpserver"
)

type Middleware struct{ db *sql.DB }

func New(db *sql.DB) *Middleware { return &Middleware{db: db} }

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 200 {
			httpserver.Error(w, r, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", "Idempotency-Key 长度不能超过 200", nil)
			return
		}
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			httpserver.Error(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录", nil)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			httpserver.Error(w, r, http.StatusBadRequest, "REQUEST_READ_FAILED", "无法读取请求", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hashValue := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"), body...))
		requestHash := hex.EncodeToString(hashValue[:])
		route := r.Pattern
		if route == "" {
			route = r.Method + " " + r.URL.Path
		}
		now := time.Now().UTC()
		_, _ = m.db.ExecContext(r.Context(), "DELETE FROM idempotency_keys WHERE expires_at <= ?", now.UnixMilli())
		_, insertErr := m.db.ExecContext(r.Context(), `INSERT INTO idempotency_keys
            (actor_id, route, key, request_hash, status_code, response_body, expires_at, created_at)
            VALUES(?, ?, ?, ?, 0, '', ?, ?)`, session.AdminID, route, key, requestHash, now.Add(24*time.Hour).UnixMilli(), now.UnixMilli())
		if insertErr != nil {
			var existingHash, responseBody string
			var status int
			err := m.db.QueryRowContext(r.Context(), `SELECT request_hash, status_code, response_body
                FROM idempotency_keys WHERE actor_id = ? AND route = ? AND key = ?`, session.AdminID, route, key).
				Scan(&existingHash, &status, &responseBody)
			if err != nil {
				httpserver.Error(w, r, http.StatusInternalServerError, "IDEMPOTENCY_READ_FAILED", "幂等记录读取失败", nil)
				return
			}
			if existingHash != requestHash {
				httpserver.Error(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key 已用于不同请求", nil)
				return
			}
			if status == 0 {
				httpserver.Error(w, r, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", "相同请求正在处理中", nil)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(responseBody))
			return
		}

		recorder := newRecorder()
		defer func() {
			if recovered := recover(); recovered != nil {
				_, _ = m.db.ExecContext(r.Context(), "DELETE FROM idempotency_keys WHERE actor_id = ? AND route = ? AND key = ?", session.AdminID, route, key)
				panic(recovered)
			}
		}()
		next.ServeHTTP(recorder, r)
		responseBody := recorder.body.String()
		if _, err := m.db.ExecContext(r.Context(), `UPDATE idempotency_keys SET status_code = ?, response_body = ?
            WHERE actor_id = ? AND route = ? AND key = ?`, recorder.status, responseBody, session.AdminID, route, key); err != nil {
			_, _ = m.db.ExecContext(r.Context(), "DELETE FROM idempotency_keys WHERE actor_id = ? AND route = ? AND key = ?", session.AdminID, route, key)
			httpserver.Error(w, r, http.StatusInternalServerError, "IDEMPOTENCY_STORE_FAILED", "幂等结果保存失败", nil)
			return
		}
		for name, values := range recorder.header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(recorder.status)
		_, _ = w.Write(recorder.body.Bytes())
	})
}

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newRecorder() *recorder                       { return &recorder{header: make(http.Header), status: http.StatusOK} }
func (r *recorder) Header() http.Header            { return r.header }
func (r *recorder) WriteHeader(status int)         { r.status = status }
func (r *recorder) Write(body []byte) (int, error) { return r.body.Write(body) }

var _ http.ResponseWriter = (*recorder)(nil)
