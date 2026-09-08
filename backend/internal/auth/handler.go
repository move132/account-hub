package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/security"
)

const (
	sessionCookie = "account_session"
	csrfCookie    = "account_csrf"
)

type principalKey string

const sessionKey principalKey = "auth_session"

type attemptBucket struct {
	Count       int
	WindowStart time.Time
}

type Handler struct {
	service  *Service
	audit    *audit.Repository
	mu       sync.Mutex
	attempts map[string]attemptBucket
}

func NewHandler(service *Service, auditRepository *audit.Repository) *Handler {
	return &Handler{service: service, audit: auditRepository, attempts: make(map[string]attemptBucket)}
}

func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionKey).(Session)
	return session, ok
}

func (h *Handler) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			httpserver.Error(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录", nil)
			return
		}
		session, err := h.service.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			clearCookies(w)
			httpserver.Error(w, r, http.StatusUnauthorized, "SESSION_INVALID", "登录已失效", nil)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) RequireMutation(next http.Handler) http.Handler {
	return h.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := SessionFromContext(r.Context())
		csrf, err := r.Cookie(csrfCookie)
		if err != nil || !h.service.VerifyCSRF(session, valueOrEmpty(csrf), r.Header.Get("X-CSRF-Token")) {
			httpserver.Error(w, r, http.StatusForbidden, "CSRF_INVALID", "安全校验失败，请刷新页面后重试", nil)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	key := remoteHost(r.RemoteAddr) + "|" + body.Username
	if h.rateLimited(key) {
		_ = h.audit.Log(r.Context(), audit.Entry{Action: "login_rate_limited", TargetKind: "auth", Summary: "登录触发限流", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
		httpserver.Error(w, r, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "登录尝试过于频繁，请稍后重试", nil)
		return
	}
	sessionToken, csrfToken, session, err := h.service.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		h.recordFailure(key)
		_ = h.audit.Log(r.Context(), audit.Entry{Action: "login_failed", TargetKind: "auth", Summary: "管理员登录失败", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
		if errors.Is(err, ErrInvalidCredentials) {
			httpserver.Error(w, r, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "用户名或密码错误", nil)
			return
		}
		httpserver.Error(w, r, http.StatusInternalServerError, "LOGIN_FAILED", "登录失败", nil)
		return
	}
	h.clearFailures(key)
	setCookies(w, sessionToken, csrfToken, time.UnixMilli(session.ExpiresAt))
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "login", TargetKind: "auth", Summary: "管理员登录", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, map[string]any{
		"username": session.Username, "expires_at": session.ExpiresAt, "password_managed_by_env": h.service.PasswordManaged(),
	}, "登录成功")
}

func remoteHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	session, _ := SessionFromContext(r.Context())
	_ = h.service.Logout(r.Context(), session.IDHash)
	clearCookies(w)
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "logout", TargetKind: "auth", Summary: "管理员退出", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, nil, "退出成功")
}

func (h *Handler) CurrentSession(w http.ResponseWriter, r *http.Request) {
	session, _ := SessionFromContext(r.Context())
	httpserver.Write(w, http.StatusOK, map[string]any{
		"username": session.Username, "expires_at": session.ExpiresAt, "password_managed_by_env": h.service.PasswordManaged(),
	}, "查询成功")
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	session, _ := SessionFromContext(r.Context())
	if err := h.service.ChangePassword(r.Context(), session, body.CurrentPassword, body.NewPassword, body.ConfirmPassword); err != nil {
		switch {
		case errors.Is(err, ErrPasswordManaged):
			httpserver.Error(w, r, http.StatusConflict, "PASSWORD_MANAGED_BY_ENV", "管理员密码由环境变量管理", nil)
		case errors.Is(err, ErrInvalidCredentials):
			httpserver.Error(w, r, http.StatusUnauthorized, "CURRENT_PASSWORD_INVALID", "当前密码错误", nil)
		default:
			httpserver.Error(w, r, http.StatusBadRequest, "PASSWORD_CHANGE_FAILED", err.Error(), nil)
		}
		return
	}
	_ = h.audit.Log(r.Context(), audit.Entry{AdminID: session.AdminID, Action: "password_change", TargetKind: "auth", Summary: "修改管理员密码", RequestID: httpserver.RequestID(r.Context()), RemoteAddr: r.RemoteAddr})
	httpserver.Write(w, http.StatusOK, nil, "密码修改成功")
}

func valueOrEmpty(cookie *http.Cookie) string {
	if cookie == nil {
		return ""
	}
	return cookie.Value
}

func setCookies(w http.ResponseWriter, sessionToken, csrfToken string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessionToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrfToken, Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, Expires: expires})
}

func clearCookies(w http.ResponseWriter) {
	expired := time.Unix(0, 0)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expired, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: "", Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, Expires: expired, MaxAge: -1})
}

func (h *Handler) rateLimited(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	bucket := h.attempts[key]
	if time.Since(bucket.WindowStart) > 15*time.Minute {
		delete(h.attempts, key)
		return false
	}
	return bucket.Count >= 5
}

func (h *Handler) recordFailure(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	bucket := h.attempts[key]
	if bucket.WindowStart.IsZero() || time.Since(bucket.WindowStart) > 15*time.Minute {
		bucket = attemptBucket{WindowStart: time.Now()}
	}
	bucket.Count++
	h.attempts[key] = bucket
}

func (h *Handler) clearFailures(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.attempts, key)
}

func SessionHashFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return security.HashToken(cookie.Value)
}
