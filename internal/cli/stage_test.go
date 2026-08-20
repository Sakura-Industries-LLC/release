package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/profile/goprof"
	"github.com/meigma/release/internal/stage"
)

const (
	// stageGoreleaserPath is the RELEASE_GORELEASER_PATH fixture.
	stageGoreleaserPath = "/opt/goreleaser"
	// stageDist is the workflow --dist basename.
	stageDist = "dist"
)

func TestStageWritesImageInputs(t *testing.T) {
	dist := chdirDist(t)
	stdout, stderr, got, err := executeStageSeam(
		t,
		nil,
		[]string{"stage", "--profile", "go", "--dist", dist},
		nil,
	)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Empty(t, got.Path)
	assert.Equal(t, goprof.RootName(stageDist), got.Dist)
	assert.Nil(t, got.Environ)
	assert.NotNil(t, got.Stdout)
	assert.NotNil(t, got.Stderr)

	payload, err := os.ReadFile(filepath.Join(dist, stage.ImageInputsName))
	require.NoError(t, err)
	assert.Equal(t, expectedImageInputsJSON(), string(payload))
	decoded, err := stage.DecodeImageInputs(bytes.NewReader(payload))
	require.NoError(t, err)
	assert.Equal(t, expectedImageInputs(), decoded)
}

func TestStageJSONStillWritesImageInputs(t *testing.T) {
	dist := chdirDist(t)
	stdout, stderr, err := executeStage(
		t,
		nil,
		[]string{"stage", "--profile", "go", "--dist", dist, "--json"},
	)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.Contains(t, stdout, `"command":"stage"`)
	assert.Contains(t, stdout, `"ok":true`)
	assert.NotContains(t, stdout, `"schema":"release.dev/oci-build-inputs/v1"`)

	payload, err := os.ReadFile(filepath.Join(dist, stage.ImageInputsName))
	require.NoError(t, err)
	assert.Equal(t, expectedImageInputsJSON(), string(payload))
}

func TestStagePassesGoreleaserPathAndDist(t *testing.T) {
	dist := chdirDist(t)
	_, _, got, err := executeStageSeam(t, map[string]string{
		"RELEASE_GORELEASER_PATH": stageGoreleaserPath,
	}, []string{"stage", "--profile", "go", "--dist", dist}, nil)
	require.NoError(t, err)
	assert.Equal(t, stageGoreleaserPath, got.Path)
	assert.Equal(t, goprof.RootName(stageDist), got.Dist)
	assert.Nil(t, got.Environ)
}

func TestStageGoreleaserErrorIsCommandFailure(t *testing.T) {
	dist := chdirDist(t)
	stdout, stderr, err := executeWith(t, nil, []string{
		"stage", "--profile", "go", "--dist", dist, "--json",
	}, cli.BuildInfo{}, func(context.Context, goprof.GoReleaserOptions) error {
		return errors.New("goreleaser failed")
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Empty(t, stderr)
	assertFailureEnvelope(t, stdout, "goreleaser failed")
	assert.NotContains(t, err.Error(), "checksums.txt")

	_, statErr := os.Stat(filepath.Join(dist, stage.ImageInputsName))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestStageGoreleaserErrorRunsBeforeDistValidation(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Mkdir(stageDist, 0o755))

	stdout, _, err := executeWith(t, nil, []string{
		"stage", "--profile", "go", "--dist", stageDist, "--json",
	}, cli.BuildInfo{}, func(context.Context, goprof.GoReleaserOptions) error {
		return errors.New("goreleaser failed")
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assertFailureEnvelope(t, stdout, "goreleaser failed")
	assert.NotContains(t, err.Error(), "checksums.txt")
	assert.NotContains(t, stdout, "checksums.txt")

	_, statErr := os.Stat(filepath.Join(stageDist, stage.ImageInputsName))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestStageUsageErrorsDoNotBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing profile",
			args: []string{"stage", "--dist", stageDist, "--json"},
			want: "--profile is required",
		},
		{
			name: "unknown profile",
			args: []string{"stage", "--profile", "rust", "--dist", stageDist, "--json"},
			want: `unknown profile "rust"`,
		},
		{
			name: "missing dist",
			args: []string{"stage", "--profile", "go", "--json"},
			want: "--dist is required",
		},
		{
			name: "absolute dist",
			args: []string{"stage", "--profile", "go", "--dist", "/abs/dist", "--json"},
			want: "not a basename",
		},
		{
			name: "nested dist",
			args: []string{"stage", "--profile", "go", "--dist", "nested/dist", "--json"},
			want: "not a basename",
		},
		{
			name: "dot dist",
			args: []string{"stage", "--profile", "go", "--dist", ".", "--json"},
			want: "not a basename",
		},
		{
			name: "backslash dist",
			args: []string{"stage", "--profile", "go", "--dist", `a\b`, "--json"},
			want: "not a basename",
		},
		{
			name: "parent dist",
			args: []string{"stage", "--profile", "go", "--dist", "..", "--json"},
			want: "not a basename",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := execute(t, nil, tt.args, cli.BuildInfo{})
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Empty(t, stderr)
			assertFailureEnvelope(t, stdout, tt.want)
			if tt.want == "not a basename" {
				assert.Contains(t, err.Error(), "relative to the working directory")
			}
		})
	}
}

func TestStageMalformedBooleanEnvIsUsage(t *testing.T) {
	t.Run("json yes", func(t *testing.T) {
		stdout, stderr, err := execute(t, map[string]string{
			"RELEASE_JSON": "yes",
		}, []string{"stage", "--profile", "go", "--dist", stageDist}, cli.BuildInfo{})
		require.Error(t, err)
		assert.Equal(t, 2, cli.ExitCode(err))
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
		assert.Contains(t, err.Error(), "RELEASE_JSON")
	})
	t.Run("dry-run maybe", func(t *testing.T) {
		stdout, stderr, err := execute(t, map[string]string{
			"RELEASE_DRY_RUN": "maybe",
			"RELEASE_JSON":    "true",
		}, []string{"stage", "--profile", "go", "--dist", stageDist}, cli.BuildInfo{})
		require.Error(t, err)
		assert.Equal(t, 2, cli.ExitCode(err))
		assert.Empty(t, stderr)
		assertFailureEnvelope(t, stdout, "RELEASE_DRY_RUN")
	})
}

func TestStageDefaultGoReleaserSeam(t *testing.T) {
	dist := chdirDist(t)
	missing := filepath.Join(t.TempDir(), "missing-goreleaser")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			if key == "RELEASE_GORELEASER_PATH" {
				return missing, true
			}
			return "", false
		},
	})
	command.SetArgs([]string{"stage", "--profile", "go", "--dist", dist, "--json"})
	err := command.Execute()
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "resolve")
	assert.Contains(t, err.Error(), missing)
	assertFailureEnvelope(t, stdout.String(), "resolve")
}

func TestStageRoutesGoreleaserOutputToStderr(t *testing.T) {
	tests := []struct {
		name string
		json bool
	}{
		{name: "plain"},
		{name: "json", json: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dist := chdirDist(t)
			args := []string{"stage", "--profile", "go", "--dist", dist}
			if tt.json {
				args = append(args, "--json")
			}
			const progress = "goreleaser progress"
			const diag = "goreleaser diagnostic"
			stdout, stderr, err := executeWith(
				t,
				nil,
				args,
				cli.BuildInfo{},
				func(_ context.Context, options goprof.GoReleaserOptions) error {
					_, err := io.WriteString(options.Stdout, progress)
					require.NoError(t, err)
					_, err = io.WriteString(options.Stderr, diag)
					require.NoError(t, err)
					return nil
				},
			)
			require.NoError(t, err)
			assert.Contains(t, stderr, progress)
			assert.Contains(t, stderr, diag)
			assert.NotContains(t, stdout, progress)
			assert.NotContains(t, stdout, diag)
			if tt.json {
				assert.Equal(t, 1, countJSONDocuments(stdout))
				assert.Contains(t, stdout, `"ok":true`)
				return
			}
			assert.Empty(t, stdout)
		})
	}
}

func TestStageFailureWritesNoImageInputs(t *testing.T) {
	dist := chdirDist(t)
	require.NoError(t, os.WriteFile(filepath.Join(dist, "archive.tar.gz"), []byte("mutated"), 0o644))

	stdout, _, err := executeStage(
		t,
		nil,
		[]string{"stage", "--profile", "go", "--dist", dist, "--json"},
	)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assertFailureEnvelope(t, stdout, "archive.tar.gz")

	_, statErr := os.Stat(filepath.Join(dist, stage.ImageInputsName))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

// expectedImageInputs is the projection written for [goodDist].
func expectedImageInputs() stage.ImageInputs {
	return stage.ImageInputs{
		Schema:  stage.ImageInputsSchema,
		Profile: "go",
		Binaries: []stage.ImageInputBinary{
			{
				Platform: "linux/amd64",
				Name:     "app",
				Path:     "app_linux_amd64/app",
				Digest:   "sha256:" + sha256Hex("amd64"),
			},
			{
				Platform: "linux/arm64",
				Name:     "app",
				Path:     "app_linux_arm64/app",
				Digest:   "sha256:" + sha256Hex("arm64"),
			},
		},
	}
}

// expectedImageInputsJSON is the compact projection document written for [goodDist].
func expectedImageInputsJSON() string {
	return `{"schema":"release.dev/oci-build-inputs/v1","profile":"go","binaries":[` +
		`{"platform":"linux/amd64","name":"app","path":"app_linux_amd64/app","digest":"sha256:` + sha256Hex("amd64") + `"},` +
		`{"platform":"linux/arm64","name":"app","path":"app_linux_arm64/app","digest":"sha256:` + sha256Hex("arm64") + `"}` +
		"]}\n"
}
