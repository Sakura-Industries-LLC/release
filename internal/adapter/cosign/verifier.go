package cosign

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/meigma/release/internal/stage/pubgh"
)

// VerifierOptions configures a [Verifier].
type VerifierOptions struct {
	// Path is the cosign executable. An empty path resolves "cosign" from
	// PATH with [exec.LookPath] when verifying.
	Path string

	// Dir is the child working directory and must be the resolved
	// distribution directory. An empty Dir is rejected before any process
	// starts.
	Dir string

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. The slice is used as-is and is never logged.
	Environ []string

	// Stderr receives cosign diagnostics while the process runs. A nil
	// value discards them. A nonzero exit still captures a bounded tail
	// for the returned error.
	Stderr io.Writer
}

// Verifier invokes the cosign CLI to verify a detached Sigstore bundle.
//
// It implements [pubgh.BlobVerifier]. It performs no policy decisions of
// its own: identity and issuer come from the request.
type Verifier struct {
	// path is the cosign binary. Empty resolves [defaultBinary] from PATH
	// at verify time.
	path string

	// dir is the child working directory. Empty is rejected by
	// [Verifier.Verify].
	dir string

	// environ is the process environment. Nil inherits [os.Environ].
	environ []string

	// stderr receives cosign diagnostics. Nil discards them.
	stderr io.Writer
}

// NewVerifier constructs a [Verifier] from options.
//
// Path resolution is deferred until [Verifier.Verify] so a missing binary
// is reported when verifying, not at construction.
func NewVerifier(options VerifierOptions) *Verifier {
	return &Verifier{
		path:    options.Path,
		dir:     options.Dir,
		environ: options.Environ,
		stderr:  options.Stderr,
	}
}

// Verify implements [pubgh.BlobVerifier].
//
// It runs `cosign verify-blob --bundle --certificate-identity
// --certificate-oidc-issuer` as an explicit argument slice through
// [exec.CommandContext], with [exec.Cmd.Dir] set to the configured
// distribution directory. A nil context, a nil receiver, an empty Dir,
// or an empty payload, bundle, identity, or issuer is rejected before
// any process starts. A nonzero exit returns an error that names the
// exit code and a tail of stderr limited to [stderrTailLimit] bytes.
// Cosign's stdout is discarded.
func (v *Verifier) Verify(ctx context.Context, request pubgh.BlobVerification) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if v == nil {
		return errors.New("cosign verifier is nil")
	}
	if v.dir == "" {
		return errors.New("distribution directory is empty")
	}
	if request.Payload == "" {
		return errors.New("payload name is empty")
	}
	if request.Bundle == "" {
		return errors.New("bundle name is empty")
	}
	if request.Identity == "" {
		return errors.New("certificate identity is empty")
	}
	if request.Issuer == "" {
		return errors.New("certificate issuer is empty")
	}

	path, err := resolveBinary(v.path)
	if err != nil {
		return err
	}

	// Path is a resolved executable. The argument list is a fixed slice,
	// never a shell string.
	cmd := exec.CommandContext(
		ctx,
		path,
		"verify-blob",
		"--bundle",
		request.Bundle,
		"--certificate-identity",
		request.Identity,
		"--certificate-oidc-issuer",
		request.Issuer,
		request.Payload,
	)
	cmd.Dir = v.dir
	if v.environ != nil {
		cmd.Env = v.environ
	}
	cmd.Stdout = io.Discard

	tail := newTailBuffer(stderrTailLimit)
	stderr := io.Writer(tail)
	if v.stderr != nil {
		stderr = io.MultiWriter(v.stderr, tail)
	}
	cmd.Stderr = stderr
	// WaitDelay unblocks Wait if a grandchild still holds the stderr pipe
	// after CommandContext kills only the direct child.
	cmd.WaitDelay = waitDelay
	if runErr := cmd.Run(); runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("verify %s: %w", request.Payload, ctxErr)
		}

		return verifyError(request, tail, runErr)
	}

	return nil
}

// verifyError formats a cosign verify-blob process failure.
func verifyError(request pubgh.BlobVerification, tail *tailBuffer, err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("cosign verify-blob %s: %w", request.Payload, err)
	}
	if tail.String() == "" {
		return fmt.Errorf("cosign verify-blob %s: exit %d", request.Payload, exitErr.ExitCode())
	}

	return fmt.Errorf("cosign verify-blob %s: exit %d: %s", request.Payload, exitErr.ExitCode(), tail.String())
}
