package melange

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/stage/image"
)

const (
	// defaultBinary is the PATH name used when [Options.Path] is empty.
	defaultBinary = "melange"

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
// [execx.Run]. Compile uses [image.APKBuildRequest.Sources] index 0 as
// the compile-check architecture. A nil context, a nil receiver, an
// empty required request field, an empty Sources slice, or a source with
// an empty architecture or directory is rejected before any process
// starts. A nonzero exit returns an error that names the Melange
// subcommand, the exit code, and a bounded tail of stderr. Compile stdout
// is discarded.
func (b *Builder) Build(
	ctx context.Context,
	request image.APKBuildRequest,
) (image.APKRepositories, error) {
	if err := validateBuildRequest(ctx, b, request); err != nil {
		return image.APKRepositories{}, err
	}

	if err := b.run(ctx, compileArgs(request)...); err != nil {
		return image.APKRepositories{}, err
	}
	if err := b.run(ctx, keygenCmd, request.KeyPath); err != nil {
		return image.APKRepositories{}, err
	}
	for _, source := range request.Sources {
		if err := b.run(ctx, buildArgs(request, source)...); err != nil {
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
// The argument list is a fixed slice, never a shell string. args[0] is
// the Melange subcommand name used in error text.
func (b *Builder) run(ctx context.Context, args ...string) error {
	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    b.path,
		Args:    args,
		Env:     b.environ,
		Stderr:  b.stderr,
	})
	if err != nil {
		subcommand := args[0]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("melange %s: %w", subcommand, ctxErr)
		}

		return commandError(subcommand, err)
	}

	return nil
}

// commandError formats a Melange process failure for subcommand.
func commandError(subcommand string, err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("melange %s: %w", subcommand, err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("melange %s: %w", subcommand, err)
	}
	if runErr.StderrTail() == "" {
		return fmt.Errorf("melange %s: exit %d", subcommand, exitCode)
	}

	return fmt.Errorf("melange %s: exit %d: %s", subcommand, exitCode, runErr.StderrTail())
}
