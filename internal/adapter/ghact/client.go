package ghact

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/stage/pubgh"
)

// Client fetches Actions artifact metadata through go-github.
type Client struct {
	// github is the already-authenticated API client.
	github *github.Client
}

// New constructs a [Client] around an already-authenticated go-github client.
//
// The caller supplies authentication. Token text must not be stored on
// [Client] or included in returned errors.
func New(client *github.Client) *Client {
	return &Client{github: client}
}

// NewAuthenticated constructs a [Client] for token at the given GitHub API.
//
// An empty apiURL selects the public https://api.github.com client. A
// non-empty apiURL uses [github.Client.WithEnterpriseURLs], with serverURL
// as the upload base when set and apiURL otherwise. Token text is applied
// only as Authorization and never stored on [Client].
func NewAuthenticated(token, apiURL, serverURL string) (*Client, error) {
	client := github.NewClient(nil).WithAuthToken(token)
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

// Get implements [pubgh.ArtifactMeta].
func (c *Client) Get(
	ctx context.Context,
	repository pubgh.Repository,
	id pubgh.ArtifactID,
) (pubgh.ArtifactMetadata, error) {
	if ctx == nil {
		return pubgh.ArtifactMetadata{}, errors.New("context is nil")
	}
	if c == nil || c.github == nil {
		return pubgh.ArtifactMetadata{}, errors.New("github client is nil")
	}

	return c.get(ctx, repository, id)
}

// get fetches and maps artifact metadata after exported guards.
func (c *Client) get(
	ctx context.Context,
	repository pubgh.Repository,
	id pubgh.ArtifactID,
) (pubgh.ArtifactMetadata, error) {
	artifact, _, err := c.github.Actions.GetArtifact(ctx, repository.Owner, repository.Name, id.Int64())
	if err != nil {
		return pubgh.ArtifactMetadata{}, classify(err)
	}

	return mapArtifact(artifact)
}

// classify maps a go-github failure onto a retryable sentinel or a diagnostic.
//
// The returned error never includes request headers, URLs, or token text.
func classify(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: request canceled", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: request deadline exceeded", context.DeadlineExceeded)
	}

	var rateLimit *github.RateLimitError
	if errors.As(err, &rateLimit) {
		return fmt.Errorf("%w: rate limited", pubgh.ErrRetryable)
	}
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		return fmt.Errorf("%w: secondary rate limited", pubgh.ErrRetryable)
	}

	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response == nil {
		return errors.New("malformed artifact metadata: github request failed")
	}

	switch code := apiErr.Response.StatusCode; {
	case code == http.StatusNotFound:
		return errors.New("artifact not found")
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("github authentication failed: status %d", code)
	case code == http.StatusTooManyRequests || code >= http.StatusInternalServerError:
		return fmt.Errorf("%w: status %d", pubgh.ErrRetryable, code)
	default:
		return fmt.Errorf("malformed artifact metadata: status %d", code)
	}
}

// mapArtifact converts a go-github artifact into a domain value.
func mapArtifact(artifact *github.Artifact) (pubgh.ArtifactMetadata, error) {
	if artifact == nil {
		return pubgh.ArtifactMetadata{}, errors.New("malformed artifact metadata: empty artifact payload")
	}
	if artifact.ID == nil {
		return pubgh.ArtifactMetadata{}, errors.New("malformed artifact metadata: artifact id is missing")
	}
	id, err := pubgh.ArtifactIDFromInt(*artifact.ID)
	if err != nil {
		return pubgh.ArtifactMetadata{}, fmt.Errorf("malformed artifact metadata: %w", err)
	}

	meta := pubgh.ArtifactMetadata{ID: id}
	if artifact.Name != nil {
		meta.Name = *artifact.Name
	}
	if artifact.SizeInBytes != nil {
		meta.SizeBytes = *artifact.SizeInBytes
	}
	if artifact.Expired != nil {
		meta.Expired = *artifact.Expired
	}
	if artifact.ExpiresAt != nil {
		meta.ExpiresAt = artifact.ExpiresAt.Time
	}
	if artifact.Digest != nil && *artifact.Digest != "" {
		digest, err := pubgh.ParseArtifactDigest(*artifact.Digest)
		if err != nil {
			return pubgh.ArtifactMetadata{}, fmt.Errorf("malformed artifact metadata: %w", err)
		}
		meta.Digest = digest
	}
	if artifact.WorkflowRun != nil {
		if artifact.WorkflowRun.ID == nil {
			return pubgh.ArtifactMetadata{}, errors.New("malformed artifact metadata: workflow-run id is missing")
		}
		run, err := pubgh.RunIDFromInt(*artifact.WorkflowRun.ID)
		if err != nil {
			return pubgh.ArtifactMetadata{}, fmt.Errorf("malformed artifact metadata: %w", err)
		}
		meta.HasRun = true
		meta.Run = run
	}

	return meta, nil
}
