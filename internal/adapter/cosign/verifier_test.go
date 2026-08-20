package cosign

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	testPayload  = "checksums.txt"
	testBundle   = "checksums.txt.sigstore.json"
	testIdentity = "https://github.com/owner/repo/.github/workflows/go-pre-publish.yml@refs/heads/main"
	testIssuer   = "https://token.actions.githubusercontent.com"
	// verifyFakeScript is a POSIX fake cosign that records argv and cwd.
	verifyFakeScript = `#!/bin/sh
if [ -n "${COSIGN_STARTED:-}" ]; then
	: > "$COSIGN_STARTED"
fi
if [ -n "${COSIGN_RECORD:-}" ]; then
	printf '%s\n' "$@" > "$COSIGN_RECORD"
fi
if [ -n "${COSIGN_CWD:-}" ]; then
	pwd > "$COSIGN_CWD"
fi
if [ -n "${COSIGN_STDERR_FILE:-}" ]; then
	cat "$COSIGN_STDERR_FILE" >&2
elif [ -n "${COSIGN_STDERR:-}" ]; then
	printf '%s' "$COSIGN_STDERR" >&2
fi
if [ -n "${COSIGN_SLEEP:-}" ]; then
	exec sleep "$COSIGN_SLEEP"
fi
exit "${COSIGN_EXIT:-0}"
`
)

func TestVerifyInvokesCosign(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	work := mkdirWork(t, dir)
	record := filepath.Join(dir, "args")
	cwdFile := filepath.Join(dir, "cwd")
	path := writeVerifyFake(t)

	err := NewVerifier(VerifierOptions{
		Path:    path,
		Dir:     work,
		Environ: fakeEnviron(t, "COSIGN_RECORD="+record, "COSIGN_CWD="+cwdFile),
	}).Verify(context.Background(), mustRequest())
	require.NoError(t, err)
	assertRecordedVerifyArgv(t, record)
	assertRecordedDir(t, cwdFile, work)
}

func TestVerifyNonzeroExitIncludesCodeAndStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeVerifyFake(t)

	err := NewVerifier(VerifierOptions{
		Path: path,
		Dir:  mkdirWork(t, dir),
		Environ: fakeEnviron(t,
			"COSIGN_EXIT=3",
			"COSIGN_STDERR=identity mismatch",
		),
	}).Verify(context.Background(), mustRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 3")
	assert.Contains(t, err.Error(), "identity mismatch")
}

func TestVerifyResolvesEmptyPath(t *testing.T) {
	skipWindows(t)

	t.Run("finds cosign on PATH", func(t *testing.T) {
		dir := t.TempDir()
		work := mkdirWork(t, dir)
		record := filepath.Join(dir, "args")
		t.Setenv("PATH", filepath.Dir(fakeVerifyPath))
		t.Setenv("COSIGN_RECORD", record)

		err := NewVerifier(VerifierOptions{Dir: work}).Verify(context.Background(), mustRequest())
		require.NoError(t, err)
		assertRecordedVerifyArgv(t, record)
	})

	t.Run("missing cosign is a clear error", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		err := NewVerifier(VerifierOptions{Dir: t.TempDir()}).Verify(context.Background(), mustRequest())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cosign")
		assert.Contains(t, err.Error(), "PATH")
	})
}

func TestVerifyCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelVerifyAfterStart(
		t,
		writeVerifyFake(t),
		mkdirWork(t, dir),
		fakeEnviron(t, "COSIGN_STARTED="+started, "COSIGN_SLEEP=30"),
		started,
		cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// The subtests share one marker file, so this test does not run in parallel.
func TestVerifyRejectsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeVerifyFake(t)
	work := mkdirWork(t, dir)
	environ := fakeEnviron(t, "COSIGN_STARTED="+started)
	verifier := NewVerifier(VerifierOptions{Path: path, Dir: work, Environ: environ})
	request := mustRequest()

	tests := []struct {
		name     string
		ctx      context.Context
		verifier *Verifier
		request  pubgh.BlobVerification
		want     string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			verifier: verifier,
			request:  request,
			want:     "context is nil",
		},
		{
			name:     "nil verifier",
			ctx:      context.Background(),
			verifier: nil,
			request:  request,
			want:     "cosign verifier is nil",
		},
		{
			name:     "empty directory",
			ctx:      context.Background(),
			verifier: NewVerifier(VerifierOptions{Path: path, Environ: environ}),
			request:  request,
			want:     "distribution directory is empty",
		},
		{
			name:     "empty payload",
			ctx:      context.Background(),
			verifier: verifier,
			request:  pubgh.BlobVerification{Bundle: testBundle, Identity: testIdentity, Issuer: testIssuer},
			want:     "payload name is empty",
		},
		{
			name:     "empty bundle",
			ctx:      context.Background(),
			verifier: verifier,
			request:  pubgh.BlobVerification{Payload: testPayload, Identity: testIdentity, Issuer: testIssuer},
			want:     "bundle name is empty",
		},
		{
			name:     "empty identity",
			ctx:      context.Background(),
			verifier: verifier,
			request:  pubgh.BlobVerification{Payload: testPayload, Bundle: testBundle, Issuer: testIssuer},
			want:     "certificate identity is empty",
		},
		{
			name:     "empty issuer",
			ctx:      context.Background(),
			verifier: verifier,
			request:  pubgh.BlobVerification{Payload: testPayload, Bundle: testBundle, Identity: testIdentity},
			want:     "certificate issuer is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			err := test.verifier.Verify(test.ctx, test.request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
			assert.NoFileExists(t, started)
		})
	}
}

// cancelVerifyAfterStart runs Verify, cancels after the fake starts, and
// returns the call error. It fails the test if the call exceeds bound
// after cancel. Waiting for the start marker uses [startWait].
func cancelVerifyAfterStart(
	t *testing.T,
	path string,
	dir string,
	environ []string,
	started string,
	bound time.Duration,
) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- NewVerifier(VerifierOptions{
			Path:    path,
			Dir:     dir,
			Environ: environ,
		}).Verify(ctx, mustRequest())
	}()

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(started)

		return statErr == nil
	}, startWait, cancelPoll)
	cancel()

	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("Verify did not return within %s after cancel", bound)
	}

	return nil
}

// writeVerifyFake returns the shared fake cosign executable written by TestMain.
func writeVerifyFake(t *testing.T) string {
	t.Helper()

	return fakeVerifyPath
}

// mkdirWork creates the child working directory used as [VerifierOptions.Dir].
func mkdirWork(t *testing.T, dir string) string {
	t.Helper()

	work := filepath.Join(dir, "dist")
	require.NoError(t, os.Mkdir(work, 0o755))

	return work
}

// assertRecordedVerifyArgv requires the fake to have recorded the contract argv.
func assertRecordedVerifyArgv(t *testing.T, record string) {
	t.Helper()

	body, err := os.ReadFile(record)
	require.NoError(t, err)
	got := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	assert.Equal(t, []string{
		"verify-blob",
		"--bundle",
		testBundle,
		"--certificate-identity",
		testIdentity,
		"--certificate-oidc-issuer",
		testIssuer,
		testPayload,
	}, got)
}

// assertRecordedDir requires the fake to have run with cmd.Dir set to want.
func assertRecordedDir(t *testing.T, cwdFile string, want string) {
	t.Helper()

	body, err := os.ReadFile(cwdFile)
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	assert.Equal(t, resolved, strings.TrimSpace(string(body)))
}

// mustRequest returns the fixture blob verification request.
func mustRequest() pubgh.BlobVerification {
	return pubgh.BlobVerification{
		Payload:  testPayload,
		Bundle:   testBundle,
		Identity: testIdentity,
		Issuer:   testIssuer,
	}
}
