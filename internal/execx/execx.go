package execx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const (
	// bytesPerKiB is the number of bytes in a kibibyte.
	bytesPerKiB = 1024
	// stderrTailKiB is the stderr tail retained after a process failure.
	stderrTailKiB = 4
	// stderrTailLimit is the maximum number of trailing stderr bytes retained
	// after a process failure.
	stderrTailLimit = stderrTailKiB * bytesPerKiB
	// waitDelay is how long a command waits for leaked child I/O after the
	// process exits or its context is canceled.
	waitDelay = 5 * time.Second
)

// Command describes one external program invocation.
type Command struct {
	// Program is the PATH name used when Path is empty.
	Program string

	// Path overrides Program when nonempty. Run resolves the selected value
	// through [exec.LookPath] at invocation time.
	Path string

	// Args is the argument list excluding argv[0]. Run passes it directly to
	// [exec.CommandContext] and never invokes a shell.
	Args []string

	// Dir is the child working directory. An empty value inherits the parent
	// working directory.
	Dir string

	// Env is the child environment. A nil value inherits the parent
	// environment; a nonnil value is used as-is.
	Env []string

	// Stdout receives child stdout. A nil value discards stdout.
	Stdout io.Writer

	// Stderr receives child stderr while the process runs. A nil value discards
	// the live stream. Run retains a bounded tail independently.
	Stderr io.Writer
}

// RunError reports a process start, wait, or nonzero-exit failure.
//
// It retains the underlying error and a bounded stderr tail without retaining
// argv or the child environment.
type RunError struct {
	// err is the process failure returned by [exec.Cmd.Run].
	err error

	// exitCode is the process exit code when exited is true.
	exitCode int

	// exited reports whether the process returned an exit status.
	exited bool

	// stderrTail is the bounded trailing stderr captured during the run.
	stderrTail string
}

// Run resolves and invokes command once.
//
// Run rejects a nil context and an empty executable selection before starting
// a process. It returns the context error when cancellation or deadline expiry
// ends the process. Other process failures are returned as [*RunError].
func Run(ctx context.Context, command Command) error {
	if ctx == nil {
		return errors.New("context is nil")
	}

	name := command.Path
	if name == "" {
		name = command.Program
	}
	if name == "" {
		return errors.New("executable is empty")
	}

	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, path, command.Args...)
	cmd.Dir = command.Dir
	if command.Env != nil {
		cmd.Env = command.Env
	}
	cmd.Stdout = outputOrDiscard(command.Stdout)

	tail := newTailBuffer(stderrTailLimit)
	cmd.Stderr = tail
	if command.Stderr != nil {
		cmd.Stderr = io.MultiWriter(command.Stderr, tail)
	}
	cmd.WaitDelay = waitDelay

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		return newRunError(err, tail.String())
	}

	return nil
}

// Error returns the underlying process error text.
func (e *RunError) Error() string {
	if e == nil || e.err == nil {
		return "process run failed"
	}

	return e.err.Error()
}

// Unwrap returns the underlying process error.
func (e *RunError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

// ExitCode returns the process exit code and whether one was available.
func (e *RunError) ExitCode() (int, bool) {
	if e == nil || !e.exited {
		return 0, false
	}

	return e.exitCode, true
}

// StderrTail returns the bounded trailing stderr captured during the run.
func (e *RunError) StderrTail() string {
	if e == nil {
		return ""
	}

	return e.stderrTail
}

// newRunError builds typed process failure metadata from err and stderrTail.
func newRunError(err error, stderrTail string) *RunError {
	runErr := &RunError{
		err:        err,
		stderrTail: stderrTail,
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		runErr.exitCode = exitErr.ExitCode()
		runErr.exited = true
	}

	return runErr
}

// outputOrDiscard returns output when nonnil and [io.Discard] otherwise.
func outputOrDiscard(output io.Writer) io.Writer {
	if output == nil {
		return io.Discard
	}

	return output
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
