package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSettingsAndAccountSchedulesCommitTogether(t *testing.T) {
	a := lifecycleApplication(t)
	// New intentionally leaves workers stopped: this test never contacts a provider.
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	response := login(t, server.URL, a.initialAdminPassword)
	var session, csrf *http.Cookie
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "account_session":
			session = cookie
		case "account_csrf":
			csrf = cookie
		}
	}
	if !decodeEnvelope(t, response).Success || session == nil || csrf == nil {
		t.Fatal("login failed")
	}
	if _, err := a.db.Exec(`INSERT INTO tokens(id,access_token,last_refreshed_at,next_refresh_at,created_at,updated_at)
        VALUES('token','test-access',1000,42,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO dreamina_accounts(id,email,password,last_refreshed_at,next_refresh_at,created_at,updated_at)
        VALUES('dreamina','test@example.com','test-password',2000,43,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER fail_schedule BEFORE UPDATE OF next_refresh_at ON dreamina_accounts
        BEGIN SELECT RAISE(ABORT,'injected schedule persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"settings":{"token_auto_refresh_enabled":true,"token_refresh_interval_hours":1,"dreamina_auto_refresh_enabled":true,"dreamina_refresh_interval_days":2}}`)
	response = authenticatedRequest(t, server.URL+"/api/v1/settings", http.MethodPatch, body, session, csrf, "")
	failed := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusInternalServerError || failed.Success {
		t.Fatalf("expected persistence failure: %d %+v", response.StatusCode, failed)
	}
	var enabled, interval string
	if err := a.db.QueryRow("SELECT value FROM settings WHERE key='token_auto_refresh_enabled'").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("SELECT value FROM settings WHERE key='token_refresh_interval_hours'").Scan(&interval); err != nil {
		t.Fatal(err)
	}
	var tokenNext, dreaminaNext int64
	readSchedules := func() {
		t.Helper()
		if err := a.db.QueryRow("SELECT next_refresh_at FROM tokens WHERE id='token'").Scan(&tokenNext); err != nil {
			t.Fatal(err)
		}
		if err := a.db.QueryRow("SELECT next_refresh_at FROM dreamina_accounts WHERE id='dreamina'").Scan(&dreaminaNext); err != nil {
			t.Fatal(err)
		}
	}
	readSchedules()
	if enabled != "false" || interval != "168" || tokenNext != 42 || dreaminaNext != 43 {
		t.Fatalf("partial commit: enabled=%s interval=%s token=%d dreamina=%d", enabled, interval, tokenNext, dreaminaNext)
	}
	if _, err := a.db.Exec("DROP TRIGGER fail_schedule"); err != nil {
		t.Fatal(err)
	}
	response = authenticatedRequest(t, server.URL+"/api/v1/settings", http.MethodPatch, body, session, csrf, "")
	applied := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusOK || !applied.Success {
		t.Fatalf("settings failed: %+v", applied)
	}
	readSchedules()
	if tokenNext != 1000+time.Hour.Milliseconds() || dreaminaNext != 2000+(48*time.Hour).Milliseconds() {
		t.Fatalf("schedules did not reflect committed policy: token=%d dreamina=%d", tokenNext, dreaminaNext)
	}
}
