package execx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// fixtureProgram is the PATH name used by the resolution test.
	fixtureProgram = "execx-fixture"
	// startWait bounds fixture startup without making it part of the process
	// cancellation contract.
	startWait = 30 * time.Second
	// cancelWait is how quickly a direct child must stop after cancellation.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fixture to start.
	cancelPoll = 10 * time.Millisecond
	// fixtureScript is the shared child-process fixture.
	fixtureScript = `#!/bin/sh
if [ -n "${EXECX_STARTED:-}" ]; then
	: > "$EXECX_STARTED"
fi
if [ -n "${EXECX_RECORD:-}" ]; then
	printf '%s\n' "$@" > "$EXECX_RECORD"
fi
if [ -n "${EXECX_CWD:-}" ]; then
	pwd > "$EXECX_CWD"
fi
if [ -n "${EXECX_ENV_FILE:-}" ]; then
	printf '%s' "${EXECX_VALUE:-}" > "$EXECX_ENV_FILE"
fi
if [ -n "${EXECX_STDOUT:-}" ]; then
	printf '%s' "$EXECX_STDOUT"
fi
if [ -n "${EXECX_STDERR_FILE:-}" ]; then
	cat "$EXECX_STDERR_FILE" >&2
elif [ -n "${EXECX_STDERR:-}" ]; then
	printf '%s' "$EXECX_STDERR" >&2
fi
if [ -n "${EXECX_ORPHAN:-}" ]; then
	sleep "${EXECX_SLEEP:-30}" &
	wait
	exit "${EXECX_EXIT:-0}"
fi
if [ -n "${EXECX_SLEEP:-}" ]; then
	exec sleep "$EXECX_SLEEP"
fi
exit "${EXECX_EXIT:-0}"
`
)

// fakePath is the executable fixture written once by TestMain.
var fakePath string

// TestMain writes the executable fixture before parallel tests can fork.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		os.Exit(m.Run())
	}

	dir, err := os.MkdirTemp("", "execx-test-")
	if err != nil {
		panic(err)
	}

	fakePath = filepath.Join(dir, "fixture")
	if err := os.WriteFile(fakePath, []byte(fixtureScript), 0o700); err != nil {
		panic(err)
	}

	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		panic(err)
	}
	os.Exit(code)
}

// TestRunForwardsProcessConfiguration proves the shared command boundary.
func TestRunForwardsProcessConfiguration(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	require.NoError(t, os.Mkdir(work, 0o700))
	record := filepath.Join(dir, "args")
	cwdFile := filepath.Join(dir, "cwd")
	envFile := filepath.Join(dir, "env")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), Command{
		Program: "unused",
		Path:    fakePath,
		Args:    []string{"first", "second value"},
		Dir:     work,
		Env: fixtureEnv(t,
			"EXECX_RECORD="+record,
			"EXECX_CWD="+cwdFile,
			"EXECX_ENV_FILE="+envFile,
			"EXECX_VALUE=configured",
			"EXECX_STDOUT=standard output",
			"EXECX_STDERR=standard error",
		),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	require.NoError(t, err)

	assert.Equal(t, "first\nsecond value\n", readText(t, record))
	assertSamePath(t, work, strings.TrimSpace(readText(t, cwdFile)))
	assert.Equal(t, "configured", readText(t, envFile))
	assert.Equal(t, "standard output", stdout.String())
	assert.Equal(t, "standard error", stderr.String())
}

// TestRunResolvesProgramFromPATH proves empty Path uses Program at run time.
func TestRunResolvesProgramFromPATH(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	path := filepath.Join(dir, fixtureProgram)
	require.NoError(t, os.Symlink(fakePath, path))
	t.Setenv("PATH", dir)

	var stdout bytes.Buffer
	err := Run(context.Background(), Command{
		Program: fixtureProgram,
		Env:     fixtureEnv(t, "EXECX_STDOUT=resolved"),
		Stdout:  &stdout,
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved", stdout.String())
}

// TestRunNonzeroExitReturnsMetadata proves exit and stderr-tail behavior.
func TestRunNonzeroExitReturnsMetadata(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	head := bytes.Repeat([]byte("H"), stderrTailLimit)
	wantTail := bytes.Repeat([]byte("T"), stderrTailLimit)
	stderrFile := filepath.Join(dir, "stderr")
	require.NoError(t, os.WriteFile(stderrFile, append(head, wantTail...), 0o600))
	var stderr bytes.Buffer

	err := Run(context.Background(), Command{
		Path: fakePath,
		Env: fixtureEnv(t,
			"EXECX_EXIT=7",
			"EXECX_STDERR_FILE="+stderrFile,
		),
		Stderr: &stderr,
	})
	require.Error(t, err)

	var runErr *RunError
	require.ErrorAs(t, err, &runErr)
	code, exited := runErr.ExitCode()
	assert.True(t, exited)
	assert.Equal(t, 7, code)
	assert.Equal(t, string(wantTail), runErr.StderrTail())
	assert.Equal(t, string(append(head, wantTail...)), stderr.String())
	assert.NotContains(t, runErr.Error(), string(wantTail))
}

// TestRunStartFailureUnwrapsCause proves non-exit failures stay inspectable.
func TestRunStartFailureUnwrapsCause(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := filepath.Join(t.TempDir(), "invalid")
	require.NoError(t, os.WriteFile(path, []byte("not an executable format"), 0o700))

	err := Run(context.Background(), Command{Path: path})
	require.Error(t, err)

	var runErr *RunError
	require.ErrorAs(t, err, &runErr)
	_, exited := runErr.ExitCode()
	assert.False(t, exited)
	assert.Error(t, errors.Unwrap(runErr))
}

// TestRunCanceledContextReturnsPromptly proves direct-child cancellation.
func TestRunCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(t, fixtureEnv(t,
		"EXECX_STARTED="+started,
		"EXECX_SLEEP=30",
	), started, cancelWait)

	require.ErrorIs(t, err, context.Canceled)
}

// TestRunCanceledContextUnblocksOrphanGrandchild proves WaitDelay bounds Wait.
func TestRunCanceledContextUnblocksOrphanGrandchild(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(t, fixtureEnv(t,
		"EXECX_STARTED="+started,
		"EXECX_ORPHAN=1",
		"EXECX_SLEEP=30",
	), started, waitDelay+cancelWait)

	require.ErrorIs(t, err, context.Canceled)
}

// TestRunRejectsInvalidInput covers failures before executable resolution.
func TestRunRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     context.Context
		command Command
		want    string
	}{
		{
			name:    "nil context",
			ctx:     nil,
			command: Command{Path: fakePath},
			want:    "context is nil",
		},
		{
			name:    "empty executable",
			ctx:     context.Background(),
			command: Command{},
			want:    "executable is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := Run(test.ctx, test.command)
			require.EqualError(t, err, test.want)
		})
	}
}

// TestRunResolutionFailureNamesExecutable proves deferred lookup errors.
func TestRunResolutionFailureNamesExecutable(t *testing.T) {
	t.Parallel()

	const missing = "release-execx-missing-binary"
	err := Run(context.Background(), Command{Program: missing})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve "+missing)

	var runErr *RunError
	assert.NotErrorAs(t, err, &runErr)
}

// cancelAfterStart starts the fixture, cancels it after the marker appears,
// and requires Run to return within bound.
func cancelAfterStart(t *testing.T, env []string, started string, bound time.Duration) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Command{Path: fakePath, Env: env})
	}()

	deadline := time.Now().Add(startWait)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			require.NoError(t, err)
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(cancelPoll)
	}

	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatal("Run did not return after cancellation")

		return nil
	}
}

// fixtureEnv copies the process environment and appends extra entries.
func fixtureEnv(t *testing.T, extra ...string) []string {
	t.Helper()

	return append(os.Environ(), extra...)
}

// readText reads path or fails the test.
func readText(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}

// assertSamePath requires want and got to resolve to the same path.
func assertSamePath(t *testing.T, want, got string) {
	t.Helper()

	wantResolved, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

// skipWindows skips POSIX shell fixtures on Windows.
func skipWindows(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
}
