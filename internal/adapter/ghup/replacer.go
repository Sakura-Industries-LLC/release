package ghup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "gh"
	// envGHToken is the child environment variable that carries the App token.
	//
	// The value is a variable name, not a credential.
	envGHToken = "GH_TOKEN"

	// uploadFixedArgs is the number of argv entries besides the asset paths:
	// release, upload, tag, --repo, owner/name, and --clobber.
	uploadFixedArgs = 6
)

// Options configures a [Replacer].
type Options struct {
	// Path is the gh executable. An empty path resolves "gh" from PATH
	// with [exec.LookPath] when uploading.
	Path string

	// Dir is the child working directory. An empty Dir inherits the
	// process working directory.
	Dir string

	// Token is the GitHub App token supplied as GH_TOKEN. It is revealed
	// once when [New] constructs the replacer and is never stored on
	// [Replacer].
	Token rel.Secret

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. GH_TOKEN is always applied on top. The slice is
	// used as-is and is never logged.
	Environ []string

	// Stderr receives gh diagnostics while the process runs. A nil
	// value discards them. A nonzero exit still captures a bounded tail
	// for the returned error.
	Stderr io.Writer
}

// Replacer invokes the gh CLI to clobber-upload release assets.
//
// It implements [pubgh.AssetReplacer]. It performs no release creation,
// re-drafting, or deletion. Unexpected asset names must be refused by the
// caller before [Replacer.Replace] runs.
type Replacer struct {
	// path is the gh binary. Empty resolves [defaultBinary] from PATH
	// at replace time.
	path string

	// dir is the child working directory. Empty inherits the caller cwd.
	dir string

	// environ returns the child environment with GH_TOKEN applied.
	// The token is captured inside this closure and is not stored on
	// this value.
	environ func() []string

	// stderr receives gh diagnostics. Nil discards them.
	stderr io.Writer
}

// New constructs a [Replacer] from options.
//
// Path resolution is deferred until [Replacer.Replace] so a missing binary
// is reported when uploading, not at construction. [rel.Secret.Reveal] is
// called once and the token is captured only inside the environment closure.
func New(options Options) *Replacer {
	token := options.Token.Reveal()
	base := options.Environ

	return &Replacer{
		path: options.Path,
		dir:  options.Dir,
		environ: func() []string {
			env := base
			if env == nil {
				env = os.Environ()
			}

			return applyToken(env, token)
		},
		stderr: options.Stderr,
	}
}

// Replace implements [pubgh.AssetReplacer].
//
// It runs `gh release upload <tag> <path>... --repo <owner>/<name> --clobber`
// as an explicit argument slice through [execx.Run], with the configured
// child working directory. --clobber replaces an existing asset of the
// same name. This method never deletes an asset it was not given. A nil
// context, a nil receiver, an empty tag, an empty repository, an empty
// asset list, or an empty path entry is rejected before any process
// starts. A nonzero exit returns an error that names the exit code and
// includes a bounded tail of stderr. The App token is supplied only as
// GH_TOKEN and never appears in argv or the error.
func (r *Replacer) Replace(
	ctx context.Context,
	repository pubgh.Repository,
	tag rel.Tag,
	expected []pubgh.AssetPath,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if r == nil || r.environ == nil {
		return errors.New("asset replacer is nil")
	}
	if tag == "" {
		return errors.New("tag is empty")
	}
	if repository.Owner == "" || repository.Name == "" {
		return errors.New("repository is empty")
	}
	if len(expected) == 0 {
		return errors.New("asset list is empty")
	}
	if slices.Contains(expected, "") {
		return errors.New("asset path is empty")
	}

	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    r.path,
		Args:    uploadArgs(tag, repository, expected),
		Dir:     r.dir,
		Env:     r.environ(),
		Stderr:  r.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("upload %s: %w", tag, ctxErr)
		}

		return replaceError(tag, err)
	}

	return nil
}

// uploadArgs builds the explicit `gh release upload` argument slice.
func uploadArgs(tag rel.Tag, repository pubgh.Repository, expected []pubgh.AssetPath) []string {
	args := make([]string, 0, uploadFixedArgs+len(expected))
	args = append(args, "release", "upload", tag.String())
	for _, asset := range expected {
		args = append(args, asset.String())
	}
	args = append(args, "--repo", repository.String(), "--clobber")

	return args
}

// applyToken copies env, drops any existing GH_TOKEN, and appends token.
func applyToken(env []string, token string) []string {
	prefix := envGHToken + "="
	child := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		child = append(child, entry)
	}
	child = append(child, prefix+token)

	return child
}

// replaceError formats a gh process failure without including credentials.
func replaceError(tag rel.Tag, err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("gh release upload %s: %w", tag, err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("gh release upload %s: %w", tag, err)
	}
	if runErr.StderrTail() == "" {
		return fmt.Errorf("gh release upload %s: exit %d", tag, exitCode)
	}

	return fmt.Errorf("gh release upload %s: exit %d: %s", tag, exitCode, runErr.StderrTail())
}
