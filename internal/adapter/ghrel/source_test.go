package ghrel_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghrel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// sourceCommit is the exact commit behind the release tag fixture.
	sourceCommit = "0123456789abcdef0123456789abcdef01234567"
	// sourcePublishedAt is the stable publication timestamp fixture.
	sourcePublishedAt = "2026-08-21T12:34:56Z"
)

func TestClientSatisfiesReleaseSource(t *testing.T) {
	t.Parallel()

	var _ pkgrepo.ReleaseSource = (*ghrel.Client)(nil)
}

func TestFetchDownloadsSortedDigestVerifiedRelease(t *testing.T) {
	t.Parallel()

	bodies := map[int64][]byte{
		11: []byte("rpm-package"),
		12: []byte("apk-package"),
	}
	var assetPages []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/repos/meigma/release/releases/tags/v1.2.3":
			writeJSON(t, writer, map[string]any{
				"id":           testReleaseID,
				"tag_name":     testTag,
				"draft":        false,
				"prerelease":   false,
				"published_at": sourcePublishedAt,
			})
		case "/repos/meigma/release/releases/42/assets":
			assert.Equal(t, "100", request.URL.Query().Get("per_page"))
			page := request.URL.Query().Get("page")
			assetPages = append(assetPages, page)
			if page == "" {
				writer.Header().Set(
					"Link",
					`<http://`+request.Host+`/repos/meigma/release/releases/42/assets?page=2>; rel="next"`,
				)
				writeJSON(
					t,
					writer,
					[]map[string]any{sourceAssetPayload(12, "release-cli_1.2.3_linux_arm64.apk", bodies[12])},
				)
				return
			}
			assert.Equal(t, "2", page)
			writeJSON(
				t,
				writer,
				[]map[string]any{sourceAssetPayload(11, "release-cli_1.2.3_linux_amd64.rpm", bodies[11])},
			)
		case "/repos/meigma/release/git/ref/tags/v1.2.3":
			writeJSON(t, writer, map[string]any{
				"ref": "refs/tags/v1.2.3",
				"object": map[string]any{
					"type": "commit",
					"sha":  sourceCommit,
				},
			})
		case "/repos/meigma/release/releases/assets/11":
			_, err := writer.Write(bodies[11])
			assert.NoError(t, err)
		case "/repos/meigma/release/releases/assets/12":
			_, err := writer.Write(bodies[12])
			assert.NoError(t, err)
		default:
			t.Fatalf("unexpected GitHub request %s", request.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	result, err := newClient(t, server).Fetch(context.Background(), pkgrepo.ReleaseRequest{
		Repository: "meigma/release",
		Tag:        testTag,
	}, root)
	require.NoError(t, err)

	publishedAt, err := time.Parse(time.RFC3339, sourcePublishedAt)
	require.NoError(t, err)
	assert.Equal(t, pkgrepo.Repository("meigma/release"), result.Repository)
	assert.Equal(t, testTag, result.Tag)
	assert.Equal(t, sourceCommit, result.Commit)
	assert.Equal(t, publishedAt, result.PublishedAt)
	assert.Equal(t, []string{"", "2"}, assetPages)
	require.Len(t, result.Assets, 2)
	assert.Equal(t, "release-cli_1.2.3_linux_amd64.rpm", result.Assets[0].Name.String())
	assert.Equal(t, "release-cli_1.2.3_linux_arm64.apk", result.Assets[1].Name.String())
	for id, body := range bodies {
		name := "release-cli_1.2.3_linux_amd64.rpm"
		if id == 12 {
			name = "release-cli_1.2.3_linux_arm64.apk"
		}
		content, readErr := os.ReadFile(directory + "/" + name)
		require.NoError(t, readErr)
		assert.Equal(t, body, content)
	}
}

func TestFetchRejectsDigestMismatchAndRemovesPartialAsset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/meigma/release/releases/tags/v1.2.3":
			writeJSON(t, writer, map[string]any{
				"id":           testReleaseID,
				"tag_name":     testTag,
				"published_at": sourcePublishedAt,
			})
		case "/repos/meigma/release/releases/42/assets":
			writeJSON(t, writer, []map[string]any{
				sourceAssetPayload(11, "release-cli_1.2.3_linux_amd64.rpm", []byte("advertised")),
			})
		case "/repos/meigma/release/git/ref/tags/v1.2.3":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/tags/v1.2.3",
				"object": map[string]any{"type": "commit", "sha": sourceCommit},
			})
		case "/repos/meigma/release/releases/assets/11":
			_, err := writer.Write([]byte("same-size!"))
			assert.NoError(t, err)
		default:
			t.Fatalf("unexpected GitHub request %s", request.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	_, err = newClient(t, server).Fetch(context.Background(), pkgrepo.ReleaseRequest{
		Repository: "meigma/release",
		Tag:        testTag,
	}, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GitHub reports")
	_, statErr := os.Stat(directory + "/release-cli_1.2.3_linux_amd64.rpm")
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// sourceAssetPayload returns one complete release asset transport record.
func sourceAssetPayload(id int64, name string, body []byte) map[string]any {
	digest := sha256.Sum256(body)

	return map[string]any{
		"id":     id,
		"name":   name,
		"state":  "uploaded",
		"size":   len(body),
		"digest": "sha256:" + hex.EncodeToString(digest[:]),
	}
}

// writeJSON writes one GitHub API fixture response.
func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(writer).Encode(value), "encode %#v", value)
}
