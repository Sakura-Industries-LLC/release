package melange

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/meigma/release/internal/stage/image"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "melange"
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
	// holding the write end would hang [Builder.Build] forever without
	// this bound.
	waitDelay = 5 * time.Second
	// publicKeySuffix is appended to the signing key path to form the
	// generated public key path Melange writes.
	publicKeySuffix = ".pub"
	// compileCmd is the Melange compile subcommand.
	compileCmd = "compile"
	// keygenCmd is the Melange keygen subcommand.
	keygenCmd = "keygen"
	// buildCmd is the Melange build subcommand.
	buildCmd = "build"
	// archFlag selects the APK architecture for compile and build.
	archFlag = "--arch"
	// varsFileFlag points Melange at the version vars file.
	varsFileFlag = "--vars-file"
)

// Options configures a [Builder].
type Options struct {
	// Path is the Melange executable. An empty path resolves "melange"
	// from PATH with [exec.LookPath] when building.
	Path string

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. The slice is used as-is and is never logged.
	Environ []string

	// Stderr receives Melange diagnostics while the process runs. A nil
	// value discards them. A nonzero exit still captures a bounded tail
	// for the returned error.
	Stderr io.Writer
}

// Builder invokes the Melange CLI to compile, keygen, and build signed
// APK repositories.
//
// It implements [image.APKBuilder]. It performs no package-index or
// ELF inspection of its own.
type Builder struct {
	// path is the Melange binary. Empty resolves [defaultBinary] from
	// PATH at build time.
	path string

	// environ is the process environment. Nil inherits [os.Environ].
	environ []string

	// stderr receives Melange diagnostics. Nil discards them.
	stderr io.Writer
}

// New constructs a [Builder] from options.
//
// Path resolution is deferred until [Builder.Build] so a missing binary
// is reported when building, not at construction.
func New(options Options) *Builder {
	return &Builder{
		path:    options.Path,
		environ: options.Environ,
		stderr:  options.Stderr,
	}
}

// Build implements [image.APKBuilder].
//
// It runs `melange compile`, `melange keygen`, and one `melange build`
// per source, in request order, as explicit argument slices through
// [exec.CommandContext]. Compile uses [image.APKBuildRequest.Sources]
// index 0 as the compile-check architecture. A nil context, a nil
// receiver, an empty required request field, an empty Sources slice, or
// a source with an empty architecture or directory is rejected before
// any process starts. A nonzero exit returns an error that names the
// Melange subcommand, the exit code, and a tail of stderr limited to
// [stderrTailLimit] bytes. Compile stdout is discarded.
func (b *Builder) Build(
	ctx context.Context,
	request image.APKBuildRequest,
) (image.APKRepositories, error) {
	if err := validateBuildRequest(ctx, b, request); err != nil {
		return image.APKRepositories{}, err
	}

	path, err := resolveBinary(b.path)
	if err != nil {
		return image.APKRepositories{}, err
	}

	if err := b.run(ctx, path, compileArgs(request)...); err != nil {
		return image.APKRepositories{}, err
	}
	if err := b.run(ctx, path, keygenCmd, request.KeyPath); err != nil {
		return image.APKRepositories{}, err
	}
	for _, source := range request.Sources {
		if err := b.run(ctx, path, buildArgs(request, source)...); err != nil {
			return image.APKRepositories{}, err
		}
	}

	return image.APKRepositories{
		Dir:       request.OutDir,
		PublicKey: request.KeyPath + publicKeySuffix,
	}, nil
}

// validateBuildRequest rejects a nil receiver, a nil context, or an
// incomplete request before any process starts.
func validateBuildRequest(
	ctx context.Context,
	builder *Builder,
	request image.APKBuildRequest,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if builder == nil {
		return errors.New("melange builder is nil")
	}
	if request.Config == "" {
		return errors.New("config is empty")
	}
	if request.VarsFile == "" {
		return errors.New("vars file is empty")
	}
	if request.KeyPath == "" {
		return errors.New("key path is empty")
	}
	if request.OutDir == "" {
		return errors.New("output directory is empty")
	}
	if request.Runner == "" {
		return errors.New("runner is empty")
	}
	if request.Namespace == "" {
		return errors.New("namespace is empty")
	}
	if request.BuildDate == "" {
		return errors.New("build date is empty")
	}
	if request.GitRepoURL == "" {
		return errors.New("git repository URL is empty")
	}
	if request.GitCommit == "" {
		return errors.New("git commit is empty")
	}
	if len(request.Sources) == 0 {
		return errors.New("sources are empty")
	}
	for i, source := range request.Sources {
		if source.Arch == "" {
			return fmt.Errorf("source %d architecture is empty", i)
		}
		if source.Dir == "" {
			return fmt.Errorf("source %d directory is empty", i)
		}
	}

	return nil
}

// compileArgs returns the argv for `melange compile`.
//
// The compile-check architecture is [image.APKBuildRequest.Sources]
// index 0. The caller must validate that Sources is nonempty.
func compileArgs(request image.APKBuildRequest) []string {
	return []string{
		compileCmd,
		archFlag,
		string(request.Sources[0].Arch),
		varsFileFlag,
		request.VarsFile,
		request.Config,
	}
}

// buildArgs returns the argv for `melange build` against source.
func buildArgs(request image.APKBuildRequest, source image.APKBuildSource) []string {
	return []string{
		buildCmd,
		archFlag,
		string(source.Arch),
		"--runner",
		request.Runner,
		"--source-dir",
		source.Dir,
		"--out-dir",
		request.OutDir,
		"--signing-key",
		request.KeyPath,
		"--namespace",
		request.Namespace,
		"--build-date",
		request.BuildDate,
		"--git-repo-url",
		request.GitRepoURL,
		"--git-commit",
		request.GitCommit,
		varsFileFlag,
		request.VarsFile,
		"--generate-provenance",
		request.Config,
	}
}

// run starts one Melange invocation and maps process failure to an error.
//
// Path is a resolved executable. The argument list is a fixed slice,
// never a shell string. args[0] is the Melange subcommand name used in
// error text.
func (b *Builder) run(ctx context.Context, path string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	if b.environ != nil {
		cmd.Env = b.environ
	}
	cmd.Stdout = io.Discard

	tail := newTailBuffer(stderrTailLimit)
	stderr := io.Writer(tail)
	if b.stderr != nil {
		stderr = io.MultiWriter(b.stderr, tail)
	}
	cmd.Stderr = stderr
	// WaitDelay unblocks Wait if a grandchild still holds the stderr pipe
	// after CommandContext kills only the direct child.
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		subcommand := args[0]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("melange %s: %w", subcommand, ctxErr)
		}

		return commandError(subcommand, tail, err)
	}

	return nil
}

// resolveBinary returns the Melange executable path.
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

// commandError formats a Melange process failure for subcommand.
func commandError(subcommand string, tail *tailBuffer, err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("melange %s: %w", subcommand, err)
	}
	if tail.String() == "" {
		return fmt.Errorf("melange %s: exit %d", subcommand, exitErr.ExitCode())
	}

	return fmt.Errorf("melange %s: exit %d: %s", subcommand, exitErr.ExitCode(), tail.String())
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
