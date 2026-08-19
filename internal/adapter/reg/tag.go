package reg

import (
	"context"
	"errors"
	"fmt"
	"slices"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

// Commit applies tags to digest serially, verifying each one.
//
// Commit implements [puboci.TagCommitter]. image@digest is resolved once.
// Each tag is then written with oras and checked to resolve back to digest
// before the next write starts. An empty tags slice is success and performs
// no registry calls after input checks. The first failure stops the sequence
// and reports the failing tag and how many tags were already applied. Absent
// content wraps [puboci.ErrTagAbsent]. Transient registry failures wrap
// [puboci.ErrRetryable].
func (c *Client) Commit(
	ctx context.Context,
	image puboci.Image,
	digest rel.Digest,
	tags []rel.Tag,
) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}
	if _, err := rel.ParseDigest(digest.String()); err != nil {
		return fmt.Errorf("commit digest: %w", err)
	}
	if slices.Contains(tags, "") {
		return errors.New("commit tag is empty")
	}
	if len(tags) == 0 {
		return nil
	}

	repo, err := c.repository(image)
	if err != nil {
		return fmt.Errorf("commit %s: %w", digest, err)
	}

	desc, err := repo.Resolve(ctx, digest.String())
	if err != nil {
		return fmt.Errorf("commit %s: %w", digest, classify(err))
	}

	for i, tag := range tags {
		written, commitErr := commitTag(ctx, repo, desc, digest, tag)
		if commitErr != nil {
			applied := i
			if written {
				applied = i + 1
			}

			return fmt.Errorf("commit tag %s: applied %d of %d: %w", tag, applied, len(tags), commitErr)
		}
	}

	return nil
}

// commitTag writes one tag and verifies it resolves to digest.
//
// written is true when [remote.Repository.Tag] succeeded, even if the
// following resolve fails. Callers use that to count a live tag that the
// next run will reconcile from fresh state.
func commitTag(
	ctx context.Context,
	repo *remote.Repository,
	desc ocispec.Descriptor,
	digest rel.Digest,
	tag rel.Tag,
) (bool, error) {
	if err := repo.Tag(ctx, desc, tag.String()); err != nil {
		return false, classify(err)
	}

	got, err := repo.Resolve(ctx, tag.String())
	if err != nil {
		return true, classify(err)
	}

	resolved, err := rel.ParseDigest(got.Digest.String())
	if err != nil {
		return true, fmt.Errorf("registry digest: %w", err)
	}
	if resolved != digest {
		return true, fmt.Errorf("resolved %s, want %s", resolved, digest)
	}

	return true, nil
}
