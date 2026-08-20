package cosign

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

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testImage  = "ghcr.io/owner/repo"
	// startWait is how long cancelAfterStart waits for the fake to
	// create its start marker. The budget is load-dependent, not a
	// contract, and only bounds how a hung fixture is reported.
	startWait = 30 * time.Second
	// cancelWait is how long SignRecursive must return after cancel.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fake to start.
	cancelPoll       = 10 * time.Millisecond
	fakeCosignScript = `#!/bin/sh
if [ -n "${COSIGN_STARTED:-}" ]; then
	: > "$COSIGN_STARTED"
fi
if [ -n "${COSIGN_RECORD:-}" ]; then
	printf '%s\n' "$@" > "$COSIGN_RECORD"
fi
if [ -n "${COSIGN_STDERR_FILE:-}" ]; then
	cat "$COSIGN_STDERR_FILE" >&2
elif [ -n "${COSIGN_STDERR:-}" ]; then
	printf '%s' "$COSIGN_STDERR" >&2
fi
if [ -n "${COSIGN_ORPHAN:-}" ]; then
	sleep "${COSIGN_SLEEP:-30}" &
	wait
	exit "${COSIGN_EXIT:-0}"
fi
if [ -n "${COSIGN_SLEEP:-}" ]; then
	exec sleep "$COSIGN_SLEEP"
fi
exit "${COSIGN_EXIT:-0}"
`
)

func TestSignRecursiveInvokesCosign(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)

	err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "COSIGN_RECORD="+record),
	}).SignRecursive(context.Background(), mustRef(t))
	require.NoError(t, err)
	assertRecordedArgv(t, record)
}

func TestSignRecursiveNonzeroExitIncludesCodeAndStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)

	err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"COSIGN_EXIT=3",
			"COSIGN_STDERR=denied by fulcio",
		),
	}).SignRecursive(context.Background(), mustRef(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 3")
	assert.Contains(t, err.Error(), "denied by fulcio")
}

func TestSignRecursiveTruncatesLargeStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t)
	head := bytes.Repeat([]byte("H"), stderrTailLimit)
	tail := bytes.Repeat([]byte("T"), stderrTailLimit)
	stderrFile := filepath.Join(dir, "stderr.txt")
	require.NoError(t, os.WriteFile(stderrFile, append(head, tail...), 0o600))

	err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"COSIGN_EXIT=1",
			"COSIGN_STDERR_FILE="+stderrFile,
		),
	}).SignRecursive(context.Background(), mustRef(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 1")
	assert.NotContains(t, err.Error(), string(head))
	assert.Contains(t, err.Error(), string(tail))
	assert.LessOrEqual(t, strings.Count(err.Error(), "T"), stderrTailLimit)
}

func TestSignRecursiveResolvesEmptyPath(t *testing.T) {
	skipWindows(t)

	t.Run("finds cosign on PATH", func(t *testing.T) {
		dir := t.TempDir()
		record := filepath.Join(dir, "args")
		t.Setenv("PATH", filepath.Dir(fakeSignPath))
		t.Setenv("COSIGN_RECORD", record)

		err := New(Options{}).SignRecursive(context.Background(), mustRef(t))
		require.NoError(t, err)
		assertRecordedArgv(t, record)
	})

	t.Run("missing cosign is a clear error", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		err := New(Options{}).SignRecursive(context.Background(), mustRef(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cosign")
		assert.Contains(t, err.Error(), "PATH")
	})
}

func TestSignRecursiveCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		fakeEnviron(t, "COSIGN_STARTED="+started, "COSIGN_SLEEP=30"),
		started,
		cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSignRecursiveCanceledContextUnblocksOrphanGrandchild(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		fakeEnviron(t, "COSIGN_STARTED="+started, "COSIGN_ORPHAN=1", "COSIGN_SLEEP=30"),
		started,
		waitDelay+cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSignRecursiveWritesStderrSink(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)
	var sink bytes.Buffer

	err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "COSIGN_STDERR=diagnostic line"),
		Stderr:  &sink,
	}).SignRecursive(context.Background(), mustRef(t))
	require.NoError(t, err)
	assert.Equal(t, "diagnostic line", sink.String())
}

// The subtests share one marker file, so this test does not run in parallel.
func TestSignRecursiveRejectsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t)
	environ := fakeEnviron(t, "COSIGN_STARTED="+started)
	signer := New(Options{Path: path, Environ: environ})

	tests := []struct {
		name   string
		ctx    context.Context
		signer *Signer
		ref    puboci.DigestRef
		want   string
	}{
		{
			name:   "nil context",
			ctx:    nil,
			signer: signer,
			ref:    mustRef(t),
			want:   "context is nil",
		},
		{
			name:   "nil signer",
			ctx:    context.Background(),
			signer: nil,
			ref:    mustRef(t),
			want:   "cosign signer is nil",
		},
		{
			name:   "zero reference",
			ctx:    context.Background(),
			signer: signer,
			ref:    puboci.DigestRef{},
			want:   "digest reference is empty",
		},
		{
			name:   "empty image",
			ctx:    context.Background(),
			signer: signer,
			ref:    puboci.DigestRef{Digest: mustDigest(t)},
			want:   "digest reference is empty",
		},
		{
			name:   "empty digest",
			ctx:    context.Background(),
			signer: signer,
			ref:    puboci.DigestRef{Image: mustImage(t)},
			want:   "digest reference is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			err := test.signer.SignRecursive(test.ctx, test.ref)
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

// cancelAfterStart runs SignRecursive, cancels after the fake starts, and
// returns the call error. It fails the test if the call exceeds bound
// after cancel. Waiting for the start marker uses [startWait].
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
	ref := mustRef(t)
	done := make(chan error, 1)
	go func() {
		done <- New(Options{Path: path, Environ: environ}).SignRecursive(ctx, ref)
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
		t.Fatalf("SignRecursive did not return within %s after cancel", bound)
	}

	return nil
}

// fakeSignPath is the shared fake cosign used by Signer tests.
// TestMain writes it once because a parallel sibling's fork/exec can
// inherit an open write descriptor and fail with ETXTBSY on Linux.
var fakeSignPath string

// fakeVerifyPath is the shared fake cosign used by Verifier tests.
// TestMain writes it once because a parallel sibling's fork/exec can
// inherit an open write descriptor and fail with ETXTBSY on Linux.
var fakeVerifyPath string

// TestMain writes the package's fake cosign executables once before any
// test can exec them. Writing per test races on Linux: a parallel
// sibling's fork/exec can inherit an open write descriptor and fail
// with ETXTBSY.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cosign-fake-")
	if err != nil {
		panic(err)
	}
	signDir := filepath.Join(dir, "sign")
	verifyDir := filepath.Join(dir, "verify")
	if err := os.Mkdir(signDir, 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	if err := os.Mkdir(verifyDir, 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	signPath := filepath.Join(signDir, defaultBinary)
	if err := os.WriteFile(signPath, []byte(fakeCosignScript), 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	verifyPath := filepath.Join(verifyDir, defaultBinary)
	if err := os.WriteFile(verifyPath, []byte(verifyFakeScript), 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	fakeSignPath = signPath
	fakeVerifyPath = verifyPath
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeFake returns the shared fake cosign executable written by TestMain.
func writeFake(t *testing.T) string {
	t.Helper()

	return fakeSignPath
}

// fakeEnviron copies the process environment and appends extra KEY=value pairs.
func fakeEnviron(t *testing.T, extra ...string) []string {
	t.Helper()

	return append(append([]string{}, os.Environ()...), extra...)
}

// assertRecordedArgv requires the fake to have recorded the contract argv.
func assertRecordedArgv(t *testing.T, record string) {
	t.Helper()

	body, err := os.ReadFile(record)
	require.NoError(t, err)
	got := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	assert.Equal(t, []string{
		"sign",
		"--yes",
		"--recursive",
		testImage + "@" + testDigest,
	}, got)
}

// mustRef returns the fixture digest reference.
func mustRef(t *testing.T) puboci.DigestRef {
	t.Helper()

	return puboci.DigestRef{Image: mustImage(t), Digest: mustDigest(t)}
}

// mustImage parses the fixture image.
func mustImage(t *testing.T) puboci.Image {
	t.Helper()

	image, err := puboci.ParseImage(testImage)
	require.NoError(t, err)

	return image
}

// mustDigest parses the fixture digest.
func mustDigest(t *testing.T) rel.Digest {
	t.Helper()

	digest, err := rel.ParseDigest(testDigest)
	require.NoError(t, err)

	return digest
}
