package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	testLightweightTag = "v1.2.3"
	testAnnotatedTag   = "v1.2.3-annotated"
	testMissingTag     = "missing-tag"
	// startWait is how long cancelAfterStart waits for the fake to
	// create its start marker. The budget is load-dependent, not a
	// contract, and only bounds how a hung fixture is reported.
	startWait = 30 * time.Second
	// cancelWait is how long Resolve must return after cancel.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fake to start.
	cancelPoll    = 10 * time.Millisecond
	fakeGitScript = `#!/bin/sh
if [ -n "${GIT_STARTED:-}" ]; then
	: > "$GIT_STARTED"
fi
if [ -n "${GIT_SLEEP:-}" ]; then
	exec sleep "$GIT_SLEEP"
fi
printf '%s' "${GIT_STDOUT:-}"
exit "${GIT_EXIT:-0}"
`
)

func TestResolveLightweightTag(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	runGit(t, repo.git, repo.dir, "-c", "tag.gpgsign=false", "tag", testLightweightTag)
	want := parseHEAD(t, repo)

	got, err := New(Options{Path: repo.git, Dir: repo.dir}).Resolve(
		context.Background(),
		mustTag(t, testLightweightTag),
	)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestResolveAnnotatedTagPointsAtCommit(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	runGit(t, repo.git, repo.dir, "-c", "tag.gpgsign=false", "tag", "-a", testAnnotatedTag, "-m", "annotated")
	want := parseHEAD(t, repo)
	tagObject := strings.TrimSpace(runGit(t, repo.git, repo.dir, "rev-parse", testAnnotatedTag))
	require.NotEqual(t, string(want), tagObject, "annotated tag object must differ from the commit")

	got, err := New(Options{Path: repo.git, Dir: repo.dir}).Resolve(
		context.Background(),
		mustTag(t, testAnnotatedTag),
	)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.NotEqual(t, tagObject, string(got))
}

func TestResolveUnknownTagNamesTag(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)

	_, err := New(Options{Path: repo.git, Dir: repo.dir}).Resolve(
		context.Background(),
		mustTag(t, testMissingTag),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), testMissingTag)
	assert.Contains(t, err.Error(), "unknown tag")
	assert.NotContains(t, err.Error(), "invalid commit SHA")
}

func TestResolveMalformedSHANamesOutput(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t)
	const garbage = "not-a-commit-sha"

	_, err := New(Options{
		Path:    path,
		Dir:     dir,
		Environ: fakeEnviron(t, "GIT_STDOUT="+garbage),
	}).Resolve(context.Background(), mustTag(t, testLightweightTag))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid commit SHA")
	assert.Contains(t, err.Error(), garbage)
	assert.NotContains(t, err.Error(), "unknown tag")
}

func TestResolveEmptyTagRejectedBeforeStart(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t)

	_, err := New(Options{
		Path:    path,
		Dir:     dir,
		Environ: fakeEnviron(t, "GIT_STARTED="+started),
	}).Resolve(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag is empty")
	assert.NoFileExists(t, started)
}

func TestResolveCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		dir,
		fakeEnviron(t, "GIT_STARTED="+started, "GIT_SLEEP=30"),
		started,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestResolveRejectsNilGuardsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t)
	environ := fakeEnviron(t, "GIT_STARTED="+started)
	resolver := New(Options{Path: path, Dir: dir, Environ: environ})
	var nilCtx context.Context

	tests := []struct {
		name     string
		ctx      context.Context
		resolver *Resolver
		tag      rel.Tag
		want     string
	}{
		{
			name:     "nil context",
			ctx:      nilCtx,
			resolver: resolver,
			tag:      mustTag(t, testLightweightTag),
			want:     "context is nil",
		},
		{
			name:     "nil resolver",
			ctx:      context.Background(),
			resolver: nil,
			tag:      mustTag(t, testLightweightTag),
			want:     "git resolver is nil",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			_, err := test.resolver.Resolve(test.ctx, test.tag)
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

// requireGit returns the git executable or skips when git is unavailable.
func requireGit(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath(defaultBinary)
	if err != nil {
		t.Skip("git is unavailable")
	}

	return path
}

// gitRepo is a temporary repository used by Resolve tests.
type gitRepo struct {
	// git is the git executable.
	git string

	// dir is the repository working tree.
	dir string
}

// initRepo creates a temporary git repository with one empty commit.
func initRepo(t *testing.T) gitRepo {
	t.Helper()

	git := requireGit(t)
	dir := t.TempDir()
	runGit(t, git, dir, "init")
	runGit(t, git, dir, "config", "user.email", "dev@example.com")
	runGit(t, git, dir, "config", "user.name", "Dev")
	runGit(t, git, dir, "config", "commit.gpgsign", "false")
	runGit(t, git, dir, "config", "tag.gpgsign", "false")
	runGit(t, git, dir, "commit", "--allow-empty", "-m", "initial")

	return gitRepo{git: git, dir: dir}
}

// parseHEAD returns HEAD as a [pubgh.CommitSHA] via `git rev-parse HEAD`.
func parseHEAD(t *testing.T, repo gitRepo) pubgh.CommitSHA {
	t.Helper()

	raw := strings.TrimSpace(runGit(t, repo.git, repo.dir, "rev-parse", "HEAD"))
	sha, err := pubgh.ParseCommitSHA(raw)
	require.NoError(t, err)

	return sha
}

// runGit runs git in dir and returns trimmed-ready stdout.
func runGit(t *testing.T, git string, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)

	return string(out)
}

// mustTag parses raw as a [rel.Tag].
func mustTag(t *testing.T, raw string) rel.Tag {
	t.Helper()

	tag, err := rel.ParseTag(raw)
	require.NoError(t, err)

	return tag
}

// cancelAfterStart runs Resolve, cancels after the fake starts, and
// returns the call error. It fails the test if the call exceeds
// [cancelWait] after cancel. Waiting for the start marker uses [startWait].
func cancelAfterStart(
	t *testing.T,
	path string,
	dir string,
	environ []string,
	started string,
) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tag := mustTag(t, testLightweightTag)
	done := make(chan error, 1)
	go func() {
		_, resolveErr := New(Options{Path: path, Dir: dir, Environ: environ}).Resolve(ctx, tag)
		done <- resolveErr
	}()

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(started)

		return statErr == nil
	}, startWait, cancelPoll)
	cancel()

	select {
	case err := <-done:
		return err
	case <-time.After(cancelWait):
		t.Fatalf("Resolve did not return within %s after cancel", cancelWait)
	}

	return nil
}

// fakePath is the shared fake git executable. TestMain writes it once
// because a parallel sibling's fork/exec can inherit an open write
// descriptor and fail with ETXTBSY on Linux.
var fakePath string

// TestMain writes the fake git executable once before any test can exec
// it. Writing per test races on Linux: a parallel sibling's fork/exec
// can inherit an open write descriptor and fail with ETXTBSY.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gitx-fake-")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, defaultBinary)
	if err := os.WriteFile(path, []byte(fakeGitScript), 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	fakePath = path
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeFake returns the shared fake git executable written by TestMain.
func writeFake(t *testing.T) string {
	t.Helper()

	return fakePath
}

// fakeEnviron copies the process environment and appends extra KEY=value pairs.
func fakeEnviron(t *testing.T, extra ...string) []string {
	t.Helper()

	return append(append([]string{}, os.Environ()...), extra...)
}
