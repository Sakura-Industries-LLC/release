package goprof

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/meigma/release/internal/execx"
)

const (
	// defaultBinary is the PATH name used when [GoReleaserOptions.Path]
	// is empty.
	defaultBinary = "goreleaser"

	// escapeByte is the ASCII ESC that starts a CSI sequence.
	escapeByte = 0x1b
	// csiIntroducer is the byte that follows ESC in a CSI sequence.
	csiIntroducer = '['
	// csiFinalMin is the first CSI final-byte value (inclusive).
	csiFinalMin = 0x40
	// csiFinalMax is the last CSI final-byte value (inclusive).
	csiFinalMax = 0x7e
)

// GoReleaserOptions configures one GoReleaser release run.
type GoReleaserOptions struct {
	// Path is the GoReleaser executable. An empty path resolves
	// "goreleaser" from PATH with [exec.LookPath] at run time.
	Path string

	// Dist is the distribution directory GoReleaser is configured to
	// write. It must be a [RootName]: GoReleaser has no --dist flag, so
	// the value is validated, not passed. The zero value is rejected
	// before any process starts.
	Dist RootName

	// Environ is the child process environment. A nil value inherits
	// [os.Environ]. The slice is used as-is and is never logged.
	Environ []string

	// Stdout receives GoReleaser progress output. A nil value discards
	// it.
	Stdout io.Writer

	// Stderr receives GoReleaser diagnostics while the process runs. A
	// nil value discards them. A nonzero exit still captures a bounded
	// tail for the returned error.
	Stderr io.Writer
}

// RunGoReleaser builds the release bundle with GoReleaser.
//
// It runs `goreleaser release --clean --skip=publish` as an explicit
// argument slice through [execx.Run]. `--clean` removes the distribution
// directory first. `--skip=publish` is a command-line boundary against
// publication that complements `release.disable: true` in the consumer's
// configuration. Changelog and release-note behavior also come from that
// configuration, not from this argv. GoReleaser has no --dist flag, so
// [GoReleaserOptions.Dist] is validated as a [RootName] and never
// forwarded. A nil context or a zero Dist is rejected before any process
// starts. A nonzero exit returns an error that names the exit code and
// includes a bounded tail of stderr with ANSI color sequences stripped.
// Child stdout and stderr are copied to the supplied writers unchanged,
// including color; nil writers discard that stream. The error never
// includes the child environment.
func RunGoReleaser(ctx context.Context, options GoReleaserOptions) error {
	if err := validateGoReleaser(ctx, options); err != nil {
		return err
	}

	err := execx.Run(ctx, execx.Command{
		Program: defaultBinary,
		Path:    options.Path,
		Args:    []string{"release", "--clean", "--skip=publish"},
		Env:     options.Environ,
		Stdout:  options.Stdout,
		Stderr:  options.Stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("goreleaser release: %w", ctxErr)
		}

		return goreleaserError(err)
	}

	return nil
}

// validateGoReleaser rejects a nil context or a zero Dist before any
// process starts.
//
// Dist is a [RootName]; construction already rejected empty, ".",
// "..", and path-separator values. The remaining check is the zero
// value that a caller can still pass without going through
// [ParseRootName].
func validateGoReleaser(ctx context.Context, options GoReleaserOptions) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if options.Dist == "" {
		return errors.New("dist is empty")
	}

	return nil
}

// goreleaserError formats a GoReleaser process failure.
//
// The retained stderr tail is stripped of ANSI before it is copied into
// the error so a --json envelope does not embed escape sequences. The
// live [GoReleaserOptions.Stderr] stream is not filtered.
func goreleaserError(err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return fmt.Errorf("goreleaser release: %w", err)
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return fmt.Errorf("goreleaser release: %w", err)
	}
	text := stripANSI(runErr.StderrTail())
	if text == "" {
		return fmt.Errorf("goreleaser release: exit %d", exitCode)
	}

	return fmt.Errorf("goreleaser release: exit %d: %s", exitCode, text)
}

// stripANSI removes CSI sequences and bare ESC bytes from s.
//
// GoReleaser writes color even when stderr is a pipe. The live
// [GoReleaserOptions.Stderr] stream keeps those sequences so a human
// can read the workflow log. The retained tail is stripped here so a
// --json envelope does not embed raw escapes. This is not a general
// terminal sanitizer: it handles the CSI form GoReleaser uses
// (`ESC [ … final-byte`) and a lone ESC.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != escapeByte {
			b.WriteByte(s[i])
			i++

			continue
		}
		i++
		if i >= len(s) {
			break
		}
		if s[i] != csiIntroducer {
			continue
		}
		i++
		for i < len(s) {
			c := s[i]
			i++
			if c >= csiFinalMin && c <= csiFinalMax {
				break
			}
		}
	}

	return b.String()
}
