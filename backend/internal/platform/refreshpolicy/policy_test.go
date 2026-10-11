package refreshpolicy

import (
	"context"
	"testing"
	"time"

	"account-hub/internal/platform/upstream"
)

func TestFailureBackoff(t *testing.T) {
	now := time.Unix(1700000000, 0)
	normal := 7 * 24 * time.Hour
	for _, test := range []struct {
		name     string
		code     string
		failures int
		delay    time.Duration
	}{
		{"initial_timeout", upstream.Timeout, 0, 5 * time.Minute},
		{"network_retry", upstream.NetworkError, 1, 10 * time.Minute},
		{"rate_limit_retry", upstream.RateLimited, 3, 40 * time.Minute},
		{"retry_limit", upstream.Unavailable, 20, time.Hour},
		{"negative_count", upstream.Timeout, -1, 5 * time.Minute},
		{"credentials", upstream.InvalidCredentials, 5, normal},
		{"challenge", upstream.ChallengeRequired, 1, normal},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := NextDeadline(context.Background(), nil, test.failures, now, normal, &upstream.Error{Code: test.code})
			if want := now.Add(test.delay).UnixMilli(); got != want {
				t.Fatalf("deadline=%d want=%d", got, want)
			}
		})
	}
}

func TestZeroIntervalKeepsSuccessAndBoundsCredentialFailures(t *testing.T) {
	now := time.Unix(1700000000, 0)
	if got := NextDeadline(context.Background(), nil, 0, now, 0, &upstream.Error{Code: upstream.InvalidCredentials}); got != now.Add(time.Hour).UnixMilli() {
		t.Fatalf("invalid credentials would hot-loop: %d", got)
	}
	if got := NextDeadline(context.Background(), nil, 0, now, 0, nil); got != now.UnixMilli() {
		t.Fatalf("zero-interval success changed: %d", got)
	}
}

func TestCancellationPreservesPreviousDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Unix(1700000000, 0)
	previous := now.Add(-time.Hour).UnixMilli()
	for _, test := range []struct {
		name     string
		previous *int64
		want     int64
	}{
		{"existing_deadline", &previous, previous},
		{"no_deadline", nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NextDeadline(ctx, test.previous, 2, now, 7*24*time.Hour, context.Canceled); got != test.want {
				t.Fatalf("cancelled deadline=%d want=%d", got, test.want)
			}
		})
	}
	// An operation that actually succeeded still uses its normal cadence, even
	// if cancellation arrived before the caller persisted the successful result.
	normal := 3 * time.Hour
	if got := NextDeadline(ctx, &previous, 2, now, normal, nil); got != now.Add(normal).UnixMilli() {
		t.Fatalf("completed success was treated as cancellation: %d", got)
	}
}
