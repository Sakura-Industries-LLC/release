package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghtap/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubbrew"
)

const (
	// homebrewCommit is the source release commit fixture.
	homebrewCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// homebrewToken is the secret fixture that must never enter output.
	homebrewToken = "ghs_homebrew_command_secret"
)

// TestPublishHomebrewEmitsPublishedEnvelope proves command resolution, confined
// cask loading, secret factory delivery, and the stable JSON result contract.
func TestPublishHomebrewEmitsPublishedEnvelope(t *testing.T) {
	t.Parallel()

	fixture := newHomebrewCLI(t)
	var readerToken rel.Secret
	var writerToken rel.Secret
	fixture.options.NewTapReader = func(token rel.Secret, _ cli.GitHubEndpoint) (pubbrew.RepositoryReader, error) {
		readerToken = token
		return fixture.reader, nil
	}
	fixture.options.NewTapWriter = func(token rel.Secret, _ cli.GitHubEndpoint) (pubbrew.RepositoryWriter, error) {
		writerToken = token
		return fixture.writer, nil
	}
	fixture.reader.EXPECT().ReadBase(mock.Anything, fixture.tap, fixture.path).Return(pubbrew.BaseSnapshot{
		Branch: "main",
		Commit: "1111111111111111111111111111111111111111",
		File: pubbrew.File{
			Present: true,
			Content: fixture.content,
			SHA:     "2222222222222222222222222222222222222222",
		},
	}, nil).Once()
	fixture.reader.EXPECT().ReadPullRequest(
		mock.Anything,
		fixture.tap,
		pubbrew.BranchName("main"),
		pubbrew.BranchName("release/release-cli/v1.2.3"),
	).Return(pubbrew.PullRequest{State: pubbrew.PullRequestAbsent}, nil).Once()

	err := fixture.execute(
		"--json",
		"publish",
		"homebrew",
		"--dist",
		fixture.dist,
		"--tap",
		fixture.tap.String(),
		"--cask",
		"release-cli",
	)
	require.NoError(t, err)
	assert.Equal(t, homebrewToken, readerToken.Reveal())
	assert.Equal(t, homebrewToken, writerToken.Reveal())
	assert.Empty(t, fixture.stderr.String())
	assert.NotContains(t, fixture.stdout.String(), homebrewToken)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(fixture.stdout.String()), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, "publish homebrew", envelope.Command)
	assert.True(t, envelope.OK)
	payload, ok := envelope.Result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, fixture.tap.String(), payload["tap"])
	assert.Equal(t, "release-cli", payload["cask"])
	assert.Equal(t, "release/release-cli/v1.2.3", payload["branch"])
	assert.Equal(t, "published", payload["state"])
}

// TestPublishHomebrewMissingTokenIsUsage proves configuration fails before a
// repository port is constructed.
func TestPublishHomebrewMissingTokenIsUsage(t *testing.T) {
	t.Parallel()

	fixture := newHomebrewCLI(t)
	fixture.environment["RELEASE_APP_TOKEN"] = ""
	constructed := false
	fixture.options.NewTapReader = func(rel.Secret, cli.GitHubEndpoint) (pubbrew.RepositoryReader, error) {
		constructed = true
		return fixture.reader, nil
	}

	err := fixture.execute(
		"--json",
		"publish",
		"homebrew",
		"--dist",
		fixture.dist,
		"--tap",
		fixture.tap.String(),
		"--cask",
		"release-cli",
	)
	require.Error(t, err)
	require.ErrorIs(t, err, cli.ErrUsage)
	assert.False(t, constructed)
	assert.Contains(t, err.Error(), "RELEASE_APP_TOKEN is required")
	assert.NotContains(t, fixture.stdout.String(), homebrewToken)
}

// TestPublishHomebrewRefusesEscapingSymlink proves the generated cask is opened
// through the confined distribution root.
func TestPublishHomebrewRefusesEscapingSymlink(t *testing.T) {
	t.Parallel()

	fixture := newHomebrewCLI(t)
	outside := filepath.Join(t.TempDir(), "outside.rb")
	require.NoError(t, os.WriteFile(outside, fixture.content, 0o600))
	require.NoError(t, os.Remove(filepath.Join(fixture.dist, "homebrew", "Casks", "release-cli.rb")))
	require.NoError(t, os.Symlink(outside, filepath.Join(fixture.dist, "homebrew", "Casks", "release-cli.rb")))
	constructed := false
	fixture.options.NewTapReader = func(rel.Secret, cli.GitHubEndpoint) (pubbrew.RepositoryReader, error) {
		constructed = true
		return fixture.reader, nil
	}

	err := fixture.execute(
		"publish",
		"homebrew",
		"--dist",
		fixture.dist,
		"--tap",
		fixture.tap.String(),
		"--cask",
		"release-cli",
	)
	require.Error(t, err)
	assert.False(t, constructed)
	assert.Contains(t, err.Error(), "open generated cask")
	assert.NotContains(t, err.Error(), string(fixture.content))
}

// homebrewCLI holds one isolated command fixture.
type homebrewCLI struct {
	// dist contains the generated GoReleaser cask.
	dist string
	// content is the expected cask Ruby source.
	content []byte
	// tap is the parsed target repository.
	tap pubbrew.Repository
	// path is the expected tap path.
	path pubbrew.FilePath
	// environment is the command's injected process environment.
	environment map[string]string
	// options constructs the root command.
	options cli.Options
	// reader is the generated tap read mock.
	reader *mocks.MockRepositoryReader
	// writer is the generated tap write mock.
	writer *mocks.MockRepositoryWriter
	// stdout receives machine-readable output.
	stdout *strings.Builder
	// stderr receives diagnostics.
	stderr *strings.Builder
}

// newHomebrewCLI constructs one valid command fixture.
func newHomebrewCLI(t *testing.T) *homebrewCLI {
	t.Helper()

	dist := t.TempDir()
	directory := filepath.Join(dist, "homebrew", "Casks")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	content := []byte("cask \"release-cli\" do\n  version \"1.2.3\"\nend\n")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "release-cli.rb"), content, 0o600))
	tap, err := pubbrew.ParseRepository("meigma/homebrew-tap")
	require.NoError(t, err)
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	environment := map[string]string{
		"RELEASE_APP_TOKEN": homebrewToken,
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_REF_NAME":   "v1.2.3",
		"GITHUB_SHA":        homebrewCommit,
	}

	fixture := &homebrewCLI{
		dist:        dist,
		content:     content,
		tap:         tap,
		path:        "Casks/release-cli.rb",
		environment: environment,
		reader:      mocks.NewMockRepositoryReader(t),
		writer:      mocks.NewMockRepositoryWriter(t),
		stdout:      stdout,
		stderr:      stderr,
	}
	fixture.options = cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := fixture.environment[key]
			return value, ok && value != ""
		},
	}

	return fixture
}

// execute constructs and runs a fresh root command with arguments.
func (fixture *homebrewCLI) execute(arguments ...string) error {
	command := cli.NewRootCommand(fixture.options)
	command.SetArgs(arguments)
	return command.ExecuteContext(context.Background())
}
