package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestOverviewCountsMatchOriginalLists(t *testing.T) {
	ctx := context.Background()
	application, err := New(ctx, Config{DatabasePath: filepath.Join(t.TempDir(), "overview.db"), AdminUsername: "admin", AdminSessionTTL: time.Hour,
		UpstreamRequestTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(ctx)
	var cookies []*http.Cookie
	call := func(method, path string, body []byte) (*httptest.ResponseRecorder, envelope) {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
			if cookie.Name == "account_csrf" {
				request.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
		recorder := httptest.NewRecorder()
		application.Handler().ServeHTTP(recorder, request)
		var result envelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return recorder, result
	}
	if recorder, _ := call(http.MethodGet, "/api/v1/overview", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated overview: %d", recorder.Code)
	}
	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": application.initialAdminPassword})
	recorder, _ := call(http.MethodPost, "/api/v1/auth/login", loginBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d", recorder.Code)
	}
	cookies = recorder.Result().Cookies()
	assertCounts := func(wantTokens, wantDreamina [3]int) {
		t.Helper()
		recorder, result := call(http.MethodGet, "/api/v1/overview", nil)
		if recorder.Code != http.StatusOK || len(result.Data) != 3 || result.Data["as_of"].(float64) <= 0 {
			t.Fatalf("overview failed: %d %v", recorder.Code, result.Data)
		}
		for _, provider := range []struct {
			key  string
			want [3]int
		}{{"tokens", wantTokens}, {"dreamina", wantDreamina}} {
			counts, ok := result.Data[provider.key].(map[string]any)
			if !ok || len(counts) != 3 {
				t.Fatalf("%s must contain exactly three counts: %v", provider.key, counts)
			}
			for index, key := range []string{"total", "enabled", "disabled"} {
				if counts[key] != float64(provider.want[index]) {
					t.Fatalf("%s.%s = %v, want %d", provider.key, key, counts[key], provider.want[index])
				}
			}
		}
	}
	assertCounts([3]int{}, [3]int{})
	now := time.Now().UTC().UnixMilli()
	for _, fixture := range []struct {
		provider          string
		enabled, disabled int
	}{{"tokens", 41, 19}, {"dreamina", 12, 8}} {
		for index := 0; index < fixture.enabled+fixture.disabled; index++ {
			id := fmt.Sprintf("%s-%02d", fixture.provider, index)
			status := "enabled"
			if index >= fixture.enabled {
				status = "disabled"
			}
			if fixture.provider == "tokens" {
				_, err = application.DB().Exec(`INSERT INTO tokens(id,email,access_token,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, "same@example.com", "dummy-access-"+id, status, now, now)
			} else {
				_, err = application.DB().Exec(`INSERT INTO dreamina_accounts(id,email,password,session_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, "same@example.com", "dummy-password", "dummy-session-"+id, status, now, now)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		endpoint := "/api/v1/tokens"
		if fixture.provider == "dreamina" {
			endpoint = "/api/v1/dreamina/accounts"
		}
		for _, scope := range []struct {
			status string
			count  int
		}{{"", fixture.enabled + fixture.disabled}, {"enabled", fixture.enabled}, {"disabled", fixture.disabled}} {
			recorder, result := call(http.MethodGet, endpoint+"?page_size=1&status="+scope.status, nil)
			if recorder.Code != http.StatusOK || result.Data["total"] != float64(scope.count) || len(result.Data["items"].([]any)) != 1 {
				t.Fatalf("original list %s: status=%d data=%v", endpoint, recorder.Code, result.Data)
			}
		}
	}
	assertCounts([3]int{60, 41, 19}, [3]int{20, 12, 8})
	recorder, result := call(http.MethodPatch, "/api/v1/tokens/tokens-00", []byte(`{"version":1,"status":"disabled"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("original status update: %d %v", recorder.Code, result.Data)
	}
	assertCounts([3]int{60, 40, 20}, [3]int{20, 12, 8})
	recorder, result = call(http.MethodDelete, "/api/v1/tokens/tokens-00", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("original delete: %d %v", recorder.Code, result.Data)
	}
	assertCounts([3]int{59, 40, 19}, [3]int{20, 12, 8})
}
