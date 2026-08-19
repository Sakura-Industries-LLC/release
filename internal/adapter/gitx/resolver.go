package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "git"
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
	// holding the write end would hang [Resolver.Resolve] forever
	// without this bound.
	waitDelay = 5 * time.Second
)

// Options configures a [Resolver].
type Options struct {
	// Path is the git executable. An empty path resolves "git" from PATH
	// with [exec.LookPath] when resolving a tag.
	Path string

	// Dir is the child working directory and should be the git checkout.
	// An empty Dir inherits the process working directory.
	Dir string

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. The slice is used as-is and is never logged.
	Environ []string

	// Stderr receives git diagnostics while the process runs. A nil
	// value discards them. A nonzero exit still captures a bounded tail
	// for the returned error.
	Stderr io.Writer
}

// Resolver invokes git to resolve a tag to the commit it points at.
//
// It implements [pubgh.RefResolver]. The resolved commit is what binds
// a release tag to github.sha. The adapter never creates or mutates
// tags.
type Resolver struct {
	// path is the git binary. Empty resolves [defaultBinary] from PATH
	// at resolve time.
	path string

	// dir is the child working directory. Empty inherits the process
	// working directory.
	dir string

	// environ is the process environment. Nil inherits [os.Environ].
	environ []string

	// stderr receives git diagnostics. Nil discards them.
	stderr io.Writer
}

// New constructs a [Resolver] from options.
//
// Path resolution is deferred until [Resolver.Resolve] so a missing
// binary is reported when resolving, not at construction.
func New(options Options) *Resolver {
	return &Resolver{
		path:    options.Path,
		dir:     options.Dir,
		environ: options.Environ,
		stderr:  options.Stderr,
	}
}

// Resolve implements [pubgh.RefResolver].
//
// It runs `git rev-list -n 1 <tag>` as an explicit argument slice
// through [exec.CommandContext], with [exec.Cmd.Dir] set to the
// configured checkout. That command yields the commit the tag points
// at, which is the binding between the release tag and github.sha. A
// nil context, a nil receiver, or an empty tag is rejected before any
// process starts. A nonzero exit is an unknown-tag error that names
// the tag. Successful stdout is trimmed and parsed as a
// [pubgh.CommitSHA]; any other output is an error that names what was
// returned. A nonzero exit includes a tail of stderr limited to
// [stderrTailLimit] bytes.
func (r *Resolver) Resolve(ctx context.Context, tag rel.Tag) (pubgh.CommitSHA, error) {
	if ctx == nil {
		return "", errors.New("context is nil")
	}
	if r == nil {
		return "", errors.New("git resolver is nil")
	}
	if tag == "" {
		return "", errors.New("tag is empty")
	}

	path, err := resolveBinary(r.path)
	if err != nil {
		return "", err
	}

	// Path is a resolved executable. The argument list is a fixed slice,
	// never a shell string.
	cmd := exec.CommandContext(ctx, path, "rev-list", "-n", "1", tag.String())
	cmd.Dir = r.dir
	if r.environ != nil {
		cmd.Env = r.environ
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	tail := newTailBuffer(stderrTailLimit)
	stderr := io.Writer(tail)
	if r.stderr != nil {
		stderr = io.MultiWriter(r.stderr, tail)
	}
	cmd.Stderr = stderr
	// WaitDelay unblocks Wait if a grandchild still holds the stderr pipe
	// after CommandContext kills only the direct child.
	cmd.WaitDelay = waitDelay
	if runErr := cmd.Run(); runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("resolve %s: %w", tag, ctxErr)
		}

		return "", resolveError(tag, tail, runErr)
	}

	raw := strings.TrimSpace(stdout.String())
	sha, parseErr := pubgh.ParseCommitSHA(raw)
	if parseErr != nil {
		return "", fmt.Errorf("git rev-list -n 1 %s: invalid commit SHA %q: %w", tag, raw, parseErr)
	}

	return sha, nil
}

// resolveBinary returns the git executable path.
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

// resolveError formats a git rev-list process failure for an unknown tag.
func resolveError(tag rel.Tag, tail *tailBuffer, err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("git rev-list -n 1 %s: unknown tag: %w", tag, err)
	}
	if tail.String() == "" {
		return fmt.Errorf("git rev-list -n 1 %s: unknown tag: exit %d", tag, exitErr.ExitCode())
	}

	return fmt.Errorf("git rev-list -n 1 %s: unknown tag: exit %d: %s", tag, exitErr.ExitCode(), tail.String())
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
