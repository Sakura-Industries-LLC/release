package puboci_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	cosignmocks "github.com/meigma/release/internal/adapter/cosign/mocks"
	regmocks "github.com/meigma/release/internal/adapter/reg/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

func TestPrepareHappyPath(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	expectEmptyRegistry(tc.reader, tc.plan)
	gotContent := expectSuccessfulPublish(t, tc)

	got, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.NoError(t, err)
	assert.Equal(t, wantPrepareResult(tc, true), got)
	assert.Equal(t, wantPublishOrder(tc), gotContent.order)
	assert.Equal(t, wantPushedBytes(t, tc), gotContent.byDigest)
}

func TestPrepareSharedLayerPushedOnce(t *testing.T) {
	t.Parallel()

	shared := []byte(testSharedLayer)
	tc := newPrepareTest(t, shared, shared)
	expectEmptyRegistry(tc.reader, tc.plan)
	gotContent := expectSuccessfulPublish(t, tc)

	got, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.NoError(t, err)
	assert.True(t, got.Authoritative)
	assert.Equal(t, wantPublishOrder(tc), gotContent.order)

	layerCalls := 0
	layerKey := "blob:" + tc.layout.Blobs[1].Digest.String()
	for _, step := range gotContent.order {
		if step == layerKey {
			layerCalls++
		}
	}
	assert.Equal(t, 1, layerCalls)
	assert.Equal(t, shared, gotContent.byDigest[tc.layout.Blobs[1].Digest.String()])
}

func TestPrepareIndexDigestMismatch(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	tc.input.IndexDigest = tc.plan.other

	_, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "layout index digest")
	assert.Contains(t, err.Error(), tc.layout.Index.Digest.String())
	assert.Contains(t, err.Error(), tc.plan.other.String())
}

func TestPrepareImmutableTagConflict(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	expectDigest(tc.reader, tc.plan.exact, tc.plan.other)
	expectAbsent(tc.reader, tc.plan.minor)
	expectAbsent(tc.reader, tc.plan.major)
	expectAbsent(tc.reader, tc.plan.latest)

	_, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.ErrorIs(t, err, rel.ErrImmutableTag)
	assert.Contains(t, err.Error(), "plan tags")
}

func TestPrepareDryRun(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	tc.input.DryRun = true
	expectEmptyRegistry(tc.reader, tc.plan)

	got, err := puboci.Prepare(context.Background(), tc.input, tc.reader, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, wantPrepareResult(tc, false), got)
}

func TestPreparePushBlobFailure(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	expectEmptyRegistry(tc.reader, tc.plan)
	first := tc.layout.Blobs[0]
	tc.pusher.EXPECT().
		PushBlob(mock.Anything, tc.input.Image, first, mock.Anything).
		Return(errors.New("blob rejected")).
		Once()

	_, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "push blob")
	assert.Contains(t, err.Error(), first.Digest.String())
}

func TestPrepareIndexVerifyFailure(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	expectEmptyRegistry(tc.reader, tc.plan)
	expectPushes(t, tc, &pushedContent{})
	tc.pusher.EXPECT().
		Verify(mock.Anything, tc.input.Image.Pin(tc.layout.Index.Digest)).
		Return(errors.New("index missing")).
		Once()

	_, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify index")
	assert.Contains(t, err.Error(), tc.layout.Index.Digest.String())
}

func TestPrepareSignFailure(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	expectEmptyRegistry(tc.reader, tc.plan)
	expectPushes(t, tc, &pushedContent{})
	expectVerifies(tc, &pushedContent{})
	tc.signer.EXPECT().
		SignRecursive(mock.Anything, tc.input.Image.Pin(tc.layout.Index.Digest)).
		Return(errors.New("cosign failed")).
		Once()

	got, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign")
	assert.Contains(t, err.Error(), tc.layout.Index.Digest.String())
	assert.False(t, got.Authoritative)
	assert.Equal(t, puboci.OCIPrepareResult{}, got)
}

func TestPrepareRetryableBlobThenSucceeds(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)

	first := tc.layout.Blobs[0]
	var attempts [][]byte
	tc.pusher.EXPECT().
		PushBlob(mock.Anything, tc.input.Image, first, mock.Anything).
		RunAndReturn(func(_ context.Context, _ puboci.Image, _ puboci.Descriptor, content io.Reader) error {
			attempts = append(attempts, readAll(t, content))

			return puboci.ErrRetryable
		}).
		Times(2)
	tc.pusher.EXPECT().
		PushBlob(mock.Anything, tc.input.Image, first, mock.Anything).
		RunAndReturn(func(_ context.Context, _ puboci.Image, _ puboci.Descriptor, content io.Reader) error {
			attempts = append(attempts, readAll(t, content))

			return nil
		}).
		Once()

	gotContent := &pushedContent{byDigest: make(map[string][]byte)}
	for _, blob := range tc.layout.Blobs[1:] {
		expectPushBlob(t, tc, blob, gotContent)
	}
	for _, platform := range tc.layout.Platforms {
		expectPushManifest(t, tc, platform.Descriptor, gotContent)
	}
	expectPushIndex(t, tc, gotContent)
	expectVerifies(tc, gotContent)
	expectSign(tc, gotContent)

	got, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.NoError(t, err)
	assert.True(t, got.Authoritative)
	require.Len(t, attempts, 3)
	wantBlob := readLayoutFile(t, tc.files, first.Digest)
	for _, body := range attempts {
		assert.Equal(t, wantBlob, body)
	}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, waits)
}

func TestPrepareRetryablePushExhausted(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)
	first := tc.layout.Blobs[0]
	tc.pusher.EXPECT().
		PushBlob(mock.Anything, tc.input.Image, first, mock.Anything).
		Return(puboci.ErrRetryable).
		Times(4)

	_, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.ErrorIs(t, err, puboci.ErrRetryable)
	assert.Contains(t, err.Error(), "push blob")
	assert.Contains(t, err.Error(), "after 4 attempts")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, waits)
}

func TestPrepareRetryableVerifyThenSucceeds(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)
	expectPushes(t, tc, &pushedContent{})
	indexRef := tc.input.Image.Pin(tc.layout.Index.Digest)
	tc.pusher.EXPECT().
		Verify(mock.Anything, indexRef).
		Return(puboci.ErrRetryable).
		Once()
	tc.pusher.EXPECT().
		Verify(mock.Anything, indexRef).
		Return(nil).
		Once()
	for _, platform := range tc.layout.Platforms {
		tc.pusher.EXPECT().
			Verify(mock.Anything, tc.input.Image.Pin(platform.Descriptor.Digest)).
			Return(nil).
			Once()
	}
	tc.signer.EXPECT().
		SignRecursive(mock.Anything, indexRef).
		Return(nil).
		Once()

	got, err := puboci.Prepare(context.Background(), tc.input, tc.reader, tc.pusher, tc.signer)
	require.NoError(t, err)
	assert.True(t, got.Authoritative)
	assert.Equal(t, []time.Duration{time.Second}, waits)
}

func TestPrepareCancelDuringBackoff(t *testing.T) {
	t.Parallel()

	tc := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	expectEmptyRegistry(tc.reader, tc.plan)
	first := tc.layout.Blobs[0]
	tc.pusher.EXPECT().
		PushBlob(mock.Anything, tc.input.Image, first, mock.Anything).
		Return(puboci.ErrRetryable).
		Once()

	ctx, cancel := context.WithCancel(context.Background())
	tc.input.Sleep = func(_ context.Context, _ time.Duration) error {
		cancel()

		return context.Canceled
	}

	_, err := puboci.Prepare(ctx, tc.input, tc.reader, tc.pusher, tc.signer)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPrepareRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := newPrepareTest(t, []byte(testAMD64Layer), []byte(testARM64Layer))

	tests := []struct {
		name    string
		ctx     context.Context
		input   puboci.PrepareInput
		state   puboci.StateReader
		wantErr string
	}{
		{
			name:    "nil context",
			input:   valid.input,
			state:   valid.reader,
			wantErr: "context is nil",
		},
		{
			name: "nil layout",
			ctx:  context.Background(),
			input: puboci.PrepareInput{
				Image:       valid.input.Image,
				Version:     valid.input.Version,
				IndexDigest: valid.input.IndexDigest,
			},
			state:   valid.reader,
			wantErr: "layout is nil",
		},
		{
			name: "nil state reader",
			ctx:  context.Background(),
			input: puboci.PrepareInput{
				Image:       valid.input.Image,
				Version:     valid.input.Version,
				IndexDigest: valid.input.IndexDigest,
				Layout:      valid.input.Layout,
			},
			wantErr: "state reader is nil",
		},
		{
			name: "zero digest",
			ctx:  context.Background(),
			input: puboci.PrepareInput{
				Image:   valid.input.Image,
				Version: valid.input.Version,
				Layout:  valid.input.Layout,
			},
			state:   valid.reader,
			wantErr: "digest is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := puboci.Prepare(test.ctx, test.input, test.state, valid.pusher, valid.signer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

// prepareTest is one Prepare invocation and the collaborators it needs.
type prepareTest struct {
	// input is the candidate prepare request.
	input puboci.PrepareInput
	// layout is the validated form of input.Layout.
	layout puboci.Layout
	// files is the extracted layout directory used to build input.Layout.
	files fstest.MapFS
	// plan is the repository and tags CollectState reads.
	plan planFixture
	// reader is the registry state port.
	reader *regmocks.MockStateReader
	// pusher is the registry content port.
	pusher *regmocks.MockContentPusher
	// signer is the recursive signing port.
	signer *cosignmocks.MockSigner
}

// pushedContent records publish order and the bytes streamed for each digest.
type pushedContent struct {
	// order is method:digest entries in the order the write ports ran.
	order []string
	// byDigest is the exact reader contents keyed by digest.
	byDigest map[string][]byte
}

// newPrepareTest builds a two-platform layout and unused write ports.
func newPrepareTest(t *testing.T, amd64Layer, arm64Layer []byte) *prepareTest {
	t.Helper()

	fixture := newTwoPlatformLayout(t, amd64Layer, arm64Layer)
	layout, err := puboci.ReadLayout(fixture.files)
	require.NoError(t, err)

	plan := newPlanFixture(t)
	plan.digest = layout.Index.Digest

	return &prepareTest{
		input: puboci.PrepareInput{
			Image:       plan.image,
			Version:     plan.version,
			IndexDigest: layout.Index.Digest,
			Layout:      fixture.files,
			Sleep:       instantSleep,
		},
		layout: layout,
		files:  fixture.files,
		plan:   plan,
		reader: regmocks.NewMockStateReader(t),
		pusher: regmocks.NewMockContentPusher(t),
		signer: cosignmocks.NewMockSigner(t),
	}
}

// expectEmptyRegistry expects every planned tag to be absent.
func expectEmptyRegistry(reader *regmocks.MockStateReader, plan planFixture) {
	expectAbsent(reader, plan.exact)
	expectAbsent(reader, plan.minor)
	expectAbsent(reader, plan.major)
	expectAbsent(reader, plan.latest)
}

// expectSuccessfulPublish expects the full push, verify, and sign sequence.
func expectSuccessfulPublish(t *testing.T, tc *prepareTest) *pushedContent {
	t.Helper()

	got := &pushedContent{byDigest: make(map[string][]byte)}
	expectPushes(t, tc, got)
	expectVerifies(tc, got)
	expectSign(tc, got)

	return got
}

// expectPushes expects blobs, then platform manifests, then the index.
func expectPushes(t *testing.T, tc *prepareTest, got *pushedContent) {
	t.Helper()

	if got.byDigest == nil {
		got.byDigest = make(map[string][]byte)
	}
	for _, blob := range tc.layout.Blobs {
		expectPushBlob(t, tc, blob, got)
	}
	for _, platform := range tc.layout.Platforms {
		expectPushManifest(t, tc, platform.Descriptor, got)
	}
	expectPushIndex(t, tc, got)
}

// expectPushBlob expects one streamed blob and records its bytes.
func expectPushBlob(t *testing.T, tc *prepareTest, desc puboci.Descriptor, got *pushedContent) {
	t.Helper()

	tc.pusher.EXPECT().
		PushBlob(mock.Anything, tc.input.Image, desc, mock.Anything).
		RunAndReturn(func(_ context.Context, _ puboci.Image, gotDesc puboci.Descriptor, content io.Reader) error {
			recordPush(t, got, "blob", gotDesc, content)

			return nil
		}).
		Once()
}

// expectPushManifest expects one streamed platform manifest and records its bytes.
func expectPushManifest(t *testing.T, tc *prepareTest, desc puboci.Descriptor, got *pushedContent) {
	t.Helper()

	tc.pusher.EXPECT().
		PushManifest(mock.Anything, tc.input.Image, desc, mock.Anything).
		RunAndReturn(func(_ context.Context, _ puboci.Image, gotDesc puboci.Descriptor, content io.Reader) error {
			recordPush(t, got, "manifest", gotDesc, content)

			return nil
		}).
		Once()
}

// expectPushIndex expects the retained index bytes to be pushed last.
func expectPushIndex(t *testing.T, tc *prepareTest, got *pushedContent) {
	t.Helper()

	tc.pusher.EXPECT().
		PushManifest(mock.Anything, tc.input.Image, tc.layout.Index, mock.Anything).
		RunAndReturn(func(_ context.Context, _ puboci.Image, gotDesc puboci.Descriptor, content io.Reader) error {
			recordPush(t, got, "index", gotDesc, content)

			return nil
		}).
		Once()
}

// expectVerifies expects the index, then each platform manifest, to resolve.
func expectVerifies(tc *prepareTest, got *pushedContent) {
	tc.pusher.EXPECT().
		Verify(mock.Anything, tc.input.Image.Pin(tc.layout.Index.Digest)).
		Run(func(_ context.Context, ref puboci.DigestRef) {
			got.order = append(got.order, "verify:"+ref.Digest.String())
		}).
		Return(nil).
		Once()
	for _, platform := range tc.layout.Platforms {
		digest := platform.Descriptor.Digest
		tc.pusher.EXPECT().
			Verify(mock.Anything, tc.input.Image.Pin(digest)).
			Run(func(_ context.Context, ref puboci.DigestRef) {
				got.order = append(got.order, "verify:"+ref.Digest.String())
			}).
			Return(nil).
			Once()
	}
}

// expectSign expects SignRecursive on the published index digest.
func expectSign(tc *prepareTest, got *pushedContent) {
	tc.signer.EXPECT().
		SignRecursive(mock.Anything, tc.input.Image.Pin(tc.layout.Index.Digest)).
		Run(func(_ context.Context, ref puboci.DigestRef) {
			got.order = append(got.order, "sign:"+ref.Digest.String())
		}).
		Return(nil).
		Once()
}

// recordPush appends a labelled digest to the observed order and stores the bytes.
func recordPush(t *testing.T, got *pushedContent, kind string, desc puboci.Descriptor, content io.Reader) {
	t.Helper()

	body, err := io.ReadAll(content)
	require.NoError(t, err)
	got.order = append(got.order, kind+":"+desc.Digest.String())
	got.byDigest[desc.Digest.String()] = body
}

// wantPublishOrder is blobs, platform manifests, index, verifies, then sign.
func wantPublishOrder(tc *prepareTest) []string {
	order := make([]string, 0, len(tc.layout.Blobs)+2*len(tc.layout.Platforms)+3)
	for _, blob := range tc.layout.Blobs {
		order = append(order, "blob:"+blob.Digest.String())
	}
	for _, platform := range tc.layout.Platforms {
		order = append(order, "manifest:"+platform.Descriptor.Digest.String())
	}
	order = append(order, "index:"+tc.layout.Index.Digest.String())
	order = append(order, "verify:"+tc.layout.Index.Digest.String())
	for _, platform := range tc.layout.Platforms {
		order = append(order, "verify:"+platform.Descriptor.Digest.String())
	}
	order = append(order, "sign:"+tc.layout.Index.Digest.String())

	return order
}

// wantPushedBytes is the layout file contents keyed by digest.
func wantPushedBytes(t *testing.T, tc *prepareTest) map[string][]byte {
	t.Helper()

	want := make(map[string][]byte, len(tc.layout.Blobs)+len(tc.layout.Platforms)+1)
	for _, blob := range tc.layout.Blobs {
		want[blob.Digest.String()] = readLayoutFile(t, tc.files, blob.Digest)
	}
	for _, platform := range tc.layout.Platforms {
		want[platform.Descriptor.Digest.String()] = readLayoutFile(t, tc.files, platform.Descriptor.Digest)
	}
	want[tc.layout.Index.Digest.String()] = bytes.Clone(tc.layout.IndexBytes)

	return want
}

// readLayoutFile reads digest's blob bytes from the fixture filesystem.
func readLayoutFile(t *testing.T, files fstest.MapFS, digest rel.Digest) []byte {
	t.Helper()

	name, err := puboci.BlobPath(digest)
	require.NoError(t, err)
	file, err := files.Open(name)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, file.Close())
	}()
	body, err := io.ReadAll(file)
	require.NoError(t, err)

	return body
}

// wantPrepareResult is the document Prepare should emit for tc.
func wantPrepareResult(tc *prepareTest, authoritative bool) puboci.OCIPrepareResult {
	return puboci.NewPrepareResult(
		tc.input.Image,
		tc.input.Version,
		tc.layout.Index.Digest,
		tc.layout.Platforms,
		emptyState(tc.input.Version),
		authoritative,
	)
}

// instantSleep is a SleepFunc that never waits.
func instantSleep(_ context.Context, _ time.Duration) error {
	return nil
}

// recordSleep appends each backoff duration to waits.
func recordSleep(waits *[]time.Duration) puboci.SleepFunc {
	return func(_ context.Context, d time.Duration) error {
		*waits = append(*waits, d)

		return nil
	}
}

// readAll consumes content and returns the bytes.
func readAll(t *testing.T, content io.Reader) []byte {
	t.Helper()

	body, err := io.ReadAll(content)
	require.NoError(t, err)

	return body
}
