package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDreaminaLegacySessionImport(t *testing.T) {
	config := Config{
		DatabasePath:    filepath.Join(t.TempDir(), "test.db"),
		AdminUsername:   "admin",
		DisplayTimezone: DisplayTimezone, AdminSessionTTL: time.Hour,
		UpstreamRequestTimeout: time.Second, JobScanInterval: time.Hour,
	}
	application, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	response := login(t, server.URL, application.initialAdminPassword)
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "account_session":
			sessionCookie = cookie
		case "account_csrf":
			csrfCookie = cookie
		}
	}
	if result := decodeEnvelope(t, response); !result.Success || sessionCookie == nil || csrfCookie == nil {
		t.Fatal("login failed")
	}
	create := authenticatedRequest(t, server.URL+"/api/v1/dreamina/accounts", http.MethodPost,
		[]byte(`{"email":"user@example.com","password":"original-password","session_id":"existing-session","note":"original-note"}`), sessionCookie, csrfCookie, "initial-session")
	if result := decodeEnvelope(t, create); !result.Success {
		t.Fatalf("create initial Session: %+v", result)
	}
	requestImport := func(content, filename, query, key string) (int, envelope) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if filename == "" {
			if err := writer.WriteField("content", content); err != nil {
				t.Fatal(err)
			}
		} else {
			part, err := writer.CreateFormFile("file", filename)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(part, content); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/dreamina-imports?"+query, &body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("X-CSRF-Token", csrfCookie.Value)
		request.Header.Set("Idempotency-Key", key)
		request.AddCookie(sessionCookie)
		request.AddCookie(csrfCookie)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, decodeEnvelope(t, response)
	}
	content := "other@example.com,changed-password,existing-session,duplicate\n" +
		"user@example.com,first-password,first-session,first-note\n" +
		"user@example.com,second-password,second-session\n" +
		"other@example.com,third-password,first-session\n"
	status, preview := requestImport(content, "", "dry_run=true", "")
	if status != http.StatusOK || !preview.Success || preview.Data["valid"] != float64(2) || preview.Data["duplicates"] != float64(2) || preview.Data["invalid"] != float64(0) {
		t.Fatalf("paste preview failed: status=%d body=%+v", status, preview)
	}
	serialized, _ := json.Marshal(preview)
	for _, secret := range []string{"changed-password", "first-password", "first-session", "second-session"} {
		if bytes.Contains(serialized, []byte(secret)) {
			t.Fatal("preview leaked a credential")
		}
	}
	status, invalid := requestImport(content+"invalid@example.com,password,\n", "sessions.csv", "dry_run=false", "invalid-import")
	if status != http.StatusBadRequest || invalid.Data["error_code"] != "IMPORT_HAS_ERRORS" {
		t.Fatalf("invalid input should block the import: status=%d body=%+v", status, invalid)
	}
	var count int
	if err := application.DB().QueryRow("SELECT COUNT(*) FROM dreamina_accounts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("preview or invalid import changed accounts: count=%d err=%v", count, err)
	}
	status, imported := requestImport(content, "sessions.txt", "dry_run=false", "import-sessions")
	if status != http.StatusAccepted || !imported.Success || imported.Data["accepted"] != float64(2) || imported.Data["skipped"] != float64(2) {
		t.Fatalf("TXT import failed: status=%d body=%+v", status, imported)
	}
	jobID := imported.Data["job"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := application.jobs.Get(context.Background(), jobID, false)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == "succeeded" {
			if job.Succeeded != 2 || job.Failed != 0 {
				t.Fatalf("unexpected import job counts: %+v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("import did not complete: %+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := application.DB().QueryRow("SELECT COUNT(*) FROM dreamina_accounts WHERE email = 'user@example.com'").Scan(&count); err != nil || count != 3 {
		t.Fatalf("different Sessions for the same email were not imported: count=%d err=%v", count, err)
	}
	var password, note string
	if err := application.DB().QueryRow("SELECT password, note FROM dreamina_accounts WHERE session_id = 'first-session'").Scan(&password, &note); err != nil || password != "first-password" || note != "first-note" {
		t.Fatalf("legacy columns were not stored correctly: err=%v", err)
	}
	if err := application.DB().QueryRow("SELECT password, note FROM dreamina_accounts WHERE session_id = 'existing-session'").Scan(&password, &note); err != nil || password != "original-password" || note != "original-note" {
		t.Fatalf("duplicate import modified an existing Session: err=%v", err)
	}
	status, duplicate := requestImport(content, "", "dry_run=false", "duplicate-sessions")
	if status != http.StatusOK || !duplicate.Success || duplicate.Data["job"] != nil || duplicate.Data["accepted"] != float64(0) || duplicate.Data["skipped"] != float64(4) {
		t.Fatalf("all-duplicate import should succeed without a job: status=%d body=%+v", status, duplicate)
	}
	if err := application.DB().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid or duplicate input should not create jobs: count=%d err=%v", count, err)
	}
}
