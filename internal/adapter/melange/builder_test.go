package melange

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
	testConfig     = "/abs/output/configuration/melange.yaml"
	testVarsFile   = "/abs/work/vars.json"
	testKeyPath    = "/abs/work/apk-signing.rsa"
	testOutDir     = "/abs/output/packages"
	testSourceAMD  = "/abs/work/sources/x86_64"
	testSourceARM  = "/abs/work/sources/aarch64"
	testRunner     = "docker"
	testNamespace  = "meigma"
	testBuildDate  = "2026-04-08T12:00:00Z"
	testGitRepoURL = "https://github.com/meigma/release"
	testGitCommit  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// invocationSep marks the end of one recorded Melange argv.
	invocationSep = "---"
	// startWait is how long cancelAfterStart waits for the fake to
	// create its start marker. The budget is load-dependent, not a
	// contract, and only bounds how a hung fixture is reported.
	startWait = 30 * time.Second
	// cancelWait is how long Build must return after cancel.
	cancelWait = 2 * time.Second
	// cancelPoll is the interval used while waiting for the fake to start.
	cancelPoll        = 10 * time.Millisecond
	fakeMelangeScript = `#!/bin/sh
if [ -n "${MELANGE_STARTED:-}" ]; then
	: > "$MELANGE_STARTED"
fi
if [ -n "${MELANGE_RECORD:-}" ]; then
	{
		printf '%s\n' "$@"
		printf '%s\n' '---'
	} >> "$MELANGE_RECORD"
fi
if [ -n "${MELANGE_STDERR_FILE:-}" ]; then
	cat "$MELANGE_STDERR_FILE" >&2
elif [ -n "${MELANGE_STDERR:-}" ]; then
	printf '%s' "$MELANGE_STDERR" >&2
fi
if [ -n "${MELANGE_ORPHAN:-}" ]; then
	sleep "${MELANGE_SLEEP:-30}" &
	wait
	exit "${MELANGE_EXIT:-0}"
fi
if [ -n "${MELANGE_SLEEP:-}" ]; then
	exec sleep "$MELANGE_SLEEP"
fi
exit_code=0
if [ -n "${MELANGE_FAIL_CMD:-}" ]; then
	if [ "$1" = "${MELANGE_FAIL_CMD}" ]; then
		exit_code="${MELANGE_EXIT:-1}"
	fi
else
	exit_code="${MELANGE_EXIT:-0}"
fi
exit "${exit_code}"
`
)

func TestBuildInvokesMelangeInOrder(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)

	got, err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "MELANGE_RECORD="+record),
	}).Build(context.Background(), validRequest())
	require.NoError(t, err)
	assert.Equal(t, image.APKRepositories{
		Dir:       testOutDir,
		PublicKey: testKeyPath + publicKeySuffix,
	}, got)
	assertRecordedArgv(t, record, twoSourceArgv())
}

func TestBuildCompileUsesFirstSourceArchitecture(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)
	request := validRequest()
	request.Sources = []image.APKBuildSource{
		{Arch: image.ArchAArch64, Dir: testSourceARM},
		{Arch: image.ArchX8664, Dir: testSourceAMD},
	}

	_, err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "MELANGE_RECORD="+record),
	}).Build(context.Background(), request)
	require.NoError(t, err)

	invocations := readInvocations(t, record)
	require.NotEmpty(t, invocations)
	assert.Equal(t, []string{
		"compile",
		"--arch",
		string(image.ArchAArch64),
		"--vars-file",
		testVarsFile,
		testConfig,
	}, invocations[0])
	require.Len(t, invocations, 4)
	assert.Equal(t, string(image.ArchAArch64), flagValue(t, invocations[2], "--arch"))
	assert.Equal(t, string(image.ArchX8664), flagValue(t, invocations[3], "--arch"))
}

func TestBuildStopsAfterCompileFailure(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)

	_, err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"MELANGE_RECORD="+record,
			"MELANGE_EXIT=3",
			"MELANGE_FAIL_CMD=compile",
			"MELANGE_STDERR=compile failed",
		),
	}).Build(context.Background(), validRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile")
	assert.Contains(t, err.Error(), "exit 3")
	assert.Contains(t, err.Error(), "compile failed")
	assertRecordedArgv(t, record, [][]string{compileArgv(image.ArchX8664)})
}

func TestBuildNonzeroExitIncludesSubcommandCodeAndStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	path := writeFake(t)

	_, err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"MELANGE_RECORD="+record,
			"MELANGE_EXIT=4",
			"MELANGE_FAIL_CMD=build",
			"MELANGE_STDERR=denied by signing key",
		),
	}).Build(context.Background(), validRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build")
	assert.Contains(t, err.Error(), "exit 4")
	assert.Contains(t, err.Error(), "denied by signing key")
	assertRecordedArgv(t, record, [][]string{
		compileArgv(image.ArchX8664),
		{"keygen", testKeyPath},
		buildArgv(image.ArchX8664, testSourceAMD),
	})
}

func TestBuildTruncatesLargeStderr(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	path := writeFake(t)
	head := bytes.Repeat([]byte("H"), stderrTailLimit)
	tail := bytes.Repeat([]byte("T"), stderrTailLimit)
	stderrFile := filepath.Join(dir, "stderr.txt")
	require.NoError(t, os.WriteFile(stderrFile, append(head, tail...), 0o600))

	_, err := New(Options{
		Path: path,
		Environ: fakeEnviron(t,
			"MELANGE_EXIT=1",
			"MELANGE_STDERR_FILE="+stderrFile,
		),
	}).Build(context.Background(), validRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile")
	assert.Contains(t, err.Error(), "exit 1")
	assert.NotContains(t, err.Error(), string(head))
	assert.Contains(t, err.Error(), string(tail))
	assert.LessOrEqual(t, strings.Count(err.Error(), "T"), stderrTailLimit)
}

func TestBuildResolvesEmptyPath(t *testing.T) {
	skipWindows(t)

	t.Run("finds melange on PATH", func(t *testing.T) {
		dir := t.TempDir()
		record := filepath.Join(dir, "args")
		t.Setenv("PATH", filepath.Dir(fakePath))
		t.Setenv("MELANGE_RECORD", record)

		got, err := New(Options{}).Build(context.Background(), validRequest())
		require.NoError(t, err)
		assert.Equal(t, testOutDir, got.Dir)
		assertRecordedArgv(t, record, twoSourceArgv())
	})

	t.Run("missing melange is a clear error", func(t *testing.T) {
		started := filepath.Join(t.TempDir(), "started")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("MELANGE_STARTED", started)

		_, err := New(Options{}).Build(context.Background(), validRequest())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "melange")
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
		fakeEnviron(t, "MELANGE_STARTED="+started, "MELANGE_SLEEP=30"),
		started,
		cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestBuildCanceledContextUnblocksOrphanGrandchild(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	err := cancelAfterStart(
		t,
		writeFake(t),
		fakeEnviron(t, "MELANGE_STARTED="+started, "MELANGE_ORPHAN=1", "MELANGE_SLEEP=30"),
		started,
		waitDelay+cancelWait,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestBuildWritesStderrSink(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	path := writeFake(t)
	var sink bytes.Buffer

	_, err := New(Options{
		Path:    path,
		Environ: fakeEnviron(t, "MELANGE_STDERR=diagnostic line"),
		Stderr:  &sink,
	}).Build(context.Background(), validRequest())
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("diagnostic line", 4), sink.String())
}

// The subtests share one marker file, so this test does not run in parallel.
func TestBuildRejectsBeforeStart(t *testing.T) {
	skipWindows(t)

	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	path := writeFake(t)
	environ := fakeEnviron(t, "MELANGE_STARTED="+started)
	builder := New(Options{Path: path, Environ: environ})
	valid := validRequest()

	tests := []struct {
		name    string
		ctx     context.Context
		builder *Builder
		request image.APKBuildRequest
		want    string
	}{
		{
			name:    "nil context",
			ctx:     nil,
			builder: builder,
			request: valid,
			want:    "context is nil",
		},
		{
			name:    "nil builder",
			ctx:     context.Background(),
			builder: nil,
			request: valid,
			want:    "melange builder is nil",
		},
		{
			name:    "empty config",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.Config = ""
			}),
			want: "config is empty",
		},
		{
			name:    "empty vars file",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.VarsFile = ""
			}),
			want: "vars file is empty",
		},
		{
			name:    "empty key path",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.KeyPath = ""
			}),
			want: "key path is empty",
		},
		{
			name:    "empty output directory",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.OutDir = ""
			}),
			want: "output directory is empty",
		},
		{
			name:    "empty runner",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.Runner = ""
			}),
			want: "runner is empty",
		},
		{
			name:    "empty namespace",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.Namespace = ""
			}),
			want: "namespace is empty",
		},
		{
			name:    "empty build date",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.BuildDate = ""
			}),
			want: "build date is empty",
		},
		{
			name:    "empty git repository URL",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.GitRepoURL = ""
			}),
			want: "git repository URL is empty",
		},
		{
			name:    "empty git commit",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.GitCommit = ""
			}),
			want: "git commit is empty",
		},
		{
			name:    "empty sources",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.Sources = nil
			}),
			want: "sources are empty",
		},
		{
			name:    "empty source architecture",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.Sources = []image.APKBuildSource{
					{Arch: "", Dir: testSourceAMD},
					{Arch: image.ArchAArch64, Dir: testSourceARM},
				}
			}),
			want: "source 0 architecture is empty",
		},
		{
			name:    "empty source directory",
			ctx:     context.Background(),
			builder: builder,
			request: withRequest(valid, func(request *image.APKBuildRequest) {
				request.Sources = []image.APKBuildSource{
					{Arch: image.ArchX8664, Dir: testSourceAMD},
					{Arch: image.ArchAArch64, Dir: ""},
				}
			}),
			want: "source 1 directory is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(started))

			_, err := test.builder.Build(test.ctx, test.request)
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
	request := validRequest()
	done := make(chan error, 1)
	go func() {
		_, err := New(Options{Path: path, Environ: environ}).Build(ctx, request)
		done <- err
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

// fakePath is the shared fake Melange executable. TestMain writes it
// once because a parallel sibling's fork/exec can inherit an open write
// descriptor and fail with ETXTBSY on Linux.
var fakePath string

// TestMain writes the fake Melange executable once before any test can
// exec it. Writing per test races on Linux: a parallel sibling's
// fork/exec can inherit an open write descriptor and fail with ETXTBSY.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "melange-fake-")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, defaultBinary)
	if err := os.WriteFile(path, []byte(fakeMelangeScript), 0o755); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	fakePath = path
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeFake returns the shared fake Melange executable written by TestMain.
func writeFake(t *testing.T) string {
	t.Helper()

	return fakePath
}

// fakeEnviron copies the process environment and appends extra KEY=value pairs.
func fakeEnviron(t *testing.T, extra ...string) []string {
	t.Helper()

	return append(append([]string{}, os.Environ()...), extra...)
}

// validRequest returns a complete two-source APK build request.
func validRequest() image.APKBuildRequest {
	return image.APKBuildRequest{
		Config:   testConfig,
		VarsFile: testVarsFile,
		KeyPath:  testKeyPath,
		OutDir:   testOutDir,
		Sources: []image.APKBuildSource{
			{Arch: image.ArchX8664, Dir: testSourceAMD},
			{Arch: image.ArchAArch64, Dir: testSourceARM},
		},
		Runner:     testRunner,
		Namespace:  testNamespace,
		BuildDate:  testBuildDate,
		GitRepoURL: testGitRepoURL,
		GitCommit:  testGitCommit,
	}
}

// withRequest returns a copy of request after apply mutates it.
func withRequest(
	request image.APKBuildRequest,
	apply func(*image.APKBuildRequest),
) image.APKBuildRequest {
	apply(&request)

	return request
}

// twoSourceArgv is the contract argv for [validRequest].
func twoSourceArgv() [][]string {
	return [][]string{
		compileArgv(image.ArchX8664),
		{"keygen", testKeyPath},
		buildArgv(image.ArchX8664, testSourceAMD),
		buildArgv(image.ArchAArch64, testSourceARM),
	}
}

// compileArgv is the contract argv for `melange compile` against arch.
func compileArgv(arch image.APKArch) []string {
	return []string{
		"compile",
		"--arch",
		string(arch),
		"--vars-file",
		testVarsFile,
		testConfig,
	}
}

// buildArgv is the contract argv for `melange build` against one source.
func buildArgv(arch image.APKArch, dir string) []string {
	return []string{
		"build",
		"--arch",
		string(arch),
		"--runner",
		testRunner,
		"--source-dir",
		dir,
		"--out-dir",
		testOutDir,
		"--signing-key",
		testKeyPath,
		"--namespace",
		testNamespace,
		"--build-date",
		testBuildDate,
		"--git-repo-url",
		testGitRepoURL,
		"--git-commit",
		testGitCommit,
		"--vars-file",
		testVarsFile,
		"--generate-provenance",
		testConfig,
	}
}

// assertRecordedArgv requires the fake to have recorded want, in order.
func assertRecordedArgv(t *testing.T, record string, want [][]string) {
	t.Helper()

	assert.Equal(t, want, readInvocations(t, record))
}

// readInvocations parses the fake's recorded argv, one invocation per slice.
func readInvocations(t *testing.T, record string) [][]string {
	t.Helper()

	body, err := os.ReadFile(record)
	require.NoError(t, err)

	var (
		invocations [][]string
		current     []string
	)
	for line := range strings.SplitSeq(strings.TrimSuffix(string(body), "\n"), "\n") {
		if line == invocationSep {
			invocations = append(invocations, current)
			current = nil

			continue
		}
		current = append(current, line)
	}
	require.Empty(t, current, "recorded argv ended without a separator")

	return invocations
}

// flagValue returns the argument after name in argv.
func flagValue(t *testing.T, argv []string, name string) string {
	t.Helper()

	for i, arg := range argv {
		if arg == name {
			require.Less(t, i+1, len(argv), "flag %s is missing a value", name)

			return argv[i+1]
		}
	}
	t.Fatalf("flag %s not found in %v", name, argv)

	return ""
}
