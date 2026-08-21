package ghtap

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/stage/pubbrew"
)

// errNotFound marks a repository resource that GitHub does not expose.
var errNotFound = errors.New("github repository resource not found")

// classify maps a go-github failure onto a safe domain sentinel or diagnostic.
//
// It never includes request headers, response bodies, URLs, or token text.
func classify(err error, resource string) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: request canceled", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: request deadline exceeded", context.DeadlineExceeded)
	}

	var rateLimit *github.RateLimitError
	if errors.As(err, &rateLimit) {
		return fmt.Errorf("%w: rate limited", pubbrew.ErrRetryable)
	}
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		return fmt.Errorf("%w: secondary rate limited", pubbrew.ErrRetryable)
	}

	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response == nil {
		return fmt.Errorf("%s request failed", resource)
	}

	switch code := apiErr.Response.StatusCode; {
	case code == http.StatusNotFound:
		return fmt.Errorf("%w: %s", errNotFound, resource)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("github authentication failed: status %d", code)
	case code == http.StatusConflict || code == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %s status %d", pubbrew.ErrConflict, resource, code)
	case code == http.StatusTooManyRequests || code >= http.StatusInternalServerError:
		return fmt.Errorf("%w: %s status %d", pubbrew.ErrRetryable, resource, code)
	default:
		return fmt.Errorf("%s request failed: status %d", resource, code)
	}
}

// isNotFound reports whether err is a raw or classified GitHub 404 response.
func isNotFound(err error) bool {
	if errors.Is(err, errNotFound) {
		return true
	}
	var apiErr *github.ErrorResponse

	return errors.As(err, &apiErr) &&
		apiErr.Response != nil &&
		apiErr.Response.StatusCode == http.StatusNotFound
}
