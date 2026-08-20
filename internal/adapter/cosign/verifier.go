package cosign

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/meigma/release/internal/execx"
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
// [execx.Run], with the configured distribution directory as the child
// working directory. A nil context, a nil receiver, an empty Dir, or an
// empty payload, bundle, identity, or issuer is rejected before any
// process starts. A nonzero exit returns an error that names the exit
// code and includes a bounded tail of stderr. Cosign's stdout is
// discarded.
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

	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    v.path,
		Args: []string{
			"verify-blob",
			"--bundle",
			request.Bundle,
			"--certificate-identity",
			request.Identity,
			"--certificate-oidc-issuer",
			request.Issuer,
			request.Payload,
		},
		Dir:    v.dir,
		Env:    v.environ,
		Stderr: v.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("verify %s: %w", request.Payload, ctxErr)
		}

		return verifyError(request, err)
	}

	return nil
}

// verifyError formats a cosign verify-blob process failure.
func verifyError(request pubgh.BlobVerification, err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("cosign verify-blob %s: %w", request.Payload, err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("cosign verify-blob %s: %w", request.Payload, err)
	}
	if runErr.StderrTail() == "" {
		return fmt.Errorf("cosign verify-blob %s: exit %d", request.Payload, exitCode)
	}

	return fmt.Errorf(
		"cosign verify-blob %s: exit %d: %s",
		request.Payload,
		exitCode,
		runErr.StderrTail(),
	)
}
