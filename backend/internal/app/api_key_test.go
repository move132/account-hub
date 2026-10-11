package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"account-hub/internal/platform/security"
)

type apiKeyFixture struct {
	t       *testing.T
	app     *Application
	cookies []*http.Cookie
	logs    *bytes.Buffer
}

func newAPIKeyFixture(t *testing.T) *apiKeyFixture {
	t.Helper()
	logs := &bytes.Buffer{}
	application, err := New(context.Background(), Config{
		DatabasePath: filepath.Join(t.TempDir(), "api-keys.db"), AdminUsername: "admin",
		AdminSessionTTL: time.Hour, DisplayTimezone: DisplayTimezone,
		JobScanInterval: time.Hour, UpstreamRequestTimeout: time.Second,
	}, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Shutdown(context.Background()) })
	f := &apiKeyFixture{t: t, app: application, logs: logs}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": application.initialAdminPassword})
	response, _ := f.request("POST", "/api/v1/auth/login", string(body), "", false, false, "")
	if response.Code != http.StatusOK {
		t.Fatalf("login: %s", response.Body)
	}
	f.cookies = response.Result().Cookies()
	return f
}

func (f *apiKeyFixture) request(method, path, body, authorization string, session, csrf bool, replayKey string) (*httptest.ResponseRecorder, envelope) {
	f.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if replayKey != "" {
		request.Header.Set("Idempotency-Key", replayKey)
	}
	if session {
		for _, cookie := range f.cookies {
			request.AddCookie(cookie)
			if csrf && cookie.Name == "account_csrf" {
				request.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
	}
	response := httptest.NewRecorder()
	f.app.Handler().ServeHTTP(response, request)
	return response, decodeEnvelope(f.t, response.Result())
}

func (f *apiKeyFixture) createKey(name string) (string, string) {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"api_key_action": map[string]string{"action": "create", "name": name}})
	response, data := f.request("PATCH", "/api/v1/settings", string(body), "", true, true, "key-creation-is-not-cached")
	if response.Code != http.StatusCreated {
		f.t.Fatalf("create key: %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("creation response must not be cached")
	}
	key := data.Data["api_key"].(map[string]any)
	return key["id"].(string), data.Data["secret"].(string)
}

func TestAPIKeySettingsAndExistingTokenLifecycle(t *testing.T) {
	f := newAPIKeyFixture(t)
	id, secret := f.createKey("  同步脚本  ")
	if len(secret) != 47 || !strings.HasPrefix(secret, "ahk_") {
		t.Fatal("unexpected key format")
	}
	var storedHash, storedName string
	if err := f.app.DB().QueryRow(`SELECT key_hash,name FROM api_keys WHERE id=?`, id).Scan(&storedHash, &storedName); err != nil {
		t.Fatal(err)
	}
	if storedHash != security.HashToken(secret) || storedName != "同步脚本" {
		t.Fatal("key hash or normalized name was not stored correctly")
	}
	response, settings := f.request("GET", "/api/v1/settings", "", "", true, false, "")
	if response.Code != 200 || len(settings.Data["settings"].([]any)) != 10 || len(settings.Data["api_keys"].([]any)) != 1 {
		t.Fatalf("settings compatibility: %s", response.Body)
	}
	if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), storedHash) {
		t.Fatal("settings exposed key credentials")
	}
	var count int
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_keys`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("key generation entered the response cache: count=%d err=%v", count, err)
	}
	keyAuth := "Bearer " + secret
	createBody := `{"name":"API account","access_token":"business-secret"}`
	response, created := f.request("POST", "/api/v1/tokens", createBody, keyAuth, false, false, "create-account")
	if response.Code != 201 {
		t.Fatalf("key-only create: %s", response.Body)
	}
	item := created.Data["token"].(map[string]any)
	tokenID := item["id"].(string)
	response, replay := f.request("POST", "/api/v1/tokens", createBody, keyAuth, false, false, "create-account")
	if response.Code != 201 || response.Header().Get("Idempotent-Replay") != "true" || replay.Data["token"].(map[string]any)["id"] != tokenID {
		t.Fatalf("key retry did not replay: %s", response.Body)
	}
	response, listed := f.request("GET", "/api/v1/tokens", "", keyAuth, false, false, "")
	if response.Code != 200 || listed.Data["total"] != float64(1) || strings.Contains(response.Body.String(), "business-secret") {
		t.Fatalf("key-only list: %s", response.Body)
	}
	response, detail := f.request("GET", "/api/v1/tokens/"+tokenID, "", keyAuth, false, false, "")
	if response.Code != 200 || detail.Data["token"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("key-only detail: %s", response.Body)
	}
	response, updated := f.request("PATCH", "/api/v1/tokens/"+tokenID, `{"name":"updated","version":1}`, keyAuth, false, false, "")
	if response.Code != 200 || updated.Data["token"].(map[string]any)["name"] != "updated" {
		t.Fatalf("key-only update: %s", response.Body)
	}
	response, conflict := f.request("PATCH", "/api/v1/tokens/"+tokenID, `{"name":"stale","version":1}`, keyAuth, false, false, "")
	if response.Code != 409 || conflict.Data["error_code"] != "VERSION_CONFLICT" {
		t.Fatalf("version conflict lost: %s", response.Body)
	}
	response, _ = f.request("DELETE", "/api/v1/tokens/"+tokenID, "", keyAuth, false, false, "")
	if response.Code != 200 {
		t.Fatalf("key-only delete: %s", response.Body)
	}
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE api_key_id=? AND admin_id IS NULL AND action IN ('token_create','token_update','token_delete')`, id).Scan(&count); err != nil || count != 3 {
		t.Fatalf("API key attribution: count=%d err=%v", count, err)
	}
	response, auditEntries := f.request("GET", "/api/v1/audit-logs", "", "", true, false, "")
	if response.Code != 200 || len(auditEntries.Data["items"].([]any)) < 4 || !strings.Contains(response.Body.String(), `"api_key_id":"`+id+`"`) {
		t.Fatalf("audit response omitted API key: %s", response.Body)
	}
	response, _ = f.request("PATCH", "/api/v1/settings", fmt.Sprintf(`{"api_key_action":{"action":"delete","id":%q}}`, id), "", true, true, "")
	if response.Code != 200 {
		t.Fatalf("delete key: %s", response.Body)
	}
	response, _ = f.request("POST", "/api/v1/tokens", createBody, keyAuth, false, false, "create-account")
	if response.Code != 401 || response.Header().Get("Idempotent-Replay") != "" {
		t.Fatalf("deleted key could replay a request: %s", response.Body)
	}
	_, settings = f.request("GET", "/api/v1/settings", "", "", true, false, "")
	if len(settings.Data["api_keys"].([]any)) != 0 {
		t.Fatal("deleted key remains in active list")
	}
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE api_key_id=?`, id).Scan(&count); err != nil || count != 3 {
		t.Fatalf("key deletion removed audit attribution: count=%d err=%v", count, err)
	}
	if strings.Contains(f.logs.String(), secret) {
		t.Fatal("API key appeared in logs")
	}
}

func TestAPIKeyAuthenticationBoundariesAndCSRF(t *testing.T) {
	f := newAPIKeyFixture(t)
	_, secret := f.createKey("boundary")
	keyAuth := "Bearer " + secret
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/v1/settings"}, {"PATCH", "/api/v1/settings"},
		{"GET", "/api/v1/auth/session"}, {"PATCH", "/api/v1/auth/password"},
		{"GET", "/api/v1/audit-logs"}, {"GET", "/api/v1/jobs"},
		{"GET", "/api/v1/dreamina/accounts"}, {"GET", "/api/v1/tokens/missing/edit"},
		{"GET", "/api/v1/tokens/missing/storage"}, {"POST", "/api/v1/token-exports"},
		{"POST", "/api/v1/token-imports"}, {"POST", "/api/v1/token-batch-jobs"},
	} {
		response, _ := f.request(endpoint.method, endpoint.path, `{}`, keyAuth, false, false, "")
		if response.Code != 401 {
			t.Fatalf("key accessed admin route %s %s: %d", endpoint.method, endpoint.path, response.Code)
		}
	}
	for _, header := range []string{"Bearer invalid", "Basic " + secret, "Bearer", "Bearer " + secret + " extra"} {
		response, _ := f.request("POST", "/api/v1/tokens", `{"access_token":"should-not-create"}`, header, true, true, "")
		if response.Code != 401 {
			t.Fatalf("invalid Authorization fell back to valid cookie: %d", response.Code)
		}
	}
	for _, headers := range [][]string{{""}, {keyAuth, keyAuth}} {
		request := httptest.NewRequest("GET", "/api/v1/tokens", nil)
		for _, header := range headers {
			request.Header.Add("Authorization", header)
		}
		for _, cookie := range f.cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		f.app.Handler().ServeHTTP(response, request)
		if response.Code != 401 {
			t.Fatalf("ambiguous authorization accepted: %d", response.Code)
		}
	}
	for _, endpoint := range []struct{ method, path, body string }{
		{"POST", "/api/v1/tokens", `{"access_token":"csrf-create"}`},
		{"PATCH", "/api/v1/tokens/missing", `{"name":"csrf-update","version":1}`},
		{"DELETE", "/api/v1/tokens/missing", `{}`},
		{"POST", "/api/v1/tokens/missing/checks", `{}`},
		{"POST", "/api/v1/tokens/missing/refreshes", `{}`},
		{"PATCH", "/api/v1/settings", `{"api_key_action":{"action":"create","name":"csrf"}}`},
	} {
		response, _ := f.request(endpoint.method, endpoint.path, endpoint.body, "", true, false, "")
		if response.Code != 403 {
			t.Fatalf("cookie mutation bypassed CSRF on %s: %d", endpoint.path, response.Code)
		}
	}
	for _, operation := range []string{"checks", "refreshes"} {
		response, _ := f.request("POST", "/api/v1/tokens/missing/"+operation, `{}`, keyAuth, false, false, "")
		if response.Code != 404 {
			t.Fatalf("valid key did not reach %s handler: %d %s", operation, response.Code, response.Body)
		}
	}
	response, _ := f.request("POST", "/api/v1/tokens", `{"access_token":"cookie-still-works"}`, "", true, true, "")
	if response.Code != 201 {
		t.Fatalf("cookie flow regressed: %s", response.Body)
	}
}

func TestAPIKeyIdempotencyIsIsolatedPerKeyAndAdministrator(t *testing.T) {
	f := newAPIKeyFixture(t)
	_, first := f.createKey("first")
	_, second := f.createKey("second")
	for index, secret := range []string{first, second, ""} {
		header := ""
		if secret != "" {
			header = "Bearer " + secret
		}
		response, _ := f.request("POST", "/api/v1/tokens", fmt.Sprintf(`{"access_token":"isolated-%d"}`, index), header, secret == "", secret == "", "same-request-key")
		if response.Code != 201 || response.Header().Get("Idempotent-Replay") != "" {
			t.Fatalf("actor namespace collision: %s", response.Body)
		}
	}
	response, _ := f.request("POST", "/api/v1/tokens", `{"access_token":"different-body"}`, "Bearer "+first, false, false, "same-request-key")
	if response.Code != 409 {
		t.Fatalf("same key did not detect different request: %s", response.Body)
	}
	var count int
	if err := f.app.DB().QueryRow(`SELECT COUNT(DISTINCT actor_id) FROM idempotency_keys WHERE key='same-request-key'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("actor namespaces: count=%d err=%v", count, err)
	}
}

func TestAPIKeySettingsValidationAndAuditAtomicity(t *testing.T) {
	f := newAPIKeyFixture(t)
	for _, body := range []string{
		`{"api_key_action":{"action":"create","name":"  "}}`,
		`{"api_key_action":{"action":"create","name":"valid","id":"unexpected"}}`,
		`{"api_key_action":{"action":"delete","id":""}}`,
		`{"api_key_action":{"action":"enable","id":"unexpected"}}`,
		`{"api_key_action":{"action":"create","name":"valid","unknown":true}}`,
		`{"settings":{"token_refresh_interval_hours":24},"api_key_action":{"action":"create","name":"mixed"}}`,
	} {
		response, _ := f.request("PATCH", "/api/v1/settings", body, "", true, true, "")
		if response.Code != 400 {
			t.Fatalf("invalid key command accepted: %s", response.Body)
		}
	}
	if _, err := f.app.DB().Exec(`CREATE TRIGGER reject_key_audit BEFORE INSERT ON audit_logs WHEN NEW.action IN ('api_key_create','api_key_delete') BEGIN SELECT RAISE(FAIL,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	response, _ := f.request("PATCH", "/api/v1/settings", `{"api_key_action":{"action":"create","name":"rollback"}}`, "", true, true, "")
	var count int
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&count); err != nil || count != 0 || response.Code != 500 || strings.Contains(response.Body.String(), "audit unavailable") {
		t.Fatalf("failed creation was not rolled back: status=%d count=%d err=%v", response.Code, count, err)
	}
	if _, err := f.app.DB().Exec(`DROP TRIGGER reject_key_audit`); err != nil {
		t.Fatal(err)
	}
	id, secret := f.createKey("retain-on-fail")
	if _, err := f.app.DB().Exec(`CREATE TRIGGER reject_key_delete_audit BEFORE INSERT ON audit_logs WHEN NEW.action='api_key_delete' BEGIN SELECT RAISE(FAIL,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	response, _ = f.request("PATCH", "/api/v1/settings", fmt.Sprintf(`{"api_key_action":{"action":"delete","id":%q}}`, id), "", true, true, "")
	if response.Code != 500 {
		t.Fatalf("failed delete: %s", response.Body)
	}
	response, _ = f.request("GET", "/api/v1/tokens", "", "Bearer "+secret, false, false, "")
	if response.Code != 200 {
		t.Fatal("failed audited deletion still revoked the key")
	}
}

func TestAPIKeyPersistsAcrossRestartAndAdminLogout(t *testing.T) {
	f := newAPIKeyFixture(t)
	_, secret := f.createKey("persistent")
	config := f.app.config
	if _, err := os.Stat(config.DatabasePath + ".api-key.key"); !os.IsNotExist(err) {
		t.Fatalf("creating an API Key must not create a key file: %v", err)
	}
	if err := f.app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), config, slog.New(slog.NewJSONHandler(f.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	f.app = restarted
	if _, err := os.Stat(config.DatabasePath + ".api-key.key"); !os.IsNotExist(err) {
		t.Fatalf("restarting must not create a key file: %v", err)
	}
	response, _ := f.request("POST", "/api/v1/auth/logout", `{}`, "", true, true, "")
	if response.Code != 200 {
		t.Fatalf("logout: %s", response.Body)
	}
	response, _ = f.request("GET", "/api/v1/tokens", "", "Bearer "+secret, false, false, "")
	if response.Code != 200 {
		t.Fatalf("restart or admin logout invalidated independent key: %s", response.Body)
	}
}

func TestAPIKeyLastUsedAtTracksAuthenticatedRequests(t *testing.T) {
	f := newAPIKeyFixture(t)
	id, secret := f.createKey("used")
	otherID, _ := f.createKey("unused")
	usage := func() map[string]any {
		t.Helper()
		response, data := f.request("GET", "/api/v1/settings", "", "", true, false, "")
		if response.Code != 200 {
			t.Fatalf("read usage: %s", response.Body)
		}
		result := make(map[string]any)
		for _, value := range data.Data["api_keys"].([]any) {
			item := value.(map[string]any)
			lastUsed, exists := item["last_used_at"]
			if !exists {
				t.Fatal("key view omitted last_used_at")
			}
			result[item["id"].(string)] = lastUsed
		}
		return result
	}
	if times := usage(); times[id] != nil || times[otherID] != nil {
		t.Fatal("new keys should not have a usage timestamp")
	}
	// Viewing settings, Cookie requests, and rejected credentials are not API key use.
	f.request("GET", "/api/v1/tokens", "", "", true, false, "")
	f.request("GET", "/api/v1/tokens", "", "Bearer ahk_"+strings.Repeat("A", 43), false, false, "")
	f.request("GET", "/api/v1/settings", "", "Bearer "+secret, false, false, "")
	if times := usage(); times[id] != nil || times[otherID] != nil {
		t.Fatal("unauthenticated or session requests updated key usage")
	}
	before := time.Now().UTC().UnixMilli()
	response, _ := f.request("GET", "/api/v1/tokens", "", "Bearer "+secret, false, false, "")
	after := time.Now().UTC().UnixMilli()
	if response.Code != 200 {
		t.Fatalf("key request: %s", response.Body)
	}
	times := usage()
	lastUsed, ok := times[id].(float64)
	if !ok || int64(lastUsed) < before || int64(lastUsed) > after || times[otherID] != nil {
		t.Fatalf("incorrect usage timestamps: %v", times)
	}
	if usage()[id] != lastUsed {
		t.Fatal("reading settings changed the usage timestamp")
	}
	// A later authenticated request counts even when the requested resource is missing.
	if _, err := f.app.DB().Exec(`UPDATE api_keys SET last_used_at=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	response, _ = f.request("GET", "/api/v1/tokens/missing", "", "Bearer "+secret, false, false, "")
	times = usage()
	lastUsed, ok = times[id].(float64)
	if response.Code != 404 || !ok || int64(lastUsed) < before || times[otherID] != nil {
		t.Fatalf("authenticated business error did not record use: status=%d times=%v", response.Code, times)
	}
	response, _ = f.request("PATCH", "/api/v1/settings", fmt.Sprintf(`{"api_key_action":{"action":"delete","id":%q}}`, id), "", true, true, "")
	if response.Code != 200 {
		t.Fatalf("delete key: %s", response.Body)
	}
	response, _ = f.request("GET", "/api/v1/tokens", "", "Bearer "+secret, false, false, "")
	var stored int64
	if err := f.app.DB().QueryRow(`SELECT last_used_at FROM api_keys WHERE id=?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if response.Code != 401 || stored != int64(lastUsed) {
		t.Fatal("a deleted key changed its usage timestamp")
	}
}

func TestAPIKeySecretIsOnlyReturnedOnCreation(t *testing.T) {
	f := newAPIKeyFixture(t)
	id, secret := f.createKey("show once")
	var columns int
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('api_keys') WHERE name IN ('secret', 'secret_ciphertext')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("database retained full Key storage: columns=%d err=%v", columns, err)
	}
	var hash string
	if err := f.app.DB().QueryRow(`SELECT key_hash FROM api_keys WHERE id=?`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != security.HashToken(secret) {
		t.Fatal("Key verification hash was not stored correctly")
	}
	response, listing := f.request("GET", "/api/v1/settings", "", "", true, false, "")
	view := listing.Data["api_keys"].([]any)[0].(map[string]any)
	if _, exists := view["can_copy"]; exists || strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), hash) {
		t.Fatal("list exposed a credential or offered copying")
	}
	response, _ = f.request("PATCH", "/api/v1/settings", fmt.Sprintf(`{"api_key_action":{"action":"copy","id":%q}}`, id), "", true, true, "")
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), secret) {
		t.Fatal("the removed copy action returned a Key")
	}
	var count int
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_keys WHERE instr(response_body,?)>0`, secret).Scan(&count); err != nil || count != 0 {
		t.Fatalf("creation persisted a plaintext response: count=%d err=%v", count, err)
	}
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE instr(summary,?)>0`, secret).Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit persisted a plaintext Key: count=%d err=%v", count, err)
	}
	if strings.Contains(f.logs.String(), secret) {
		t.Fatal("creation logged plaintext")
	}
}

func TestLegacyAPIKeyRemainsUsableWithoutKeyFile(t *testing.T) {
	f := newAPIKeyFixture(t)
	id, secret := f.createKey("legacy")
	if _, err := f.app.DB().Exec(`ALTER TABLE api_keys ADD COLUMN secret_ciphertext TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.DB().Exec(`UPDATE api_keys SET secret_ciphertext='obsolete-encrypted-copy' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	query := `SELECT json_array(id,name,key_hash,key_masked,created_by,created_at,deleted_at,last_used_at) FROM api_keys WHERE id=?`
	var before, after string
	if err := f.app.DB().QueryRow(query, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	config := f.app.config
	if err := f.app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), config, slog.New(slog.NewJSONHandler(f.logs, nil)))
	if err != nil {
		t.Fatalf("legacy Key migration required an encryption key file: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	f.app = restarted
	if err := f.app.DB().QueryRow(query, id).Scan(&after); err != nil || before != after {
		t.Fatalf("legacy Key identity changed during migration: %v", err)
	}
	var columns int
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('api_keys') WHERE name='secret_ciphertext'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("legacy encrypted storage was not removed: columns=%d err=%v", columns, err)
	}
	response, _ := f.request("GET", "/api/v1/tokens", "", "Bearer "+secret, false, false, "")
	if response.Code != 200 {
		t.Fatal("legacy Key lost its API access")
	}
	f.request("PATCH", "/api/v1/settings", fmt.Sprintf(`{"api_key_action":{"action":"delete","id":%q}}`, id), "", true, true, "")
	response, _ = f.request("GET", "/api/v1/tokens", "", "Bearer "+secret, false, false, "")
	if response.Code != 401 {
		t.Fatal("migrated Key remained valid after deletion")
	}
}

func TestAPIKeyNameLengthLimit(t *testing.T) {
	f := newAPIKeyFixture(t)
	for _, name := range []string{strings.Repeat("a", 15), strings.Repeat("字", 15)} {
		f.createKey(name)
	}
	for _, name := range []string{strings.Repeat("a", 16), strings.Repeat("字", 16)} {
		body, err := json.Marshal(map[string]any{"api_key_action": map[string]string{"action": "create", "name": name}})
		if err != nil {
			t.Fatal(err)
		}
		response, _ := f.request("PATCH", "/api/v1/settings", string(body), "", true, true, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("overlong name was accepted: status=%d", response.Code)
		}
	}
	var count int
	if err := f.app.DB().QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("invalid names created records: count=%d err=%v", count, err)
	}
}
