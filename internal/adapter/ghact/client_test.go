package ghact_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghact"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testToken  = "ghs_this_must_never_appear_in_errors"
)

func TestGetMapsArtifact(t *testing.T) {
	t.Parallel()

	expires := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/repos/meigma/release/actions/artifacts/11", request.URL.Path)
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"id":            11,
			"name":          "release-assets",
			"size_in_bytes": 42,
			"expired":       false,
			"expires_at":    expires.Format(time.RFC3339),
			"digest":        testDigest,
			"workflow_run":  map[string]any{"id": 99},
		}))
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).Get(context.Background(), mustRepo(t), mustArtifactID(t, 11))
	require.NoError(t, err)
	assert.Equal(t, int64(11), got.ID.Int64())
	assert.Equal(t, "release-assets", got.Name)
	assert.Equal(t, testDigest, got.Digest.String())
	assert.Equal(t, int64(42), got.SizeBytes)
	assert.True(t, got.HasRun)
	assert.Equal(t, int64(99), got.Run.Int64())
	assert.True(t, got.ExpiresAt.Equal(expires))
	assert.False(t, got.Expired)
}

func TestGetClassifiesAPIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		wantSent error
		wantText string
	}{
		{name: "absent", status: http.StatusNotFound, wantText: "artifact not found"},
		{
			name:     "unauthorized",
			status:   http.StatusUnauthorized,
			wantText: "github authentication failed",
		},
		{name: "forbidden", status: http.StatusForbidden, wantText: "github authentication failed"},
		{
			name:     "too many requests",
			status:   http.StatusTooManyRequests,
			wantSent: pubgh.ErrRetryable,
			wantText: "retryable",
		},
		{name: "server error", status: http.StatusBadGateway, wantSent: pubgh.ErrRetryable, wantText: "retryable"},
		{
			name:     "other client error",
			status:   http.StatusUnprocessableEntity,
			wantText: "malformed artifact metadata",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(`{"message":"nope"}`))
			}))
			t.Cleanup(server.Close)

			_, err := newClient(t, server).Get(
				context.Background(),
				mustRepo(t),
				mustArtifactID(t, 1),
			)
			require.Error(t, err)
			if test.wantSent != nil {
				require.ErrorIs(t, err, test.wantSent)
			} else {
				require.NotErrorIs(t, err, pubgh.ErrRetryable)
			}
			assert.Contains(t, err.Error(), test.wantText)
			assert.NotContains(t, err.Error(), testToken)
		})
	}
}

func TestGetRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"name": "release-assets",
		}))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).Get(context.Background(), mustRepo(t), mustArtifactID(t, 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed artifact metadata: artifact id is missing")
	assert.Contains(t, err.Error(), "artifact id is missing")
	assert.NotContains(t, err.Error(), testToken)
}

func TestGetRejectsUnparseableDigest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"id":           1,
			"digest":       "not-a-digest",
			"workflow_run": map[string]any{"id": 2},
		}))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).Get(context.Background(), mustRepo(t), mustArtifactID(t, 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed artifact metadata")
	assert.NotContains(t, err.Error(), testToken)
}

func TestGetRejectsNilClient(t *testing.T) {
	t.Parallel()

	client := ghact.New(nil)
	_, err := client.Get(context.Background(), mustRepo(t), mustArtifactID(t, 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github client is nil")
}

func TestWrappedAPIErrorOmitsToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).Get(context.Background(), mustRepo(t), mustArtifactID(t, 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github authentication failed")
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), "Bearer")
}

func TestGetCanceledErrorOmitsURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newClient(t, server).Get(ctx, mustRepo(t), mustArtifactID(t, 1))
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "request canceled")
	assert.NotContains(t, err.Error(), server.URL)
	assert.NotContains(t, err.Error(), testToken)
}

func TestNewAuthenticatedUsesCustomAPIBase(t *testing.T) {
	t.Parallel()

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit = true
		assert.Equal(t, "/api/v3/repos/meigma/release/actions/artifacts/11", request.URL.Path)
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		assert.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"id":           11,
			"name":         "release-assets",
			"digest":       testDigest,
			"workflow_run": map[string]any{"id": 99},
		}))
	}))
	t.Cleanup(server.Close)

	client, err := ghact.NewAuthenticated(testToken, server.URL, server.URL)
	require.NoError(t, err)
	got, err := client.Get(context.Background(), mustRepo(t), mustArtifactID(t, 11))
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, int64(11), got.ID.Int64())
	assert.Equal(t, testDigest, got.Digest.String())
}

func TestNewAuthenticatedEmptyAPIURLUsesPublicHost(t *testing.T) {
	t.Parallel()

	client, err := ghact.NewAuthenticated(testToken, "", "")
	require.NoError(t, err)
	require.NotNil(t, client)
}

// newClient returns an authenticated go-github client pointed at server.
func newClient(t *testing.T, server *httptest.Server) *ghact.Client {
	t.Helper()

	parsed, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	client := github.NewClient(server.Client()).WithAuthToken(testToken)
	client.BaseURL = parsed

	return ghact.New(client)
}

// mustRepo parses the fixture repository or fails the test.
func mustRepo(t *testing.T) pubgh.Repository {
	t.Helper()

	repo, err := pubgh.ParseRepository("meigma/release")
	require.NoError(t, err)

	return repo
}

// mustArtifactID parses an artifact id or fails the test.
func mustArtifactID(t *testing.T, value int64) pubgh.ArtifactID {
	t.Helper()

	id, err := pubgh.ArtifactIDFromInt(value)
	require.NoError(t, err)

	return id
}
