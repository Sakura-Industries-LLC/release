package ghup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	testTag   = "v1.2.3"
	testOwner = "owner"
	testName  = "repo"
	testToken = "test-app-token-value"
	// testConflictToken is an inherited GH_TOKEN that must lose to [testToken].
	// The length is deliberately different so the fixture can tell them apart
	// without writing either value.
	testConflictToken = "wrong-inherited-gh-token"
	testPayload       = "checksums.txt"
	testBundle        = "checksums.txt.sigstore.json"
	testArtifact      = "release.tar.gz"
	// cancelWait is how long the cancel test waits for the fake to start
	// and then for Replace to return.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fake to start.
	cancelPoll = 10 * time.Millisecond
	// fakeGHScript records argv, cwd, and whether GH_TOKEN is set.
	//
	// It records only whether the token is nonempty and its length, never
	// the value itself.
	fakeGHScript = `#!/bin/sh
if [ -n "${GHUP_STARTED:-}" ]; then
	: > "$GHUP_STARTED"
fi
if [ -n "${GHUP_RECORD:-}" ]; then
	printf '%s\n' "$@" > "$GHUP_RECORD"
fi
if [ -n "${GHUP_CWD:-}" ]; then
	pwd > "$GHUP_CWD"
fi
if [ -n "${GHUP_TOKEN:-}" ]; then
	if [ -n "${GH_TOKEN:-}" ]; then
		printf 'set %d\n' "${#GH_TOKEN}" > "$GHUP_TOKEN"
	else
		printf 'unset\n' > "$GHUP_TOKEN"
	fi
fi
if [ -n "${GHUP_STDERR_FILE:-}" ]; then
	cat "$GHUP_STDERR_FILE" >&2
elif [ -n "${GHUP_STDERR:-}" ]; then
	printf '%s' "$GHUP_STDERR" >&2
fi
if [ -n "${GHUP_SLEEP:-}" ]; then
	exec sleep "$GHUP_SLEEP"
fi
exit "${GHUP_EXIT:-0}"
`
)

func TestReplaceInvokesGH(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	work := mkdirWork(t, dir)
	record := filepath.Join(dir, "args")
	cwdFile := filepath.Join(dir, "cwd")
	tokenFile := filepath.Join(dir, "token")
	path := writeFake(t, dir)

	err := New(Options{
		Path:    path,
		Dir:     work,
		Token:   rel.NewSecret(testToken),
		Environ: fakeEnviron(t, "GHUP_RECORD="+record, "GHUP_CWD="+cwdFile, "GHUP_TOKEN="+tokenFile),
	}).Replace(context.Background(), mustRepo(t), mustTag(t), mustAssets())
	require.NoError(t, err)
	assertRecordedArgv(t, record)
	assertRecordedDir(t, cwdFile, work)
	assertRecordedToken(t, tokenFile)
	assertTokenAbsentFromFile(t, record)
}

func TestReplaceDropsInheritedGHToken(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	require.NotEqual(
		t,
		len(testToken),
		len(testConflictToken),
		"fixture lengths must differ so the child record can tell them apart",
	)

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	tokenFile := filepath.Join(dir, "token")
	path := writeFake(t, dir)

	err := New(Options{
		Path:  path,
		Token: rel.NewSecret(testToken),
		Environ: fakeEnviron(t,
			envGHToken+"="+testConflictToken,
			"GHUP_RECORD="+record,
			"GHUP_TOKEN="+tokenFile,
		),
	}).Replace(context.Background(), mustRepo(t), mustTag(t), mustAssets())
	require.NoError(t, err)
	assertRecordedArgv(t, record)
	assertRecordedToken(t, tokenFile)
	assertTokenAbsentFromFile(t, record)
	assert.NotContains(t, readText(t, tokenFile), testConflictToken)
	assert.NotContains(t, readText(t, record), testConflictToken)
}

func TestReplaceNonzeroExitIncludesCodeAndOmitsToken(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t, dir)

	err := New(Options{
		Path:  path,
		Token: rel.NewSecret(testToken),
		Environ: fakeEnviron(t,
			"GHUP_EXIT=3",
			"GHUP_STDERR=HTTP 403: Resource not accessible by integration",
		),
	}).Replace(context.Background(), mustRepo(t), mustTag(t), mustAssets())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 3")
	assert.Contains(t, err.Error(), "HTTP 403: Resource not accessible by integration")
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), envGHToken+"=")
}

func TestReplaceTruncatesLargeStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t, dir)
	head := bytes.Repeat([]byte("H"), stderrTailLimit)
	tail := bytes.Repeat([]byte("T"), stderrTailLimit)
	stderrFile := filepath.Join(dir, "stderr.txt")
	require.NoError(t, os.WriteFile(stderrFile, append(head, tail...), 0o600))

	err := New(Options{
		Path:  path,
		Token: rel.NewSecret(testToken),
		Environ: fakeEnviron(t,
			"GHUP_EXIT=1",
			"GHUP_STDERR_FILE="+stderrFile,
		),
	}).Replace(context.Background(), mustRepo(t), mustTag(t), mustAssets())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 1")
	assert.NotContains(t, err.Error(), string(head))
	assert.Contains(t, err.Error(), string(tail))
	assert.LessOrEqual(t, strings.Count(err.Error(), "T"), stderrTailLimit)
	assert.NotContains(t, err.Error(), testToken)
}

func TestReplaceResolvesEmptyPath(t *testing.T) {
	skipWindows(t)

	t.Run("finds gh on PATH", func(t *testing.T) {
		dir := t.TempDir()
		record := filepath.Join(dir, "args")
		writeFake(t, dir)
		t.Setenv("PATH", dir)
		t.Setenv("GHUP_RECORD", record)

		err := New(Options{
			Token: rel.NewSecret(testToken),
		}).Replace(context.Background(), mustRepo(t), mustTag(t), mustAssets())
		require.NoError(t, err)
		assertRecordedArgv(t, record)
	})

	t.Run("missing gh is a clear error", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		err := New(Options{
			Token: rel.NewSecret(testToken),
		}).Replace(context.Background(), mustRepo(t), mustTag(t), mustAssets())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gh")
		assert.Contains(t, err.Error(), "PATH")
		assert.NotContains(t, err.Error(), testToken)
	})
}

func TestReplaceCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t, dir),
		fakeEnviron(t, "GHUP_STARTED="+started, "GHUP_SLEEP=30"),
		started,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), testToken)
}

// The subtests share one marker file, so this test does not run in parallel.
func TestReplaceRejectsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t, dir)
	environ := fakeEnviron(t, "GHUP_STARTED="+started)
	replacer := New(Options{Path: path, Token: rel.NewSecret(testToken), Environ: environ})
	repo := mustRepo(t)
	tag := mustTag(t)
	assets := mustAssets()
	var nilCtx context.Context

	tests := []struct {
		name     string
		ctx      context.Context
		replacer *Replacer
		repo     pubgh.Repository
		tag      rel.Tag
		assets   []pubgh.AssetPath
		want     string
	}{
		{
			name:     "nil context",
			ctx:      nilCtx,
			replacer: replacer,
			repo:     repo,
			tag:      tag,
			assets:   assets,
			want:     "context is nil",
		},
		{
			name:     "nil replacer",
			ctx:      context.Background(),
			replacer: nil,
			repo:     repo,
			tag:      tag,
			assets:   assets,
			want:     "asset replacer is nil",
		},
		{
			name:     "empty tag",
			ctx:      context.Background(),
			replacer: replacer,
			repo:     repo,
			assets:   assets,
			want:     "tag is empty",
		},
		{
			name:     "empty repository",
			ctx:      context.Background(),
			replacer: replacer,
			tag:      tag,
			assets:   assets,
			want:     "repository is empty",
		},
		{
			name:     "empty owner",
			ctx:      context.Background(),
			replacer: replacer,
			repo:     pubgh.Repository{Name: testName},
			tag:      tag,
			assets:   assets,
			want:     "repository is empty",
		},
		{
			name:     "empty name",
			ctx:      context.Background(),
			replacer: replacer,
			repo:     pubgh.Repository{Owner: testOwner},
			tag:      tag,
			assets:   assets,
			want:     "repository is empty",
		},
		{
			name:     "empty asset list",
			ctx:      context.Background(),
			replacer: replacer,
			repo:     repo,
			tag:      tag,
			want:     "asset list is empty",
		},
		{
			name:     "empty path entry",
			ctx:      context.Background(),
			replacer: replacer,
			repo:     repo,
			tag:      tag,
			assets:   []pubgh.AssetPath{testPayload, "", testBundle},
			want:     "asset path is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			err := test.replacer.Replace(test.ctx, test.repo, test.tag, test.assets)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
			assert.NotContains(t, err.Error(), testToken)
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

// cancelAfterStart runs Replace, cancels after the fake starts, and
// returns the call error. It fails the test if the call exceeds
// [cancelWait].
func cancelAfterStart(
	t *testing.T,
	path string,
	environ []string,
	started string,
) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	repo := mustRepo(t)
	tag := mustTag(t)
	assets := mustAssets()
	done := make(chan error, 1)
	go func() {
		done <- New(Options{
			Path:    path,
			Token:   rel.NewSecret(testToken),
			Environ: environ,
		}).Replace(ctx, repo, tag, assets)
	}()

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(started)

		return statErr == nil
	}, cancelWait, cancelPoll)
	cancel()

	select {
	case err := <-done:
		return err
	case <-time.After(cancelWait):
		t.Fatalf("Replace did not return within %s after cancel", cancelWait)
	}

	return nil
}

// writeFake installs an executable fake gh script in dir.
func writeFake(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, defaultBinary)
	require.NoError(t, os.WriteFile(path, []byte(fakeGHScript), 0o755))
	require.NoError(t, os.Chmod(path, 0o755))

	return path
}

// mkdirWork creates the child working directory used as [Options.Dir].
func mkdirWork(t *testing.T, dir string) string {
	t.Helper()

	work := filepath.Join(dir, "dist")
	require.NoError(t, os.Mkdir(work, 0o755))

	return work
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
		"release",
		"upload",
		testTag,
		testArtifact,
		testPayload,
		testBundle,
		"--repo",
		testOwner + "/" + testName,
		"--clobber",
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

// assertRecordedToken requires GH_TOKEN to have been nonempty with the
// fixture length. The fake never writes the token value itself.
func assertRecordedToken(t *testing.T, tokenFile string) {
	t.Helper()

	body, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	assert.Equal(t, "set "+strconv.Itoa(len(testToken))+"\n", string(body))
	assert.NotContains(t, string(body), testToken)
}

// assertTokenAbsentFromFile requires the recorded argv not to contain the
// token value or a GH_TOKEN assignment.
func assertTokenAbsentFromFile(t *testing.T, record string) {
	t.Helper()

	body, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.NotContains(t, string(body), testToken)
	assert.NotContains(t, string(body), envGHToken)
}

// readText returns the contents of name or fails the test.
func readText(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(name)
	require.NoError(t, err)

	return string(body)
}

// mustRepo returns the fixture GitHub repository.
func mustRepo(t *testing.T) pubgh.Repository {
	t.Helper()

	repo, err := pubgh.ParseRepository(testOwner + "/" + testName)
	require.NoError(t, err)

	return repo
}

// mustTag returns the fixture release tag.
func mustTag(t *testing.T) rel.Tag {
	t.Helper()

	tag, err := rel.ParseTag(testTag)
	require.NoError(t, err)

	return tag
}

// mustAssets returns the fixture upload paths in bundle order.
func mustAssets() []pubgh.AssetPath {
	return []pubgh.AssetPath{testArtifact, testPayload, testBundle}
}
