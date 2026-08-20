package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage"
)

func TestStageWritesImageInputs(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	stdout, stderr, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist}, cli.BuildInfo{})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)

	payload, err := os.ReadFile(filepath.Join(dist, stage.ImageInputsName))
	require.NoError(t, err)
	assert.Equal(t, expectedImageInputsJSON(), string(payload))
	got, err := stage.DecodeImageInputs(bytes.NewReader(payload))
	require.NoError(t, err)
	assert.Equal(t, expectedImageInputs(), got)
}

func TestStageJSONStillWritesImageInputs(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"stage", "--profile", "go", "--dist", dist, "--json"},
		cli.BuildInfo{},
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

func TestStageFailureWritesNoImageInputs(t *testing.T) {
	t.Parallel()

	dist := goodDist(t)
	require.NoError(t, os.WriteFile(filepath.Join(dist, "archive.tar.gz"), []byte("mutated"), 0o644))

	stdout, _, err := execute(t, nil, []string{"stage", "--profile", "go", "--dist", dist, "--json"}, cli.BuildInfo{})
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
