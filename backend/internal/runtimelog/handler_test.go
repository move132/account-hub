package runtimelog

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"account-hub/internal/auth"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/logging"
	platformsqlite "account-hub/internal/platform/sqlite"
)

type streamFixture struct {
	handler *Handler
	logger  *slog.Logger
	buffer  *logging.Buffer
	store   *auth.Repository
	server  *httptest.Server
	cookie  *http.Cookie
	session auth.Session
}

func newStreamFixture(t *testing.T) *streamFixture {
	t.Helper()
	db, err := platformsqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := auth.NewRepository(db)
	password, err := auth.Bootstrap(context.Background(), store, "admin")
	if err != nil {
		t.Fatal(err)
	}
	service := auth.NewService(store, time.Hour)
	token, _, session, err := service.Login(context.Background(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	buffer := logging.NewBuffer()
	t.Cleanup(buffer.Close)
	logger := logging.WithBuffer(slog.New(slog.NewTextHandler(io.Discard, nil)), buffer)
	handler := NewHandler(buffer, store)
	handler.heartbeatInterval = 10 * time.Millisecond
	handler.writeTimeout = 100 * time.Millisecond
	authHandler := auth.NewHandler(service, nil)
	server := httptest.NewUnstartedServer(httpserver.Middleware(logger, authHandler.Require(http.HandlerFunc(handler.Stream))))
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)
	return &streamFixture{
		handler: handler, logger: logger, buffer: buffer, store: store, server: server,
		cookie: &http.Cookie{Name: "account_session", Value: token}, session: session,
	}
}

func (f *streamFixture) open(t *testing.T, method, after, lastEventID string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, f.server.URL+"?after="+after, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(f.cookie)
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("invalid stream response: %d %v", response.StatusCode, response.Header)
	}
	return response
}

type streamEvent struct {
	name string
	id   string
	data string
}

func readEvent(t *testing.T, scanner *bufio.Scanner) streamEvent {
	t.Helper()
	var event streamEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && event.name != "" {
			return event
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			event.name = value
		}
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			event.id = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			event.data = value
		}
	}
	t.Fatalf("stream ended before the next event: %v", scanner.Err())
	return event
}

func decodeLogs(t *testing.T, event streamEvent) logging.Snapshot {
	t.Helper()
	if event.name != "logs" || event.id == "" {
		t.Fatalf("unexpected event: %+v", event)
	}
	var snapshot logging.Snapshot
	if err := json.Unmarshal([]byte(event.data), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestStreamReplaysDeliversAndResumesFromLastEventID(t *testing.T) {
	f := newStreamFixture(t)
	f.logger.Info("before opening", "password", "web-private-value")
	response := f.open(t, http.MethodGet, "", "")
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("stream caching or buffering is enabled")
	}
	scanner := bufio.NewScanner(response.Body)
	first := readEvent(t, scanner)
	history := decodeLogs(t, first)
	if len(history.Items) != 1 || history.Items[0].Message != "before opening" || strings.Contains(first.data, "web-private-value") {
		t.Fatalf("history missing or exposed credentials: %+v", history)
	}
	f.logger.Warn("live update", "request_id", "req_live")
	live := readEvent(t, scanner)
	if snapshot := decodeLogs(t, live); len(snapshot.Items) != 1 || snapshot.Items[0].Message != "live update" {
		t.Fatalf("live update missing: %+v", snapshot)
	}
	// A Last-Event-ID header takes precedence over the URL from the initial request.
	reconnected := f.open(t, http.MethodGet, history.Items[0].ID, live.id)
	resumed := decodeLogs(t, readEvent(t, bufio.NewScanner(reconnected.Body)))
	if len(resumed.Items) != 0 || resumed.Reset || resumed.Truncated {
		t.Fatalf("reconnect replayed already delivered records: %+v", resumed)
	}
	response.Body.Close()
	reconnected.Body.Close()
	f.logger.Info("while disconnected")
	replay := f.open(t, http.MethodGet, live.id, "")
	missed := decodeLogs(t, readEvent(t, bufio.NewScanner(replay.Body)))
	found := false
	for _, entry := range missed.Items {
		found = found || entry.Message == "while disconnected"
		if entry.Message == "before opening" || entry.Message == "live update" {
			t.Fatalf("replayed old entry: %+v", entry)
		}
	}
	if !found {
		t.Fatal("did not replay the missed update")
	}
}

func TestStreamRenewsDeadlinesWhileIdle(t *testing.T) {
	f := newStreamFixture(t)
	response := f.open(t, http.MethodGet, "", "")
	scanner := bufio.NewScanner(response.Body)
	decodeLogs(t, readEvent(t, scanner))
	// Cross both the server deadline and the first per-stream deadline.
	time.Sleep(160 * time.Millisecond)
	f.logger.Info("after idle")
	events := decodeLogs(t, readEvent(t, scanner))
	if len(events.Items) != 1 || events.Items[0].Message != "after idle" {
		t.Fatalf("idle stream stopped delivering: %+v", events)
	}
}

func TestStreamClosesAfterSessionRevocation(t *testing.T) {
	f := newStreamFixture(t)
	response := f.open(t, http.MethodGet, "", "")
	scanner := bufio.NewScanner(response.Body)
	readEvent(t, scanner)
	if err := f.store.DeleteSession(context.Background(), f.session.IDHash); err != nil {
		t.Fatal(err)
	}
	if event := readEvent(t, scanner); event.name != "auth-expired" {
		t.Fatalf("revoked session was not stopped: %+v", event)
	}
	if scanner.Scan() || scanner.Err() != nil {
		t.Fatalf("revoked stream did not end cleanly: %v", scanner.Err())
	}
}

func TestStreamRejectsAnonymousAndSupportsHead(t *testing.T) {
	f := newStreamFixture(t)
	response, err := f.server.Client().Get(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("anonymous stream was accepted: %d", response.StatusCode)
	}
	head := f.open(t, http.MethodHead, "", "")
	data, err := io.ReadAll(head.Body)
	if err != nil || len(data) != 0 {
		t.Fatalf("HEAD started a streaming body: %q %v", data, err)
	}
}
