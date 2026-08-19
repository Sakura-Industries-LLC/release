package reg

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// testConfigJSON is a distinct config blob for push fixtures.
	testConfigJSON = `{"architecture":"amd64","os":"linux"}`
	// testLayerPayload is a distinct layer blob for push fixtures.
	testLayerPayload = "layer-bytes"
	// digestHexBytes is the decoded length of a SHA-256 hex digest.
	digestHexBytes = 32
)

func TestClientImplementsPorts(t *testing.T) {
	t.Parallel()

	client := New(Options{})
	var reader puboci.StateReader = client
	var pusher puboci.ContentPusher = client
	require.NotNil(t, reader)
	require.NotNil(t, pusher)
}

// TestPushBlobLeavesReaderOpen pins the ownership rule that produced a real
// bug: oras hands the content reader to net/http, which always closes a
// request body, so a caller-owned [os.File] was closed twice.
func TestPushBlobLeavesReaderOpen(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	fixture := newImageFixture(t)

	dir := t.TempDir()
	name := filepath.Join(dir, "blob")
	require.NoError(t, os.WriteFile(name, fixture.layerBytes, 0o600))
	file, err := os.Open(name)
	require.NoError(t, err)

	require.NoError(t, client.PushBlob(context.Background(), image, fixture.layer, file))
	require.NoError(t, file.Close(), "the adapter must not close a caller-owned reader")
	assert.Equal(t, fixture.layerBytes, getBlob(t, server, fixture.layer.Digest))
}

func TestPushImageRoundTrip(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	fixture := newImageFixture(t)

	require.NoError(t, client.PushBlob(
		context.Background(),
		image,
		fixture.config,
		bytes.NewReader(fixture.configBytes),
	))
	require.NoError(t, client.PushBlob(
		context.Background(),
		image,
		fixture.layer,
		bytes.NewReader(fixture.layerBytes),
	))
	require.NoError(t, client.PushManifest(
		context.Background(),
		image,
		fixture.manifest,
		bytes.NewReader(fixture.manifestBytes),
	))
	require.NoError(t, client.PushManifest(
		context.Background(),
		image,
		fixture.index,
		bytes.NewReader(fixture.indexBytes),
	))

	require.NoError(t, client.Verify(context.Background(), image.Pin(fixture.index.Digest)))
	require.NoError(t, client.Verify(context.Background(), image.Pin(fixture.manifest.Digest)))
	indexBody, indexType := getManifest(t, server, fixture.index.Digest)
	manifestBody, manifestType := getManifest(t, server, fixture.manifest.Digest)
	assert.Equal(t, fixture.indexBytes, indexBody)
	assert.Equal(t, ocispec.MediaTypeImageIndex, indexType)
	assert.Equal(t, fixture.manifestBytes, manifestBody)
	assert.Equal(t, ocispec.MediaTypeImageManifest, manifestType)
	assert.Equal(t, fixture.layerBytes, getBlob(t, server, fixture.layer.Digest))
}

func TestPushBlobConvergesOnRetry(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	body := []byte(testLayerPayload)
	desc := descriptorFor(t, ocispec.MediaTypeImageLayer, body)

	require.NoError(t, client.PushBlob(context.Background(), image, desc, bytes.NewReader(body)))
	require.NoError(t, client.PushBlob(context.Background(), image, desc, bytes.NewReader(body)))
	assert.Equal(t, body, getBlob(t, server, desc.Digest))
}

func TestPushBlobConflictIsError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusConflict)
	}))
	t.Cleanup(server.Close)

	body := []byte(testLayerPayload)
	desc := descriptorFor(t, ocispec.MediaTypeImageLayer, body)
	err := newPlainClient(server).PushBlob(
		context.Background(),
		mustImage(t, server),
		desc,
		bytes.NewReader(body),
	)
	require.Error(t, err)
	require.NotErrorIs(t, err, errdef.ErrAlreadyExists)
	assert.Contains(t, err.Error(), "push blob")
	assert.Contains(t, err.Error(), desc.Digest.String())
}

func TestAlreadyPresentIsSuccess(t *testing.T) {
	t.Parallel()

	assert.True(t, isAlreadyPresent(errdef.ErrAlreadyExists))
	assert.True(t, isAlreadyPresent(errors.Join(errdef.ErrAlreadyExists)))
	assert.False(t, isAlreadyPresent(nil))
	assert.False(t, isAlreadyPresent(&errcode.ErrorResponse{StatusCode: http.StatusConflict}))
	assert.False(t, isAlreadyPresent(&errcode.ErrorResponse{StatusCode: http.StatusBadRequest}))
}

func TestPushBlobRejectsDigestMismatch(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	body := []byte(testLayerPayload)
	desc := descriptorFor(t, ocispec.MediaTypeImageLayer, []byte(testConfigJSON))
	desc.Size = int64(len(body))

	err := client.PushBlob(context.Background(), image, desc, bytes.NewReader(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "push blob")
	assert.Contains(t, err.Error(), desc.Digest.String())
	assert.NotContains(t, err.Error(), testToken)
}

func TestVerifyAbsentDigestWrapsErrTagAbsent(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	missing, err := rel.ParseDigest("sha256:" + hex.EncodeToString(bytes.Repeat([]byte{0xab}, digestHexBytes)))
	require.NoError(t, err)

	verifyErr := newPlainClient(server).Verify(context.Background(), mustImage(t, server).Pin(missing))
	require.Error(t, verifyErr)
	require.ErrorIs(t, verifyErr, puboci.ErrTagAbsent)
	assert.NotContains(t, verifyErr.Error(), testToken)
}

func TestPushStatusClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		wantSent error
		wantText string
	}{
		{
			name:     "service unavailable",
			status:   http.StatusServiceUnavailable,
			wantSent: puboci.ErrRetryable,
			wantText: "retryable",
		},
		{
			name:     "unauthorized",
			status:   http.StatusUnauthorized,
			wantText: "registry authentication failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
			}))
			t.Cleanup(server.Close)

			client := New(Options{
				Credentials: Credentials{
					Username: "octocat",
					Password: rel.NewSecret(testToken),
				},
				PlainHTTP:  true,
				HTTPClient: server.Client(),
			})
			body := []byte(testLayerPayload)
			err := client.PushBlob(
				context.Background(),
				mustImage(t, server),
				descriptorFor(t, ocispec.MediaTypeImageLayer, body),
				bytes.NewReader(body),
			)
			require.Error(t, err)
			if test.wantSent != nil {
				require.ErrorIs(t, err, test.wantSent)
			} else {
				require.NotErrorIs(t, err, puboci.ErrRetryable)
				require.NotErrorIs(t, err, puboci.ErrTagAbsent)
			}
			assert.Contains(t, err.Error(), test.wantText)
			assert.NotContains(t, err.Error(), testToken)
			assert.NotContains(t, err.Error(), "Authorization")
			assert.NotContains(t, err.Error(), server.URL)
		})
	}
}

func TestCanceledPushDoesNotComplete(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	body := []byte(testLayerPayload)
	err := newPlainClient(server).PushBlob(
		ctx,
		mustImage(t, server),
		descriptorFor(t, ocispec.MediaTypeImageLayer, body),
		bytes.NewReader(body),
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, hits.Load())
	assert.NotContains(t, err.Error(), server.URL)
	assert.NotContains(t, err.Error(), testToken)
}

func TestPushAndVerifyRejectNil(t *testing.T) {
	t.Parallel()

	client := New(Options{})
	image, err := puboci.ParseImage("ghcr.io/" + testRepo)
	require.NoError(t, err)
	body := []byte(testLayerPayload)
	desc := descriptorFor(t, ocispec.MediaTypeImageLayer, body)
	ref := image.Pin(desc.Digest)

	var missing context.Context
	require.EqualError(t, client.PushBlob(missing, image, desc, bytes.NewReader(body)), "context is nil")
	require.EqualError(t, client.PushManifest(missing, image, desc, bytes.NewReader(body)), "context is nil")
	require.EqualError(t, client.Verify(missing, ref), "context is nil")
	require.EqualError(
		t,
		(*Client)(nil).PushBlob(context.Background(), image, desc, bytes.NewReader(body)),
		"registry client is nil",
	)
	require.EqualError(
		t,
		(*Client)(nil).PushManifest(context.Background(), image, desc, bytes.NewReader(body)),
		"registry client is nil",
	)
	require.EqualError(t, (*Client)(nil).Verify(context.Background(), ref), "registry client is nil")
	require.EqualError(
		t,
		client.PushBlob(context.Background(), image, desc, nil),
		"push blob "+desc.Digest.String()+": content is nil",
	)
	require.EqualError(
		t,
		client.PushManifest(context.Background(), image, desc, nil),
		"push manifest "+desc.Digest.String()+": content is nil",
	)
}

// imageFixture is a one-platform image used by digest-push tests.
type imageFixture struct {
	// configBytes is the config blob.
	configBytes []byte
	// layerBytes is the layer blob.
	layerBytes []byte
	// manifestBytes is the platform manifest document.
	manifestBytes []byte
	// indexBytes is the image index document.
	indexBytes []byte
	// config is the config descriptor.
	config puboci.Descriptor
	// layer is the layer descriptor.
	layer puboci.Descriptor
	// manifest is the platform manifest descriptor.
	manifest puboci.Descriptor
	// index is the image index descriptor.
	index puboci.Descriptor
}

// newImageFixture builds a config, layer, platform manifest, and index.
func newImageFixture(t *testing.T) imageFixture {
	t.Helper()

	configBytes := []byte(testConfigJSON)
	layerBytes := []byte(testLayerPayload)
	config := descriptorFor(t, ocispec.MediaTypeImageConfig, configBytes)
	layer := descriptorFor(t, ocispec.MediaTypeImageLayer, layerBytes)
	manifestBytes := encodeJSON(t, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: ociSchemaVersion},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ociDescriptor(config),
		Layers:    []ocispec.Descriptor{ociDescriptor(layer)},
	})
	manifest := descriptorFor(t, ocispec.MediaTypeImageManifest, manifestBytes)
	manifestDesc := ociDescriptor(manifest)
	manifestDesc.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}
	indexBytes := encodeJSON(t, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: ociSchemaVersion},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{manifestDesc},
	})

	return imageFixture{
		configBytes:   configBytes,
		layerBytes:    layerBytes,
		manifestBytes: manifestBytes,
		indexBytes:    indexBytes,
		config:        config,
		layer:         layer,
		manifest:      manifest,
		index:         descriptorFor(t, ocispec.MediaTypeImageIndex, indexBytes),
	}
}

// descriptorFor returns the descriptor of data at mediaType.
func descriptorFor(t *testing.T, mediaType string, data []byte) puboci.Descriptor {
	t.Helper()

	digest, err := rel.ParseDigest(digestOf(data))
	require.NoError(t, err)

	return puboci.Descriptor{
		MediaType: mediaType,
		Digest:    digest,
		Size:      int64(len(data)),
	}
}

// encodeJSON marshals value as JSON.
func encodeJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	require.NoError(t, err)

	return body
}

// getManifest fetches a digest-addressed manifest and its stored media type.
func getManifest(t *testing.T, server *httptest.Server, digest rel.Digest) ([]byte, string) {
	t.Helper()

	return getPath(t, server, "/v2/"+testRepo+"/manifests/"+digest.String())
}

// getBlob fetches a digest-addressed blob from the fake registry.
func getBlob(t *testing.T, server *httptest.Server, digest rel.Digest) []byte {
	t.Helper()

	body, _ := getPath(t, server, "/v2/"+testRepo+"/blobs/"+digest.String())

	return body
}

// getPath GETs path from server and returns the response body and Content-Type.
func getPath(t *testing.T, server *httptest.Server, path string) ([]byte, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
	require.NoError(t, err)
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	return body, resp.Header.Get("Content-Type")
}
