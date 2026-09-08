package dreamina

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

func TestClientFlows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/passport/web/email/login/":
			if !strings.Contains(r.URL.RawQuery, "aid=513641") {
				t.Error("missing aid")
			}
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "session-new", Expires: time.Now().Add(time.Hour)})
			_, _ = w.Write([]byte(`{"message":"success","data":{}}`))
		case "/commerce/v1/benefits/user_credit":
			if got := r.Header.Get("Store-Country-Code-Src"); got != "uid" {
				t.Errorf("Store-Country-Code-Src = %q", got)
			}
			if got := r.Header.Get("Sec-Fetch-Site"); got != "same-site" {
				t.Errorf("Sec-Fetch-Site = %q", got)
			}
			if got := r.Header.Get("User-Agent"); !strings.Contains(got, "Macintosh") || !strings.Contains(got, "Chrome/142") {
				t.Errorf("unexpected user agent %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ret":0,"data":{"credit":{"gift_credit":321,"total_credit":999}}}`))
		case "/commerce/v1/benefits/credit_receive":
			_, _ = w.Write([]byte(`{"ret":3001,"message":"already received","data":{"received":false}}`))
		case "/mweb/v1/get_asset_list":
			_, _ = w.Write([]byte(`{"data":{"asset_list":[{"id":"a1"}]}}`))
		default:
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
	client := NewClientWithBases(upstream.NewClient(settingRepository, time.Second, 0), server.URL, server.URL, server.URL, "Asia/Shanghai")
	login, err := client.Login(ctx, "user@example.com", "password")
	if err != nil || login.SessionID != "session-new" {
		t.Fatalf("Login: %+v err=%v", login, err)
	}
	credit, err := client.Check(ctx, "session-new")
	if err != nil || credit.Balance != "321" {
		t.Fatalf("Check: %+v err=%v", credit, err)
	}
	if _, err := client.ClaimCredit(ctx, "session-new"); err != nil {
		t.Fatalf("ClaimCredit: %v", err)
	}
	assets, err := client.Assets(ctx, "session-new", 0, 50)
	if err != nil || assets == nil {
		t.Fatalf("Assets: %#v err=%v", assets, err)
	}
}

func TestClientLoginUsesSessionCookieMaxAgeAsExpiresAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/passport/web/email/login/" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "session-new", MaxAge: 3600})
		_, _ = w.Write([]byte(`{"message":"success","data":{}}`))
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
	client := NewClientWithBases(upstream.NewClient(settingRepository, time.Second, 0), server.URL, server.URL, server.URL, "Asia/Shanghai")
	login, err := client.Login(ctx, "user@example.com", "password")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if login.SessionID != "session-new" {
		t.Fatalf("SessionID=%q", login.SessionID)
	}
	expiresAt, err := time.Parse(time.RFC3339, login.SessionExpiresAt)
	if err != nil {
		t.Fatalf("SessionExpiresAt=%q err=%v", login.SessionExpiresAt, err)
	}
	remaining := time.Until(expiresAt)
	if remaining < 59*time.Minute || remaining > 61*time.Minute {
		t.Fatalf("remaining=%s, want about 1h", remaining)
	}
}

func TestClientCheckInvalidJSONIncludesResponseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/commerce/v1/benefits/user_credit" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html>blocked</html>`))
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
	client := NewClientWithBases(upstream.NewClient(settingRepository, time.Second, 0), server.URL, server.URL, server.URL, "Asia/Shanghai")
	_, err = client.Check(ctx, "session-old")
	if err == nil {
		t.Fatal("expected error")
	}
	if upstream.Code(err) != upstream.InvalidResponse || !strings.Contains(err.Error(), "Content-Type: text/html") {
		t.Fatalf("unexpected error: %v code=%s", err, upstream.Code(err))
	}
}

func TestClientAssetsInvalidJSONIncludesResponseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mweb/v1/get_asset_list" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q", got)
		}
		if got := r.Header.Get("did"); got == "" {
			t.Error("missing did header")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html>blocked</html>`))
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
	client := NewClientWithBases(upstream.NewClient(settingRepository, time.Second, 0), server.URL, server.URL, server.URL, "Asia/Shanghai")
	_, err = client.Assets(ctx, "session-old", 0, 50)
	if err == nil {
		t.Fatal("expected error")
	}
	if upstream.Code(err) != upstream.InvalidResponse || !strings.Contains(err.Error(), "Content-Type: text/html") {
		t.Fatalf("unexpected error: %v code=%s", err, upstream.Code(err))
	}
}
