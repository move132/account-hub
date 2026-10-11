package apikey

import (
	"context"
	"net/http"
	"strings"

	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/principal"
)

type Authenticator interface {
	Authenticate(context.Context, string) (string, error)
}

type Middleware struct{ authenticator Authenticator }

func NewMiddleware(authenticator Authenticator) *Middleware {
	return &Middleware{authenticator: authenticator}
}

// Allow delegates requests without Authorization to the existing session/CSRF chain.
// Only explicitly registered routes use this adapter; invalid headers never fall back to cookies.
func (m *Middleware) Allow(next, sessionHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) == 0 {
			sessionHandler.ServeHTTP(w, r)
			return
		}
		if len(headers) != 1 {
			httpserver.Error(w, r, http.StatusUnauthorized, "API_KEY_INVALID", "请提供有效的 Bearer API Key", nil)
			return
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			httpserver.Error(w, r, http.StatusUnauthorized, "API_KEY_INVALID", "请提供有效的 Bearer API Key", nil)
			return
		}
		id, err := m.authenticator.Authenticate(r.Context(), parts[1])
		if err != nil {
			if apperror.KindOf(err) == apperror.Unauthenticated {
				httpserver.Error(w, r, http.StatusUnauthorized, "API_KEY_INVALID", "API Key 无效或已删除", nil)
			} else {
				httpserver.ReportError(w, r, err, "API_KEY_AUTH_UNAVAILABLE", "暂时无法验证 API Key")
			}
			return
		}
		ctx := principal.WithContext(r.Context(), principal.Identity{Kind: principal.APIKey, ID: id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
