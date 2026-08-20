package goprof

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDist is a valid distribution directory basename.
var testDist = RootName("dist")

const (
	// startWait is how long cancelAfterStart waits for the fake to
	// create its start marker. The budget is load-dependent, not a
	// contract, and only bounds how a hung fixture is reported.
	startWait = 30 * time.Second
	// cancelWait is how long RunGoReleaser must return after cancel.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fake to start.
	cancelPoll           = 10 * time.Millisecond
	fakeGoreleaserScript = `#!/bin/sh
if [ -n "${GORELEASER_STARTED:-}" ]; then
	: > "$GORELEASER_STARTED"
fi
if [ -n "${GORELEASER_RECORD:-}" ]; then
	{
		printf '%s\n' "$0"
		printf '%s\n' "$@"
	} > "$GORELEASER_RECORD"
fi
if [ -n "${GORELEASER_STDOUT:-}" ]; then
	printf '%s' "$GORELEASER_STDOUT"
fi
if [ -n "${GORELEASER_STDERR_FILE:-}" ]; then
	cat "$GORELEASER_STDERR_FILE" >&2
elif [ -n "${GORELEASER_STDERR:-}" ]; then
	printf '%s' "$GORELEASER_STDERR" >&2
fi
if [ -n "${GORELEASER_ORPHAN:-}" ]; then
	sleep "${GORELEASER_SLEEP:-30}" &
	wait
	exit "${GORELEASER_EXIT:-0}"
fi
if [ -n "${GORELEASER_SLEEP:-}" ]; then
	exec sleep "$GORELEASER_SLEEP"
fi
exit "${GORELEASER_EXIT:-0}"
`
)

func TestRunGoReleaserInvokesReleaseSkipPublish(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path:    path,
		Dist:    testDist,
		Environ: fakeEnviron(t, "GORELEASER_RECORD="+record),
	})
	require.NoError(t, err)
	assertRecordedArgv(t, record, path)
}

func TestRunGoReleaserUsesSuppliedPath(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path:    path,
		Dist:    testDist,
		Environ: fakeEnviron(t, "GORELEASER_RECORD="+record, "PATH="+dir),
	})
	require.NoError(t, err)
	assertRecordedArgv(t, record, path)
}

func TestRunGoReleaserResolvesEmptyPath(t *testing.T) {
	skipWindows(t)

	t.Run("finds goreleaser on PATH", func(t *testing.T) {
		dir := t.TempDir()
		record := filepath.Join(dir, "args")
		t.Setenv("PATH", filepath.Dir(fakePath))
		t.Setenv("GORELEASER_RECORD", record)

		err := RunGoReleaser(context.Background(), GoReleaserOptions{Dist: testDist})
		require.NoError(t, err)
		assertRecordedArgv(t, record, fakePath)
	})

	t.Run("missing goreleaser is a clear error", func(t *testing.T) {
		started := filepath.Join(t.TempDir(), "started")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("GORELEASER_STARTED", started)

		err := RunGoReleaser(context.Background(), GoReleaserOptions{Dist: testDist})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "goreleaser")
		assert.Contains(t, err.Error(), "PATH")
		assert.NoFileExists(t, started)
	})

	t.Run("unresolvable Path is a clear error", func(t *testing.T) {
		started := filepath.Join(t.TempDir(), "started")
		missing := filepath.Join(t.TempDir(), "goreleaser")

		err := RunGoReleaser(context.Background(), GoReleaserOptions{
			Path:    missing,
			Dist:    testDist,
			Environ: fakeEnviron(t, "GORELEASER_STARTED="+started),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve")
		assert.Contains(t, err.Error(), missing)
		assert.NoFileExists(t, started)
	})
}

func TestRunGoReleaserNonzeroExitIncludesCodeAndStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path: path,
		Dist: testDist,
		Environ: fakeEnviron(t,
			"GORELEASER_EXIT=3",
			"GORELEASER_STDERR=denied by signing",
		),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 3")
	assert.Contains(t, err.Error(), "denied by signing")
}

func TestRunGoReleaserTruncatesLargeStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t)
	head := bytes.Repeat([]byte("H"), stderrTailLimit)
	tail := bytes.Repeat([]byte("T"), stderrTailLimit)
	stderrFile := filepath.Join(dir, "stderr.txt")
	require.NoError(t, os.WriteFile(stderrFile, append(head, tail...), 0o600))

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path: path,
		Dist: testDist,
		Environ: fakeEnviron(t,
			"GORELEASER_EXIT=1",
			"GORELEASER_STDERR_FILE="+stderrFile,
		),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 1")
	assert.NotContains(t, err.Error(), string(head))
	assert.Contains(t, err.Error(), string(tail))
	assert.LessOrEqual(t, strings.Count(err.Error(), "T"), stderrTailLimit)
}

func TestRunGoReleaserStripsANSIFromErrorTail(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)
	// Matches the CSI sequences GoReleaser writes for "starting release",
	// plus a bare ESC that must not eat the following visible byte.
	colored := "\x1b[1;94m  •\x1b[m \x1b[1mstarting release\x1b[m\n\x1bpartial"
	var sink bytes.Buffer

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path: path,
		Dist: testDist,
		Environ: fakeEnviron(t,
			"GORELEASER_EXIT=1",
			"GORELEASER_STDERR="+colored,
		),
		Stderr: &sink,
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\x1b")
	assert.Contains(t, err.Error(), "  • starting release")
	assert.Contains(t, err.Error(), "partial")
	assert.Equal(t, colored, sink.String())
}

func TestRunGoReleaserStripsANSIThenAppliesTailLimit(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t)
	// Color around every visible byte so the raw stream is far larger
	// than the bound. The retained tail is still capped after stripping.
	var raw bytes.Buffer
	head := bytes.Repeat([]byte("H"), stderrTailLimit)
	tail := bytes.Repeat([]byte("T"), stderrTailLimit)
	for _, c := range append(head, tail...) {
		raw.WriteString("\x1b[1m")
		raw.WriteByte(c)
		raw.WriteString("\x1b[m")
	}
	stderrFile := filepath.Join(dir, "stderr.txt")
	require.NoError(t, os.WriteFile(stderrFile, raw.Bytes(), 0o600))
	var sink bytes.Buffer

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path: path,
		Dist: testDist,
		Environ: fakeEnviron(t,
			"GORELEASER_EXIT=1",
			"GORELEASER_STDERR_FILE="+stderrFile,
		),
		Stderr: &sink,
	})
	require.Error(t, err)
	stripped := stripANSI(string(raw.Bytes()[len(raw.Bytes())-stderrTailLimit:]))
	if len(stripped) > stderrTailLimit {
		stripped = stripped[len(stripped)-stderrTailLimit:]
	}
	assert.Contains(t, err.Error(), "exit 1")
	assert.NotContains(t, err.Error(), "\x1b")
	assert.Contains(t, err.Error(), stripped)
	assert.LessOrEqual(t, strings.Count(err.Error(), "T")+strings.Count(err.Error(), "H"), stderrTailLimit)
	assert.Equal(t, raw.String(), sink.String())
}

func TestRunGoReleaserWritesStdoutAndStderrSinks(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path:    path,
		Dist:    testDist,
		Environ: fakeEnviron(t, "GORELEASER_STDOUT=progress line", "GORELEASER_STDERR=diagnostic line"),
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	require.NoError(t, err)
	assert.Equal(t, "progress line", stdout.String())
	assert.Equal(t, "diagnostic line", stderr.String())
}

func TestRunGoReleaserNilWritersDiscardOutput(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)

	err := RunGoReleaser(context.Background(), GoReleaserOptions{
		Path: path,
		Dist: testDist,
		Environ: fakeEnviron(t,
			"GORELEASER_STDOUT=progress line",
			"GORELEASER_STDERR=diagnostic line",
		),
	})
	require.NoError(t, err)
}

func TestRunGoReleaserCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		fakeEnviron(t, "GORELEASER_STARTED="+started, "GORELEASER_SLEEP=30"),
		started,
		cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRunGoReleaserCanceledContextUnblocksOrphanGrandchild(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		fakeEnviron(t, "GORELEASER_STARTED="+started, "GORELEASER_ORPHAN=1", "GORELEASER_SLEEP=30"),
		started,
		waitDelay+cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// The subtests share one marker file, so this test does not run in parallel.
func TestRunGoReleaserRejectsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t)
	environ := fakeEnviron(t, "GORELEASER_STARTED="+started)
	valid := GoReleaserOptions{Path: path, Dist: testDist, Environ: environ}

	tests := []struct {
		name    string
		ctx     context.Context
		options GoReleaserOptions
		want    string
	}{
		{
			name:    "nil context",
			ctx:     nil,
			options: valid,
			want:    "context is nil",
		},
		{
			name: "empty Dist",
			ctx:  context.Background(),
			options: GoReleaserOptions{
				Path:    path,
				Dist:    "",
				Environ: environ,
			},
			want: "dist is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			err := RunGoReleaser(test.ctx, test.options)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
			assert.NoFileExists(t, started)
		})
	}
}

// skipWindows skips POSIX shell fixtures on Windows.
func skipWindows(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("posix shell fixture")
	}
}

// cancelAfterStart runs RunGoReleaser, cancels after the fake starts,
// and returns the call error. It fails the test if the call exceeds
// bound after cancel. Waiting for the start marker uses [startWait].
func cancelAfterStart(
	t *testing.T,
	path string,
	environ []string,
	started string,
	bound time.Duration,
) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- RunGoReleaser(ctx, GoReleaserOptions{
			Path:    path,
			Dist:    testDist,
			Environ: environ,
		})
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(started)

		return err == nil
	}, startWait, cancelPoll)
	cancel()

	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("RunGoReleaser did not return within %s after cancel", bound)
	}

	return nil
}

// fakePath is the shared fake GoReleaser executable. TestMain writes it
// once because a parallel sibling's fork/exec can inherit an open write
// descriptor and fail with ETXTBSY on Linux.
var fakePath string

// TestMain writes the fake GoReleaser executable once before any test
// can exec it. Writing per test races on Linux: a parallel sibling's
// fork/exec can inherit an open write descriptor and fail with ETXTBSY.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "goreleaser-fake-")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, defaultBinary)
	if err := os.WriteFile(path, []byte(fakeGoreleaserScript), 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	fakePath = path
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeFake returns the shared fake GoReleaser executable written by TestMain.
func writeFake(t *testing.T) string {
	t.Helper()

	return fakePath
}

// fakeEnviron copies the process environment and appends extra KEY=value pairs.
func fakeEnviron(t *testing.T, extra ...string) []string {
	t.Helper()

	return append(append([]string{}, os.Environ()...), extra...)
}

// assertRecordedArgv requires the fake to have recorded the supplied
// binary path and the contract argv.
func assertRecordedArgv(t *testing.T, record, wantPath string) {
	t.Helper()

	body, err := os.ReadFile(record)
	require.NoError(t, err)
	got := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	require.GreaterOrEqual(t, len(got), 1)
	assert.Equal(t, wantPath, got[0])
	assert.Equal(t, []string{
		"release",
		"--clean",
		"--skip=publish",
	}, got[1:])
}
