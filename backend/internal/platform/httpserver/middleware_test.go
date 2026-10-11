package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareStreamsAttachmentsAndLogsStatus(t *testing.T) {
	var logs bytes.Buffer
	response := httptest.NewRecorder()
	handler := Middleware(slog.New(slog.NewJSONHandler(&logs, nil)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=export.txt")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "first")
		if response.Body.String() != "first" {
			t.Fatal("attachment was buffered until handler completion")
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, "second")
	}))
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	if response.Code != http.StatusAccepted || response.Body.String() != "firstsecond" || !response.Flushed {
		t.Fatalf("response=%+v", response)
	}
	if !strings.Contains(logs.String(), `"status":202`) || !strings.Contains(logs.String(), `"bytes":11`) {
		t.Fatalf("missing response metrics: %s", logs.String())
	}
}

func TestMiddlewareNormalizesMethodErrorWithoutLosingAllow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/action", func(http.ResponseWriter, *http.Request) {})
	response := httptest.NewRecorder()
	Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/action", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "POST" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	var body Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Success {
		t.Fatalf("body=%s err=%v", response.Body, err)
	}
	if !strings.Contains(response.Body.String(), "METHOD_NOT_ALLOWED") {
		t.Fatalf("body=%s", response.Body)
	}
}

func TestMiddlewarePanicBeforeWriteReturnsJSON(t *testing.T) {
	response := httptest.NewRecorder()
	handler := Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "2000")
		w.Header().Set("Content-Disposition", "attachment; filename=export.txt")
		w.Header().Set("Content-Encoding", "gzip")
		panic("private panic")
	}))
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/action", nil))
	if response.Header().Get("Content-Length") != "" || response.Header().Get("Content-Encoding") != "" || response.Header().Get("Content-Disposition") != "" {
		t.Fatalf("abandoned attachment headers remain: %v", response.Header())
	}
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private panic") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
