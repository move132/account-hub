package refreshpolicy

import (
	"context"
	"time"

	"account-hub/internal/platform/upstream"
)

// NextDeadline separates the normal refresh cadence from transient failures.
// Callers supply their persisted deadline and failure count; retry delay is bounded at one hour.
func NextDeadline(ctx context.Context, previousDeadline *int64, consecutiveFailures int, now time.Time, normalInterval time.Duration, operationErr error) int64 {
	if operationErr == nil {
		return now.Add(normalInterval).UnixMilli()
	}
	if ctx.Err() != nil {
		if previousDeadline != nil {
			return *previousDeadline
		}
		return 0
	}
	switch upstream.Code(operationErr) {
	case upstream.NetworkError, upstream.Timeout, upstream.Unavailable, upstream.RateLimited:
		failures := min(max(consecutiveFailures, 0), 4)
		delay := min(5*time.Minute*time.Duration(1<<failures), time.Hour)
		return now.Add(delay).UnixMilli()
	default:
		// Invalid credentials and protocol/challenge errors require user action or
		// provider recovery. Keep the normal interval rather than hot-looping.
		return now.Add(max(normalInterval, time.Hour)).UnixMilli()
	}
}
