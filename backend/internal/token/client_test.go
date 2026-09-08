package token

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

func TestClientCheckAndRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/me":
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Errorf("authorization=%q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"id":"user_1","email":"user@example.com"}`))
		case "/api/auth/session":
			http.SetCookie(w, &http.Cookie{Name: "__Secure-next-auth.session-token.0", Value: "new-"})
			http.SetCookie(w, &http.Cookie{Name: "__Secure-next-auth.session-token.1", Value: "session"})
			_, _ = w.Write([]byte(`{"accessToken":"access-secret","expires":"2030-01-01T00:00:00Z"}`))
		default:
			t.Errorf("unexpected extra upstream request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
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
	upstreamClient := upstream.NewBrowserClient(settingRepository, time.Second, 0)
	defer upstreamClient.CloseIdleConnections()
	client := NewClientWithBases(upstreamClient, server.URL, server.URL)
	profile, err := client.Check(ctx, "access-secret")
	if err != nil || profile.Email != "user@example.com" || profile.UserID != "user_1" {
		t.Fatalf("Check: %+v err=%v", profile, err)
	}
	refreshed, err := client.Refresh(ctx, "old-session")
	if err != nil || refreshed.AccessToken != "access-secret" || refreshed.SessionToken != "new-session" {
		t.Fatalf("Refresh: %+v err=%v", refreshed, err)
	}
}

func TestClientRejectsMalformedProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"unexpected":true}`)) }))
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
	upstreamClient := upstream.NewBrowserClient(settingRepository, time.Second, 0)
	defer upstreamClient.CloseIdleConnections()
	client := NewClientWithBases(upstreamClient, server.URL, server.URL)
	if _, err := client.Check(ctx, "access-secret"); upstream.Code(err) != upstream.InvalidResponse {
		t.Fatalf("code=%s err=%v", upstream.Code(err), err)
	}
}
