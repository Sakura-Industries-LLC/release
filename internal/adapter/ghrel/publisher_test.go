package ghrel_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/adapter/ghrel"
)

func TestPublishPatchesDraftFalseOnly(t *testing.T) {
	t.Parallel()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		assert.Equal(t, http.MethodPatch, request.Method)
		assert.Equal(t, "/repos/meigma/release/releases/42", request.URL.Path)
		assert.Equal(t, "Bearer "+testToken, request.Header.Get("Authorization"))
		body, err := io.ReadAll(request.Body)
		if !assert.NoError(t, err) {
			return
		}
		var payload map[string]any
		if !assert.NoError(t, json.Unmarshal(body, &payload)) {
			return
		}
		assert.Equal(t, map[string]any{"draft": false}, payload)
		assert.NoError(t, json.NewEncoder(writer).Encode(releasePayload(testReleaseID, testTag, false)))
	}))
	t.Cleanup(server.Close)

	err := newClient(t, server).Publish(
		context.Background(),
		mustRepo(t),
		mustReleaseID(t),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, hits)
}

func TestPublishClassifiesAPIErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	t.Cleanup(server.Close)

	err := newClient(t, server).Publish(
		context.Background(),
		mustRepo(t),
		mustReleaseID(t),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github authentication failed")
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), "Bearer")
}

func TestPublishRejectsNilClient(t *testing.T) {
	t.Parallel()

	client := ghrel.New(nil)
	err := client.Publish(context.Background(), mustRepo(t), mustReleaseID(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github client is nil")
}

func TestPublishCanceledErrorOmitsURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newClient(t, server).Publish(ctx, mustRepo(t), mustReleaseID(t))
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "request canceled")
	assert.NotContains(t, err.Error(), server.URL)
	assert.NotContains(t, err.Error(), testToken)
}
