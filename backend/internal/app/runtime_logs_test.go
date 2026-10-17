package app

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRuntimeLogsRequireAdminAndRedactBootstrapPassword(t *testing.T) {
	f := newAPIKeyFixture(t)
	_, secret := f.createKey("日志权限测试")
	for _, authorization := range []string{"", "Bearer " + secret} {
		response, _ := f.request(http.MethodGet, "/api/v1/runtime-logs/stream", "", authorization, false, false, "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("non-admin can read runtime logs: %d", response.Code)
		}
	}
	if !strings.Contains(f.logs.String(), f.app.initialAdminPassword) {
		t.Fatal("bootstrap password disappeared from the original terminal log")
	}
	snapshot := f.app.runtimeLogs.Snapshot("")
	if len(snapshot.Items) == 0 || !strings.Contains(snapshot.Items[0].Fields, "[REDACTED]") {
		t.Fatalf("bootstrap log was not captured and sanitized: %+v", snapshot)
	}
	for _, entry := range snapshot.Items {
		if strings.Contains(entry.Fields, f.app.initialAdminPassword) || strings.Contains(entry.Fields, secret) {
			t.Fatal("runtime buffer contains a plaintext credential")
		}
	}
}

func TestShutdownReleasesAnActiveRuntimeLogStream(t *testing.T) {
	a := lifecycleApplication(t)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	response := login(t, server.URL, a.initialAdminPassword)
	_ = decodeEnvelope(t, response)
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/runtime-logs/stream", nil)
	for _, cookie := range response.Cookies() {
		request.AddCookie(cookie)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	stream, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	scanner := bufio.NewScanner(stream.Body)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "id: ") {
		t.Fatalf("stream did not start: %v", scanner.Err())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown waited for a live subscription: %v", err)
	}
	if _, err := io.Copy(io.Discard, stream.Body); err != nil {
		t.Fatalf("shutdown did not finish the stream cleanly: %v", err)
	}
}
