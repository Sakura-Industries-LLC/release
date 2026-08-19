package pubgh

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// retryAttempts is one initial call plus three octokit retries.
	retryAttempts = 4
	// retryWait is the first backoff; later waits double (1s, 2s, 4s).
	retryWait = time.Second
)

// backoffError is a failed wait between retry attempts, including a
// cancelled context observed before the next call.
type backoffError struct {
	// err is the wait or context error.
	err error
}

// Error returns the wait error text.
func (e backoffError) Error() string {
	return e.err.Error()
}

// Unwrap returns the wait error.
func (e backoffError) Unwrap() error {
	return e.err
}

// retryOp runs op up to [retryAttempts] times when the error is [ErrRetryable].
//
// A cancelled context observed before a call and a failed sleep return as
// [backoffError] so callers can wrap them separately from operation
// failures. Non-retryable errors from op are wrapped as "step: err".
// Exhausted retryable failures wrap the last error as "step after N attempts".
func retryOp(ctx context.Context, sleep SleepFunc, step string, op func() error) error {
	var lastErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return backoffError{err: err}
		}
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err
		if !errors.Is(err, ErrRetryable) || attempt == retryAttempts {
			if errors.Is(err, ErrRetryable) {
				return fmt.Errorf("%s after %d attempts: %w", step, attempt, err)
			}

			return fmt.Errorf("%s: %w", step, err)
		}
		if err := sleep(ctx, retryWait<<(attempt-1)); err != nil {
			return backoffError{err: err}
		}
	}
	if lastErr == nil {
		return errors.New("retry budget exhausted")
	}

	return fmt.Errorf("%s after %d attempts: %w", step, retryAttempts, lastErr)
}

// sleepContext waits for d or returns ctx.Err() if the context ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
