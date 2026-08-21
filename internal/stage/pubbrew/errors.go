package pubbrew

import "errors"

// Publication sentinel errors.
var (
	// ErrConflict reports remote state that the publisher cannot safely
	// overwrite or reconcile.
	ErrConflict = errors.New("homebrew publication conflict")
	// ErrRetryable reports a transient repository failure that may succeed on
	// a bounded retry.
	ErrRetryable = errors.New("retryable homebrew repository failure")
)
