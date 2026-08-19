package puboci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/meigma/release/internal/rel"
)

const (
	// retryAttempts is one initial publication call plus three retries.
	retryAttempts = 4
	// retryWait is the first backoff; later waits double (1s, 2s, 4s).
	retryWait = time.Second
)

// SleepFunc waits for d or until ctx is cancelled.
type SleepFunc func(ctx context.Context, d time.Duration) error

// PrepareInput is the candidate image, layout, and expected index digest.
type PrepareInput struct {
	// Image is the untagged repository that will receive the content.
	Image Image
	// Version is the candidate MAJOR.MINOR.PATCH release.
	Version rel.Version
	// IndexDigest is the expected index digest, cross-checked against the layout.
	IndexDigest rel.Digest
	// Layout is a filesystem rooted at the extracted oci-image/layout directory.
	Layout fs.FS
	// DryRun skips every write, verification, and signature.
	DryRun bool
	// Sleep waits between retryable publication attempts. Nil selects a
	// context-aware timer.
	Sleep SleepFunc
}

// Prepare reads a local OCI layout, plans tags, and publishes digest-addressed content.
//
// It fails closed before any write when the layout index digest does not match
// [PrepareInput.IndexDigest], when [CollectState] fails, or when [rel.PlanTags]
// reports an immutable-tag or corrupt-channel conflict. A dry run returns
// [NewPrepareResult] with Authoritative false and never calls pusher or signer;
// those ports may be nil only in that mode. A real prepare pushes every layout
// blob, then each platform manifest, then the index, verifies the index and
// every platform digest (a deliberate strengthening of the workflow, which
// verifies only the index), and signs the index recursively. Each push and
// verification is attempted at most four times. Failures wrapping
// [ErrRetryable] wait 1s, then 2s, then 4s, and reopen the layout blob so the
// stream starts at byte zero. Other errors fail immediately. Context
// cancellation returns immediately. A nil [PrepareInput.Sleep] uses a
// context-aware timer. Errors name the failing step and descriptor digest and
// wrap the underlying error.
func Prepare(
	ctx context.Context,
	input PrepareInput,
	state StateReader,
	pusher ContentPusher,
	signer Signer,
) (OCIPrepareResult, error) {
	if err := validatePrepare(ctx, input, state); err != nil {
		return OCIPrepareResult{}, err
	}

	layout, err := ReadLayout(input.Layout)
	if err != nil {
		return OCIPrepareResult{}, fmt.Errorf("read layout: %w", err)
	}
	if layout.Index.Digest != input.IndexDigest {
		return OCIPrepareResult{}, fmt.Errorf(
			"layout index digest %s does not match expected %s",
			layout.Index.Digest,
			input.IndexDigest,
		)
	}

	current, err := CollectState(ctx, state, input.Image, input.Version, input.IndexDigest)
	if err != nil {
		return OCIPrepareResult{}, fmt.Errorf("collect state: %w", err)
	}
	if _, err := rel.PlanTags(input.Version, input.IndexDigest, current); err != nil {
		return OCIPrepareResult{}, fmt.Errorf("plan tags: %w", err)
	}

	if input.DryRun {
		return NewPrepareResult(
			input.Image,
			input.Version,
			input.IndexDigest,
			layout.Platforms,
			current,
			false,
		), nil
	}
	if pusher == nil {
		return OCIPrepareResult{}, errors.New("content pusher is nil")
	}
	if signer == nil {
		return OCIPrepareResult{}, errors.New("signer is nil")
	}

	sleep := input.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	if err := pushContent(ctx, input.Image, input.Layout, layout, pusher, sleep); err != nil {
		return OCIPrepareResult{}, err
	}
	if err := verifyContent(ctx, input.Image, layout, pusher, sleep); err != nil {
		return OCIPrepareResult{}, err
	}

	indexRef := input.Image.Pin(input.IndexDigest)
	if err := signer.SignRecursive(ctx, indexRef); err != nil {
		return OCIPrepareResult{}, fmt.Errorf("sign %s: %w", indexRef, err)
	}

	return NewPrepareResult(
		input.Image,
		input.Version,
		input.IndexDigest,
		layout.Platforms,
		current,
		true,
	), nil
}

// validatePrepare rejects a nil context, a nil layout, a zero image, version,
// or digest, and a nil state reader.
func validatePrepare(ctx context.Context, input PrepareInput, state StateReader) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if input.Layout == nil {
		return errors.New("layout is nil")
	}
	if input.Image == "" {
		return errors.New("image is empty")
	}
	if input.Version == (rel.Version{}) {
		return errors.New("version is zero")
	}
	if input.IndexDigest == "" {
		return errors.New("digest is empty")
	}
	if state == nil {
		return errors.New("state reader is nil")
	}

	return nil
}

// pushContent uploads blobs, platform manifests, then the index, in that order.
func pushContent(
	ctx context.Context,
	image Image,
	fsys fs.FS,
	layout Layout,
	pusher ContentPusher,
	sleep SleepFunc,
) error {
	for _, blob := range layout.Blobs {
		if err := pushBlob(ctx, image, fsys, blob, pusher, sleep); err != nil {
			return err
		}
	}
	for _, platform := range layout.Platforms {
		if err := pushManifest(ctx, image, fsys, platform.Descriptor, pusher, sleep); err != nil {
			return err
		}
	}

	return pushIndex(ctx, image, layout, pusher, sleep)
}

// pushBlob streams one config or layer blob from the layout filesystem.
func pushBlob(
	ctx context.Context,
	image Image,
	fsys fs.FS,
	desc Descriptor,
	pusher ContentPusher,
	sleep SleepFunc,
) error {
	return withRetry(ctx, sleep, "push blob", desc.Digest, func() error {
		file, err := openLayoutBlob(fsys, desc.Digest)
		if err != nil {
			return err
		}
		err = pusher.PushBlob(ctx, image, desc, file)
		closeErr := file.Close()
		if err != nil {
			return err
		}

		return closeErr
	})
}

// pushManifest streams one platform manifest from the layout filesystem.
func pushManifest(
	ctx context.Context,
	image Image,
	fsys fs.FS,
	desc Descriptor,
	pusher ContentPusher,
	sleep SleepFunc,
) error {
	return withRetry(ctx, sleep, "push manifest", desc.Digest, func() error {
		file, err := openLayoutBlob(fsys, desc.Digest)
		if err != nil {
			return err
		}
		err = pusher.PushManifest(ctx, image, desc, file)
		closeErr := file.Close()
		if err != nil {
			return err
		}

		return closeErr
	})
}

// pushIndex uploads the exact index.json bytes retained by [ReadLayout].
func pushIndex(ctx context.Context, image Image, layout Layout, pusher ContentPusher, sleep SleepFunc) error {
	return withRetry(ctx, sleep, "push index", layout.Index.Digest, func() error {
		return pusher.PushManifest(ctx, image, layout.Index, bytes.NewReader(layout.IndexBytes))
	})
}

// verifyContent requires the published index, then each platform, to resolve.
//
// The publish workflow verifies only the index. Checking every platform
// manifest digest as well is a deliberate strengthening so a partial push
// cannot look successful.
func verifyContent(ctx context.Context, image Image, layout Layout, pusher ContentPusher, sleep SleepFunc) error {
	if err := withRetry(ctx, sleep, "verify index", layout.Index.Digest, func() error {
		return pusher.Verify(ctx, image.Pin(layout.Index.Digest))
	}); err != nil {
		return err
	}
	for _, platform := range layout.Platforms {
		if err := withRetry(ctx, sleep, "verify manifest", platform.Descriptor.Digest, func() error {
			return pusher.Verify(ctx, image.Pin(platform.Descriptor.Digest))
		}); err != nil {
			return err
		}
	}

	return nil
}

// withRetry runs op up to [retryAttempts] times when the error is [ErrRetryable].
//
// A cancelled context returns immediately. Non-retryable errors fail on the
// first attempt. Exhausted retryable failures name the attempt count.
func withRetry(ctx context.Context, sleep SleepFunc, step string, digest rel.Digest, op func() error) error {
	var lastErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s %s: %w", step, digest, err)
		}
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err
		if !errors.Is(err, ErrRetryable) || attempt == retryAttempts {
			if errors.Is(err, ErrRetryable) {
				return fmt.Errorf("%s %s after %d attempts: %w", step, digest, attempt, err)
			}

			return fmt.Errorf("%s %s: %w", step, digest, err)
		}
		if err := sleep(ctx, retryWait<<(attempt-1)); err != nil {
			return fmt.Errorf("%s %s: %w", step, digest, err)
		}
	}

	return fmt.Errorf("%s %s after %d attempts: %w", step, digest, retryAttempts, lastErr)
}

// openLayoutBlob opens digest's blob file on fsys for streaming.
func openLayoutBlob(fsys fs.FS, digest rel.Digest) (fs.File, error) {
	name, err := BlobPath(digest)
	if err != nil {
		return nil, err
	}
	file, err := fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	return file, nil
}

// sleepContext waits for d or returns ctx.Err() if the context ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
