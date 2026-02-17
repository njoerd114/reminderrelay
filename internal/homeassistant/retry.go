// Package homeassistant wraps the go-ha-client REST and WebSocket APIs for
// todo-list operations. It provides an [Adapter] with methods aligned to the
// sync engine's needs, a 3-attempt exponential-backoff [Retry] helper, and
// conversion between HA's JSON representation and [model.Item].
package homeassistant

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

const (
	// defaultMaxAttempts is the number of tries before Retry gives up.
	defaultMaxAttempts = 3

	// baseDelay is the starting backoff interval (before jitter).
	baseDelay = 500 * time.Millisecond

	// maxDelay caps the backoff interval.
	maxDelay = 5 * time.Second
)

// Retry executes fn up to maxAttempts times with exponential backoff and
// jitter. It returns nil on the first successful call, or a wrapped error
// containing the last failure if all attempts are exhausted.
func Retry(ctx context.Context, maxAttempts int, fn func() error) error {
	var lastErr error
	for attempt := range maxAttempts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("retry cancelled: %w", err)
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if attempt < maxAttempts-1 {
			delay := backoffDelay(attempt)
			select {
			case <-ctx.Done():
				return fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", maxAttempts, lastErr)
}

// backoffDelay computes the delay for a given attempt index, applying
// exponential growth with 50–100 % jitter.
func backoffDelay(attempt int) time.Duration {
	delay := baseDelay * (1 << attempt)
	if delay > maxDelay {
		delay = maxDelay
	}
	// Jitter: uniform in [delay/2, delay).
	jitter := time.Duration(rand.Int63n(int64(delay) / 2)) //nolint:gosec // jitter does not need crypto/rand
	return delay/2 + jitter
}
