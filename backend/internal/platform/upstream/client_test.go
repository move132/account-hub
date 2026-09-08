package upstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/settings"
)

func TestClientRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client := testClient(t, time.Second, 1)
	response, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	response.Body.Close()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
}

func TestClientAllowsResponseBodyReadAfterDoReturns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client := testClient(t, time.Second, 0)
	response, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body=%q", body)
	}
}

func TestClientClassifiesCredentialsAndTimeout(t *testing.T) {
	t.Run("credentials", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
		defer server.Close()
		client := testClient(t, time.Second, 2)
		_, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) { return http.NewRequest(http.MethodGet, server.URL, nil) })
		if Code(err) != InvalidCredentials {
			t.Fatalf("code=%s err=%v", Code(err), err)
		}
	})
	t.Run("rate limited", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
		defer server.Close()
		client := testClient(t, time.Second, 0)
		_, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) { return http.NewRequest(http.MethodGet, server.URL, nil) })
		if Code(err) != RateLimited {
			t.Fatalf("code=%s err=%v", Code(err), err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte("{}"))
		}))
		defer server.Close()
		client := testClient(t, 20*time.Millisecond, 2)
		_, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) { return http.NewRequest(http.MethodGet, server.URL, nil) })
		if Code(err) != Timeout {
			t.Fatalf("code=%s err=%v", Code(err), err)
		}
		var upstreamError *Error
		if !errors.As(err, &upstreamError) {
			t.Fatal("expected typed upstream error")
		}
	})
}

func TestClientClassifiesAccessRejections(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		challenge bool
		code      string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, code: "invalid_credentials"},
		{name: "forbidden", status: http.StatusForbidden, code: "upstream_forbidden"},
		{name: "challenge forbidden", status: http.StatusForbidden, challenge: true, code: "upstream_challenge"},
		{name: "challenge success status", status: http.StatusOK, challenge: true, code: "upstream_challenge"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if test.challenge {
					w.Header().Set("Cf-Mitigated", "challenge")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("private-response-body"))
			}))
			defer server.Close()
			var logs bytes.Buffer
			client := testClient(t, time.Second, 2).WithLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
			response, err := client.Do(context.Background(), func(_ *http.Client) (*http.Request, error) {
				request, err := http.NewRequest(http.MethodGet, server.URL+"?token=private-query-token", nil)
				if err == nil {
					request.Header.Set("Authorization", "Bearer private-access-token")
					request.Header.Set("Cookie", "session=private-session-token")
				}
				return request, err
			})
			if response != nil {
				response.Body.Close()
			}
			var rejection *Error
			if Code(err) != test.code || !errors.As(err, &rejection) || rejection.HTTPStatus != test.status {
				t.Fatalf("code=%s err=%v, want %s with HTTP %d", Code(err), err, test.code, test.status)
			}
			if calls.Load() != 1 {
				t.Fatalf("access rejection was retried: calls=%d", calls.Load())
			}
			if !strings.Contains(logs.String(), `"error_code":"`+test.code+`"`) {
				t.Fatalf("log does not match returned error: %s", logs.String())
			}
			if strings.Contains(logs.String(), "private-") || strings.Contains(err.Error(), "private-") {
				t.Fatal("upstream diagnostics exposed private request or response data")
			}
		})
	}
}

func testClient(t *testing.T, timeout time.Duration, retries int) *Client {
	t.Helper()
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repository := settings.NewRepository(db)
	if err := repository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	return NewClient(repository, timeout, retries)
}
