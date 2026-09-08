package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenContentRoutesRequireAuthenticationAndCSRF(t *testing.T) {
	application, err := New(context.Background(), Config{
		DatabasePath:  filepath.Join(t.TempDir(), "content-auth.db"),
		AdminUsername: "admin", AdminPassword: "content-test-password", AdminPasswordManaged: true, AdminSessionTTL: time.Hour,
		DisplayTimezone: DisplayTimezone, JobScanInterval: time.Hour, UpstreamRequestTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/storage"}, {http.MethodGet, "/library/files"},
		{http.MethodGet, "/conversations"}, {http.MethodGet, "/conversations/conversation-1"},
		{http.MethodGet, "/files/file_abc/image"}, {http.MethodPost, "/library/deletions"},
	} {
		request, _ := http.NewRequest(endpoint.method, server.URL+"/api/v1/tokens/missing"+endpoint.path, bytes.NewBufferString(`{"files":[]}`))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body := decodeEnvelope(t, response)
		if response.StatusCode != http.StatusUnauthorized || body.Success {
			t.Fatalf("unauthenticated %s: status=%d body=%+v", endpoint.path, response.StatusCode, body)
		}
	}
	loggedIn := login(t, server.URL, "content-test-password")
	cookies := loggedIn.Cookies()
	_ = decodeEnvelope(t, loggedIn)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/tokens/missing/library/deletions", bytes.NewBufferString(`{"files":[]}`))
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusForbidden || body.Success {
		t.Fatalf("deletion bypassed CSRF: status=%d body=%+v", response.StatusCode, body)
	}
}
