package app

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDreaminaExportMatchesSora2VideoArchive(t *testing.T) {
	config := Config{
		DatabasePath: filepath.Join(t.TempDir(), "test.db"), AdminUsername: "admin",
		AdminPassword: "correct-horse-battery-staple", AdminPasswordManaged: true,
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

	loginResponse := login(t, server.URL, config.AdminPassword)
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Cookies() {
		switch cookie.Name {
		case "account_session":
			sessionCookie = cookie
		case "account_csrf":
			csrfCookie = cookie
		}
	}
	if result := decodeEnvelope(t, loginResponse); !result.Success || sessionCookie == nil || csrfCookie == nil {
		t.Fatal("login failed")
	}

	inputs := []string{
		`{"email":"active@example.com","password":"password","session_id":"active-session","status":"enabled"}`,
		`{"email":"empty@example.com","password":"password","session_id":"","status":"enabled"}`,
		`{"email":"disabled@example.com","password":"password","session_id":"disabled-session","status":"disabled"}`,
	}
	for index, input := range inputs {
		response := authenticatedRequest(t, server.URL+"/api/v1/dreamina/accounts", http.MethodPost,
			[]byte(input), sessionCookie, csrfCookie, "dreamina-export-create-"+string(rune('a'+index)))
		if result := decodeEnvelope(t, response); !result.Success {
			t.Fatalf("create Dreamina account %d: %+v", index, result)
		}
	}

	exportResponse := authenticatedRequest(t, server.URL+"/api/v1/dreamina-exports", http.MethodPost,
		[]byte(`{}`), sessionCookie, csrfCookie, "")
	defer exportResponse.Body.Close()
	payload, err := io.ReadAll(exportResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if exportResponse.StatusCode != http.StatusOK || exportResponse.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("unexpected export response: status=%d content-type=%q body=%q", exportResponse.StatusCode, exportResponse.Header.Get("Content-Type"), payload)
	}
	if disposition := exportResponse.Header.Get("Content-Disposition"); !strings.Contains(disposition, "dreamina_sessions_export_") || !strings.Contains(disposition, ".zip") {
		t.Fatalf("unexpected Content-Disposition: %q", disposition)
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	contents := make(map[string]string, len(archive.File))
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		value, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		contents[file.Name] = string(value)
	}
	if len(contents) != 2 || contents["1-1.txt"] != "active-session" || contents["session-all.txt"] != "active-session" {
		t.Fatalf("unexpected ZIP contents: %#v", contents)
	}
}
