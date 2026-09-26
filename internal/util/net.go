package util

import (
	"context"
	"io"
	"time"
)

// maxErrBodyRead caps how much of an auth error response is kept.
const maxErrBodyRead = 2048

// SleepContext waits for d, returning early with the context error when the
// context is cancelled first.
func SleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ReadLimited drains at most maxErrBodyRead bytes of a response body.
func ReadLimited(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, maxErrBodyRead))

	return string(raw)
}
