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

// scoopScaffoldPaths is the stable lexical file order returned to automation.
var scoopScaffoldPaths = []string{
	".gitattributes",
	".github/dependabot.yml",
	".github/workflows/manifests.yml",
	"README.md",
}

// TestInitScoopBucketWritesDeterministicScaffold verifies the complete machine-facing scaffold contract.
func TestInitScoopBucketWritesDeterministicScaffold(t *testing.T) {
	firstOutput := filepath.Join(t.TempDir(), "scoop-tools")
	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"--json", "init", "scoop-bucket", "--bucket", "acme/scoop-tools", "--output", firstOutput},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "init scoop-bucket", envelope.Command)
	assert.True(t, envelope.OK)
	rawResult, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ScoopBucketInitResult
	require.NoError(t, json.Unmarshal(rawResult, &result))
	assert.Equal(t, "acme/scoop-tools", result.Bucket)
	assert.Equal(t, firstOutput, result.Output)
	assert.Equal(t, scoopScaffoldPaths, result.Files)

	assert.Equal(t, `# Scoop's pinned bucket tests require CRLF text in the Windows checkout.
* text=auto eol=crlf
`, readTextFile(t, filepath.Join(firstOutput, ".gitattributes")))
	assert.Equal(t, `version: 2
updates:
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
`, readTextFile(t, filepath.Join(firstOutput, ".github", "dependabot.yml")))
	assert.Equal(t, `name: Manifest CI

on:
  pull_request:
    paths:
      - '*.json'

permissions: {}

jobs:
  manifests:
    permissions:
      contents: read
    uses: meigma/release/.github/workflows/scoop-bucket-ci.yml@0123456789abcdef0123456789abcdef01234567
`, readTextFile(t, filepath.Join(firstOutput, ".github", "workflows", "manifests.yml")))
	assert.Equal(t, `# acme/scoop-tools

This repository publishes Scoop manifests through protected pull requests.

## Add the bucket

`+"```powershell"+`
scoop bucket add scoop-tools https://github.com/acme/scoop-tools
`+"```"+`

## Install an app

`+"```powershell"+`
scoop install scoop-tools/<manifest>
`+"```"+`

Manifest pull requests are validated on Windows AMD64 and ARM64 before merge.
`, readTextFile(t, filepath.Join(firstOutput, "README.md")))

	secondOutput := filepath.Join(t.TempDir(), "scoop-tools")
	secondStdout, secondStderr, secondErr := execute(
		t,
		nil,
		[]string{"init", "scoop-bucket", "--bucket", "acme/scoop-tools", "--output", secondOutput},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.NoError(t, secondErr)
	assert.Empty(t, secondStdout)
	assert.Empty(t, secondStderr)
	for _, generatedPath := range scoopScaffoldPaths {
		assert.Equal(
			t,
			readTextFile(t, filepath.Join(firstOutput, filepath.FromSlash(generatedPath))),
			readTextFile(t, filepath.Join(secondOutput, filepath.FromSlash(generatedPath))),
		)
	}
}

// TestInitScoopBucketAcceptsEmptyOutput verifies callers may pre-create the destination directory.
func TestInitScoopBucketAcceptsEmptyOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "bucket")
	require.NoError(t, os.Mkdir(output, 0o755))

	stdout, stderr, err := execute(
		t,
		nil,
		[]string{"init", "scoop-bucket", "--bucket", "acme/scoop-tools", "--output", output},
		cli.BuildInfo{Commit: initializerCommit},
	)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.FileExists(t, filepath.Join(output, ".github", "workflows", "manifests.yml"))
}

// TestInitScoopBucketRejectsOccupiedOutput verifies initialization never overlays user paths.
func TestInitScoopBucketRejectsOccupiedOutput(t *testing.T) {
	tests := []struct {
		// Name identifies the output-path condition.
		Name string
		// Prepare creates the occupied output path.
		Prepare func(t *testing.T, output string)
		// Verify proves the occupied path and its contents survived.
		Verify func(t *testing.T, output string)
		// WantError is the stable diagnostic fragment.
		WantError string
	}{
		{
			Name: "nonempty directory",
			Prepare: func(t *testing.T, output string) {
				t.Helper()
				writeFile(t, filepath.Join(output, "keep.txt"), "keep\n")
			},
			Verify: func(t *testing.T, output string) {
				t.Helper()
				assert.Equal(t, "keep\n", readTextFile(t, filepath.Join(output, "keep.txt")))
			},
			WantError: "is not empty",
		},
		{
			Name: "regular file",
			Prepare: func(t *testing.T, output string) {
				t.Helper()
				writeFile(t, output, "keep\n")
			},
			Verify: func(t *testing.T, output string) {
				t.Helper()
				assert.Equal(t, "keep\n", readTextFile(t, output))
			},
			WantError: "is not a directory",
		},
		{
			Name: "symbolic link",
			Prepare: func(t *testing.T, output string) {
				t.Helper()
				target := output + "-target"
				require.NoError(t, os.Mkdir(target, 0o755))
				require.NoError(t, os.Symlink(target, output))
			},
			Verify: func(t *testing.T, output string) {
				t.Helper()
				target, err := os.Readlink(output)
				require.NoError(t, err)
				assert.Equal(t, output+"-target", target)
				assert.DirExists(t, target)
			},
			WantError: "is not a directory",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "bucket")
			test.Prepare(t, output)

			stdout, stderr, err := execute(
				t,
				nil,
				[]string{"--json", "init", "scoop-bucket", "--bucket", "acme/scoop-tools", "--output", output},
				cli.BuildInfo{Commit: initializerCommit},
			)
			require.Error(t, err)
			require.NotErrorIs(t, err, cli.ErrUsage)
			assert.Empty(t, stderr)
			assertScoopInitFailure(t, stdout, test.WantError)
			test.Verify(t, output)
		})
	}
}

// TestInitScoopBucketRejectsInvalidConfiguration verifies usage failures happen before filesystem mutation.
func TestInitScoopBucketRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		// Name identifies the invalid input.
		Name string
		// Bucket is the supplied repository value.
		Bucket string
		// Commit is the stamped source commit.
		Commit string
		// WantError is the stable diagnostic fragment.
		WantError string
	}{
		{
			Name:      "repository is not owner and name",
			Bucket:    "acme",
			Commit:    initializerCommit,
			WantError: "must be owner/name",
		},
		{
			Name:      "build commit is unstamped",
			Bucket:    "acme/scoop-tools",
			Commit:    "none",
			WantError: "release-cli build commit",
		},
		{
			Name:      "build commit is abbreviated",
			Bucket:    "acme/scoop-tools",
			Commit:    "0123456",
			WantError: "release-cli build commit",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "bucket")
			stdout, stderr, err := execute(
				t,
				nil,
				[]string{"--json", "init", "scoop-bucket", "--bucket", test.Bucket, "--output", output},
				cli.BuildInfo{Commit: test.Commit},
			)
			require.Error(t, err)
			require.ErrorIs(t, err, cli.ErrUsage)
			assert.Empty(t, stderr)
			assertScoopInitFailure(t, stdout, test.WantError)
			_, statErr := os.Lstat(output)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

// assertScoopInitFailure checks one init scoop-bucket failure envelope.
func assertScoopInitFailure(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "init scoop-bucket", envelope.Command)
	assert.False(t, envelope.OK)
	rawResult, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(rawResult, &result))
	assert.Contains(t, result.Error, wantError)
}
