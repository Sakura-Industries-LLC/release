package goprof

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	// defaultBinary is the PATH name used when [GoReleaserOptions.Path]
	// is empty.
	defaultBinary = "goreleaser"
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
	// holding the write end would hang [RunGoReleaser] forever without
	// this bound.
	waitDelay = 5 * time.Second
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
// argument slice through [exec.CommandContext]. `--clean` removes the
// distribution directory first. `--skip=publish` is a command-line
// boundary against publication that complements `release.disable: true`
// in the consumer's configuration. Changelog and release-note behavior
// also come from that configuration, not from this argv. GoReleaser has
// no --dist flag, so [GoReleaserOptions.Dist] is validated as a
// [RootName] and never forwarded. A nil context or a zero Dist is
// rejected before any process starts. A nonzero exit returns an error
// that names the exit code and a tail of stderr limited to
// [stderrTailLimit] bytes, with ANSI color sequences stripped. Child
// stdout and stderr are copied to the supplied writers unchanged,
// including color; nil writers discard that stream. The error never
// includes the child environment.
func RunGoReleaser(ctx context.Context, options GoReleaserOptions) error {
	if err := validateGoReleaser(ctx, options); err != nil {
		return err
	}

	path, err := resolveBinary(options.Path)
	if err != nil {
		return err
	}

	// Path is a resolved executable. The argument list is a fixed
	// slice, never a shell string.
	cmd := exec.CommandContext(ctx, path, "release", "--clean", "--skip=publish")
	if options.Environ != nil {
		cmd.Env = options.Environ
	}

	stdout := io.Discard
	if options.Stdout != nil {
		stdout = options.Stdout
	}
	cmd.Stdout = stdout

	tail := newTailBuffer(stderrTailLimit)
	stderr := io.Writer(tail)
	if options.Stderr != nil {
		stderr = io.MultiWriter(options.Stderr, tail)
	}
	cmd.Stderr = stderr
	// WaitDelay unblocks Wait if a grandchild still holds the stderr
	// pipe after CommandContext kills only the direct child.
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("goreleaser release: %w", ctxErr)
		}

		return goreleaserError(tail, err)
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

// resolveBinary returns the GoReleaser executable path.
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

// goreleaserError formats a GoReleaser process failure.
//
// The retained stderr tail is stripped of ANSI before it is copied
// into the error so a --json envelope does not embed escape
// sequences. The live [GoReleaserOptions.Stderr] stream is not
// filtered.
func goreleaserError(tail *tailBuffer, err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("goreleaser release: %w", err)
	}
	text := stripANSI(tail.String())
	if len(text) > stderrTailLimit {
		text = text[len(text)-stderrTailLimit:]
	}
	if text == "" {
		return fmt.Errorf("goreleaser release: exit %d", exitErr.ExitCode())
	}

	return fmt.Errorf("goreleaser release: exit %d: %s", exitErr.ExitCode(), text)
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
