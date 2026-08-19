package reg

import (
	"bytes"
	"context"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

func TestClientImplementsTagCommitter(t *testing.T) {
	t.Parallel()

	var committer puboci.TagCommitter = New(Options{})
	require.NotNil(t, committer)
}

func TestCommitAppliesTagsInOrder(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		puts []string
	)
	backend := registry.New()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			mu.Lock()
			puts = append(puts, request.URL.Path)
			mu.Unlock()
		}
		backend.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)

	client := newPlainClient(server)
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")
	mu.Lock()
	start := len(puts)
	mu.Unlock()

	require.NoError(t, client.Commit(context.Background(), image, digest, tags))

	wantPaths := []string{
		"/v2/" + testRepo + "/manifests/1.4.0",
		"/v2/" + testRepo + "/manifests/1.4",
		"/v2/" + testRepo + "/manifests/1",
		"/v2/" + testRepo + "/manifests/latest",
	}
	mu.Lock()
	gotPuts := append([]string(nil), puts[start:]...)
	mu.Unlock()
	assert.Equal(t, wantPaths, filterTagPuts(gotPuts, tags))
	requireTagsResolveTo(t, client, image, digest, tags)
}

func TestCommitEmptyTagsIsNoop(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)

	require.NoError(t, client.Commit(context.Background(), image, digest, nil))
	require.NoError(t, client.Commit(context.Background(), image, digest, []rel.Tag{}))

	_, err := client.Resolve(context.Background(), mustRef(t, server))
	require.Error(t, err)
	require.ErrorIs(t, err, puboci.ErrTagAbsent)
}

func TestCommitMissingDigestWrapsErrTagAbsent(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	missing, err := rel.ParseDigest("sha256:" + hex.EncodeToString(bytes.Repeat([]byte{0xcd}, digestHexBytes)))
	require.NoError(t, err)
	tags := mustTags(t, "1.4.0")

	commitErr := client.Commit(context.Background(), image, missing, tags)
	require.Error(t, commitErr)
	require.ErrorIs(t, commitErr, puboci.ErrTagAbsent)
	assert.Contains(t, commitErr.Error(), missing.String())
	assert.NotContains(t, commitErr.Error(), testToken)
	assert.NotContains(t, commitErr.Error(), server.URL)

	_, resolveErr := client.Resolve(context.Background(), image.Reference(tags[0]))
	require.Error(t, resolveErr)
	require.ErrorIs(t, resolveErr, puboci.ErrTagAbsent)
}

func TestCommitRetryableTagWriteNamesTag(t *testing.T) {
	t.Parallel()

	backend := registry.New()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/manifests/1.4") {
			writer.WriteHeader(http.StatusServiceUnavailable)

			return
		}
		backend.ServeHTTP(writer, request)
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
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")

	err := client.Commit(context.Background(), image, digest, tags)
	require.Error(t, err)
	require.ErrorIs(t, err, puboci.ErrRetryable)
	assert.Contains(t, err.Error(), "1.4")
	assert.Contains(t, err.Error(), "applied 1 of 4")
	assert.NotContains(t, err.Error(), testToken)
	assert.NotContains(t, err.Error(), "Authorization")
	assert.NotContains(t, err.Error(), server.URL)
}

func TestCommitMidSequenceFailureLeavesEarlierTags(t *testing.T) {
	t.Parallel()

	backend := registry.New()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/manifests/1") {
			writer.WriteHeader(http.StatusServiceUnavailable)

			return
		}
		backend.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)

	client := newPlainClient(server)
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")

	err := client.Commit(context.Background(), image, digest, tags)
	require.Error(t, err)
	require.ErrorIs(t, err, puboci.ErrRetryable)
	assert.Contains(t, err.Error(), "commit tag 1:")
	assert.Contains(t, err.Error(), "applied 2 of 4")

	requireTagsResolveTo(t, client, image, digest, tags[:2])
	_, resolveErr := client.Resolve(context.Background(), image.Reference(tags[2]))
	require.Error(t, resolveErr)
	require.ErrorIs(t, resolveErr, puboci.ErrTagAbsent)
	_, latestErr := client.Resolve(context.Background(), image.Reference(tags[3]))
	require.Error(t, latestErr)
	require.ErrorIs(t, latestErr, puboci.ErrTagAbsent)
}

func TestCommitCanceledContextStopsBeforeNextTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	server := newRegistryServer(t)
	client := New(Options{
		PlainHTTP: true,
		HTTPClient: &http.Client{
			Transport: cancelOnTagPUT{
				base:   server.Client().Transport,
				tag:    "1.4",
				cancel: cancel,
			},
		},
	})
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")

	err := client.Commit(ctx, image, digest, tags)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "commit tag 1.4:")
	assert.Contains(t, err.Error(), "applied 1 of 4")
	assert.NotContains(t, err.Error(), server.URL)

	requireTagsResolveTo(t, client, image, digest, tags[:1])
	_, resolveErr := client.Resolve(context.Background(), image.Reference(tags[1]))
	require.Error(t, resolveErr)
	require.ErrorIs(t, resolveErr, puboci.ErrTagAbsent)
}

func TestCommitConvergesOnRetry(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")

	require.NoError(t, client.Commit(context.Background(), image, digest, tags))
	require.NoError(t, client.Commit(context.Background(), image, digest, tags))
	requireTagsResolveTo(t, client, image, digest, tags)
}

func TestCommitRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	client := New(Options{})
	image, err := puboci.ParseImage("ghcr.io/" + testRepo)
	require.NoError(t, err)
	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(bytes.Repeat([]byte{0xab}, digestHexBytes)))
	require.NoError(t, err)
	tags := mustTags(t, "1.4.0")

	var missing context.Context
	require.EqualError(t, client.Commit(missing, image, digest, tags), "context is nil")
	require.EqualError(
		t,
		(*Client)(nil).Commit(context.Background(), image, digest, tags),
		"registry client is nil",
	)
	require.ErrorContains(
		t,
		client.Commit(context.Background(), image, "", tags),
		"commit digest:",
	)
	require.EqualError(
		t,
		client.Commit(context.Background(), image, digest, []rel.Tag{""}),
		"commit tag is empty",
	)
}

func TestCommitCanceledBeforeStartWritesNothing(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(bytes.Repeat([]byte{0xab}, digestHexBytes)))
	require.NoError(t, err)
	tags := mustTags(t, "1.4.0")

	commitErr := newPlainClient(server).Commit(ctx, mustImage(t, server), digest, tags)
	require.Error(t, commitErr)
	require.ErrorIs(t, commitErr, context.Canceled)
	assert.Zero(t, hits.Load())
	assert.NotContains(t, commitErr.Error(), server.URL)
	assert.NotContains(t, commitErr.Error(), testToken)
}

func TestCommitCountsWrittenTagWhenVerifyFails(t *testing.T) {
	t.Parallel()

	backend := registry.New()
	var failVerify atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/manifests/1") {
			backend.ServeHTTP(writer, request)
			failVerify.Store(true)

			return
		}
		if request.Method == http.MethodHead && strings.HasSuffix(request.URL.Path, "/manifests/1") &&
			failVerify.CompareAndSwap(true, false) {
			writer.WriteHeader(http.StatusServiceUnavailable)

			return
		}
		backend.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)

	client := newPlainClient(server)
	image := mustImage(t, server)
	digest := pushFixture(t, client, image)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")

	err := client.Commit(context.Background(), image, digest, tags)
	require.Error(t, err)
	require.ErrorIs(t, err, puboci.ErrRetryable)
	assert.Contains(t, err.Error(), "commit tag 1:")
	assert.Contains(t, err.Error(), "applied 3 of 4")

	requireTagsResolveTo(t, client, image, digest, tags[:3])
	_, latestErr := client.Resolve(context.Background(), image.Reference(tags[3]))
	require.Error(t, latestErr)
	require.ErrorIs(t, latestErr, puboci.ErrTagAbsent)
}

func TestPrepareThenFinalizeAppliesTagsAfterAttestationPoint(t *testing.T) {
	t.Parallel()

	server := newRegistryServer(t)
	client := newPlainClient(server)
	image := mustImage(t, server)
	fixture := newImageFixture(t)
	layout := layoutFromFixture(t, fixture)
	version, err := rel.ParseVersion("1.4.0")
	require.NoError(t, err)
	tags := mustTags(t, "1.4.0", "1.4", "1", "latest")
	signer := &recordingSigner{}
	input := puboci.PrepareInput{
		Image:       image,
		Version:     version,
		IndexDigest: fixture.index.Digest,
		Layout:      layout,
		Sleep:       instantSleep,
	}

	prepared, err := puboci.Prepare(context.Background(), input, client, client, signer)
	require.NoError(t, err)
	require.True(t, prepared.Authoritative)
	require.NoError(t, client.Verify(context.Background(), image.Pin(fixture.index.Digest)))
	require.Equal(t, []puboci.DigestRef{image.Pin(fixture.index.Digest)}, signer.refs)
	requireTagsAbsent(t, client, image, tags)

	finalized, err := puboci.Finalize(
		context.Background(),
		puboci.FinalizeInput{Prepared: prepared, Sleep: instantSleep},
		client,
		client,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"1.4.0", "1.4", "1", "latest"}, finalized.Applied)
	assert.Equal(t, []string{}, finalized.Accepted)
	assert.Equal(t, []string{}, finalized.Retained)
	requireTagsResolveTo(t, client, image, fixture.index.Digest, tags)

	preparedAgain, err := puboci.Prepare(context.Background(), input, client, client, signer)
	require.NoError(t, err)
	require.True(t, preparedAgain.Authoritative)

	finalizedAgain, err := puboci.Finalize(
		context.Background(),
		puboci.FinalizeInput{Prepared: preparedAgain, Sleep: instantSleep},
		client,
		client,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{}, finalizedAgain.Applied)
	assert.Equal(t, []string{"1.4.0", "1.4", "1", "latest"}, finalizedAgain.Accepted)
	assert.Equal(t, []string{}, finalizedAgain.Retained)
	requireTagsResolveTo(t, client, image, fixture.index.Digest, tags)
}

// pushFixture uploads a one-platform index to image and returns its digest.
func pushFixture(t *testing.T, client *Client, image puboci.Image) rel.Digest {
	t.Helper()

	fixture := newImageFixture(t)
	ctx := context.Background()
	require.NoError(t, client.PushBlob(ctx, image, fixture.config, bytes.NewReader(fixture.configBytes)))
	require.NoError(t, client.PushBlob(ctx, image, fixture.layer, bytes.NewReader(fixture.layerBytes)))
	require.NoError(t, client.PushManifest(ctx, image, fixture.manifest, bytes.NewReader(fixture.manifestBytes)))
	require.NoError(t, client.PushManifest(ctx, image, fixture.index, bytes.NewReader(fixture.indexBytes)))

	return fixture.index.Digest
}

// mustTags parses names as OCI tags.
func mustTags(t *testing.T, names ...string) []rel.Tag {
	t.Helper()

	tags := make([]rel.Tag, len(names))
	for i, name := range names {
		tag, err := rel.ParseTag(name)
		require.NoError(t, err)
		tags[i] = tag
	}

	return tags
}

// requireTagsResolveTo asserts every tag resolves to digest.
func requireTagsResolveTo(t *testing.T, client *Client, image puboci.Image, digest rel.Digest, tags []rel.Tag) {
	t.Helper()

	for _, tag := range tags {
		got, err := client.Resolve(context.Background(), image.Reference(tag))
		require.NoError(t, err, "tag %s", tag)
		assert.Equal(t, digest, got, "tag %s", tag)
	}
}

// filterTagPuts returns PUT paths that apply tags, in request order.
func filterTagPuts(paths []string, tags []rel.Tag) []string {
	wanted := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		wanted["/v2/"+testRepo+"/manifests/"+tag.String()] = struct{}{}
	}

	filtered := make([]string, 0, len(tags))
	for _, path := range paths {
		if _, ok := wanted[path]; ok {
			filtered = append(filtered, path)
		}
	}

	return filtered
}

// cancelOnTagPUT cancels ctx when a PUT for tag is issued.
type cancelOnTagPUT struct {
	// base is the next transport.
	base http.RoundTripper
	// tag is the tag whose PUT cancels the context.
	tag string
	// cancel stops the commit context.
	cancel context.CancelFunc
}

// RoundTrip cancels after observing the matching tag PUT, then returns the canceled context error.
func (t cancelOnTagPUT) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/manifests/"+t.tag) {
		t.cancel()

		return nil, context.Canceled
	}

	return t.base.RoundTrip(request)
}

// requireTagsAbsent asserts none of the tags resolve.
func requireTagsAbsent(t *testing.T, client *Client, image puboci.Image, tags []rel.Tag) {
	t.Helper()

	for _, tag := range tags {
		_, err := client.Resolve(context.Background(), image.Reference(tag))
		require.Error(t, err, "tag %s", tag)
		require.ErrorIs(t, err, puboci.ErrTagAbsent, "tag %s", tag)
	}
}

// layoutFromFixture builds an extracted oci-image/layout directory from fixture.
func layoutFromFixture(t *testing.T, fixture imageFixture) fs.FS {
	t.Helper()

	configPath, err := puboci.BlobPath(fixture.config.Digest)
	require.NoError(t, err)
	layerPath, err := puboci.BlobPath(fixture.layer.Digest)
	require.NoError(t, err)
	manifestPath, err := puboci.BlobPath(fixture.manifest.Digest)
	require.NoError(t, err)

	return fstest.MapFS{
		"oci-layout": {Data: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		"index.json": {Data: fixture.indexBytes},
		configPath:   {Data: fixture.configBytes},
		layerPath:    {Data: fixture.layerBytes},
		manifestPath: {Data: fixture.manifestBytes},
	}
}

// recordingSigner is a local [puboci.Signer] stub that records signed refs.
//
// It is a test double for a port this package does not own.
type recordingSigner struct {
	// refs are the digest refs passed to SignRecursive, in call order.
	refs []puboci.DigestRef
}

// SignRecursive records ref and succeeds.
func (s *recordingSigner) SignRecursive(_ context.Context, ref puboci.DigestRef) error {
	s.refs = append(s.refs, ref)

	return nil
}

// instantSleep is a [puboci.SleepFunc] that never waits.
func instantSleep(_ context.Context, _ time.Duration) error {
	return nil
}
