package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type envelope struct {
	Code    int            `json:"code"`
	Success bool           `json:"success"`
	Data    map[string]any `json:"data"`
	Msg     string         `json:"msg"`
	TS      int64          `json:"ts"`
}

func TestAuthenticationAndSettingsFlow(t *testing.T) {
	config := Config{
		ListenAddress: "127.0.0.1:0", DatabasePath: filepath.Join(t.TempDir(), "test.db"),
		AdminUsername: "admin", AdminPassword: "correct-horse-battery-staple", AdminPasswordManaged: true,
		DisplayTimezone: DisplayTimezone, AdminSessionTTL: time.Hour,
		UpstreamRequestTimeout: time.Second, UpstreamRetryCount: 0, JobScanInterval: time.Hour,
	}
	application, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer application.Shutdown(context.Background())
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	loginBody := []byte(`{"username":"admin","password":"correct-horse-battery-staple"}`)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", bytes.NewReader(loginBody))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	login := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusOK || !login.Success || login.Code != http.StatusOK || login.TS == 0 {
		t.Fatalf("unexpected login response: status=%d body=%+v", response.StatusCode, login)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "account_session":
			sessionCookie = cookie
		case "account_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil || !sessionCookie.HttpOnly || sessionCookie.Secure {
		t.Fatal("login cookies do not match the documented policy")
	}

	settingsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/settings", nil)
	settingsRequest.AddCookie(sessionCookie)
	settingsResponse, err := http.DefaultClient.Do(settingsRequest)
	if err != nil {
		t.Fatal(err)
	}
	settingsEnvelope := decodeEnvelope(t, settingsResponse)
	items, ok := settingsEnvelope.Data["settings"].([]any)
	if !ok || len(items) != 10 {
		t.Fatalf("settings count=%d, want 10", len(items))
	}

	patchBody := []byte(`{"settings":{"token_refresh_interval_hours":24}}`)
	patchRequest, _ := http.NewRequest(http.MethodPatch, server.URL+"/api/v1/settings", bytes.NewReader(patchBody))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest.Header.Set("X-CSRF-Token", csrfCookie.Value)
	patchRequest.AddCookie(sessionCookie)
	patchRequest.AddCookie(csrfCookie)
	patchResponse, err := http.DefaultClient.Do(patchRequest)
	if err != nil {
		t.Fatal(err)
	}
	patched := decodeEnvelope(t, patchResponse)
	if patchResponse.StatusCode != http.StatusOK || !patched.Success {
		t.Fatalf("settings patch failed: status=%d body=%+v", patchResponse.StatusCode, patched)
	}

	createBody := []byte(`{"name":"Test","email":"test@example.com","access_token":"top-secret-token","status":"enabled","note":"review,note"}`)
	first := authenticatedRequest(t, server.URL+"/api/v1/tokens", http.MethodPost, createBody, sessionCookie, csrfCookie, "create-once")
	firstEnvelope := decodeEnvelope(t, first)
	second := authenticatedRequest(t, server.URL+"/api/v1/tokens", http.MethodPost, createBody, sessionCookie, csrfCookie, "create-once")
	secondEnvelope := decodeEnvelope(t, second)
	firstToken := firstEnvelope.Data["token"].(map[string]any)
	secondToken := secondEnvelope.Data["token"].(map[string]any)
	if firstToken["id"] != secondToken["id"] || second.Header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("idempotent create returned different resources: first=%v second=%v", firstToken["id"], secondToken["id"])
	}
	serialized, _ := json.Marshal(firstEnvelope)
	if bytes.Contains(serialized, []byte("top-secret-token")) {
		t.Fatal("create response leaked access token")
	}
	if _, exists := firstToken["video_eligible"]; exists {
		t.Fatal("create response still exposes the retired capability field")
	}
	exportedResponse := authenticatedRequest(t, server.URL+"/api/v1/token-exports", http.MethodPost, []byte(`{}`), sessionCookie, csrfCookie, "")
	exported := decodeEnvelope(t, exportedResponse)
	exportContent, ok := exported.Data["content"].(string)
	if !ok || !exported.Success || strings.Contains(exportContent, "video_eligible") || strings.Contains(exportContent, "top-secret-token") {
		t.Fatalf("export contains a retired field or secret: %+v", exported)
	}
	exportRows, err := csv.NewReader(strings.NewReader(exportContent)).ReadAll()
	if err != nil || len(exportRows) != 2 || len(exportRows[0]) != 9 || len(exportRows[1]) != 9 || exportRows[0][6] != "note" || exportRows[1][6] != "review,note" {
		t.Fatalf("export columns shifted or note was damaged: rows=%v err=%v", exportRows, err)
	}
	var count int
	if err := application.DB().QueryRow("SELECT COUNT(1) FROM tokens").Scan(&count); err != nil || count != 1 {
		t.Fatalf("token count=%d err=%v", count, err)
	}
	var storedAccessToken string
	if err := application.DB().QueryRow("SELECT access_token FROM tokens LIMIT 1").Scan(&storedAccessToken); err != nil || storedAccessToken != "top-secret-token" {
		t.Fatalf("business credential was not stored as specified: value=%q err=%v", storedAccessToken, err)
	}

	csvData := "name,email,access_token,session_token,access_expires_at,status,note\nImported,import@example.com,import-secret,,,enabled,test\n"
	previewResponse := multipartRequest(t, server.URL+"/api/v1/token-imports?dry_run=true", csvData, sessionCookie, csrfCookie, "preview-import")
	previewEnvelope := decodeEnvelope(t, previewResponse)
	previewJSON, _ := json.Marshal(previewEnvelope)
	if !previewEnvelope.Success || bytes.Contains(previewJSON, []byte("import-secret")) {
		t.Fatalf("import preview failed or leaked secret: %s", previewJSON)
	}
	commitResponse := multipartRequest(t, server.URL+"/api/v1/token-imports?dry_run=false", csvData, sessionCookie, csrfCookie, "commit-import")
	commitEnvelope := decodeEnvelope(t, commitResponse)
	jobData := commitEnvelope.Data["job"].(map[string]any)
	jobID := jobData["id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/jobs/"+jobID, nil)
		jobRequest.AddCookie(sessionCookie)
		jobResponse, err := http.DefaultClient.Do(jobRequest)
		if err != nil {
			t.Fatal(err)
		}
		jobEnvelope := decodeEnvelope(t, jobResponse)
		current := jobEnvelope.Data["job"].(map[string]any)
		if current["state"] == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := application.DB().QueryRow("SELECT COUNT(1) FROM tokens").Scan(&count); err != nil || count != 2 {
		t.Fatalf("token count after import=%d err=%v", count, err)
	}

	for _, path := range []string{"/api/v1/not-found", "/api/v1/auth/login"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		result := decodeEnvelope(t, response)
		if result.Success || result.Code != response.StatusCode || result.TS == 0 || result.Data["error_code"] == nil {
			t.Fatalf("non-uniform error for %s: status=%d body=%+v", path, response.StatusCode, result)
		}
	}
}

func TestFreshDatabaseRequiresAdminPassword(t *testing.T) {
	config := Config{DatabasePath: filepath.Join(t.TempDir(), "test.db"), AdminUsername: "admin", JobScanInterval: time.Hour}
	application, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if application != nil {
		application.Shutdown(context.Background())
	}
	if err == nil {
		t.Fatal("expected empty database without ADMIN_PASSWORD to fail")
	}
}

func TestAdminPasswordCanBeChangedAfterBootstrap(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	config := Config{
		ListenAddress: "127.0.0.1:0", DatabasePath: dbPath,
		AdminUsername: "admin", AdminPassword: "correct-horse-battery-staple", AdminPasswordManaged: true,
		DisplayTimezone: DisplayTimezone, AdminSessionTTL: time.Hour,
		UpstreamRequestTimeout: time.Second, UpstreamRetryCount: 0, JobScanInterval: time.Hour,
	}
	application, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(application.Handler())

	loginResponse := login(t, server.URL, "correct-horse-battery-staple")
	loginEnvelope := decodeEnvelope(t, loginResponse)
	if managed, ok := loginEnvelope.Data["password_managed_by_env"].(bool); !ok || managed {
		t.Fatalf("password should be page-managed after bootstrap: %+v", loginEnvelope.Data)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Cookies() {
		switch cookie.Name {
		case "account_session":
			sessionCookie = cookie
		case "account_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatal("login did not set auth cookies")
	}

	changeBody := []byte(`{"current_password":"correct-horse-battery-staple","new_password":"new-correct-horse-battery-staple","confirm_password":"new-correct-horse-battery-staple"}`)
	changeResponse := authenticatedRequest(t, server.URL+"/api/v1/auth/password", http.MethodPatch, changeBody, sessionCookie, csrfCookie, "")
	changed := decodeEnvelope(t, changeResponse)
	if changeResponse.StatusCode != http.StatusOK || !changed.Success {
		t.Fatalf("password change failed: status=%d body=%+v", changeResponse.StatusCode, changed)
	}

	server.Close()
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	restarted, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer restarted.Shutdown(context.Background())
	restartedServer := httptest.NewServer(restarted.Handler())
	defer restartedServer.Close()

	oldLogin := login(t, restartedServer.URL, "correct-horse-battery-staple")
	oldEnvelope := decodeEnvelope(t, oldLogin)
	if oldLogin.StatusCode == http.StatusOK || oldEnvelope.Success {
		t.Fatal("environment password unexpectedly overwrote the page-managed password")
	}
	newLogin := login(t, restartedServer.URL, "new-correct-horse-battery-staple")
	newEnvelope := decodeEnvelope(t, newLogin)
	if newLogin.StatusCode != http.StatusOK || !newEnvelope.Success {
		t.Fatalf("new password login failed after restart: status=%d body=%+v", newLogin.StatusCode, newEnvelope)
	}
}

func login(t *testing.T, serverURL, password string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": "admin", "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, serverURL+"/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeEnvelope(t *testing.T, response *http.Response) envelope {
	t.Helper()
	defer response.Body.Close()
	var result envelope
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

func authenticatedRequest(t *testing.T, url, method string, body []byte, sessionCookie, csrfCookie *http.Cookie, idempotencyKey string) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(method, url, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func multipartRequest(t *testing.T, url, csvData string, sessionCookie, csrfCookie *http.Cookie, idempotencyKey string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "tokens.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(csvData))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, url, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
