package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
)

func TestVersionHuman(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(t, nil, []string{"version"}, cli.BuildInfo{
		Version:  "1.2.3",
		Commit:   "abc1234",
		Protocol: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "release-cli 1.2.3 (abc1234, protocol 1)\n", stdout)
	assert.Empty(t, stderr)
}

func TestVersionJSON(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(t, nil, []string{"version", "--json"}, cli.BuildInfo{
		Version:  "1.2.3",
		Commit:   "abc1234",
		Protocol: 1,
	})
	require.NoError(t, err)
	assert.Empty(t, stderr)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "version", envelope.Command)
	assert.True(t, envelope.OK)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.VersionResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, "1.2.3", result.Version)
	assert.Equal(t, "abc1234", result.Commit)
	assert.Equal(t, 1, result.Protocol)
}

func TestStageUnknownProfileIsUsage(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"stage", "--profile", "rust", "--dist", t.TempDir()},
		cli.BuildInfo{},
	)
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Contains(t, err.Error(), "unknown profile")
}

func TestStageUnknownProfileJSON(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"stage", "--profile", "rust", "--dist", t.TempDir(), "--json"},
		cli.BuildInfo{},
	)
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stderr)
	assertFailureEnvelope(t, stdout, "unknown profile")
}

func TestStageEnvOnly(t *testing.T) {
	stdout, stderr, err := execute(t, map[string]string{
		"RELEASE_PROFILE": "go",
		"RELEASE_DIST":    goodDist(t),
		"RELEASE_JSON":    "true",
	}, []string{"stage"}, cli.BuildInfo{})
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.Contains(t, stdout, `"ok":true`)
}

func TestBareInvocationRequiresSubcommand(t *testing.T) {
	t.Parallel()

	stdout, _, err := execute(t, nil, []string{"--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Contains(t, err.Error(), "a subcommand is required")
}

func TestAssetNamedUnknownFlagIsExitOne(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeFile(t, filepath.Join(dist, "checksums.txt"), digest+"  unknown flag\n")

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "unknown flag")
	assertFailureEnvelope(t, stdout, "unknown flag")
}

func TestStageJSONValidationFailure(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	require.NoError(t, os.WriteFile(filepath.Join(dist, "archive.tar.gz"), []byte("mutated"), 0o644))

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"stage", "--profile", "go", "--dist", dist, "--json"},
		cli.BuildInfo{},
	)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Empty(t, stderr)
	assertFailureEnvelope(t, stdout, "archive.tar.gz")
}

func TestStageUnknownFlagNoEnvelope(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(t, nil, []string{"stage", "--json", "--not-a-flag"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Usage:")
}

func TestUnknownCommandNoEnvelope(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(t, nil, []string{"publish", "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Contains(t, err.Error(), "unknown command")
	assert.Empty(t, stderr)
}

func TestStageFlagOverridesEnv(t *testing.T) {
	stdout, _, err := execute(t, map[string]string{
		"RELEASE_PROFILE": "rust",
		"RELEASE_DIST":    t.TempDir(),
	}, []string{"stage", "--profile", "go", "--dist", goodDist(t), "--json"}, cli.BuildInfo{})
	require.NoError(t, err)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.Contains(t, stdout, `"ok":true`)
	assert.Contains(t, stdout, `"command":"stage"`)
}

func TestStageSuccessSilentWithoutJSON(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", goodDist(t)}, cli.BuildInfo{})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestStageJSONSuccess(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"stage", "--profile", "go", "--dist", goodDist(t), "--json"},
		cli.BuildInfo{},
	)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.True(t, envelope.OK)
	assert.Equal(t, "stage", envelope.Command)
}

func TestStageBadChecksum(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	require.NoError(t, os.WriteFile(filepath.Join(dist, "archive.tar.gz"), []byte("mutated"), 0o644))

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "archive.tar.gz")
	assertFailureEnvelope(t, stdout, "archive.tar.gz")
}

func TestStageMissingArchitectureRecord(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	writeFile(t, filepath.Join(dist, "artifacts.json"), `[
		{"type":"Binary","goos":"linux","goarch":"amd64","path":"dist/app_linux_amd64/app","name":"app"}
	]`)

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "missing linux Binary record for arm64")
	assertFailureEnvelope(t, stdout, "missing linux Binary record for arm64")
}

func TestStageEscapedPath(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	writeFile(t, filepath.Join(dist, "artifacts.json"), `[
		{"type":"Binary","goos":"linux","goarch":"amd64","path":"dist/../secret","name":"secret"},
		{"type":"Binary","goos":"linux","goarch":"arm64","path":"dist/app_linux_arm64/app","name":"app"}
	]`)

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "escapes the dist root")
	assertFailureEnvelope(t, stdout, "escapes the dist root")
}

func TestStageClearedExecuteBit(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	require.NoError(t, os.Chmod(filepath.Join(dist, "app_linux_arm64", "app"), 0o644))

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "is not executable")
	assertFailureEnvelope(t, stdout, "is not executable")
}

func TestStageSymlinkEscape(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o755))
	link := filepath.Join(dist, "app_linux_amd64", "app")
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(outside, link))

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "app_linux_amd64/app")
	assertFailureEnvelope(t, stdout, "app_linux_amd64/app")
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, cli.ExitCode(nil))
	assert.Equal(t, 2, cli.ExitCode(cli.UsageError(assert.AnError)))
	assert.Equal(t, 1, cli.ExitCode(assert.AnError))
}

// execute runs the command tree with injected streams and the given env.
func execute(
	t *testing.T,
	env map[string]string,
	args []string,
	build cli.BuildInfo,
) (string, string, error) {
	t.Helper()

	for key, value := range env {
		t.Setenv(key, value)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command := cli.NewRootCommand(cli.Options{
		Out:   stdout,
		Err:   stderr,
		Build: build,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// assertFailureEnvelope checks stdout is one ok:false stage envelope.
func assertFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "stage", envelope.Command)
	assert.False(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	if wantError != "" {
		assert.Contains(t, result.Error, wantError)
	}
	assert.NotEmpty(t, result.Error)
}

// countJSONDocuments returns how many JSON values stdout contains.
func countJSONDocuments(stdout string) int {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(stdout)))
	count := 0
	for decoder.More() {
		var document json.RawMessage
		if err := decoder.Decode(&document); err != nil {
			return -1
		}
		count++
	}

	return count
}

// goodDist writes a valid Go profile bundle under a directory named dist.
func goodDist(t *testing.T) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), "dist")
	payload := []byte("archive")
	sum := sha256.Sum256(payload)
	writeFile(t, filepath.Join(root, "archive.tar.gz"), "archive")
	writeFile(t, filepath.Join(root, "checksums.txt"), hex.EncodeToString(sum[:])+"  archive.tar.gz\n")
	writeFile(t, filepath.Join(root, "checksums.txt.sigstore.json"), "{bundle}")
	writeExec(t, filepath.Join(root, "app_linux_amd64", "app"), []byte("amd64"))
	writeExec(t, filepath.Join(root, "app_linux_arm64", "app"), []byte("arm64"))
	writeFile(t, filepath.Join(root, "artifacts.json"), `[
		{"type":"Binary","goos":"linux","goarch":"amd64","path":"dist/app_linux_amd64/app","name":"app"},
		{"type":"Binary","goos":"linux","goarch":"arm64","path":"dist/app_linux_arm64/app","name":"app"}
	]`)

	return root
}

// writeFile creates path with data, making parent directories as needed.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

// writeExec creates an owner-executable file at path.
func writeExec(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o755))
	require.NoError(t, os.Chmod(path, 0o755))
}
