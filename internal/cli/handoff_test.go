package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghact"
	ghactmocks "github.com/meigma/release/internal/adapter/ghact/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	handoffDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	handoffToken  = "ghs_should_never_appear"
)

func TestVerifyHandoffMissingRepositoryIsUsage(t *testing.T) {
	t.Parallel()

	stdout, _, err := executeHandoff(t, map[string]string{
		"GITHUB_RUN_ID": "100",
		"GITHUB_TOKEN":  handoffToken,
	}, []string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Contains(t, err.Error(), "GITHUB_REPOSITORY is required")
}

func TestVerifyHandoffMissingRunIsUsage(t *testing.T) {
	t.Parallel()

	_, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_TOKEN":      handoffToken,
	}, []string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "GITHUB_RUN_ID is required")
}

func TestVerifyHandoffMalformedRepositoryIsUsage(t *testing.T) {
	t.Parallel()

	_, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "not-a-repo",
		"GITHUB_RUN_ID":     "100",
		"GITHUB_TOKEN":      handoffToken,
	}, []string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "not an owner/name pair")
}

func TestVerifyHandoffMissingFlagsIsUsage(t *testing.T) {
	t.Parallel()

	_, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_RUN_ID":     "100",
	}, []string{"verify", "handoff"}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "--artifact-id is required")
}

func TestVerifyHandoffConfigErrorBeforeNetwork(t *testing.T) {
	t.Parallel()

	_, _, err := executeHandoff(t, nil, []string{
		"verify", "handoff",
		"--artifact-id", "1",
		"--digest", handoffDigest,
	}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
}

func TestVerifyHandoffMissingTokenIsUsage(t *testing.T) {
	t.Parallel()

	called := false
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			values := map[string]string{
				"GITHUB_REPOSITORY": "meigma/release",
				"GITHUB_RUN_ID":     "100",
			}
			value, ok := values[key]
			return value, ok
		},
		NewArtifactMeta: func(string, cli.GitHubEndpoint) (pubgh.ArtifactMeta, error) {
			called = true
			return unusedMeta(t), nil
		},
	})
	command.SetArgs([]string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest})
	err := command.Execute()
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "GITHUB_TOKEN or GH_TOKEN is required")
	assert.False(t, called)
}

func TestVerifyHandoffJSONSuccess(t *testing.T) {
	t.Parallel()

	expires := time.Date(2026, 8, 19, 15, 4, 5, 0, time.UTC)
	meta := matchingMeta(t, 11, 100, expires)

	stdout, stderr, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_RUN_ID":     "100",
		"GITHUB_TOKEN":      handoffToken,
	}, []string{"verify", "handoff", "--artifact-id", "11", "--digest", handoffDigest, "--json"}, meta)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, "verify handoff", envelope.Command)
	assert.True(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.HandoffResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, int64(11), result.Artifact.ID)
	assert.Equal(t, "release-assets", result.Artifact.Name)
	assert.Equal(t, handoffDigest, result.Artifact.Digest)
	assert.Equal(t, int64(42), result.Artifact.SizeBytes)
	assert.Equal(t, int64(100), result.Artifact.RunID)
	assert.Equal(t, "2026-08-19T15:04:05Z", result.Artifact.ExpiresAt)
}

func TestVerifyHandoffSilentSuccessWithoutJSON(t *testing.T) {
	t.Parallel()

	meta := matchingMeta(t, 11, 100, time.Time{})
	stdout, stderr, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_RUN_ID":     "100",
		"GH_TOKEN":          handoffToken,
	}, []string{"verify", "handoff", "--artifact-id", "11", "--digest", handoffDigest}, meta)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVerifyHandoffMismatchIsExitOne(t *testing.T) {
	t.Parallel()

	meta := matchingMeta(t, 11, 200, time.Time{})
	stdout, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_RUN_ID":     "100",
		"GITHUB_TOKEN":      handoffToken,
	}, []string{"verify", "handoff", "--artifact-id", "11", "--digest", handoffDigest, "--json"}, meta)
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "belongs to workflow run 200")
	assert.NotContains(t, err.Error(), handoffToken)
	assert.Contains(t, stdout, `"command":"verify handoff"`)
	assert.Contains(t, stdout, `"ok":false`)
}

func TestVerifyHandoffFlagOverridesEnv(t *testing.T) {
	t.Parallel()

	var gotID pubgh.ArtifactID
	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ pubgh.Repository, id pubgh.ArtifactID) {
			gotID = id
		}).
		Return(pubgh.ArtifactMetadata{
			ID:     mustTestArtifactID(t, 11),
			Digest: mustTestDigest(t, handoffDigest),
			HasRun: true,
			Run:    mustTestRunID(t, 100),
		}, nil).
		Once()

	_, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY":   "meigma/release",
		"GITHUB_RUN_ID":       "100",
		"GITHUB_TOKEN":        handoffToken,
		"RELEASE_ARTIFACT_ID": "999",
		"RELEASE_DIGEST":      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}, []string{"verify", "handoff", "--artifact-id", "11", "--digest", handoffDigest}, meta)
	require.NoError(t, err)
	assert.Equal(t, int64(11), gotID.Int64())
}

func TestVerifyHandoffAbsentAPIURLKeepsDefault(t *testing.T) {
	t.Parallel()

	var got cli.GitHubEndpoint
	called := false
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			values := map[string]string{
				"GITHUB_REPOSITORY": "meigma/release",
				"GITHUB_RUN_ID":     "100",
				"GITHUB_TOKEN":      handoffToken,
			}
			value, ok := values[key]
			return value, ok
		},
		NewArtifactMeta: func(_ string, endpoint cli.GitHubEndpoint) (pubgh.ArtifactMeta, error) {
			called = true
			got = endpoint
			return matchingMeta(t, 1, 100, time.Time{}), nil
		},
	})
	command.SetArgs([]string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest})
	require.NoError(t, command.Execute())
	require.True(t, called)
	assert.Empty(t, got.APIURL)
	assert.Empty(t, got.ServerURL)
}

func TestVerifyHandoffPublicAPIURLKeepsDefault(t *testing.T) {
	t.Parallel()

	var got cli.GitHubEndpoint
	command := cli.NewRootCommand(cli.Options{
		Out: &strings.Builder{},
		Err: &strings.Builder{},
		LookupEnv: func(key string) (string, bool) {
			values := map[string]string{
				"GITHUB_REPOSITORY": "meigma/release",
				"GITHUB_RUN_ID":     "100",
				"GITHUB_TOKEN":      handoffToken,
				"GITHUB_API_URL":    "https://api.github.com",
			}
			value, ok := values[key]
			return value, ok
		},
		NewArtifactMeta: func(_ string, endpoint cli.GitHubEndpoint) (pubgh.ArtifactMeta, error) {
			got = endpoint
			return matchingMeta(t, 1, 100, time.Time{}), nil
		},
	})
	command.SetArgs([]string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest})
	require.NoError(t, command.Execute())
	assert.Empty(t, got.APIURL)
}

func TestVerifyHandoffCustomAPIURLIsUsedVerbatim(t *testing.T) {
	t.Parallel()

	var got cli.GitHubEndpoint
	command := cli.NewRootCommand(cli.Options{
		Out: &strings.Builder{},
		Err: &strings.Builder{},
		LookupEnv: func(key string) (string, bool) {
			values := map[string]string{
				"GITHUB_REPOSITORY": "meigma/release",
				"GITHUB_RUN_ID":     "100",
				"GITHUB_TOKEN":      handoffToken,
				"GITHUB_API_URL":    "https://github.example.internal/api/v3",
				"GITHUB_SERVER_URL": "https://github.example.internal",
			}
			value, ok := values[key]
			return value, ok
		},
		NewArtifactMeta: func(_ string, endpoint cli.GitHubEndpoint) (pubgh.ArtifactMeta, error) {
			got = endpoint
			return matchingMeta(t, 1, 100, time.Time{}), nil
		},
	})
	command.SetArgs([]string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest})
	require.NoError(t, command.Execute())
	assert.Equal(t, "https://github.example.internal/api/v3", got.APIURL)
	assert.Equal(t, "https://github.example.internal", got.ServerURL)
}

func TestVerifyHandoffMalformedAPIURLIsUsage(t *testing.T) {
	t.Parallel()

	_, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_RUN_ID":     "100",
		"GITHUB_TOKEN":      handoffToken,
		"GITHUB_API_URL":    "not-a-url",
	}, []string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "GITHUB_API_URL")
	assert.Contains(t, err.Error(), "not an absolute URL")
}

func TestVerifyHandoffEmptyAPIURLIsUsage(t *testing.T) {
	t.Parallel()

	_, _, err := executeHandoff(t, map[string]string{
		"GITHUB_REPOSITORY": "meigma/release",
		"GITHUB_RUN_ID":     "100",
		"GITHUB_TOKEN":      handoffToken,
		"GITHUB_API_URL":    "   ",
	}, []string{"verify", "handoff", "--artifact-id", "1", "--digest", handoffDigest}, unusedMeta(t))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "GITHUB_API_URL is empty")
}

func TestVerifyHandoffCustomAPIURLHitsStub(t *testing.T) {
	t.Parallel()

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit = true
		assert.Equal(t, "/api/v3/repos/meigma/release/actions/artifacts/11", request.URL.Path)
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"id":           11,
			"name":         "release-assets",
			"digest":       handoffDigest,
			"expired":      false,
			"workflow_run": map[string]any{"id": 100},
		}))
	}))
	t.Cleanup(server.Close)

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			values := map[string]string{
				"GITHUB_REPOSITORY": "meigma/release",
				"GITHUB_RUN_ID":     "100",
				"GITHUB_TOKEN":      handoffToken,
				"GITHUB_API_URL":    server.URL,
			}
			value, ok := values[key]
			return value, ok
		},
		NewArtifactMeta: func(token string, endpoint cli.GitHubEndpoint) (pubgh.ArtifactMeta, error) {
			assert.Equal(t, server.URL, endpoint.APIURL)
			return ghact.NewAuthenticated(token, endpoint.APIURL, endpoint.ServerURL)
		},
	})
	command.SetArgs([]string{"verify", "handoff", "--artifact-id", "11", "--digest", handoffDigest, "--json"})
	require.NoError(t, command.Execute())
	assert.True(t, hit, "request must arrive at GITHUB_API_URL, not api.github.com")
	assert.Contains(t, stdout.String(), `"command":"verify handoff"`)
	assert.Contains(t, stdout.String(), `"ok":true`)
}

// unusedMeta returns a generated mock that fails if Get is called.
func unusedMeta(t *testing.T) *ghactmocks.MockArtifactMeta {
	t.Helper()

	return ghactmocks.NewMockArtifactMeta(t)
}

// matchingMeta returns a generated mock that serves one successful lookup.
func matchingMeta(t *testing.T, artifactID, runID int64, expires time.Time) *ghactmocks.MockArtifactMeta {
	t.Helper()

	meta := ghactmocks.NewMockArtifactMeta(t)
	meta.EXPECT().
		Get(mock.Anything, mock.Anything, mock.Anything).
		Return(pubgh.ArtifactMetadata{
			ID:        mustTestArtifactID(t, artifactID),
			Name:      "release-assets",
			Digest:    mustTestDigest(t, handoffDigest),
			SizeBytes: 42,
			HasRun:    true,
			Run:       mustTestRunID(t, runID),
			ExpiresAt: expires,
		}, nil).
		Once()

	return meta
}

// executeHandoff runs verify handoff with an injected metadata port.
func executeHandoff(
	t *testing.T,
	env map[string]string,
	args []string,
	meta pubgh.ArtifactMeta,
) (string, string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		ArtifactMeta: meta,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// mustTestArtifactID constructs an ArtifactID or fails the test.
func mustTestArtifactID(t *testing.T, value int64) pubgh.ArtifactID {
	t.Helper()

	id, err := pubgh.ArtifactIDFromInt(value)
	require.NoError(t, err)

	return id
}

// mustTestRunID constructs a RunID or fails the test.
func mustTestRunID(t *testing.T, value int64) pubgh.RunID {
	t.Helper()

	id, err := pubgh.RunIDFromInt(value)
	require.NoError(t, err)

	return id
}

// mustTestDigest constructs an ArtifactDigest or fails the test.
func mustTestDigest(t *testing.T, value string) pubgh.ArtifactDigest {
	t.Helper()

	digest, err := pubgh.ParseArtifactDigest(value)
	require.NoError(t, err)

	return digest
}
