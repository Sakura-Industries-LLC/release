package reg

import (
	"context"
	"errors"
	"fmt"
	"io"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

// PushBlob implements [puboci.ContentPusher].
//
// Content is streamed to the registry and is never buffered. An already-present
// blob is success so a retry of a partial publication can converge.
func (c *Client) PushBlob(
	ctx context.Context,
	image puboci.Image,
	descriptor puboci.Descriptor,
	content io.Reader,
) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}

	return c.pushContent(ctx, image, descriptor, content, "blob", pushBlob)
}

// PushManifest implements [puboci.ContentPusher].
//
// The registry stores content under descriptor.MediaType. An already-present
// manifest is success. Unlike [Client.PushBlob], the authenticated oras
// manifest path may buffer the document in memory before the request.
func (c *Client) PushManifest(
	ctx context.Context,
	image puboci.Image,
	descriptor puboci.Descriptor,
	content io.Reader,
) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}

	return c.pushContent(ctx, image, descriptor, content, "manifest", pushManifest)
}

// Verify implements [puboci.ContentPusher].
//
// Missing content wraps [puboci.ErrTagAbsent]. A different resolved digest is
// a verification failure and is not classified as absent.
func (c *Client) Verify(ctx context.Context, ref puboci.DigestRef) error {
	if err := c.requireReady(ctx); err != nil {
		return err
	}

	repo, err := c.repository(ref.Image)
	if err != nil {
		return fmt.Errorf("verify %s: %w", ref.Digest, err)
	}

	desc, err := repo.Resolve(ctx, ref.Digest.String())
	if err != nil {
		return fmt.Errorf("verify %s: %w", ref.Digest, classify(err))
	}

	got, err := rel.ParseDigest(desc.Digest.String())
	if err != nil {
		return fmt.Errorf("verify %s: registry digest: %w", ref.Digest, err)
	}
	if got != ref.Digest {
		return fmt.Errorf("verify %s: resolved %s", ref.Digest, got)
	}

	return nil
}

// readerOnly hides an [io.Closer] behind a plain [io.Reader].
//
// oras hands the content reader to net/http as the request body, and the
// HTTP transport always closes a request body it is given. Content readers
// belong to the caller, so this wrapper keeps the transport from closing a
// file the engine still owns.
type readerOnly struct {
	// Reader is the caller-owned content stream.
	io.Reader
}

// pushContent streams content to the registry under descriptor.
func (c *Client) pushContent(
	ctx context.Context,
	image puboci.Image,
	descriptor puboci.Descriptor,
	content io.Reader,
	kind string,
	push func(context.Context, *remote.Repository, ocispec.Descriptor, io.Reader) error,
) error {
	if content == nil {
		return fmt.Errorf("push %s %s: content is nil", kind, descriptor.Digest)
	}
	if err := descriptor.Validate(); err != nil {
		return fmt.Errorf("push %s %s: %w", kind, descriptor.Digest, err)
	}

	repo, err := c.repository(image)
	if err != nil {
		return fmt.Errorf("push %s %s: %w", kind, descriptor.Digest, err)
	}

	err = push(ctx, repo, ociDescriptor(descriptor), readerOnly{Reader: content})
	if isAlreadyPresent(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("push %s %s: %w", kind, descriptor.Digest, classify(err))
	}

	return nil
}

// pushBlob uploads one blob through oras.
func pushBlob(
	ctx context.Context,
	repo *remote.Repository,
	descriptor ocispec.Descriptor,
	content io.Reader,
) error {
	return repo.Blobs().Push(ctx, descriptor, content)
}

// pushManifest uploads one manifest or index through oras.
func pushManifest(
	ctx context.Context,
	repo *remote.Repository,
	descriptor ocispec.Descriptor,
	content io.Reader,
) error {
	return repo.Manifests().Push(ctx, descriptor, content)
}

// isAlreadyPresent reports whether err means the content is already in the registry.
func isAlreadyPresent(err error) bool {
	return errors.Is(err, errdef.ErrAlreadyExists)
}

// ociDescriptor converts a domain descriptor into an oras descriptor.
func ociDescriptor(descriptor puboci.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: descriptor.MediaType,
		Digest:    godigest.Digest(descriptor.Digest.String()),
		Size:      descriptor.Size,
	}
}
