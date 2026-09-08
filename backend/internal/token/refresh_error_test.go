package token

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"account-hub/internal/platform/httpserver"
	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

func TestRefreshFailureIdentifiesUpstreamStage(t *testing.T) {
	for _, test := range []struct {
		name           string
		stage          string
		upstreamStatus int
		challenge      bool
		status         int
		code           string
		message        string
	}{
		{"session unauthorized", "session_refresh", 401, false, 422, "invalid_credentials", "使用 Session Token 获取 Access Token 失败"},
		{"session forbidden", "session_refresh", 403, false, 502, "upstream_forbidden", "使用 Session Token 获取 Access Token 失败"},
		{"session challenge", "session_refresh", 403, true, 502, "upstream_challenge", "使用 Session Token 获取 Access Token 失败"},
		{"profile unauthorized", "profile_check", 401, false, 422, "invalid_credentials", "已获取新 Access Token，但账号校验失败"},
		{"profile forbidden", "profile_check", 403, false, 502, "upstream_forbidden", "已获取新 Access Token，但账号校验失败"},
		{"profile challenge", "profile_check", 403, true, 502, "upstream_challenge", "已获取新 Access Token，但账号校验失败"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/auth/session" && test.stage == "profile_check" {
					_, _ = w.Write([]byte(`{"accessToken":"private-new-access","expires":"2030-01-01T00:00:00Z"}`))
					return
				}
				if test.challenge {
					w.Header().Set("Cf-Mitigated", "challenge")
				}
				w.WriteHeader(test.upstreamStatus)
				_, _ = w.Write([]byte("private-upstream-error"))
			}))
			defer server.Close()
			ctx := context.Background()
			db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			settingRepository := settings.NewRepository(db)
			if err := settingRepository.EnsureDefaults(ctx); err != nil {
				t.Fatal(err)
			}
			repository := NewRepository(db)
			upstreamClient := upstream.NewBrowserClient(settingRepository, time.Second, 0)
			defer upstreamClient.CloseIdleConnections()
			client := NewClientWithBases(upstreamClient, server.URL, server.URL)
			service := NewService(repository, client, settingRepository)
			created, err := service.Create(ctx, CreateInput{AccessToken: "private-old-access", SessionToken: "private-session"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tokens/"+created.ID+"/refresh", nil)
			request.SetPathValue("id", created.ID)
			response := httptest.NewRecorder()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler := NewHandler(service, nil, nil)
			httpserver.Middleware(logger, http.HandlerFunc(handler.Refresh)).ServeHTTP(response, request)
			var envelope struct {
				Code    int    `json:"code"`
				Success bool   `json:"success"`
				Msg     string `json:"msg"`
				Data    struct {
					ErrorCode string `json:"error_code"`
					RequestID string `json:"request_id"`
					Details   struct {
						Stage          string `json:"stage"`
						UpstreamStatus int    `json:"upstream_status"`
					} `json:"details"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || envelope.Code != test.status || envelope.Success || envelope.Data.ErrorCode != test.code {
				t.Fatalf("unexpected error response: %s", response.Body.String())
			}
			if envelope.Data.Details.Stage != test.stage || envelope.Data.Details.UpstreamStatus != test.upstreamStatus || envelope.Data.RequestID == "" {
				t.Fatalf("missing failure diagnostics: %s", response.Body.String())
			}
			if !strings.Contains(envelope.Msg, test.message) || strings.Contains(response.Body.String(), "private-") {
				t.Fatalf("unsafe or ambiguous failure message: %s", response.Body.String())
			}
			record, err := repository.Get(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if record.AccessToken != "private-old-access" || record.SessionToken != "private-session" || record.LastRefreshedAt != nil {
				t.Fatal("failed refresh overwrote stored credentials or marked the refresh successful")
			}
			attempts, err := service.Attempts(ctx, created.ID, 10)
			if err != nil || len(attempts) != 1 {
				t.Fatalf("attempts=%d err=%v", len(attempts), err)
			}
			if attempts[0].Status != "failed" || attempts[0].ErrorCode != test.code || !strings.Contains(attempts[0].ErrorMessage, test.message) {
				t.Fatalf("failure history lost the stage or cause: %+v", attempts[0])
			}
		})
	}
}
