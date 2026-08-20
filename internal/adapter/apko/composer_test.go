package apko

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

	"github.com/meigma/release/internal/stage/image"
)

const (
	testConfig     = "configuration/apko.yaml"
	testRepository = "packages"
	testKeyring    = "apk-signing.rsa.pub"
	testLockfile   = "apko.lock.json"
	testSBOMPath   = "sboms"
	testLayout     = "layout/"
	testReference  = "local/release:1.2.3"
	testBuildDate  = "2026-04-08T12:00:00Z"
	testVersion    = "1.2.3"
	testCommit     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// invocationSep marks the end of one recorded apko argv.
	invocationSep = "---"
	// recordSep marks the end of one recorded invocation, after cwd.
	recordSep = "==="
	// startWait is how long the cancel test waits for the fake to create
	// its started marker. Shell startup is load-dependent and is not the
	// contract under test.
	startWait = 30 * time.Second
	// cancelWait is how long Build must return after the context is
	// canceled.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fake to start.
	cancelPoll     = 10 * time.Millisecond
	fakeApkoScript = `#!/bin/sh
if [ -n "${APKO_STARTED:-}" ]; then
	: > "$APKO_STARTED"
fi
if [ -n "${APKO_RECORD:-}" ]; then
	{
		printf '%s\n' "$@"
		printf '%s\n' '---'
		pwd
		printf '%s\n' '==='
	} >> "$APKO_RECORD"
fi
if [ -n "${APKO_STDERR_FILE:-}" ]; then
	cat "$APKO_STDERR_FILE" >&2
elif [ -n "${APKO_STDERR:-}" ]; then
	printf '%s' "$APKO_STDERR" >&2
fi
if [ -n "${APKO_ORPHAN:-}" ]; then
	sleep "${APKO_SLEEP:-30}" &
	wait
	exit "${APKO_EXIT:-0}"
fi
if [ -n "${APKO_SLEEP:-}" ]; then
	exec sleep "$APKO_SLEEP"
fi
exit_code=0
if [ -n "${APKO_FAIL_CMD:-}" ]; then
	if [ "$1" = "${APKO_FAIL_CMD}" ]; then
		exit_code="${APKO_EXIT:-1}"
	fi
else
	exit_code="${APKO_EXIT:-0}"
fi
exit "${exit_code}"
`
)

func TestBuildInvokesApkoInOrder(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)
	request := validRequest(t)

	err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "APKO_RECORD="+record),
	}).Build(context.Background(), request)
	require.NoError(t, err)
	assertRecorded(t, record, request.Dir, contractArgv())
}

func TestBuildRepeatsArchAndAnnotationFlagsInOrder(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)
	request := validRequest(t)
	request.Arches = []image.APKArch{image.ArchAArch64, image.ArchX8664}
	request.Annotations = []image.Annotation{
		{Key: "org.opencontainers.image.revision", Value: testCommit},
		{Key: "org.opencontainers.image.version", Value: testVersion},
	}

	err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "APKO_RECORD="+record),
	}).Build(context.Background(), request)
	require.NoError(t, err)
	assertRecorded(t, record, request.Dir, [][]string{
		lockArgv(request.Arches),
		buildArgv(request.Arches, request.Annotations),
	})
}

func TestBuildStopsAfterLockFailure(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)
	request := validRequest(t)

	err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"APKO_RECORD="+record,
			"APKO_EXIT=3",
			"APKO_FAIL_CMD=lock",
			"APKO_STDERR=lock failed",
		),
	}).Build(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock")
	assert.Contains(t, err.Error(), "exit 3")
	assert.Contains(t, err.Error(), "lock failed")
	assertRecorded(t, record, request.Dir, [][]string{lockArgv(canonicalArches())})
}

func TestBuildNonzeroExitIncludesSubcommandCodeAndStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)
	request := validRequest(t)

	err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"APKO_RECORD="+record,
			"APKO_EXIT=4",
			"APKO_FAIL_CMD=build",
			"APKO_STDERR=denied by keyring",
		),
	}).Build(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build")
	assert.Contains(t, err.Error(), "exit 4")
	assert.Contains(t, err.Error(), "denied by keyring")
	assertRecorded(t, record, request.Dir, [][]string{
		lockArgv(canonicalArches()),
		buildArgv(canonicalArches(), canonicalAnnotations()),
	})
}

func TestBuildResolvesEmptyPath(t *testing.T) {
	skipWindows(t)

	t.Run("finds apko on PATH", func(t *testing.T) {
		dir := t.TempDir()
		record := filepath.Join(dir, "args")
		t.Setenv("PATH", filepath.Dir(fakePath))
		t.Setenv("APKO_RECORD", record)
		request := validRequest(t)

		err := New(Options{}).Build(context.Background(), request)
		require.NoError(t, err)
		assertRecorded(t, record, request.Dir, contractArgv())
	})

	t.Run("missing apko is a clear error", func(t *testing.T) {
		started := filepath.Join(t.TempDir(), "started")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("APKO_STARTED", started)

		err := New(Options{}).Build(context.Background(), validRequest(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "apko")
		assert.Contains(t, err.Error(), "PATH")
		assert.NoFileExists(t, started)
	})
}

func TestBuildCanceledContextReturnsPromptly(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		fakeEnviron(t, "APKO_STARTED="+started, "APKO_SLEEP=30"),
		started,
		cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestBuildWritesStderrSink(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)
	var sink bytes.Buffer

	err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "APKO_STDERR=diagnostic line"),
		Stderr:  &sink,
	}).Build(context.Background(), validRequest(t))
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("diagnostic line", 2), sink.String())
}

// The subtests share one marker file, so this test does not run in parallel.
func TestBuildRejectsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t)
	environ := fakeEnviron(t, "APKO_STARTED="+started)
	composer := New(Options{Path: path, Environ: environ})
	valid := validRequest(t)

	tests := []struct {
		name     string
		ctx      context.Context
		composer *Composer
		request  image.ComposeRequest
		want     string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			composer: composer,
			request:  valid,
			want:     "context is nil",
		},
		{
			name:     "nil composer",
			ctx:      context.Background(),
			composer: nil,
			request:  valid,
			want:     "apko composer is nil",
		},
		{
			name:     "empty directory",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Dir = ""
			}),
			want: "directory is empty",
		},
		{
			name:     "empty config",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Config = ""
			}),
			want: "config is empty",
		},
		{
			name:     "empty repository",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Repository = ""
			}),
			want: "repository is empty",
		},
		{
			name:     "empty keyring",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Keyring = ""
			}),
			want: "keyring is empty",
		},
		{
			name:     "empty lockfile",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Lockfile = ""
			}),
			want: "lockfile is empty",
		},
		{
			name:     "empty SBOM path",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.SBOMPath = ""
			}),
			want: "SBOM path is empty",
		},
		{
			name:     "empty layout",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Layout = ""
			}),
			want: "layout is empty",
		},
		{
			name:     "empty reference",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Reference = ""
			}),
			want: "reference is empty",
		},
		{
			name:     "empty build date",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.BuildDate = ""
			}),
			want: "build date is empty",
		},
		{
			name:     "empty arches",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Arches = nil
			}),
			want: "arches are empty",
		},
		{
			name:     "empty arch entry",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Arches = []image.APKArch{image.ArchX8664, ""}
			}),
			want: "arch 1 is empty",
		},
		{
			name:     "empty annotation key",
			ctx:      context.Background(),
			composer: composer,
			request: withRequest(valid, func(request *image.ComposeRequest) {
				request.Annotations = []image.Annotation{
					{Key: "org.opencontainers.image.version", Value: testVersion},
					{Key: "", Value: testCommit},
				}
			}),
			want: "annotation 1 key is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			err := test.composer.Build(test.ctx, test.request)
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

// cancelAfterStart runs Build, cancels after the fake starts, and
// returns the call error. It fails the test if the call exceeds bound
// after cancel. Waiting for the start marker uses [startWait], not bound.
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
	request := validRequest(t)
	done := make(chan error, 1)
	go func() {
		done <- New(Options{Path: path, Environ: environ}).Build(ctx, request)
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
		t.Fatalf("Build did not return within %s after cancel", bound)
	}

	return nil
}

// fakePath is the shared fake apko executable. TestMain writes it once
// because a parallel sibling's fork/exec can inherit an open write
// descriptor and fail with ETXTBSY on Linux.
var fakePath string

// TestMain writes the fake apko executable once before any test can
// exec it. Writing per test races on Linux: a parallel sibling's
// fork/exec can inherit an open write descriptor and fail with ETXTBSY.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "apko-fake-")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, defaultBinary)
	if err := os.WriteFile(path, []byte(fakeApkoScript), 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	fakePath = path
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeFake returns the shared fake apko executable written by TestMain.
func writeFake(t *testing.T) string {
	t.Helper()

	return fakePath
}

// fakeEnviron copies the process environment and appends extra KEY=value pairs.
func fakeEnviron(t *testing.T, extra ...string) []string {
	t.Helper()

	return append(append([]string{}, os.Environ()...), extra...)
}

// validRequest returns a complete two-architecture compose request.
//
// Dir is a real scratch directory so the child can chdir into it.
func validRequest(t *testing.T) image.ComposeRequest {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	return image.ComposeRequest{
		Dir:         dir,
		Config:      testConfig,
		Repository:  testRepository,
		Keyring:     testKeyring,
		Lockfile:    testLockfile,
		SBOMPath:    testSBOMPath,
		Layout:      testLayout,
		Reference:   testReference,
		BuildDate:   testBuildDate,
		Arches:      canonicalArches(),
		Annotations: canonicalAnnotations(),
	}
}

// withRequest returns a copy of request after apply mutates it.
func withRequest(
	request image.ComposeRequest,
	apply func(*image.ComposeRequest),
) image.ComposeRequest {
	apply(&request)

	return request
}

// canonicalArches is the YAML architecture order: x86_64 then aarch64.
func canonicalArches() []image.APKArch {
	return []image.APKArch{image.ArchX8664, image.ArchAArch64}
}

// canonicalAnnotations is the YAML annotation order: version then revision.
func canonicalAnnotations() []image.Annotation {
	return []image.Annotation{
		{Key: "org.opencontainers.image.version", Value: testVersion},
		{Key: "org.opencontainers.image.revision", Value: testCommit},
	}
}

// contractArgv is the YAML-identical argv for [validRequest].
func contractArgv() [][]string {
	return [][]string{
		lockArgv(canonicalArches()),
		buildArgv(canonicalArches(), canonicalAnnotations()),
	}
}

// lockArgv is the contract argv for `apko lock` against arches.
func lockArgv(arches []image.APKArch) []string {
	args := []string{"lock"}
	args = append(args, archArgv(arches)...)
	args = append(args,
		"--repository-append",
		testRepository,
		"--keyring-append",
		testKeyring,
		"--output",
		testLockfile,
		testConfig,
	)

	return args
}

// buildArgv is the contract argv for `apko build` against arches and annotations.
func buildArgv(arches []image.APKArch, annotations []image.Annotation) []string {
	args := []string{"build"}
	args = append(args, archArgv(arches)...)
	args = append(args,
		"--repository-append",
		testRepository,
		"--keyring-append",
		testKeyring,
		"--lockfile",
		testLockfile,
		"--build-date",
		testBuildDate,
		"--sbom-path",
		testSBOMPath,
	)
	for _, annotation := range annotations {
		args = append(args, "--annotations", annotation.Key+":"+annotation.Value)
	}
	args = append(args, testConfig, testReference, testLayout)

	return args
}

// archArgv returns one `--arch` flag pair per architecture, in order.
func archArgv(arches []image.APKArch) []string {
	flags := make([]string, 0, 2*len(arches))
	for _, arch := range arches {
		flags = append(flags, "--arch", string(arch))
	}

	return flags
}

// recordedInvocation is one fake apko process: argv plus working directory.
type recordedInvocation struct {
	// argv is the recorded argument list, excluding argv[0].
	argv []string
	// cwd is the recorded working directory.
	cwd string
}

// assertRecorded requires the fake to have recorded want, in order, each
// running in dir.
func assertRecorded(t *testing.T, record, dir string, want [][]string) {
	t.Helper()

	got := readInvocations(t, record)
	require.Len(t, got, len(want))
	for i := range want {
		assert.Equal(t, want[i], got[i].argv, "invocation %d argv", i)
		assertSameDir(t, dir, got[i].cwd)
	}
}

// assertSameDir requires got and want to name the same directory after
// resolving symlinks.
func assertSameDir(t *testing.T, want, got string) {
	t.Helper()

	wantResolved, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

// readInvocations parses the fake's recorded argv and cwd.
func readInvocations(t *testing.T, record string) []recordedInvocation {
	t.Helper()

	body, err := os.ReadFile(record)
	require.NoError(t, err)

	var (
		invocations []recordedInvocation
		current     []string
		haveArgv    bool
		cwd         string
	)
	for line := range strings.SplitSeq(strings.TrimSuffix(string(body), "\n"), "\n") {
		switch {
		case !haveArgv && line == invocationSep:
			haveArgv = true
		case haveArgv && cwd == "" && line != recordSep:
			cwd = line
		case haveArgv && cwd != "" && line == recordSep:
			invocations = append(invocations, recordedInvocation{
				argv: current,
				cwd:  cwd,
			})
			current = nil
			haveArgv = false
			cwd = ""
		case haveArgv:
			t.Fatalf("recorded invocation is missing a working directory")
		default:
			current = append(current, line)
		}
	}
	require.Empty(t, current, "recorded argv ended without a separator")
	require.False(t, haveArgv, "recorded invocation ended without a cwd")
	return invocations
}
