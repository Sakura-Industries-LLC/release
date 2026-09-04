package pubobj

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/Sakura-Industries-LLC/release/internal/rel"
	"github.com/Sakura-Industries-LLC/release/internal/stage"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pkgrepo"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pubgh"
)

// ObjectStore reads and writes one private release bucket.
//
// The value types are [pkgrepo.StoredContent] and [pkgrepo.UploadRequest] so
// an S3-compatible adapter can serve both the package repository and release
// downloads.
type ObjectStore interface {
	// Stat returns one object's stored digest metadata and size.
	//
	// The boolean is false only when the object does not exist.
	Stat(ctx context.Context, name string) (pkgrepo.StoredContent, bool, error)
	// Upload writes one exact object and its cache and digest metadata.
	Upload(ctx context.Context, request pkgrepo.UploadRequest) error
}

// Publish uploads the closed bundle under `<project>/<vTag>` convergently.
//
// For every name in [pubgh.Bundle.Names] order it stats `prefix/name` once, then:
//
//  1. An existing object whose stored digest equals `sha256:<hex>` is unchanged.
//  2. An existing object with a different digest is a terminal immutable-path error.
//  3. A missing object is streamed up with [pkgrepo.CacheImmutable] and the
//     bundle digest recorded as object metadata.
//
// Store errors are not retried. A later invocation converges from the current
// remote digests. A cancelled [context.Context] fails immediately.
func Publish(ctx context.Context, input PublishInput, store ObjectStore) (PublishResult, error) {
	if err := validatePublish(ctx, input, store); err != nil {
		return PublishResult{}, err
	}
	prefix, err := newPrefix(input.Project, input.Tag)
	if err != nil {
		return PublishResult{}, err
	}

	return publish(ctx, input, store, prefix)
}

// validatePublish rejects a nil context, a nil store, and a zero input.
func validatePublish(ctx context.Context, input PublishInput, store ObjectStore) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if store == nil {
		return errors.New("object store is nil")
	}
	if _, err := ParseProject(input.Project.String()); err != nil {
		return err
	}
	if input.Tag == "" {
		return errors.New("publish tag is empty")
	}
	if _, err := input.Tag.Version(); err != nil {
		return fmt.Errorf("publish tag %s: %w", input.Tag, err)
	}
	if len(input.Expected.Names()) == 0 {
		return errors.New("publish expected bundle is empty")
	}
	if len(input.Assets) != len(input.Expected.Names()) {
		return fmt.Errorf(
			"publish asset paths: got %d, want %d",
			len(input.Assets),
			len(input.Expected.Names()),
		)
	}

	return nil
}

// publish runs the per-object Stat-then-Upload loop after exported guards.
func publish(
	ctx context.Context,
	input PublishInput,
	store ObjectStore,
	prefix Prefix,
) (PublishResult, error) {
	names := input.Expected.Names()
	entries := bundleEntries(input.Expected)
	uploaded := make([]string, 0)
	unchanged := make([]string, 0)
	for index, name := range names {
		key := path.Join(prefix.String(), name)
		want, err := objectDigest(entries[name])
		if err != nil {
			return PublishResult{}, fmt.Errorf("object %s digest: %w", key, err)
		}
		stored, exists, err := store.Stat(ctx, key)
		if err != nil {
			return PublishResult{}, fmt.Errorf("stat %s: %w", key, err)
		}
		if exists {
			if stored.Digest == want {
				unchanged = append(unchanged, name)
				continue
			}

			return PublishResult{}, fmt.Errorf(
				"immutable object %q already exists with digest %s, want %s",
				key,
				stored.Digest,
				want,
			)
		}
		if err := uploadObject(ctx, store, key, want, input.Assets[index]); err != nil {
			return PublishResult{}, err
		}
		uploaded = append(uploaded, name)
	}

	return PublishResult{
		Project:   input.Project,
		Tag:       input.Tag,
		Prefix:    prefix,
		Uploaded:  uploaded,
		Unchanged: unchanged,
		State:     publicationState(len(uploaded)),
	}, nil
}

// publicationState maps the upload count onto the observable publication result.
func publicationState(uploaded int) PublishState {
	if uploaded > 0 {
		return PublishStatePublished
	}

	return PublishStateUnchanged
}

// uploadObject streams one local asset into the store.
func uploadObject(
	ctx context.Context,
	store ObjectStore,
	key string,
	digest rel.Digest,
	asset pubgh.AssetPath,
) error {
	file, err := os.Open(asset.String())
	if err != nil {
		return fmt.Errorf("open %s: %w", asset, err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()

		return errors.Join(fmt.Errorf("stat %s: %w", asset, err), closeErr)
	}
	uploadErr := store.Upload(ctx, pkgrepo.UploadRequest{
		Path:   key,
		Body:   file,
		Digest: digest,
		Size:   info.Size(),
		Cache:  pkgrepo.CacheImmutable,
	})
	closeErr := file.Close()
	if uploadErr != nil {
		return fmt.Errorf("upload %s: %w", key, uploadErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", asset, closeErr)
	}

	return nil
}

// bundleEntries maps each expected asset name to its hex digest.
func bundleEntries(expected pubgh.Bundle) map[string]stage.Digest {
	entries := make(map[string]stage.Digest, len(expected.Payloads)+len(expected.Controls))
	for _, entry := range expected.Payloads {
		entries[entry.Name] = entry.Digest
	}
	for _, entry := range expected.Controls {
		entries[entry.Name] = entry.Digest
	}

	return entries
}

// objectDigest formats a bundle hex digest as sha256:<hex>.
func objectDigest(digest stage.Digest) (rel.Digest, error) {
	return rel.ParseDigest("sha256:" + digest.String())
}
