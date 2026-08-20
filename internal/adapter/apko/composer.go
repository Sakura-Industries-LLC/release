package apko

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
	defaultBinary = "apko"

	// flagsPerArch is the argv width of one `--arch` flag plus its value.
	flagsPerArch = 2
	// flagsPerAnnotation is the argv width of one `--annotations` flag
	// plus its key:value.
	flagsPerAnnotation = 2
)

// Options configures a [Composer].
type Options struct {
	// Path is the apko executable. An empty path resolves "apko" from
	// PATH with [exec.LookPath] when composing.
	Path string

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. The slice is used as-is and is never logged.
	Environ []string

	// Stderr receives apko diagnostics while the process runs. A nil
	// value discards them. A nonzero exit still captures a bounded tail
	// for the returned error.
	Stderr io.Writer
}

// Composer invokes the apko CLI to lock a configuration and write a
// multi-architecture OCI layout.
//
// It implements [image.Composer]. It performs no lockfile or layout
// inspection of its own.
type Composer struct {
	// path is the apko binary. Empty resolves [defaultBinary] from PATH
	// at compose time.
	path string

	// environ is the process environment. Nil inherits [os.Environ].
	environ []string

	// stderr receives apko diagnostics. Nil discards them.
	stderr io.Writer
}

// New constructs a [Composer] from options.
//
// Path resolution is deferred until [Composer.Build] so a missing binary
// is reported when composing, not at construction.
func New(options Options) *Composer {
	return &Composer{
		path:    options.Path,
		environ: options.Environ,
		stderr:  options.Stderr,
	}
}

// Build implements [image.Composer].
//
// It runs `apko lock` and then `apko build` as explicit argument slices
// through [execx.Run], both with the child working directory set to
// request.Dir. Architecture flags follow [image.ComposeRequest.Arches]
// in order. Annotation flags follow [image.ComposeRequest.Annotations]
// in order. A nil context, a nil receiver, an empty required request
// field, an empty Arches slice, an empty architecture entry, or an
// annotation with an empty key is rejected before any process starts.
// A nonzero exit returns an error that names the apko subcommand, the
// exit code, and a bounded tail of stderr. Both invocations discard
// stdout.
func (c *Composer) Build(ctx context.Context, request image.ComposeRequest) error {
	if err := validateComposeRequest(ctx, c, request); err != nil {
		return err
	}

	if err := c.run(ctx, request.Dir, lockArgs(request)...); err != nil {
		return err
	}

	return c.run(ctx, request.Dir, buildArgs(request)...)
}

// validateComposeRequest rejects a nil receiver, a nil context, or an
// incomplete request before any process starts.
func validateComposeRequest(
	ctx context.Context,
	composer *Composer,
	request image.ComposeRequest,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if composer == nil {
		return errors.New("apko composer is nil")
	}
	if request.Dir == "" {
		return errors.New("directory is empty")
	}
	if request.Config == "" {
		return errors.New("config is empty")
	}
	if request.Repository == "" {
		return errors.New("repository is empty")
	}
	if request.Keyring == "" {
		return errors.New("keyring is empty")
	}
	if request.Lockfile == "" {
		return errors.New("lockfile is empty")
	}
	if request.SBOMPath == "" {
		return errors.New("SBOM path is empty")
	}
	if request.Layout == "" {
		return errors.New("layout is empty")
	}
	if request.Reference == "" {
		return errors.New("reference is empty")
	}
	if request.BuildDate == "" {
		return errors.New("build date is empty")
	}
	if len(request.Arches) == 0 {
		return errors.New("arches are empty")
	}
	for i, arch := range request.Arches {
		if arch == "" {
			return fmt.Errorf("arch %d is empty", i)
		}
	}
	for i, annotation := range request.Annotations {
		if annotation.Key == "" {
			return fmt.Errorf("annotation %d key is empty", i)
		}
	}

	return nil
}

// lockArgs returns the argv for `apko lock`.
func lockArgs(request image.ComposeRequest) []string {
	args := []string{"lock"}
	args = append(args, archFlags(request.Arches)...)
	args = append(args,
		"--repository-append",
		request.Repository,
		"--keyring-append",
		request.Keyring,
		"--output",
		request.Lockfile,
		request.Config,
	)

	return args
}

// buildArgs returns the argv for `apko build`.
func buildArgs(request image.ComposeRequest) []string {
	args := []string{"build"}
	args = append(args, archFlags(request.Arches)...)
	args = append(args,
		"--repository-append",
		request.Repository,
		"--keyring-append",
		request.Keyring,
		"--lockfile",
		request.Lockfile,
		"--build-date",
		request.BuildDate,
		"--sbom-path",
		request.SBOMPath,
	)
	args = append(args, annotationFlags(request.Annotations)...)
	args = append(args, request.Config, request.Reference, request.Layout)

	return args
}

// archFlags returns one `--arch` flag pair per architecture, in order.
func archFlags(arches []image.APKArch) []string {
	flags := make([]string, 0, flagsPerArch*len(arches))
	for _, arch := range arches {
		flags = append(flags, "--arch", string(arch))
	}

	return flags
}

// annotationFlags returns one `--annotations key:value` pair per
// annotation, in order.
func annotationFlags(annotations []image.Annotation) []string {
	flags := make([]string, 0, flagsPerAnnotation*len(annotations))
	for _, annotation := range annotations {
		flags = append(flags, "--annotations", annotation.Key+":"+annotation.Value)
	}

	return flags
}

// run starts one apko invocation in dir and maps process failure to an error.
//
// The argument list is a fixed slice, never a shell string. args[0] is
// the apko subcommand name used in error text. dir is the child working
// directory.
func (c *Composer) run(ctx context.Context, dir string, args ...string) error {
	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    c.path,
		Args:    args,
		Dir:     dir,
		Env:     c.environ,
		Stderr:  c.stderr,
	})
	if err != nil {
		subcommand := args[0]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("apko %s: %w", subcommand, ctxErr)
		}

		return commandError(subcommand, err)
	}

	return nil
}

// commandError formats an apko process failure for subcommand.
func commandError(subcommand string, err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("apko %s: %w", subcommand, err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("apko %s: %w", subcommand, err)
	}
	if runErr.StderrTail() == "" {
		return fmt.Errorf("apko %s: exit %d", subcommand, exitCode)
	}

	return fmt.Errorf("apko %s: exit %d: %s", subcommand, exitCode, runErr.StderrTail())
}
