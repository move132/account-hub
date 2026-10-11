package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"account-hub/internal/job"
	platformsqlite "account-hub/internal/platform/sqlite"
)

func lifecycleApplication(t *testing.T) *Application {
	t.Helper()
	config := LoadConfig()
	config.DatabasePath = filepath.Join(t.TempDir(), "test.db")
	config.ListenAddress = "127.0.0.1:0"
	config.JobScanInterval = 10 * time.Millisecond
	a, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return a
}

func TestConstructionDoesNotRunJobsAndStartIsIdempotent(t *testing.T) {
	a := lifecycleApplication(t)
	var calls atomic.Int32
	a.jobs.Register("token", "check", func(context.Context, string, json.RawMessage) error {
		calls.Add(1)
		return nil
	}, nil)
	created, _, err := a.jobs.Create(context.Background(), "token", "check", "", "test", "", "", []job.Target{{ID: "target", Label: "target"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ready(context.Background()); err == nil {
		t.Fatal("constructed application is unexpectedly ready")
	}
	current, err := a.jobs.Get(context.Background(), created.ID, true)
	if err != nil || current.State != "queued" || calls.Load() != 0 {
		t.Fatalf("construction ran jobs: %+v %v", current, err)
	}
	for range 2 {
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err = a.jobs.Get(context.Background(), created.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not run: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("job executed %d times", calls.Load())
	}
}

func TestShutdownCommitsActiveWorkerBeforeClosingDatabase(t *testing.T) {
	a := lifecycleApplication(t)
	entered := make(chan struct{})
	a.jobs.Register("token", "check", func(ctx context.Context, _ string, _ json.RawMessage) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	created, _, err := a.jobs.Create(context.Background(), "token", "check", "", "test", "", "", []job.Target{{ID: "target", Label: "target"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("start after shutdown unexpectedly succeeded")
	}
	db, err := platformsqlite.Open(context.Background(), a.config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var pending, running, unfinished int
	if err := db.QueryRow("SELECT pending, running FROM jobs WHERE id = ?", created.ID).Scan(&pending, &running); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM job_items WHERE job_id = ? AND state = 'running'", created.ID).Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || running != 0 || unfinished != 0 {
		t.Fatalf("shutdown lost task handoff: pending=%d running=%d unfinished=%d", pending, running, unfinished)
	}
}

func TestShutdownWaitsForInflightHTTPRequest(t *testing.T) {
	a := lifecycleApplication(t)
	entered, release := make(chan struct{}), make(chan struct{})
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(listener) }()
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not enter handler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- a.Shutdown(ctx) }()
	select {
	case err := <-shutdownDone:
		close(release)
		t.Fatalf("shutdown returned before request finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan error{requestDone, runDone, shutdownDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("lifecycle did not finish")
		}
	}
}

func TestHTTPAndJobShareApplicationErrorCodes(t *testing.T) {
	a := lifecycleApplication(t)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	loggedIn := login(t, server.URL, a.initialAdminPassword)
	var session *http.Cookie
	for _, cookie := range loggedIn.Cookies() {
		if cookie.Name == "account_session" {
			session = cookie
		}
	}
	if !decodeEnvelope(t, loggedIn).Success || session == nil {
		t.Fatal("login failed")
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/tokens/missing-account", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(session)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	result := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected not found, got %d", response.StatusCode)
	}
	created, _, err := a.jobs.Create(context.Background(), "token", "enable", "", "test", "", "", []job.Target{{ID: "missing-account", Label: "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := a.jobs.Get(context.Background(), created.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == "failed" {
			if len(current.Items) != 1 || current.Items[0].ErrorCode != result.Data["error_code"] {
				t.Fatalf("HTTP and job disagree: HTTP=%v job=%+v", result.Data, current)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
