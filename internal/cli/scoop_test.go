package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghbucket/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// scoopCommit is the source release commit fixture.
	scoopCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// scoopToken is the secret fixture that must never enter output.
	scoopToken = "ghs_scoop_command_secret"
	// scoopCommand is the envelope command path for publish scoop.
	scoopCommand = "publish scoop"
)

// TestPublishScoopHelpDocumentsRequiredFlags proves the public command contract.
func TestPublishScoopHelpDocumentsRequiredFlags(t *testing.T) {
	t.Parallel()

	fixture := newScoopCLI(t)
	err := fixture.execute("publish", "scoop", "--help")
	require.NoError(t, err)
	help := fixture.stdout.String() + fixture.stderr.String()
	assert.Contains(t, help, "--dist")
	assert.Contains(t, help, "--bucket")
	assert.Contains(t, help, "--manifest")
	assert.NotContains(t, help, "--token")
	assert.NotContains(t, help, scoopToken)
}

// TestPublishScoopConfigErrorsAreUsage proves missing flags and environment
// fail before a repository port is constructed.
func TestPublishScoopConfigErrorsAreUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  func(*scoopCLI)
		args []string
		want string
	}{
		{
			name: "missing dist",
			args: []string{
				"--json",
				"publish",
				"scoop",
				"--bucket",
				"meigma/scoop-bucket",
				"--manifest",
				"release-cli",
			},
			want: "--dist is required",
		},
		{
			name: "missing bucket",
			args: []string{"--json", "publish", "scoop", "--dist", "", "--manifest", "release-cli"},
			want: "--bucket is required",
		},
		{
			name: "missing manifest",
			args: []string{"--json", "publish", "scoop", "--dist", "", "--bucket", "meigma/scoop-bucket"},
			want: "--manifest is required",
		},
		{
			name: "missing token",
			env: func(fixture *scoopCLI) {
				fixture.environment["RELEASE_APP_TOKEN"] = ""
			},
			args: []string{
				"--json",
				"publish",
				"scoop",
				"--dist",
				"",
				"--bucket",
				"meigma/scoop-bucket",
				"--manifest",
				"release-cli",
			},
			want: "RELEASE_APP_TOKEN is required",
		},
		{
			name: "missing repository",
			env: func(fixture *scoopCLI) {
				delete(fixture.environment, "GITHUB_REPOSITORY")
			},
			args: []string{
				"--json",
				"publish",
				"scoop",
				"--dist",
				"",
				"--bucket",
				"meigma/scoop-bucket",
				"--manifest",
				"release-cli",
			},
			want: "GITHUB_REPOSITORY is required",
		},
		{
			name: "missing ref name",
			env: func(fixture *scoopCLI) {
				delete(fixture.environment, "GITHUB_REF_NAME")
			},
			args: []string{
				"--json",
				"publish",
				"scoop",
				"--dist",
				"",
				"--bucket",
				"meigma/scoop-bucket",
				"--manifest",
				"release-cli",
			},
			want: "--version is required when GITHUB_REF_NAME is unset",
		},
		{
			name: "missing sha",
			env: func(fixture *scoopCLI) {
				delete(fixture.environment, "GITHUB_SHA")
			},
			args: []string{
				"--json",
				"publish",
				"scoop",
				"--dist",
				"",
				"--bucket",
				"meigma/scoop-bucket",
				"--manifest",
				"release-cli",
			},
			want: "GITHUB_SHA is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newScoopCLI(t)
			if tt.env != nil {
				tt.env(fixture)
			}
			args := append([]string{}, tt.args...)
			for index, argument := range args {
				if argument == "--dist" && index+1 < len(args) && args[index+1] == "" {
					args[index+1] = fixture.dist
				}
			}
			constructed := false
			fixture.options.NewBucketReader = func(rel.Secret, cli.GitHubEndpoint) (pubscoop.RepositoryReader, error) {
				constructed = true
				return fixture.reader, nil
			}

			err := fixture.execute(args...)
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.False(t, constructed)
			assert.Contains(t, err.Error(), tt.want)
			assertScoopFailureEnvelope(t, fixture.stdout.String(), tt.want)
			assert.NotContains(t, fixture.stdout.String(), scoopToken)
			assert.NotContains(t, fixture.stderr.String(), scoopToken)
			assert.NotContains(t, err.Error(), scoopToken)
		})
	}
}

// TestPublishScoopTokenReachesFactoriesAsSecret proves the App token never
// appears in output and is delivered only through the authenticated factories.
func TestPublishScoopTokenReachesFactoriesAsSecret(t *testing.T) {
	t.Parallel()

	fixture := newScoopCLI(t)
	var readerToken rel.Secret
	var writerToken rel.Secret
	var readerEndpoint cli.GitHubEndpoint
	var writerEndpoint cli.GitHubEndpoint
	fixture.options.NewBucketReader = func(token rel.Secret, endpoint cli.GitHubEndpoint) (pubscoop.RepositoryReader, error) {
		readerToken = token
		readerEndpoint = endpoint
		return fixture.reader, nil
	}
	fixture.options.NewBucketWriter = func(token rel.Secret, endpoint cli.GitHubEndpoint) (pubscoop.RepositoryWriter, error) {
		writerToken = token
		writerEndpoint = endpoint
		return fixture.writer, nil
	}
	expectPublishedScoop(fixture)

	err := fixture.execute(
		"--json",
		"publish",
		"scoop",
		"--dist",
		fixture.dist,
		"--bucket",
		fixture.bucket.String(),
		"--manifest",
		"release-cli",
	)
	require.NoError(t, err)
	assert.Equal(t, scoopToken, readerToken.Reveal())
	assert.Equal(t, scoopToken, writerToken.Reveal())
	assert.Equal(t, "[REDACTED]", readerToken.String())
	assert.Equal(t, "[REDACTED]", writerToken.String())
	assert.Empty(t, readerEndpoint.APIURL)
	assert.Empty(t, writerEndpoint.APIURL)
	assert.NotContains(t, fixture.stdout.String(), scoopToken)
	assert.NotContains(t, fixture.stderr.String(), scoopToken)
}

// TestPublishScoopRefusesEscapingSymlink proves the generated manifest is
// opened through the confined distribution root.
func TestPublishScoopRefusesEscapingSymlink(t *testing.T) {
	t.Parallel()

	fixture := newScoopCLI(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	require.NoError(t, os.WriteFile(outside, fixture.content, 0o600))
	require.NoError(t, os.Remove(filepath.Join(fixture.dist, "scoop", "release-cli.json")))
	require.NoError(t, os.Symlink(outside, filepath.Join(fixture.dist, "scoop", "release-cli.json")))
	constructed := false
	fixture.options.NewBucketReader = func(rel.Secret, cli.GitHubEndpoint) (pubscoop.RepositoryReader, error) {
		constructed = true
		return fixture.reader, nil
	}

	err := fixture.execute(
		"publish",
		"scoop",
		"--dist",
		fixture.dist,
		"--bucket",
		fixture.bucket.String(),
		"--manifest",
		"release-cli",
	)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.False(t, constructed)
	assert.Contains(t, err.Error(), "open generated manifest")
	assert.NotContains(t, err.Error(), string(fixture.content))
	assert.NotContains(t, err.Error(), scoopToken)
}

// TestPublishScoopRejectsNonRegularEmptyAndOversizedManifests proves local
// file bounds fail closed before a bucket request.
func TestPublishScoopRejectsNonRegularEmptyAndOversizedManifests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T, fixture *scoopCLI)
		want    string
	}{
		{
			name: "directory",
			prepare: func(t *testing.T, fixture *scoopCLI) {
				t.Helper()
				path := filepath.Join(fixture.dist, "scoop", "release-cli.json")
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o755))
			},
			want: "generated manifest scoop/release-cli.json is not a regular file",
		},
		{
			name: "empty",
			prepare: func(t *testing.T, fixture *scoopCLI) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(fixture.dist, "scoop", "release-cli.json"), nil, 0o600))
			},
			want: "generated manifest scoop/release-cli.json is empty",
		},
		{
			name: "oversized",
			prepare: func(t *testing.T, fixture *scoopCLI) {
				t.Helper()
				oversized := make([]byte, (1<<20)+1)
				require.NoError(
					t,
					os.WriteFile(filepath.Join(fixture.dist, "scoop", "release-cli.json"), oversized, 0o600),
				)
			},
			want: "generated manifest scoop/release-cli.json exceeds 1048576 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newScoopCLI(t)
			tt.prepare(t, fixture)
			constructed := false
			fixture.options.NewBucketReader = func(rel.Secret, cli.GitHubEndpoint) (pubscoop.RepositoryReader, error) {
				constructed = true
				return fixture.reader, nil
			}

			err := fixture.execute(
				"--json",
				"publish",
				"scoop",
				"--dist",
				fixture.dist,
				"--bucket",
				fixture.bucket.String(),
				"--manifest",
				"release-cli",
			)
			require.Error(t, err)
			assert.Equal(t, 1, cli.ExitCode(err))
			assert.False(t, constructed)
			assert.Contains(t, err.Error(), tt.want)
			assertScoopFailureEnvelope(t, fixture.stdout.String(), tt.want)
			assert.NotContains(t, fixture.stdout.String(), scoopToken)
		})
	}
}

// TestPublishScoopEmitsPublishedEnvelope proves command resolution, confined
// manifest loading, secret factory delivery, and the stable JSON result contract.
func TestPublishScoopEmitsPublishedEnvelope(t *testing.T) {
	t.Parallel()

	fixture := newScoopCLI(t)
	var readerToken rel.Secret
	var writerToken rel.Secret
	fixture.options.NewBucketReader = func(token rel.Secret, _ cli.GitHubEndpoint) (pubscoop.RepositoryReader, error) {
		readerToken = token
		return fixture.reader, nil
	}
	fixture.options.NewBucketWriter = func(token rel.Secret, _ cli.GitHubEndpoint) (pubscoop.RepositoryWriter, error) {
		writerToken = token
		return fixture.writer, nil
	}
	expectPublishedScoop(fixture)

	err := fixture.execute(
		"--json",
		"publish",
		"scoop",
		"--dist",
		fixture.dist,
		"--bucket",
		fixture.bucket.String(),
		"--manifest",
		"release-cli",
	)
	require.NoError(t, err)
	assert.Equal(t, scoopToken, readerToken.Reveal())
	assert.Equal(t, scoopToken, writerToken.Reveal())
	assert.Empty(t, fixture.stderr.String())
	assert.NotContains(t, fixture.stdout.String(), scoopToken)
	assert.Equal(t, 1, countJSONDocuments(fixture.stdout.String()))

	result := decodeScoopResult(t, fixture.stdout.String())
	assert.Equal(t, fixture.bucket.String(), result.Bucket)
	assert.Equal(t, "release-cli", result.Manifest)
	assert.Equal(t, "release/release-cli/v1.2.3", result.Branch)
	assert.Equal(t, pubscoop.StatePublished, result.State)
}

// TestPublishScoopSilentSuccessWithoutJSON proves success writes no envelope
// unless --json is requested.
func TestPublishScoopSilentSuccessWithoutJSON(t *testing.T) {
	t.Parallel()

	fixture := newScoopCLI(t)
	fixture.options.BucketReader = fixture.reader
	fixture.options.BucketWriter = fixture.writer
	expectPublishedScoop(fixture)

	err := fixture.execute(
		"publish",
		"scoop",
		"--dist",
		fixture.dist,
		"--bucket",
		fixture.bucket.String(),
		"--manifest",
		"release-cli",
	)
	require.NoError(t, err)
	assert.Empty(t, fixture.stdout.String())
	assert.Empty(t, fixture.stderr.String())
}

// TestPublishScoopPropagatesDomainError proves a domain failure is a command
// failure and still emits one error envelope.
func TestPublishScoopPropagatesDomainError(t *testing.T) {
	t.Parallel()

	fixture := newScoopCLI(t)
	fixture.options.BucketReader = fixture.reader
	fixture.options.BucketWriter = fixture.writer
	fixture.reader.EXPECT().ReadBase(mock.Anything, fixture.bucket, fixture.path).
		Return(pubscoop.BaseSnapshot{}, errors.New("bucket default branch is empty")).
		Once()

	err := fixture.execute(
		"--json",
		"publish",
		"scoop",
		"--dist",
		fixture.dist,
		"--bucket",
		fixture.bucket.String(),
		"--manifest",
		"release-cli",
	)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "bucket default branch is empty")
	assertScoopFailureEnvelope(t, fixture.stdout.String(), "bucket default branch is empty")
	assert.NotContains(t, fixture.stdout.String(), scoopToken)
	assert.NotContains(t, fixture.stderr.String(), scoopToken)
}

// scoopCLI holds one isolated command fixture.
type scoopCLI struct {
	// dist contains the generated Scoop manifest.
	dist string
	// content is the expected manifest JSON.
	content []byte
	// bucket is the parsed target repository.
	bucket pubscoop.Repository
	// path is the expected bucket write path.
	path pubscoop.FilePath
	// environment is the command's injected process environment.
	environment map[string]string
	// options constructs the root command.
	options cli.Options
	// reader is the generated bucket read mock.
	reader *mocks.MockRepositoryReader
	// writer is the generated bucket write mock.
	writer *mocks.MockRepositoryWriter
	// stdout receives machine-readable output.
	stdout *strings.Builder
	// stderr receives diagnostics.
	stderr *strings.Builder
}

// newScoopCLI constructs one valid command fixture.
func newScoopCLI(t *testing.T) *scoopCLI {
	t.Helper()

	dist := t.TempDir()
	directory := filepath.Join(dist, "scoop")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	content := []byte("{\n  \"version\": \"1.2.3\",\n  \"url\": \"https://example.invalid/release-cli.zip\"\n}\n")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "release-cli.json"), content, 0o600))
	bucket, err := pubscoop.ParseRepository("meigma/scoop-bucket")
	require.NoError(t, err)
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	environment := map[string]string{
		"RELEASE_APP_TOKEN": scoopToken,
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_REF_NAME":   "v1.2.3",
		"GITHUB_SHA":        scoopCommit,
	}

	fixture := &scoopCLI{
		dist:        dist,
		content:     content,
		bucket:      bucket,
		path:        "release-cli.json",
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
func (fixture *scoopCLI) execute(arguments ...string) error {
	command := cli.NewRootCommand(fixture.options)
	command.SetArgs(arguments)
	return command.ExecuteContext(context.Background())
}

// expectPublishedScoop stubs a matching default-branch manifest.
func expectPublishedScoop(fixture *scoopCLI) {
	fixture.reader.EXPECT().ReadBase(mock.Anything, fixture.bucket, fixture.path).Return(pubscoop.BaseSnapshot{
		Branch: "main",
		Commit: "1111111111111111111111111111111111111111",
		File: pubscoop.File{
			Present: true,
			Content: fixture.content,
			SHA:     "2222222222222222222222222222222222222222",
		},
	}, nil).Once()
	fixture.reader.EXPECT().ReadPullRequest(
		mock.Anything,
		fixture.bucket,
		pubscoop.BranchName("main"),
		pubscoop.BranchName("release/release-cli/v1.2.3"),
	).Return(pubscoop.PullRequest{State: pubscoop.PullRequestAbsent}, nil).Once()
}

// decodeScoopResult unmarshals the envelope result as [pubscoop.PublishResult].
func decodeScoopResult(t *testing.T, stdout string) pubscoop.PublishResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, scoopCommand, envelope.Command)
	assert.True(t, envelope.OK)
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result pubscoop.PublishResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertScoopFailureEnvelope checks stdout is one ok:false publish-scoop envelope.
func assertScoopFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, scoopCommand, envelope.Command)
	assert.False(t, envelope.OK)
	assert.NotContains(t, stdout, scoopToken)

	if wantError == "" {
		return
	}

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
}
