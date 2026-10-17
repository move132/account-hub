package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestCaptureKeepsTerminalOutputAndRedactsWebRecords(t *testing.T) {
	var terminal bytes.Buffer
	buffer := NewBuffer()
	logger := WithBuffer(slog.New(slog.NewJSONHandler(&terminal, nil)), buffer)
	logger.With("component", "upstream").With("password", "initial-password").
		WithGroup("request").Info("request failed with Bearer bearer-secret",
		"token_id", "tok_visible", "access_token", "access-secret",
		"response", map[string]any{"session_id": "session-secret", "status": 401},
		"error", context.Canceled)
	logger.WithGroup("authorization").Info("headers", "value", "header-secret")
	logger.Debug("hidden")
	entries := buffer.Snapshot("").Items
	if len(entries) != 2 || entries[0].Level != "INFO" || entries[0].Time <= 0 {
		t.Fatalf("unexpected records: %+v", entries)
	}
	encoded, _ := json.Marshal(entries)
	for _, secret := range []string{"initial-password", "bearer-secret", "access-secret", "session-secret", "header-secret"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("web log exposed %s: %s", secret, encoded)
		}
		if !strings.Contains(terminal.String(), secret) {
			t.Fatalf("original handler lost %s", secret)
		}
	}
	for _, field := range []string{"component=upstream", "request.token_id=tok_visible", "request.error=", "context canceled"} {
		if !strings.Contains(entries[0].Fields, field) {
			t.Fatalf("missing diagnostic field %s: %+v", field, entries[0])
		}
	}
	if strings.Contains(entries[0].Fields, "request.component=") {
		t.Fatal("WithGroup moved an earlier attribute into the group")
	}
}

func TestRedactTextCoversHeadersCredentialsAndURLs(t *testing.T) {
	tests := []struct{ input, secret string }{
		{`password="private with spaces" status=failed`, "private with spaces"},
		{`{"access_token":"private-token","status":401}`, "private-token"},
		{"Authorization: Bearer bearer-value", "bearer-value"},
		{"Cookie: account_session=session-value; other=other-value", "other-value"},
		{"https://user:proxy-password@localhost/a?session_id=session-value", "proxy-password"},
		{"https://localhost/a?access_token=query-value&safe=yes", "query-value"},
		{"key ahk_abcdefghijklmnopqrstuvwxyz0123456789abcd", "ahk_abcdefghijklmnopqrstuvwxyz0123456789abcd"},
		{"generated hub_1234567890", "hub_1234567890"},
	}
	for _, test := range tests {
		t.Run(test.secret, func(t *testing.T) {
			got := redactText(test.input)
			if strings.Contains(got, test.secret) || !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("redaction failed: %q", got)
			}
		})
	}
	if got := redactText("\x1b[32mjob started\x1b[0m job_id=job_123 token_id=tok_123"); got != "job started job_id=job_123 token_id=tok_123" {
		t.Fatalf("lost safe diagnostics: %q", got)
	}
}

func TestBufferReplayDetectsEvictionAndRestart(t *testing.T) {
	buffer := NewBuffer()
	buffer.append(Entry{Message: "first"})
	first := buffer.Snapshot("")
	for i := 0; i < MaxEntries+5; i++ {
		buffer.append(Entry{Message: "next"})
	}
	snapshot := buffer.Snapshot(first.Cursor)
	if len(snapshot.Items) != MaxEntries || !snapshot.Truncated || snapshot.Reset {
		t.Fatalf("eviction was not reported: count=%d snapshot=%+v", len(snapshot.Items), snapshot)
	}
	current := buffer.Snapshot(snapshot.Cursor)
	if len(current.Items) != 0 || current.Truncated || current.Reset {
		t.Fatalf("current cursor replayed old records: %+v", current)
	}
	restarted := NewBuffer()
	restarted.append(Entry{Message: "after restart"})
	if snapshot := restarted.Snapshot(current.Cursor); !snapshot.Reset || len(snapshot.Items) != 1 {
		t.Fatalf("restart was not reported: %+v", snapshot)
	}
	select {
	case <-first.Changed:
	default:
		t.Fatal("history read and notification lost a write")
	}
	buffer.Close()
	buffer.Close()
	buffer.append(Entry{Message: "after close"})
	if got := buffer.Snapshot(current.Cursor); !got.Closed || len(got.Items) != 0 {
		t.Fatalf("closed buffer accepted a record: %+v", got)
	}
}

func TestBufferBoundsMemoryAndPreservesUTF8(t *testing.T) {
	buffer := NewBuffer()
	for range MaxEntries {
		buffer.append(Entry{Message: strings.Repeat("日志", 2000), Fields: strings.Repeat("字段", 2000)})
	}
	snapshot := buffer.Snapshot("")
	if buffer.bytes > maxBufferBytes || len(snapshot.Items) >= MaxEntries || !snapshot.Truncated {
		t.Fatalf("buffer exceeded byte bound: bytes=%d count=%d", buffer.bytes, len(snapshot.Items))
	}
	for _, entry := range snapshot.Items {
		if len(entry.Message)+len(entry.Fields) > maxRecordBytes || !utf8.ValidString(entry.Message) || !utf8.ValidString(entry.Fields) {
			t.Fatalf("record limit split UTF-8 or exceeded the limit: %+v", entry)
		}
	}
}

func TestConcurrentCaptureAndSnapshots(t *testing.T) {
	buffer := NewBuffer()
	logger := WithBuffer(slog.New(slog.NewTextHandler(io.Discard, nil)), buffer)
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			child := logger.With("worker", worker).WithGroup("operation")
			for item := range 150 {
				child.Info("processed", "item", item)
				_ = buffer.Snapshot("")
			}
		})
	}
	workers.Wait()
	snapshot := buffer.Snapshot("")
	seen := make(map[string]bool)
	for _, entry := range snapshot.Items {
		if seen[entry.ID] {
			t.Fatalf("duplicate ID: %s", entry.ID)
		}
		seen[entry.ID] = true
	}
	if len(snapshot.Items) != MaxEntries || !snapshot.Truncated {
		t.Fatalf("concurrent writes lost records: %d", len(snapshot.Items))
	}
}
