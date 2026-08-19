package cosign

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "cosign"
	// bytesPerKiB is the number of bytes in a kibibyte.
	bytesPerKiB = 1024
	// stderrTailKiB is the stderr tail retained in a nonzero-exit error.
	stderrTailKiB = 4
	// stderrTailLimit is the maximum number of trailing stderr bytes
	// included in a nonzero-exit error.
	stderrTailLimit = stderrTailKiB * bytesPerKiB
	// waitDelay is how long [exec.Cmd] waits for leaked child I/O after
	// the process exits or the context is canceled.
	//
	// Stderr is a tail buffer, not an [*os.File], so [os/exec] copies
	// through a pipe and [exec.Cmd.Wait] blocks until EOF.
	// [exec.CommandContext] kills only the direct child; a grandchild
	// holding the write end would hang [Signer.SignRecursive] forever
	// without this bound.
	waitDelay = 5 * time.Second
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
// argument slice through [exec.CommandContext]. A nil context, a nil
// receiver, or a zero-value ref is rejected before any process starts.
// A nonzero exit returns an error that names the exit code and a tail of
// stderr limited to [stderrTailLimit] bytes.
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

	path, err := resolveBinary(s.path)
	if err != nil {
		return err
	}

	// Path is a resolved executable. The argument list is a fixed slice,
	// never a shell string.
	cmd := exec.CommandContext(ctx, path, "sign", "--yes", "--recursive", ref.String())
	if s.environ != nil {
		cmd.Env = s.environ
	}
	cmd.Stdout = io.Discard

	tail := newTailBuffer(stderrTailLimit)
	stderr := io.Writer(tail)
	if s.stderr != nil {
		stderr = io.MultiWriter(s.stderr, tail)
	}
	cmd.Stderr = stderr
	// WaitDelay unblocks Wait if a grandchild still holds the stderr pipe
	// after CommandContext kills only the direct child.
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("sign %s: %w", ref, ctxErr)
		}

		return signError(ref, tail, err)
	}

	return nil
}

// resolveBinary returns the cosign executable path.
//
// An empty path looks up [defaultBinary] on PATH.
func resolveBinary(path string) (string, error) {
	name := path
	if name == "" {
		name = defaultBinary
	}

	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}

	return resolved, nil
}

// signError formats a cosign process failure.
func signError(ref puboci.DigestRef, tail *tailBuffer, err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("cosign sign %s: %w", ref, err)
	}
	if tail.String() == "" {
		return fmt.Errorf("cosign sign %s: exit %d", ref, exitErr.ExitCode())
	}

	return fmt.Errorf("cosign sign %s: exit %d: %s", ref, exitErr.ExitCode(), tail.String())
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	// limit is the maximum number of bytes retained.
	limit int

	// buf holds the retained tail.
	buf []byte
}

// newTailBuffer returns a writer that retains the last limit bytes.
func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

// Write appends p, discarding older bytes when the tail exceeds the limit.
func (b *tailBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)

		return len(p), nil
	}

	need := len(b.buf) + len(p) - b.limit
	if need > 0 {
		b.buf = b.buf[need:]
	}
	b.buf = append(b.buf, p...)

	return len(p), nil
}

// String returns the retained tail as a string.
func (b *tailBuffer) String() string {
	return string(b.buf)
}
