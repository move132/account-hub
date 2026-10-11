package token

import (
	"context"
	"time"

	"account-hub/internal/platform/upstream"
)

// refreshDeadline separates the normal refresh cadence from transient failures.
// Backoff is persisted through Record.RefreshFailures and bounded at one hour.
func refreshDeadline(ctx context.Context, record Record, now time.Time, normal time.Duration, operationErr error) int64 {
	if operationErr == nil {
		return now.Add(normal).UnixMilli()
	}
	if ctx.Err() != nil {
		if record.NextRefreshAt != nil {
			return *record.NextRefreshAt
		}
		return 0
	}
	switch upstream.Code(operationErr) {
	case upstream.NetworkError, upstream.Timeout, upstream.Unavailable, upstream.RateLimited:
		failures := min(max(record.RefreshFailures, 0), 4)
		delay := min(5*time.Minute*time.Duration(1<<failures), time.Hour)
		return now.Add(delay).UnixMilli()
	default:
		// Invalid credentials and protocol/challenge errors require user action or
		// provider recovery. Keep the normal interval rather than hot-looping.
		return now.Add(max(normal, time.Hour)).UnixMilli()
	}
}
