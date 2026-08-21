package ghtap_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghtap"
	"github.com/meigma/release/internal/stage/pubbrew"
)

const (
	// testToken is a credential marker that must never appear in errors.
	testToken = "ghs_homebrew_adapter_secret"
	// testBaseSHA is the tap default-branch commit.
	testBaseSHA = "1111111111111111111111111111111111111111"
	// testHeadSHA is the publication branch commit.
	testHeadSHA = "2222222222222222222222222222222222222222"
	// testBlobSHA is the cask blob commit.
	testBlobSHA = "3333333333333333333333333333333333333333"
	// testPullURL is the publication review URL.
	testPullURL = "https://github.com/meigma/homebrew-tap/pull/7"
)

// TestClientSatisfiesPorts proves the focused adapter implements both tap ports.
func TestClientSatisfiesPorts(t *testing.T) {
	t.Parallel()

	var (
		_ pubbrew.RepositoryReader = (*ghtap.Client)(nil)
		_ pubbrew.RepositoryWriter = (*ghtap.Client)(nil)
	)
}

// TestReadBaseReturnsDefaultBranchAndCask proves the base snapshot is bound to
// one immutable commit rather than racing against a moving branch ref.
func TestReadBaseReturnsDefaultBranchAndCask(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/repos/meigma/homebrew-tap":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"default_branch": "main"}))
		case "/repos/meigma/homebrew-tap/git/ref/heads/main":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"ref":    "refs/heads/main",
				"object": map[string]any{"sha": testBaseSHA, "type": "commit"},
			}))
		case "/repos/meigma/homebrew-tap/contents/Casks/release-cli.rb":
			assert.Equal(t, testBaseSHA, request.URL.Query().Get("ref"))
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"type":     "file",
				"sha":      testBlobSHA,
				"encoding": "base64",
				"content":  "dmVyc2lvbiAiMS4yLjIiCg==",
			}))
		default:
			t.Fatalf("unexpected request %s", request.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).ReadBase(context.Background(), mustRepository(t), testPath())
	require.NoError(t, err)
	assert.Equal(t, pubbrew.BranchName("main"), got.Branch)
	assert.Equal(t, pubbrew.CommitSHA(testBaseSHA), got.Commit)
	assert.Equal(t, []byte("version \"1.2.2\"\n"), got.File.Content)
	assert.Equal(t, pubbrew.BlobSHA(testBlobSHA), got.File.SHA)
}

// TestReadBranchMapsOneCommit proves branch reconciliation receives the exact
// head commit parent, changed paths, and decoded cask.
func TestReadBranchMapsOneCommit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/meigma/homebrew-tap/git/ref/heads/release/release-cli/v1.2.3":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"ref":    "refs/heads/release/release-cli/v1.2.3",
				"object": map[string]any{"sha": testHeadSHA, "type": "commit"},
			}))
		case "/repos/meigma/homebrew-tap/commits/" + testHeadSHA:
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"sha":     testHeadSHA,
				"parents": []map[string]any{{"sha": testBaseSHA}},
				"files":   []map[string]any{{"filename": "Casks/release-cli.rb", "status": "modified"}},
			}))
		case "/repos/meigma/homebrew-tap/contents/Casks/release-cli.rb":
			assert.Equal(t, testHeadSHA, request.URL.Query().Get("ref"))
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"type":     "file",
				"sha":      testBlobSHA,
				"encoding": "base64",
				"content":  "dmVyc2lvbiAiMS4yLjMiCg==",
			}))
		default:
			t.Fatalf("unexpected request %s", request.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).ReadBranch(
		context.Background(),
		mustRepository(t),
		"release/release-cli/v1.2.3",
		testPath(),
	)
	require.NoError(t, err)
	assert.True(t, got.Present)
	assert.Equal(t, pubbrew.CommitSHA(testHeadSHA), got.Commit)
	assert.Equal(t, pubbrew.CommitSHA(testBaseSHA), got.Parent)
	require.Len(t, got.Files, 1)
	assert.Equal(t, testPath(), got.Files[0].Path)
	assert.Equal(t, pubbrew.ChangeModified, got.Files[0].Status)
	assert.Equal(t, []byte("version \"1.2.3\"\n"), got.File.Content)
}

// TestReadBranchReturnsAbsent proves a missing deterministic branch is normal
// state rather than an adapter failure.
func TestReadBranchReturnsAbsent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).ReadBranch(
		context.Background(),
		mustRepository(t),
		"release/release-cli/v1.2.3",
		testPath(),
	)
	require.NoError(t, err)
	assert.False(t, got.Present)
}

// TestReadPullRequestMapsMergedReview proves the adapter filters the exact head
// and base and preserves the merged review URL.
func TestReadPullRequestMapsMergedReview(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/repos/meigma/homebrew-tap/pulls", request.URL.Path)
		assert.Equal(t, "all", request.URL.Query().Get("state"))
		assert.Equal(t, "meigma:release/release-cli/v1.2.3", request.URL.Query().Get("head"))
		assert.Equal(t, "main", request.URL.Query().Get("base"))
		assert.Equal(t, "100", request.URL.Query().Get("per_page"))
		assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{{
			"state":     "closed",
			"html_url":  testPullURL,
			"merged_at": "2026-08-20T00:00:00Z",
			"head":      map[string]any{"ref": "release/release-cli/v1.2.3"},
			"base":      map[string]any{"ref": "main"},
		}}))
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).ReadPullRequest(
		context.Background(),
		mustRepository(t),
		"main",
		"release/release-cli/v1.2.3",
	)
	require.NoError(t, err)
	assert.Equal(t, pubbrew.PullRequestMerged, got.State)
	assert.Equal(t, testPullURL, got.URL)
}

// TestReadBaseClassifiesRetryableFailure proves transient API failures remain
// retryable without leaking credentials or request URLs.
func TestReadBaseClassifiesRetryableFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(testToken))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).ReadBase(context.Background(), mustRepository(t), testPath())
	require.Error(t, err)
	require.ErrorIs(t, err, pubbrew.ErrRetryable)
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), server.URL)
}

// newClient returns an authenticated go-github client pointed at server.
func newClient(t *testing.T, server *httptest.Server) *ghtap.Client {
	t.Helper()

	parsed, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	client := github.NewClient(server.Client()).WithAuthToken(testToken)
	client.BaseURL = parsed

	return ghtap.New(client)
}

// mustRepository parses the tap fixture or fails the test.
func mustRepository(t *testing.T) pubbrew.Repository {
	t.Helper()

	repository, err := pubbrew.ParseRepository("meigma/homebrew-tap")
	require.NoError(t, err)

	return repository
}

// testPath returns the publisher's only changed path.
func testPath() pubbrew.FilePath {
	return "Casks/release-cli.rb"
}
