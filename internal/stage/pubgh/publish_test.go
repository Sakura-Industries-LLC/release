package pubgh_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	ghrelmocks "github.com/meigma/release/internal/adapter/ghrel/mocks"
	ghupmocks "github.com/meigma/release/internal/adapter/ghup/mocks"
	gitxmocks "github.com/meigma/release/internal/adapter/gitx/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	validCommit    = "0123456789abcdef0123456789abcdef01234567"
	otherCommit    = "fedcba9876543210fedcba9876543210fedcba98"
	payloadDigest  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	checksumDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	bundleDigest   = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	wrongDigest    = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	releaseURL     = "https://github.com/meigma/release/releases/tag/v1.2.3"
)

func TestPublishHappyPathOrder(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	draft := tc.draft()
	view := expectedAssets()
	published := draft
	published.Draft = false

	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(draft, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(pubgh.AssetsView{}, nil).
			Once(),
		tc.replacer.EXPECT().
			Replace(mock.Anything, tc.input.Repository, tc.input.Tag, tc.input.Assets).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(view, nil).
			Once(),
		tc.publisher.EXPECT().
			Publish(mock.Anything, tc.input.Repository, draft.ID).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			Get(mock.Anything, tc.input.Repository, draft.ID).
			Return(published, nil).
			Once(),
	)

	got, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.NoError(t, err)
	assert.Equal(t, pubgh.PublishResult{
		ReleaseID: draft.ID.Int64(),
		Tag:       "v1.2.3",
		URL:       releaseURL,
		Draft:     false,
		Assets:    []string{"checksums.txt", "checksums.txt.sigstore.json", "gamma.bin"},
	}, got)
	assert.Empty(t, tc.waits)
}

func TestPublishTagMismatchFailsBeforeRelease(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	tc.resolver.EXPECT().
		Resolve(mock.Anything, tc.input.Tag).
		Return(mustCommit(t, otherCommit), nil).
		Once()

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.Error(t, err)
	assert.Contains(t, err.Error(), otherCommit)
	tc.reader.AssertNotCalled(t, "FindDraft", mock.Anything, mock.Anything, mock.Anything)
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishNoDraftRecordsDraftBudget(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	tc.resolver.EXPECT().
		Resolve(mock.Anything, tc.input.Tag).
		Return(tc.input.Commit, nil).
		Once()
	tc.reader.EXPECT().
		FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
		Return(pubgh.Release{}, pubgh.ErrNoDraft).
		Times(24)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrNoDraft)
	assert.Equal(t, repeatWait(24, 5*time.Second), tc.waits)
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishAmbiguousReleaseDoesNotUpload(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	tc.resolver.EXPECT().
		Resolve(mock.Anything, tc.input.Tag).
		Return(tc.input.Commit, nil).
		Once()
	tc.reader.EXPECT().
		FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
		Return(pubgh.Release{}, pubgh.ErrAmbiguousRelease).
		Once()

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrAmbiguousRelease)
	assert.Empty(t, tc.waits)
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishUnexpectedAssetRefusesBeforeReplace(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	draft := tc.draft()
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(draft, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(pubgh.AssetsView{Assets: []pubgh.Asset{{Name: "surprise.bin"}}}, nil).
			Once(),
	)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrUnexpectedAsset)
	assert.Contains(t, err.Error(), "surprise.bin")
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishConvergenceFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		view      pubgh.AssetsView
		wantErr   string
		pollTimes int
	}{
		{
			name:      "count below expected polls the asset budget",
			view:      pubgh.AssetsView{},
			wantErr:   "asset count 0, want 3",
			pollTimes: 12,
		},
		{
			name: "count above expected fails immediately",
			view: pubgh.AssetsView{Assets: []pubgh.Asset{
				shaAsset("gamma.bin", payloadDigest),
				shaAsset("checksums.txt", checksumDigest),
				shaAsset("checksums.txt.sigstore.json", bundleDigest),
				shaAsset("extra.bin", payloadDigest),
			}},
			wantErr:   "asset count 4, want 3",
			pollTimes: 1,
		},
		{
			name: "duplicate names fail immediately",
			view: pubgh.AssetsView{Assets: []pubgh.Asset{
				shaAsset("gamma.bin", payloadDigest),
				shaAsset("gamma.bin", payloadDigest),
				shaAsset("checksums.txt", checksumDigest),
			}},
			wantErr:   "duplicate asset name gamma.bin",
			pollTimes: 1,
		},
		{
			name: "missing digest polls the asset budget",
			view: pubgh.AssetsView{Assets: []pubgh.Asset{
				shaAsset("gamma.bin", payloadDigest),
				{Name: "checksums.txt", State: "uploaded"},
				shaAsset("checksums.txt.sigstore.json", bundleDigest),
			}},
			wantErr:   "asset checksums.txt has no digest",
			pollTimes: 12,
		},
		{
			name: "state other than uploaded polls the asset budget",
			view: pubgh.AssetsView{Assets: []pubgh.Asset{
				shaAsset("gamma.bin", payloadDigest),
				{Name: "checksums.txt", Digest: "sha256:" + checksumDigest, State: "new"},
				shaAsset("checksums.txt.sigstore.json", bundleDigest),
			}},
			wantErr:   `asset checksums.txt state "new", want "uploaded"`,
			pollTimes: 12,
		},
		{
			name: "digest mismatch fails immediately",
			view: pubgh.AssetsView{Assets: []pubgh.Asset{
				shaAsset("gamma.bin", wrongDigest),
				shaAsset("checksums.txt", checksumDigest),
				shaAsset("checksums.txt.sigstore.json", bundleDigest),
			}},
			wantErr:   "asset gamma.bin digest",
			pollTimes: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tc := newPublishHarness(t)
			draft := tc.draft()
			mock.InOrder(
				tc.resolver.EXPECT().
					Resolve(mock.Anything, tc.input.Tag).
					Return(tc.input.Commit, nil).
					Once(),
				tc.reader.EXPECT().
					FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
					Return(draft, nil).
					Once(),
				tc.reader.EXPECT().
					WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
					Return(pubgh.AssetsView{}, nil).
					Once(),
				tc.replacer.EXPECT().
					Replace(mock.Anything, tc.input.Repository, tc.input.Tag, tc.input.Assets).
					Return(nil).
					Once(),
				tc.reader.EXPECT().
					WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
					Return(test.view, nil).
					Times(test.pollTimes),
			)

			_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			require.NotErrorIs(t, err, pubgh.ErrIndeterminate)
			tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
			if test.pollTimes == 12 {
				assert.Equal(t, repeatWait(12, time.Second), tc.waits)
			} else {
				assert.Empty(t, tc.waits)
			}
		})
	}
}

func TestPublishNoUndraftLeavesDraft(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	tc.input.Undraft = false
	draft := tc.draft()
	view := expectedAssets()
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(draft, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(pubgh.AssetsView{}, nil).
			Once(),
		tc.replacer.EXPECT().
			Replace(mock.Anything, tc.input.Repository, tc.input.Tag, tc.input.Assets).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(view, nil).
			Once(),
		tc.reader.EXPECT().
			Get(mock.Anything, tc.input.Repository, draft.ID).
			Return(draft, nil).
			Once(),
	)

	got, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.NoError(t, err)
	assert.True(t, got.Draft)
	assert.Equal(t, draft.ID.Int64(), got.ReleaseID)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishNoUndraftOnPublicReleaseIsIndeterminate(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	tc.input.Undraft = false
	published := tc.draft()
	published.Draft = false
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(published, nil).
			Once(),
	)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrIndeterminate)
	assert.Contains(t, err.Error(), "draft-only publication requested")
	assert.Contains(t, err.Error(), "already public")
	tc.reader.AssertNotCalled(t, "WaitAssets", mock.Anything, mock.Anything, mock.Anything)
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishUndraftStillDraftIsIndeterminate(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	draft := tc.draft()
	view := expectedAssets()
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(draft, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(pubgh.AssetsView{}, nil).
			Once(),
		tc.replacer.EXPECT().
			Replace(mock.Anything, tc.input.Repository, tc.input.Tag, tc.input.Assets).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(view, nil).
			Once(),
		tc.publisher.EXPECT().
			Publish(mock.Anything, tc.input.Repository, draft.ID).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			Get(mock.Anything, tc.input.Repository, draft.ID).
			Return(draft, nil).
			Once(),
	)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrIndeterminate)
	assert.Contains(t, err.Error(), "still a draft after publish")
}

func TestPublishUndraftMutationFailureIsIndeterminate(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	draft := tc.draft()
	view := expectedAssets()
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(draft, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(pubgh.AssetsView{}, nil).
			Once(),
		tc.replacer.EXPECT().
			Replace(mock.Anything, tc.input.Repository, tc.input.Tag, tc.input.Assets).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(view, nil).
			Once(),
		tc.publisher.EXPECT().
			Publish(mock.Anything, tc.input.Repository, draft.ID).
			Return(errors.New("timeout after draft:false")).
			Once(),
	)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrIndeterminate)
	assert.Contains(t, err.Error(), "may have applied")
	assert.Contains(t, err.Error(), "timeout after draft:false")
	tc.reader.AssertNotCalled(t, "Get", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishRerunExactAssetsSucceeds(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	published := tc.draft()
	published.Draft = false
	view := expectedAssets()
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(published, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, published.ID).
			Return(view, nil).
			Once(),
	)

	got, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.NoError(t, err)
	assert.False(t, got.Draft)
	assert.Equal(t, published.ID.Int64(), got.ReleaseID)
	assert.Equal(t, []string{"checksums.txt", "checksums.txt.sigstore.json", "gamma.bin"}, got.Assets)
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishRerunMismatchIsIndeterminate(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	published := tc.draft()
	published.Draft = false
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(published, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, published.ID).
			Return(pubgh.AssetsView{Assets: []pubgh.Asset{
				shaAsset("gamma.bin", wrongDigest),
				shaAsset("checksums.txt", checksumDigest),
				shaAsset("checksums.txt.sigstore.json", bundleDigest),
			}}, nil).
			Once(),
	)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrIndeterminate)
	tc.replacer.AssertNotCalled(t, "Replace", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tc.publisher.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishRetryableReadRecovers(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	draft := tc.draft()
	published := draft
	published.Draft = false
	view := expectedAssets()
	mock.InOrder(
		tc.resolver.EXPECT().
			Resolve(mock.Anything, tc.input.Tag).
			Return(tc.input.Commit, nil).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(pubgh.Release{}, pubgh.ErrRetryable).
			Once(),
		tc.reader.EXPECT().
			FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
			Return(draft, nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(pubgh.AssetsView{}, nil).
			Once(),
		tc.replacer.EXPECT().
			Replace(mock.Anything, tc.input.Repository, tc.input.Tag, tc.input.Assets).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			WaitAssets(mock.Anything, tc.input.Repository, draft.ID).
			Return(view, nil).
			Once(),
		tc.publisher.EXPECT().
			Publish(mock.Anything, tc.input.Repository, draft.ID).
			Return(nil).
			Once(),
		tc.reader.EXPECT().
			Get(mock.Anything, tc.input.Repository, draft.ID).
			Return(published, nil).
			Once(),
	)

	got, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.NoError(t, err)
	assert.False(t, got.Draft)
	assert.Equal(t, []time.Duration{time.Second}, tc.waits)
}

func TestPublishRetryableBudgetExhausted(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	tc.resolver.EXPECT().
		Resolve(mock.Anything, tc.input.Tag).
		Return(pubgh.CommitSHA(""), pubgh.ErrRetryable).
		Times(4)

	_, err := pubgh.Publish(context.Background(), tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, pubgh.ErrRetryable)
	assert.Contains(t, err.Error(), "after 4 attempts")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, tc.waits)
	tc.reader.AssertNotCalled(t, "FindDraft", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishCancelDuringPoll(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	tc.input.Sleep = func(_ context.Context, _ time.Duration) error {
		cancel()

		return context.Canceled
	}
	tc.resolver.EXPECT().
		Resolve(mock.Anything, tc.input.Tag).
		Return(tc.input.Commit, nil).
		Once()
	tc.reader.EXPECT().
		FindDraft(mock.Anything, tc.input.Repository, tc.input.Tag).
		Return(pubgh.Release{}, pubgh.ErrNoDraft).
		Once()

	_, err := pubgh.Publish(ctx, tc.input, tc.reader, tc.replacer, tc.publisher, tc.resolver)
	require.ErrorIs(t, err, context.Canceled)
	tc.reader.AssertNumberOfCalls(t, "FindDraft", 1)
}

func TestPublishRejectsNilPortsAndZeroInput(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t)
	var nilCtx context.Context

	tests := []struct {
		name      string
		ctx       context.Context
		input     pubgh.PublishInput
		reader    pubgh.ReleaseReader
		replacer  pubgh.AssetReplacer
		publisher pubgh.Publisher
		resolver  pubgh.RefResolver
		wantErr   string
	}{
		{
			name:      "nil context",
			ctx:       nilCtx,
			input:     tc.input,
			reader:    tc.reader,
			replacer:  tc.replacer,
			publisher: tc.publisher,
			resolver:  tc.resolver,
			wantErr:   "context is nil",
		},
		{
			name:      "nil reader",
			ctx:       context.Background(),
			input:     tc.input,
			replacer:  tc.replacer,
			publisher: tc.publisher,
			resolver:  tc.resolver,
			wantErr:   "release reader is nil",
		},
		{
			name:      "nil replacer",
			ctx:       context.Background(),
			input:     tc.input,
			reader:    tc.reader,
			publisher: tc.publisher,
			resolver:  tc.resolver,
			wantErr:   "asset replacer is nil",
		},
		{
			name:     "nil publisher",
			ctx:      context.Background(),
			input:    tc.input,
			reader:   tc.reader,
			replacer: tc.replacer,
			resolver: tc.resolver,
			wantErr:  "publisher is nil",
		},
		{
			name:      "nil resolver",
			ctx:       context.Background(),
			input:     tc.input,
			reader:    tc.reader,
			replacer:  tc.replacer,
			publisher: tc.publisher,
			wantErr:   "ref resolver is nil",
		},
		{
			name:      "zero input",
			ctx:       context.Background(),
			reader:    tc.reader,
			replacer:  tc.replacer,
			publisher: tc.publisher,
			resolver:  tc.resolver,
			wantErr:   "publish repository is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := pubgh.Publish(test.ctx, test.input, test.reader, test.replacer, test.publisher, test.resolver)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestParseCommitSHA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "lowercase object id", input: validCommit, want: validCommit},
		{name: "trims and lowercases", input: "  " + strings.ToUpper(otherCommit) + "  ", want: otherCommit},
		{name: "empty", input: "", wantErr: "commit sha is empty"},
		{name: "wrong length", input: "abc", wantErr: "has length 3, want 40"},
		{name: "not hex", input: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", wantErr: "is not hexadecimal"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := pubgh.ParseCommitSHA(test.input)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}

func TestReleaseIDFromInt(t *testing.T) {
	t.Parallel()

	got, err := pubgh.ReleaseIDFromInt(42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), got.Int64())
	assert.Equal(t, "42", got.String())

	parsed, err := pubgh.ParseReleaseID(" 42 ")
	require.NoError(t, err)
	assert.Equal(t, got, parsed)

	_, err = pubgh.ReleaseIDFromInt(0)
	require.Error(t, err)
}

// publishHarness holds the ports, input, and recorded sleeps for one [pubgh.Publish] test.
type publishHarness struct {
	reader    *ghrelmocks.MockReleaseReader
	replacer  *ghupmocks.MockAssetReplacer
	publisher *ghrelmocks.MockPublisher
	resolver  *gitxmocks.MockRefResolver
	id        pubgh.ReleaseID
	input     pubgh.PublishInput
	waits     []time.Duration
}

// newPublishHarness constructs a valid [pubgh.PublishInput] and generated mocks.
func newPublishHarness(t *testing.T) *publishHarness {
	t.Helper()

	repo, err := pubgh.ParseRepository("meigma/release")
	require.NoError(t, err)
	tag, err := rel.ParseTag("v1.2.3")
	require.NoError(t, err)
	id, err := pubgh.ReleaseIDFromInt(42)
	require.NoError(t, err)
	tc := &publishHarness{
		reader:    ghrelmocks.NewMockReleaseReader(t),
		replacer:  ghupmocks.NewMockAssetReplacer(t),
		publisher: ghrelmocks.NewMockPublisher(t),
		resolver:  gitxmocks.NewMockRefResolver(t),
		id:        id,
	}
	tc.input = pubgh.PublishInput{
		Repository: repo,
		Tag:        tag,
		Commit:     mustCommit(t, validCommit),
		Expected:   expectedBundleEntries(),
		Assets: []pubgh.AssetPath{
			"dist/gamma.bin",
			"dist/checksums.txt",
			"dist/checksums.txt.sigstore.json",
		},
		Undraft: true,
		Sleep: func(_ context.Context, d time.Duration) error {
			tc.waits = append(tc.waits, d)

			return nil
		},
	}

	return tc
}

// draft returns the matching draft release for the harness tag.
func (tc *publishHarness) draft() pubgh.Release {
	return pubgh.Release{
		ID:    tc.id,
		Tag:   tc.input.Tag,
		Draft: true,
		URL:   releaseURL,
	}
}

// mustCommit parses a commit object ID or fails the test.
func mustCommit(t *testing.T, raw string) pubgh.CommitSHA {
	t.Helper()

	sha, err := pubgh.ParseCommitSHA(raw)
	require.NoError(t, err)

	return sha
}

// expectedBundleEntries is the closed expected set used by Publish tests.
func expectedBundleEntries() pubgh.Bundle {
	return pubgh.Bundle{
		Payloads: []pubgh.BundleEntry{
			{Name: "gamma.bin", Digest: stage.Digest(payloadDigest)},
		},
		Controls: []pubgh.BundleEntry{
			{Name: "checksums.txt", Digest: stage.Digest(checksumDigest)},
			{Name: "checksums.txt.sigstore.json", Digest: stage.Digest(bundleDigest)},
		},
	}
}

// expectedAssets is the GitHub-reported view matching [expectedBundleEntries].
func expectedAssets() pubgh.AssetsView {
	return pubgh.AssetsView{Assets: []pubgh.Asset{
		shaAsset("gamma.bin", payloadDigest),
		shaAsset("checksums.txt", checksumDigest),
		shaAsset("checksums.txt.sigstore.json", bundleDigest),
	}}
}

// shaAsset is one uploaded GitHub asset with the canonical sha256 digest.
func shaAsset(name, hexDigest string) pubgh.Asset {
	return pubgh.Asset{
		Name:   name,
		Digest: "sha256:" + hexDigest,
		State:  "uploaded",
	}
}

// repeatWait returns n copies of wait.
func repeatWait(n int, wait time.Duration) []time.Duration {
	waits := make([]time.Duration, n)
	for i := range waits {
		waits[i] = wait
	}

	return waits
}
