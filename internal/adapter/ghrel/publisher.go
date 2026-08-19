package ghrel

import (
	"context"

	"github.com/google/go-github/v82/github"

	"github.com/meigma/release/internal/stage/pubgh"
)

// Publish implements [pubgh.Publisher].
//
// It sends a PATCH that sets draft to false and omits every other release
// field so GitHub cannot clobber name, body, tag, or target commitish.
func (c *Client) Publish(
	ctx context.Context,
	repository pubgh.Repository,
	release pubgh.ReleaseID,
) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}

	return c.publish(ctx, repository, release)
}

// publish undrafts a release after exported guards.
func (c *Client) publish(
	ctx context.Context,
	repository pubgh.Repository,
	release pubgh.ReleaseID,
) error {
	_, _, err := c.github.Repositories.EditRelease(
		ctx,
		repository.Owner,
		repository.Name,
		release.Int64(),
		&github.RepositoryRelease{Draft: new(bool)},
	)
	if err != nil {
		return classify(err)
	}

	return nil
}
