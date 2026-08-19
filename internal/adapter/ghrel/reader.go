package ghrel

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// listPageSize is GitHub's maximum page size for release and asset lists.
	listPageSize = 100
)

// Client reads GitHub Releases and undrafts them through go-github.
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

// FindDraft implements [pubgh.ReleaseReader].
//
// It paginates repository releases once and returns the unique release whose
// tag name equals tag, including a published (non-draft) match. Deciding
// what a non-draft means is the engine's job. Zero matches return
// [pubgh.ErrNoDraft]. Two or more matches return [pubgh.ErrAmbiguousRelease].
// [pubgh.Publish] owns the poll budget.
func (c *Client) FindDraft(
	ctx context.Context,
	repository pubgh.Repository,
	tag rel.Tag,
) (pubgh.Release, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubgh.Release{}, err
	}

	return c.findDraft(ctx, repository, tag)
}

// WaitAssets implements [pubgh.ReleaseReader].
//
// It paginates release assets once and returns the current view. It does
// not decide readiness: the caller validates counts, names, states, and
// digests. [pubgh.Publish] owns the poll budget.
func (c *Client) WaitAssets(
	ctx context.Context,
	repository pubgh.Repository,
	release pubgh.ReleaseID,
) (pubgh.AssetsView, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubgh.AssetsView{}, err
	}

	return c.listAssets(ctx, repository, release)
}

// Get implements [pubgh.ReleaseReader].
func (c *Client) Get(
	ctx context.Context,
	repository pubgh.Repository,
	release pubgh.ReleaseID,
) (pubgh.Release, error) {
	if err := c.requireReady(ctx); err != nil {
		return pubgh.Release{}, err
	}

	return c.get(ctx, repository, release)
}

// requireReady rejects a nil context or an uninitialized client.
func (c *Client) requireReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if c == nil || c.github == nil {
		return errors.New("github client is nil")
	}

	return nil
}

// findDraft lists releases once and returns the unique tag match.
func (c *Client) findDraft(
	ctx context.Context,
	repository pubgh.Repository,
	tag rel.Tag,
) (pubgh.Release, error) {
	matched, err := c.listMatchingReleases(ctx, repository, tag)
	if err != nil {
		return pubgh.Release{}, err
	}
	switch len(matched) {
	case 0:
		return pubgh.Release{}, pubgh.ErrNoDraft
	case 1:
		return matched[0], nil
	default:
		return pubgh.Release{}, pubgh.ErrAmbiguousRelease
	}
}

// get fetches and maps one release after exported guards.
func (c *Client) get(
	ctx context.Context,
	repository pubgh.Repository,
	release pubgh.ReleaseID,
) (pubgh.Release, error) {
	observed, _, err := c.github.Repositories.GetRelease(
		ctx,
		repository.Owner,
		repository.Name,
		release.Int64(),
	)
	if err != nil {
		return pubgh.Release{}, classify(err)
	}

	return mapRelease(observed)
}

// listMatchingReleases paginates releases and keeps those whose tag equals tag.
func (c *Client) listMatchingReleases(
	ctx context.Context,
	repository pubgh.Repository,
	tag rel.Tag,
) ([]pubgh.Release, error) {
	opts := &github.ListOptions{PerPage: listPageSize}
	wanted := tag.String()
	var matched []pubgh.Release
	for {
		releases, resp, err := c.github.Repositories.ListReleases(
			ctx,
			repository.Owner,
			repository.Name,
			opts,
		)
		if err != nil {
			return nil, classify(err)
		}
		for _, release := range releases {
			if release.GetTagName() != wanted {
				continue
			}
			mapped, mapErr := mapRelease(release)
			if mapErr != nil {
				return nil, mapErr
			}
			matched = append(matched, mapped)
		}
		if resp == nil || resp.NextPage == 0 {
			return matched, nil
		}
		opts.Page = resp.NextPage
	}
}

// listAssets paginates every asset attached to release.
func (c *Client) listAssets(
	ctx context.Context,
	repository pubgh.Repository,
	release pubgh.ReleaseID,
) (pubgh.AssetsView, error) {
	opts := &github.ListOptions{PerPage: listPageSize}
	var assets []pubgh.Asset
	for {
		page, resp, err := c.github.Repositories.ListReleaseAssets(
			ctx,
			repository.Owner,
			repository.Name,
			release.Int64(),
			opts,
		)
		if err != nil {
			return pubgh.AssetsView{}, classify(err)
		}
		for _, asset := range page {
			assets = append(assets, mapAsset(asset))
		}
		if resp == nil || resp.NextPage == 0 {
			return pubgh.AssetsView{Assets: assets}, nil
		}
		opts.Page = resp.NextPage
	}
}

// mapRelease converts a go-github release into a domain value.
func mapRelease(release *github.RepositoryRelease) (pubgh.Release, error) {
	if release == nil {
		return pubgh.Release{}, errors.New("malformed release metadata: empty release payload")
	}
	id, err := pubgh.ReleaseIDFromInt(release.GetID())
	if err != nil {
		return pubgh.Release{}, fmt.Errorf("malformed release metadata: %w", err)
	}
	tag, err := rel.ParseTag(release.GetTagName())
	if err != nil {
		return pubgh.Release{}, fmt.Errorf("malformed release metadata: %w", err)
	}

	return pubgh.Release{
		ID:    id,
		Tag:   tag,
		Draft: release.GetDraft(),
		URL:   release.GetHTMLURL(),
	}, nil
}

// mapAsset converts a go-github release asset into a domain value.
func mapAsset(asset *github.ReleaseAsset) pubgh.Asset {
	return pubgh.Asset{
		Name:   asset.GetName(),
		Digest: asset.GetDigest(),
		State:  asset.GetState(),
	}
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
		return errors.New("malformed release metadata: github request failed")
	}

	switch code := apiErr.Response.StatusCode; {
	case code == http.StatusNotFound:
		return errors.New("release not found")
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("github authentication failed: status %d", code)
	case code == http.StatusTooManyRequests || code >= http.StatusInternalServerError:
		return fmt.Errorf("%w: status %d", pubgh.ErrRetryable, code)
	default:
		return fmt.Errorf("malformed release metadata: status %d", code)
	}
}
