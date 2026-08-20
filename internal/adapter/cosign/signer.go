package cosign

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "cosign"
)

// Options configures a [Signer].
type Options struct {
	// Path is the cosign executable. An empty path resolves "cosign" from
	// PATH with [exec.LookPath] when signing.
	Path string

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. The slice is used as-is and is never logged.
	Environ []string

	// Stderr receives cosign diagnostics while the process runs. A nil
	// value discards them. A nonzero exit still captures a bounded tail
	// for the returned error.
	Stderr io.Writer
}

// Signer invokes the cosign CLI to attach signatures.
//
// It implements [puboci.Signer]. It performs no registry reads or tag
// mutation. Keyless credentials come from the process environment.
type Signer struct {
	// path is the cosign binary. Empty resolves [defaultBinary] from PATH
	// at sign time.
	path string

	// environ is the process environment. Nil inherits [os.Environ].
	environ []string

	// stderr receives cosign diagnostics. Nil discards them.
	stderr io.Writer
}

// New constructs a [Signer] from options.
//
// Path resolution is deferred until [Signer.SignRecursive] so a missing
// binary is reported when signing, not at construction.
func New(options Options) *Signer {
	return &Signer{
		path:    options.Path,
		environ: options.Environ,
		stderr:  options.Stderr,
	}
}

// SignRecursive implements [puboci.Signer].
//
// It runs `cosign sign --yes --recursive` against ref as an explicit
// argument slice through [execx.Run]. A nil context, a nil receiver, or
// a zero-value ref is rejected before any process starts. A nonzero exit
// returns an error that names the exit code and includes a bounded tail
// of stderr.
func (s *Signer) SignRecursive(ctx context.Context, ref puboci.DigestRef) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if s == nil {
		return errors.New("cosign signer is nil")
	}
	if ref.Image == "" || ref.Digest == "" {
		return errors.New("digest reference is empty")
	}

	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    s.path,
		Args:    []string{"sign", "--yes", "--recursive", ref.String()},
		Env:     s.environ,
		Stderr:  s.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("sign %s: %w", ref, ctxErr)
		}

		return signError(ref, err)
	}

	return nil
}

// signError formats a cosign process failure.
func signError(ref puboci.DigestRef, err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("cosign sign %s: %w", ref, err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("cosign sign %s: %w", ref, err)
	}
	if runErr.StderrTail() == "" {
		return fmt.Errorf("cosign sign %s: exit %d", ref, exitCode)
	}

	return fmt.Errorf("cosign sign %s: exit %d: %s", ref, exitCode, runErr.StderrTail())
}
