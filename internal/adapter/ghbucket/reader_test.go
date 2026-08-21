package ghbucket_test

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

	"github.com/meigma/release/internal/adapter/ghbucket"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// testToken is a credential marker that must never appear in errors.
	testToken = "ghs_scoop_adapter_secret"
	// testBaseSHA is the bucket default-branch commit.
	testBaseSHA = "1111111111111111111111111111111111111111"
	// testHeadSHA is the publication branch commit.
	testHeadSHA = "2222222222222222222222222222222222222222"
	// testBlobSHA is the manifest blob commit.
	testBlobSHA = "3333333333333333333333333333333333333333"
	// testPullURL is the publication review URL.
	testPullURL = "https://github.com/meigma/scoop-bucket/pull/7"
)

// TestClientSatisfiesPorts proves the focused adapter implements both bucket ports.
func TestClientSatisfiesPorts(t *testing.T) {
	t.Parallel()

	var (
		_ pubscoop.RepositoryReader = (*ghbucket.Client)(nil)
		_ pubscoop.RepositoryWriter = (*ghbucket.Client)(nil)
	)
}

// TestReadBaseReturnsDefaultBranchAndManifest proves the base snapshot is bound
// to one immutable commit rather than racing against a moving branch ref.
func TestReadBaseReturnsDefaultBranchAndManifest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/repos/meigma/scoop-bucket":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"default_branch": "main"}))
		case "/repos/meigma/scoop-bucket/git/ref/heads/main":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"ref":    "refs/heads/main",
				"object": map[string]any{"sha": testBaseSHA, "type": "commit"},
			}))
		case "/repos/meigma/scoop-bucket/contents/release-cli.json":
			assert.Equal(t, testBaseSHA, request.URL.Query().Get("ref"))
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"type":     "file",
				"sha":      testBlobSHA,
				"encoding": "base64",
				"content":  "eyJ2ZXJzaW9uIjoiMS4yLjIifQ==",
			}))
		default:
			t.Fatalf("unexpected request %s", request.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).ReadBase(context.Background(), mustRepository(t), testPath())
	require.NoError(t, err)
	assert.Equal(t, pubscoop.BranchName("main"), got.Branch)
	assert.Equal(t, pubscoop.CommitSHA(testBaseSHA), got.Commit)
	assert.JSONEq(t, `{"version":"1.2.2"}`, string(got.File.Content))
	assert.Equal(t, pubscoop.BlobSHA(testBlobSHA), got.File.SHA)
}

// TestReadBranchMapsOneCommit proves branch reconciliation receives the exact
// head commit parent, changed paths, and decoded manifest.
func TestReadBranchMapsOneCommit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/meigma/scoop-bucket/git/ref/heads/release/release-cli/v1.2.3":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"ref":    "refs/heads/release/release-cli/v1.2.3",
				"object": map[string]any{"sha": testHeadSHA, "type": "commit"},
			}))
		case "/repos/meigma/scoop-bucket/commits/" + testHeadSHA:
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"sha":     testHeadSHA,
				"parents": []map[string]any{{"sha": testBaseSHA}},
				"files":   []map[string]any{{"filename": "release-cli.json", "status": "modified"}},
			}))
		case "/repos/meigma/scoop-bucket/contents/release-cli.json":
			assert.Equal(t, testHeadSHA, request.URL.Query().Get("ref"))
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"type":     "file",
				"sha":      testBlobSHA,
				"encoding": "base64",
				"content":  "eyJ2ZXJzaW9uIjoiMS4yLjMifQ==",
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
	assert.Equal(t, pubscoop.CommitSHA(testHeadSHA), got.Commit)
	assert.Equal(t, pubscoop.CommitSHA(testBaseSHA), got.Parent)
	require.Len(t, got.Files, 1)
	assert.Equal(t, testPath(), got.Files[0].Path)
	assert.Equal(t, pubscoop.ChangeModified, got.Files[0].Status)
	assert.JSONEq(t, `{"version":"1.2.3"}`, string(got.File.Content))
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
		assert.Equal(t, "/repos/meigma/scoop-bucket/pulls", request.URL.Path)
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
	assert.Equal(t, pubscoop.PullRequestMerged, got.State)
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
	require.ErrorIs(t, err, pubscoop.ErrRetryable)
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), server.URL)
}

// TestReadBaseClassifiesAuthenticationFailure proves unauthorized responses
// stay fail-closed without leaking credentials.
func TestReadBaseClassifiesAuthenticationFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).ReadBase(context.Background(), mustRepository(t), testPath())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github authentication failed")
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), "Bearer")
	assert.NotContains(t, err.Error(), server.URL)
}

// TestReadBaseCanceledErrorOmitsURL proves cancellation stays typed and never
// includes the request URL or token.
func TestReadBaseCanceledErrorOmitsURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newClient(t, server).ReadBase(ctx, mustRepository(t), testPath())
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "request canceled")
	assert.NotContains(t, err.Error(), server.URL)
	assert.NotContains(t, err.Error(), testToken)
}

// TestReadBaseRejectsNilClient proves an uninitialized adapter fails closed.
func TestReadBaseRejectsNilClient(t *testing.T) {
	t.Parallel()

	client := ghbucket.New(nil)
	_, err := client.ReadBase(context.Background(), mustRepository(t), testPath())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github client is nil")
}

// TestReadBaseRejectsNilContext proves a missing context fails before I/O.
func TestReadBaseRejectsNilContext(t *testing.T) {
	t.Parallel()

	var ctx context.Context
	client := ghbucket.New(github.NewClient(nil))
	_, err := client.ReadBase(ctx, mustRepository(t), testPath())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is nil")
}

// TestNewAuthenticatedUsesCustomAPIBase proves enterprise and stub endpoints
// receive the token on the go-github v3 path.
func TestNewAuthenticatedUsesCustomAPIBase(t *testing.T) {
	t.Parallel()

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit = true
		assert.Contains(t, request.URL.Path, "/api/v3/repos/meigma/scoop-bucket")
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"default_branch": "main"}))
	}))
	t.Cleanup(server.Close)

	client, err := ghbucket.NewAuthenticated(rel.NewSecret(testToken), server.URL, server.URL)
	require.NoError(t, err)
	_, err = client.ReadBase(context.Background(), mustRepository(t), testPath())
	require.Error(t, err)
	assert.True(t, hit)
}

// TestNewAuthenticatedEmptyAPIURLUsesPublicHost proves an empty endpoint keeps
// the public GitHub client.
func TestNewAuthenticatedEmptyAPIURLUsesPublicHost(t *testing.T) {
	t.Parallel()

	client, err := ghbucket.NewAuthenticated(rel.NewSecret(testToken), "", "")
	require.NoError(t, err)
	require.NotNil(t, client)
}

// newClient returns an authenticated go-github client pointed at server.
func newClient(t *testing.T, server *httptest.Server) *ghbucket.Client {
	t.Helper()

	parsed, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	client := github.NewClient(server.Client()).WithAuthToken(testToken)
	client.BaseURL = parsed

	return ghbucket.New(client)
}

// mustRepository parses the bucket fixture or fails the test.
func mustRepository(t *testing.T) pubscoop.Repository {
	t.Helper()

	repository, err := pubscoop.ParseRepository("meigma/scoop-bucket")
	require.NoError(t, err)

	return repository
}

// testPath returns the publisher's only changed path.
func testPath() pubscoop.FilePath {
	return "release-cli.json"
}
