package ghbucket_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage/pubscoop"
)

// TestCreateBranchUsesNonForceRefCreation proves branch publication cannot
// overwrite an existing reference.
func TestCreateBranchUsesNonForceRefCreation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/repos/meigma/scoop-bucket/git/refs", request.URL.Path)
		var payload map[string]any
		if !assert.NoError(t, json.NewDecoder(request.Body).Decode(&payload)) {
			return
		}
		assert.Equal(t, "refs/heads/release/release-cli/v1.2.3", payload["ref"])
		assert.Equal(t, testBaseSHA, payload["sha"])
		_, hasForce := payload["force"]
		assert.False(t, hasForce)
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"ref":    payload["ref"],
			"object": map[string]any{"sha": testBaseSHA},
		}))
	}))
	t.Cleanup(server.Close)

	err := newClient(t, server).CreateBranch(
		context.Background(),
		mustRepository(t),
		"release/release-cli/v1.2.3",
		testBaseSHA,
	)
	require.NoError(t, err)
}

// TestPutFileUpdatesOnlyExpectedManifest proves an existing manifest is
// replaced on the publication branch with the observed base blob SHA.
func TestPutFileUpdatesOnlyExpectedManifest(t *testing.T) {
	t.Parallel()

	content := []byte("{\n  \"version\": \"1.2.3\"\n}\n")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPut, request.Method)
		assert.Equal(t, "/repos/meigma/scoop-bucket/contents/release-cli.json", request.URL.Path)
		var payload struct {
			// Message is the commit subject.
			Message string `json:"message"`
			// Content is the API-encoded manifest body.
			Content string `json:"content"`
			// SHA is the previous blob object ID.
			SHA string `json:"sha"`
			// Branch is the publication branch.
			Branch string `json:"branch"`
		}
		if !assert.NoError(t, json.NewDecoder(request.Body).Decode(&payload)) {
			return
		}
		assert.Equal(t, "chore(manifest): update release-cli to 1.2.3", payload.Message)
		assert.Equal(t, base64.StdEncoding.EncodeToString(content), payload.Content)
		assert.Equal(t, testBlobSHA, payload.SHA)
		assert.Equal(t, "release/release-cli/v1.2.3", payload.Branch)
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"content": map[string]any{"sha": "4444444444444444444444444444444444444444"},
			"commit":  map[string]any{"sha": testHeadSHA},
		}))
	}))
	t.Cleanup(server.Close)

	err := newClient(t, server).PutFile(
		context.Background(),
		mustRepository(t),
		"release/release-cli/v1.2.3",
		testPath(),
		testBlobSHA,
		content,
		"chore(manifest): update release-cli to 1.2.3",
	)
	require.NoError(t, err)
}

// TestCreatePullRequestLeavesMergeManual proves publication opens a normal
// review without draft or auto-merge behavior.
func TestCreatePullRequestLeavesMergeManual(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/repos/meigma/scoop-bucket/pulls", request.URL.Path)
		var payload map[string]any
		if !assert.NoError(t, json.NewDecoder(request.Body).Decode(&payload)) {
			return
		}
		assert.Equal(t, "release/release-cli/v1.2.3", payload["head"])
		assert.Equal(t, "main", payload["base"])
		assert.Equal(t, false, payload["draft"])
		assert.Equal(t, false, payload["maintainer_can_modify"])
		_, hasAutoMerge := payload["auto_merge"]
		assert.False(t, hasAutoMerge)
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"state":    "open",
			"html_url": testPullURL,
		}))
	}))
	t.Cleanup(server.Close)

	url, err := newClient(t, server).CreatePullRequest(
		context.Background(),
		mustRepository(t),
		pubscoop.PullRequestInput{
			Base:  "main",
			Head:  "release/release-cli/v1.2.3",
			Title: "chore(manifest): update release-cli to 1.2.3",
			Body:  "Source release: https://github.com/meigma/release/releases/tag/v1.2.3",
		},
	)
	require.NoError(t, err)
	assert.Equal(t, testPullURL, url)
}

// TestCreateBranchClassifiesConflict proves GitHub refuses rather than
// overwrites an existing publication branch.
func TestCreateBranchClassifiesConflict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
	}))
	t.Cleanup(server.Close)

	err := newClient(t, server).CreateBranch(
		context.Background(),
		mustRepository(t),
		"release/release-cli/v1.2.3",
		testBaseSHA,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, pubscoop.ErrConflict)
	assert.NotContains(t, err.Error(), testToken)
}
