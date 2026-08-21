package pubscoop

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// retryAttempts bounds transient repository calls.
	retryAttempts = 4
	// retryBaseDelay is the first exponential retry delay.
	retryBaseDelay = time.Second
)

// SleepFunc waits between retryable repository observations.
type SleepFunc func(ctx context.Context, delay time.Duration) error

// sleepContext waits for delay or context cancellation.
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryRead retries a side-effect-free repository observation after failures
// classified with [ErrRetryable].
func retryRead[T any](ctx context.Context, sleep SleepFunc, operation func() (T, error)) (T, error) {
	var zero T
	var last error
	for attempt := range retryAttempts {
		value, err := operation()
		if err == nil {
			return value, nil
		}
		last = err
		if !errors.Is(err, ErrRetryable) || attempt == retryAttempts-1 {
			return zero, err
		}
		if err := sleep(ctx, retryBaseDelay<<attempt); err != nil {
			return zero, fmt.Errorf("retry wait: %w", err)
		}
	}

	return zero, last
}
