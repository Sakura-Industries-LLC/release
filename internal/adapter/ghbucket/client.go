package ghbucket

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/rel"
)

// Client reads and mutates one GitHub bucket through go-github.
type Client struct {
	// github is the already-authenticated API client.
	github *github.Client
}

// New constructs a [Client] around an already-authenticated go-github client.
func New(client *github.Client) *Client {
	return &Client{github: client}
}

// NewAuthenticated constructs a [Client] for token at the given GitHub API.
//
// An empty apiURL selects public GitHub. Token text is applied only to the
// Authorization header and is never retained separately or returned in errors.
func NewAuthenticated(token rel.Secret, apiURL, serverURL string) (*Client, error) {
	client := github.NewClient(nil).WithAuthToken(token.Reveal())
	if apiURL == "" {
		return New(client), nil
	}
	uploadURL := serverURL
	if uploadURL == "" {
		uploadURL = apiURL
	}
	enterprise, err := client.WithEnterpriseURLs(apiURL, uploadURL)
	if err != nil {
		return nil, fmt.Errorf("github enterprise urls: %w", err)
	}

	return New(enterprise), nil
}

// requireReady rejects a nil context or uninitialized client.
func (c *Client) requireReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if c == nil || c.github == nil {
		return errors.New("github client is nil")
	}

	return nil
}
