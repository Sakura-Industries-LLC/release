package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "git"
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
// through [execx.Run], with the configured checkout as the child working
// directory. That command yields the commit the tag points at, which is
// the binding between the release tag and github.sha. A nil context, a
// nil receiver, or an empty tag is rejected before any process starts.
// A nonzero exit is an unknown-tag error that names the tag. Successful
// stdout is trimmed and parsed as a [pubgh.CommitSHA]; any other output
// is an error that names what was returned. A nonzero exit includes a
// bounded tail of stderr.
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

	var stdout bytes.Buffer
	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    r.path,
		Args:    []string{"rev-list", "-n", "1", tag.String()},
		Dir:     r.dir,
		Env:     r.environ,
		Stdout:  &stdout,
		Stderr:  r.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("resolve %s: %w", tag, ctxErr)
		}

		return "", resolveError(tag, err)
	}

	raw := strings.TrimSpace(stdout.String())
	sha, parseErr := pubgh.ParseCommitSHA(raw)
	if parseErr != nil {
		return "", fmt.Errorf("git rev-list -n 1 %s: invalid commit SHA %q: %w", tag, raw, parseErr)
	}

	return sha, nil
}

// resolveError formats a git rev-list process failure for an unknown tag.
func resolveError(tag rel.Tag, err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("git rev-list -n 1 %s: unknown tag: %w", tag, err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("git rev-list -n 1 %s: unknown tag: %w", tag, err)
	}
	if runErr.StderrTail() == "" {
		return fmt.Errorf("git rev-list -n 1 %s: unknown tag: exit %d", tag, exitCode)
	}

	return fmt.Errorf(
		"git rev-list -n 1 %s: unknown tag: exit %d: %s",
		tag,
		exitCode,
		runErr.StderrTail(),
	)
}
