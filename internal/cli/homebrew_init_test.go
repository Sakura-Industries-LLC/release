package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
)

const initializerCommit = "0123456789abcdef0123456789abcdef01234567"

// TestInitHomebrewTapWritesDeterministicScaffold verifies the complete machine-facing scaffold contract.
func TestInitHomebrewTapWritesDeterministicScaffold(t *testing.T) {
	output := filepath.Join(t.TempDir(), "homebrew-tools")
	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"--json", "init", "homebrew-tap", "--tap", "acme/homebrew-tools", "--output", output},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "init homebrew-tap", envelope.Command)
	assert.True(t, envelope.OK)
	rawResult, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.HomebrewTapInitResult
	require.NoError(t, json.Unmarshal(rawResult, &result))
	assert.Equal(t, "acme/homebrew-tools", result.Tap)
	assert.Equal(t, output, result.Output)
	assert.Equal(t, []string{
		".github/dependabot.yml",
		".github/workflows/casks.yml",
		"Casks/.gitkeep",
		"README.md",
	}, result.Files)

	workflow := readTextFile(t, filepath.Join(output, ".github", "workflows", "casks.yml"))
	assert.Equal(t, `name: Cask CI

on:
  pull_request:
    paths:
      - 'Casks/**'

permissions: {}

jobs:
  casks:
    permissions:
      contents: read
    uses: meigma/release/.github/workflows/homebrew-tap-ci.yml@0123456789abcdef0123456789abcdef01234567
`, workflow)
	assert.Equal(t, `version: 2
updates:
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
`, readTextFile(t, filepath.Join(output, ".github", "dependabot.yml")))
	assert.Empty(t, readTextFile(t, filepath.Join(output, "Casks", ".gitkeep")))
	readme := readTextFile(t, filepath.Join(output, "README.md"))
	assert.Contains(t, readme, "# acme/homebrew-tools")
	assert.Contains(t, readme, "brew install --cask acme/tools/<cask>")
	_, err = os.Stat(filepath.Join(output, "Formula"))
	require.ErrorIs(t, err, os.ErrNotExist)

	secondStdout, secondStderr, secondErr := execute(
		t,
		nil,
		[]string{"init", "homebrew-tap", "--tap", "acme/homebrew-tools", "--output", output},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.Error(t, secondErr)
	assert.Empty(t, secondStdout)
	assert.Empty(t, secondStderr)
	assert.Equal(t, workflow, readTextFile(t, filepath.Join(output, ".github", "workflows", "casks.yml")))
}

// TestInitHomebrewTapAcceptsEmptyOutput verifies callers may pre-create the destination directory.
func TestInitHomebrewTapAcceptsEmptyOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "tap")
	require.NoError(t, os.Mkdir(output, 0o755))

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"init", "homebrew-tap", "--tap", "acme/homebrew-tools", "--output", output},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.FileExists(t, filepath.Join(output, ".github", "workflows", "casks.yml"))
}

// TestInitHomebrewTapRejectsNonemptyOutput verifies initialization never overlays user files.
func TestInitHomebrewTapRejectsNonemptyOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "tap")
	writeFile(t, filepath.Join(output, "keep.txt"), "keep\n")

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"--json", "init", "homebrew-tap", "--tap", "acme/homebrew-tools", "--output", output},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.Error(t, err)
	require.NotErrorIs(t, err, cli.ErrUsage)
	assert.Empty(t, stderr)
	assertHomebrewInitFailure(t, stdout, "is not empty")
	assert.Equal(t, "keep\n", readTextFile(t, filepath.Join(output, "keep.txt")))
	entries, readErr := os.ReadDir(output)
	require.NoError(t, readErr)
	require.Len(t, entries, 1)
	assert.Equal(t, "keep.txt", entries[0].Name())
}

// TestInitHomebrewTapRejectsInvalidConfiguration verifies usage failures happen before filesystem mutation.
func TestInitHomebrewTapRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		tap       string
		commit    string
		wantError string
	}{
		{
			name:      "repository is not a named tap",
			tap:       "acme/tools",
			commit:    initializerCommit,
			wantError: "must use homebrew-<name>",
		},
		{
			name:      "build commit is not a release commit",
			tap:       "acme/homebrew-tools",
			commit:    "none",
			wantError: "release-cli build commit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "tap")
			stdout, stderr, err := execute(
				t,
				nil,
				[]string{"--json", "init", "homebrew-tap", "--tap", test.tap, "--output", output},
				cli.BuildInfo{Commit: test.commit},
			)
			require.Error(t, err)
			require.ErrorIs(t, err, cli.ErrUsage)
			assert.Empty(t, stderr)
			assertHomebrewInitFailure(t, stdout, test.wantError)
			_, statErr := os.Stat(output)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

// assertHomebrewInitFailure checks one init homebrew-tap failure envelope.
func assertHomebrewInitFailure(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "init homebrew-tap", envelope.Command)
	assert.False(t, envelope.OK)
	rawResult, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(rawResult, &result))
	assert.Contains(t, result.Error, wantError)
}

// readTextFile reads one generated text file.
func readTextFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	require.NoError(t, err)
	return string(content)
}
