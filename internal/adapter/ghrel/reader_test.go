package ghrel_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghrel"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	testToken     = "ghs_this_must_never_appear_in_errors"
	testTag       = "v1.2.3"
	testDigest    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testReleaseID = int64(42)
	htmlURL       = "https://github.com/meigma/release/releases/tag/v1.2.3"
)

func TestClientSatisfiesPorts(t *testing.T) {
	t.Parallel()

	var (
		_ pubgh.ReleaseReader = (*ghrel.Client)(nil)
		_ pubgh.Publisher     = (*ghrel.Client)(nil)
	)
}

func TestFindDraftPaginatesAndSelectsExactTag(t *testing.T) {
	t.Parallel()

	var pages []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "/repos/meigma/release/releases", request.URL.Path)
		assert.Equal(t, "100", request.URL.Query().Get("per_page"))
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		page := request.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		parsed, err := strconv.Atoi(page)
		if !assert.NoError(t, err) {
			return
		}
		pages = append(pages, parsed)
		switch page {
		case "1":
			writer.Header().Set(
				"Link",
				`<http://`+request.Host+`/repos/meigma/release/releases?page=2>; rel="next"`,
			)
			assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
				releasePayload(7, "v9.9.9", true),
			}))
		case "2":
			assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
				releasePayload(testReleaseID, testTag, true),
				releasePayload(8, "v0.0.1", true),
			}))
		default:
			t.Fatalf("unexpected list page %q", page)
		}
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).FindDraft(
		context.Background(),
		mustRepo(t),
		mustTag(t),
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, pages)
	assert.Equal(t, testReleaseID, got.ID.Int64())
	assert.Equal(t, testTag, got.Tag.String())
	assert.True(t, got.Draft)
	assert.Equal(t, htmlURL, got.URL)
}

func TestFindDraftReportsNoDraftImmediately(t *testing.T) {
	t.Parallel()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		assert.Equal(t, "/repos/meigma/release/releases", request.URL.Path)
		assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{}))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).FindDraft(
		context.Background(),
		mustRepo(t),
		mustTag(t),
	)
	require.ErrorIs(t, err, pubgh.ErrNoDraft)
	assert.Equal(t, 1, hits)
}

func TestFindDraftReportsAmbiguousRelease(t *testing.T) {
	t.Parallel()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
			releasePayload(1, testTag, true),
			releasePayload(2, testTag, true),
		}))
	}))
	t.Cleanup(server.Close)

	_, err := newClient(t, server).FindDraft(
		context.Background(),
		mustRepo(t),
		mustTag(t),
	)
	require.ErrorIs(t, err, pubgh.ErrAmbiguousRelease)
	assert.Equal(t, 1, hits)
}

func TestFindDraftReturnsPublishedMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
			releasePayload(testReleaseID, testTag, false),
		}))
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).FindDraft(
		context.Background(),
		mustRepo(t),
		mustTag(t),
	)
	require.NoError(t, err)
	assert.Equal(t, testReleaseID, got.ID.Int64())
	assert.False(t, got.Draft)
}

func TestWaitAssetsReturnsCurrentView(t *testing.T) {
	t.Parallel()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		assert.Equal(t, "/repos/meigma/release/releases/42/assets", request.URL.Path)
		assert.Equal(t, "100", request.URL.Query().Get("per_page"))
		assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
			assetPayload("checksums.txt", testDigest, "uploaded"),
		}))
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).WaitAssets(
		context.Background(),
		mustRepo(t),
		mustReleaseID(t),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, hits)
	require.Len(t, got.Assets, 1)
	assert.Equal(t, "checksums.txt", got.Assets[0].Name)
	assert.Equal(t, testDigest, got.Assets[0].Digest)
	assert.Equal(t, "uploaded", got.Assets[0].State)
}

func TestWaitAssetsReturnsIncompleteSnapshot(t *testing.T) {
	t.Parallel()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
			assetPayload("checksums.txt", "", "uploaded"),
		}))
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).WaitAssets(
		context.Background(),
		mustRepo(t),
		mustReleaseID(t),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, hits)
	require.Len(t, got.Assets, 1)
	assert.Equal(t, "checksums.txt", got.Assets[0].Name)
	assert.Empty(t, got.Assets[0].Digest)
	assert.Equal(t, "uploaded", got.Assets[0].State)
}

func TestWaitAssetsPaginates(t *testing.T) {
	t.Parallel()

	var pages []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/repos/meigma/release/releases/42/assets", request.URL.Path)
		page := request.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		parsed, err := strconv.Atoi(page)
		if !assert.NoError(t, err) {
			return
		}
		pages = append(pages, parsed)
		switch page {
		case "1":
			writer.Header().Set(
				"Link",
				`<http://`+request.Host+`/repos/meigma/release/releases/42/assets?page=2>; rel="next"`,
			)
			assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
				assetPayload("checksums.txt", testDigest, "uploaded"),
			}))
		case "2":
			assert.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
				assetPayload("checksums.txt.sigstore.json", "", "new"),
			}))
		default:
			t.Fatalf("unexpected asset page %q", page)
		}
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).WaitAssets(
		context.Background(),
		mustRepo(t),
		mustReleaseID(t),
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, pages)
	require.Len(t, got.Assets, 2)
	assert.Equal(t, "checksums.txt", got.Assets[0].Name)
	assert.Equal(t, "checksums.txt.sigstore.json", got.Assets[1].Name)
	assert.Empty(t, got.Assets[1].Digest)
	assert.Equal(t, "new", got.Assets[1].State)
}

func TestGetClassifiesAPIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		wantSent error
		wantText string
	}{
		{name: "absent", status: http.StatusNotFound, wantText: "release not found"},
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
		{
			name:     "server error",
			status:   http.StatusServiceUnavailable,
			wantSent: pubgh.ErrRetryable,
			wantText: "retryable",
		},
		{
			name:     "other client error",
			status:   http.StatusUnprocessableEntity,
			wantText: "malformed release metadata",
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
				mustReleaseID(t),
			)
			require.Error(t, err)
			if test.wantSent != nil {
				require.ErrorIs(t, err, test.wantSent)
			} else {
				require.NotErrorIs(t, err, pubgh.ErrRetryable)
			}
			assert.Contains(t, err.Error(), test.wantText)
			assert.NotContains(t, err.Error(), testToken)
			assert.NotContains(t, err.Error(), "Bearer")
		})
	}
}

func TestGetMapsRelease(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/repos/meigma/release/releases/42", request.URL.Path)
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		assert.NoError(t, json.NewEncoder(writer).Encode(releasePayload(testReleaseID, testTag, true)))
	}))
	t.Cleanup(server.Close)

	got, err := newClient(t, server).Get(context.Background(), mustRepo(t), mustReleaseID(t))
	require.NoError(t, err)
	assert.Equal(t, testReleaseID, got.ID.Int64())
	assert.Equal(t, testTag, got.Tag.String())
	assert.True(t, got.Draft)
	assert.Equal(t, htmlURL, got.URL)
}

func TestFindDraftCanceledStopsBeforeRequest(t *testing.T) {
	t.Parallel()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newClient(t, server).FindDraft(ctx, mustRepo(t), mustTag(t))
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "request canceled")
	assert.Zero(t, hits)
	assert.NotContains(t, err.Error(), server.URL)
	assert.NotContains(t, err.Error(), testToken)
}

func TestGetCanceledErrorOmitsURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newClient(t, server).Get(ctx, mustRepo(t), mustReleaseID(t))
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "request canceled")
	assert.NotContains(t, err.Error(), server.URL)
	assert.NotContains(t, err.Error(), testToken)
}

func TestGetRejectsNilClient(t *testing.T) {
	t.Parallel()

	client := ghrel.New(nil)
	_, err := client.Get(context.Background(), mustRepo(t), mustReleaseID(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github client is nil")
}

func TestGetRejectsNilContext(t *testing.T) {
	t.Parallel()

	var ctx context.Context
	client := ghrel.New(github.NewClient(nil))
	_, err := client.Get(ctx, mustRepo(t), mustReleaseID(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is nil")
}

func TestNewAuthenticatedUsesCustomAPIBase(t *testing.T) {
	t.Parallel()

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit = true
		assert.Equal(t, "/api/v3/repos/meigma/release/releases/42", request.URL.Path)
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		assert.NoError(t, json.NewEncoder(writer).Encode(releasePayload(testReleaseID, testTag, true)))
	}))
	t.Cleanup(server.Close)

	client, err := ghrel.NewAuthenticated(rel.NewSecret(testToken), server.URL, server.URL)
	require.NoError(t, err)
	got, err := client.Get(context.Background(), mustRepo(t), mustReleaseID(t))
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, testReleaseID, got.ID.Int64())
}

func TestNewAuthenticatedEmptyAPIURLUsesPublicHost(t *testing.T) {
	t.Parallel()

	client, err := ghrel.NewAuthenticated(rel.NewSecret(testToken), "", "")
	require.NoError(t, err)
	require.NotNil(t, client)
}

// newClient returns an authenticated go-github client pointed at server.
func newClient(t *testing.T, server *httptest.Server) *ghrel.Client {
	t.Helper()

	parsed, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	client := github.NewClient(server.Client()).WithAuthToken(testToken)
	client.BaseURL = parsed

	return ghrel.New(client)
}

// mustRepo parses the fixture repository or fails the test.
func mustRepo(t *testing.T) pubgh.Repository {
	t.Helper()

	repo, err := pubgh.ParseRepository("meigma/release")
	require.NoError(t, err)

	return repo
}

// mustTag parses the fixture tag or fails the test.
func mustTag(t *testing.T) rel.Tag {
	t.Helper()

	tag, err := rel.ParseTag(testTag)
	require.NoError(t, err)

	return tag
}

// mustReleaseID parses the fixture release id or fails the test.
func mustReleaseID(t *testing.T) pubgh.ReleaseID {
	t.Helper()

	id, err := pubgh.ReleaseIDFromInt(testReleaseID)
	require.NoError(t, err)

	return id
}

// releasePayload is one GitHub release JSON object.
func releasePayload(id int64, tag string, draft bool) map[string]any {
	return map[string]any{
		"id":       id,
		"tag_name": tag,
		"draft":    draft,
		"html_url": htmlURL,
	}
}

// assetPayload is one GitHub release-asset JSON object.
func assetPayload(name, digest, state string) map[string]any {
	payload := map[string]any{
		"name":  name,
		"state": state,
	}
	if digest != "" {
		payload["digest"] = digest
	}

	return payload
}
