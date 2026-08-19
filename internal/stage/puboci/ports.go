package puboci

import (
	"context"
	"io"

	"github.com/meigma/release/internal/rel"
)

// ContentPusher writes digest-addressed OCI content and checks that it resolves.
//
// Pushes address content by digest and are idempotent: an already-present blob
// or manifest is success, not an error. Implementations stream [io.Reader]
// content and must not require the caller to buffer a layer. [ContentPusher.Verify]
// resolves [DigestRef] and fails unless the registry returns that same digest.
// Absent content is classified as [ErrTagAbsent]. Transient failures are
// classified as [ErrRetryable] and are not retried here.
type ContentPusher interface {
	// PushBlob uploads one blob addressed by descriptor.Digest.
	//
	// An already-present blob is success. content is the blob bytes and is
	// consumed at most once. Callers never pass a layer that has been
	// buffered into memory.
	PushBlob(ctx context.Context, image Image, descriptor Descriptor, content io.Reader) error

	// PushManifest uploads one manifest or index addressed by descriptor.Digest.
	//
	// The registry stores content under descriptor.MediaType. An
	// already-present manifest is success. content is the exact encoded
	// document and is consumed at most once.
	PushManifest(ctx context.Context, image Image, descriptor Descriptor, content io.Reader) error

	// Verify resolves ref and requires the registry digest to equal ref.Digest.
	//
	// Missing content wraps [ErrTagAbsent]. Transient registry failures
	// wrap [ErrRetryable]. A different resolved digest is a verification
	// failure and is not classified as absent.
	Verify(ctx context.Context, ref DigestRef) error
}

// Signer attaches signatures to a published image index.
//
// [Signer.SignRecursive] signs the index at ref and every manifest that
// index references. Implementations invoke `cosign sign --yes --recursive`
// against image@digest. Sign failures are returned as received; this port
// does not classify them as [ErrRetryable].
type Signer interface {
	// SignRecursive signs ref and every referenced platform manifest.
	//
	// ref is the published index. The call writes signatures only; it does
	// not mutate tags.
	SignRecursive(ctx context.Context, ref DigestRef) error
}

// TagCommitter applies tags to a published digest serially.
//
// [TagCommitter.Commit] writes each tag in order and verifies that it
// resolves to digest. Implementations must not apply tags in parallel.
// Transient failures wrap [ErrRetryable]. Absent content wraps
// [ErrTagAbsent]. Errors must not contain credentials or full URLs.
type TagCommitter interface {
	// Commit applies tags to digest serially, verifying each one.
	Commit(ctx context.Context, image Image, digest rel.Digest, tags []rel.Tag) error
}
