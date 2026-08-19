package puboci_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	regmocks "github.com/meigma/release/internal/adapter/reg/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	validDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestParseImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "ghcr repository", input: "ghcr.io/owner/repo", want: "ghcr.io/owner/repo"},
		{name: "localhost with port", input: "localhost:5000/team/app", want: "localhost:5000/team/app"},
		{name: "nested path", input: "ghcr.io/owner/repo/app", want: "ghcr.io/owner/repo/app"},
		{name: "empty", input: "", wantErr: `image "" is empty`},
		{name: "uppercase", input: "ghcr.io/Owner/repo", wantErr: "contains an uppercase letter"},
		{name: "scheme", input: "https://ghcr.io/owner/repo", wantErr: "has a scheme"},
		{name: "tag suffix", input: "ghcr.io/owner/repo:1.2.3", wantErr: "has a tag"},
		{
			name:    "digest",
			input:   "ghcr.io/owner/repo@sha256:" + strings.Repeat("a", 64),
			wantErr: "has a digest",
		},
		{name: "leading slash", input: "/ghcr.io/owner/repo", wantErr: "has a leading slash"},
		{name: "trailing slash", input: "ghcr.io/owner/repo/", wantErr: "has a trailing slash"},
		{name: "space", input: "ghcr.io/owner/repo extra", wantErr: "contains a space"},
		{name: "host only", input: "ghcr.io", wantErr: "has no path"},
		{name: "empty path element", input: "ghcr.io//repo", wantErr: "has an empty path element"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := puboci.ParseImage(test.input)
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

func TestReferenceString(t *testing.T) {
	t.Parallel()

	image := mustImage(t, "ghcr.io/owner/repo")
	ref := image.Reference(rel.Tag("1.2.3"))
	assert.Equal(t, "ghcr.io/owner/repo:1.2.3", ref.String())
}

func TestCollectState(t *testing.T) {
	t.Parallel()

	fixture := newPlanFixture(t)
	older := rel.Version{Major: 1, Minor: 2, Patch: 2}

	tests := []struct {
		name    string
		setup   func(reader *regmocks.MockStateReader)
		want    rel.ChannelState
		wantErr string
		wantIs  error
	}{
		{
			name: "every tag absent",
			setup: func(reader *regmocks.MockStateReader) {
				expectAbsent(reader, fixture.exact)
				expectAbsent(reader, fixture.minor)
				expectAbsent(reader, fixture.major)
				expectAbsent(reader, fixture.latest)
			},
			want: emptyState(fixture.version),
		},
		{
			name: "exact present at the candidate digest",
			setup: func(reader *regmocks.MockStateReader) {
				expectDigest(reader, fixture.exact, fixture.digest)
				expectAbsent(reader, fixture.minor)
				expectAbsent(reader, fixture.major)
				expectAbsent(reader, fixture.latest)
			},
			want: withExact(emptyState(fixture.version), rel.TagState{
				Present: true,
				Digest:  fixture.digest,
			}),
		},
		{
			name: "exact present at another digest",
			setup: func(reader *regmocks.MockStateReader) {
				expectDigest(reader, fixture.exact, fixture.other)
				expectAbsent(reader, fixture.minor)
				expectAbsent(reader, fixture.major)
				expectAbsent(reader, fixture.latest)
			},
			want: withExact(emptyState(fixture.version), rel.TagState{
				Present: true,
				Digest:  fixture.other,
			}),
		},
		{
			name: "channel present at the candidate digest",
			setup: func(reader *regmocks.MockStateReader) {
				expectAbsent(reader, fixture.exact)
				expectDigest(reader, fixture.minor, fixture.digest)
				expectAbsent(reader, fixture.major)
				expectAbsent(reader, fixture.latest)
			},
			want: withChannel(emptyState(fixture.version), fixture.minorChannel, rel.TagState{
				Present: true,
				Digest:  fixture.digest,
			}),
		},
		{
			name: "channel present at another digest reads the annotation once",
			setup: func(reader *regmocks.MockStateReader) {
				expectAbsent(reader, fixture.exact)
				expectDigest(reader, fixture.minor, fixture.other)
				reader.EXPECT().
					Version(mock.Anything, fixture.minor).
					Return(older, nil).
					Once()
				expectAbsent(reader, fixture.major)
				expectAbsent(reader, fixture.latest)
			},
			want: withChannel(emptyState(fixture.version), fixture.minorChannel, rel.TagState{
				Present:    true,
				Digest:     fixture.other,
				HasVersion: true,
				Version:    older,
			}),
		},
		{
			name: "wrapped tag-absent is treated as absent",
			setup: func(reader *regmocks.MockStateReader) {
				reader.EXPECT().
					Resolve(mock.Anything, fixture.exact).
					Return(rel.Digest(""), fmtWrap(puboci.ErrTagAbsent)).
					Once()
				expectAbsent(reader, fixture.minor)
				expectAbsent(reader, fixture.major)
				expectAbsent(reader, fixture.latest)
			},
			want: emptyState(fixture.version),
		},
		{
			name: "retryable resolve is propagated",
			setup: func(reader *regmocks.MockStateReader) {
				reader.EXPECT().
					Resolve(mock.Anything, fixture.exact).
					Return(rel.Digest(""), puboci.ErrRetryable).
					Once()
			},
			wantErr: fixture.exact.String(),
			wantIs:  puboci.ErrRetryable,
		},
		{
			name: "corrupt version is propagated",
			setup: func(reader *regmocks.MockStateReader) {
				expectAbsent(reader, fixture.exact)
				expectDigest(reader, fixture.minor, fixture.other)
				reader.EXPECT().
					Version(mock.Anything, fixture.minor).
					Return(rel.Version{}, puboci.ErrCorruptState).
					Once()
			},
			wantErr: fixture.minor.String(),
			wantIs:  puboci.ErrCorruptState,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			reader := regmocks.NewMockStateReader(t)
			test.setup(reader)

			got, err := puboci.CollectState(
				context.Background(),
				reader,
				fixture.image,
				fixture.version,
				fixture.digest,
			)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				require.ErrorIs(t, err, test.wantIs)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCollectStateRejectsNilReader(t *testing.T) {
	t.Parallel()

	fixture := newPlanFixture(t)
	_, err := puboci.CollectState(
		context.Background(),
		nil,
		fixture.image,
		fixture.version,
		fixture.digest,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state reader is nil")
}

func TestCollectStateRejectsNilContext(t *testing.T) {
	t.Parallel()

	fixture := newPlanFixture(t)
	reader := regmocks.NewMockStateReader(t)
	var ctx context.Context
	_, err := puboci.CollectState(ctx, reader, fixture.image, fixture.version, fixture.digest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is nil")
}

func TestCollectStateCancelledContext(t *testing.T) {
	t.Parallel()

	fixture := newPlanFixture(t)
	reader := regmocks.NewMockStateReader(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := puboci.CollectState(ctx, reader, fixture.image, fixture.version, fixture.digest)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), context.Canceled.Error())
}

func TestPlanTagsNewRelease(t *testing.T) {
	t.Parallel()

	fixture := newPlanFixture(t)
	reader := regmocks.NewMockStateReader(t)
	expectAbsent(reader, fixture.exact)
	expectAbsent(reader, fixture.minor)
	expectAbsent(reader, fixture.major)
	expectAbsent(reader, fixture.latest)

	got, err := puboci.PlanTags(
		context.Background(),
		reader,
		fixture.image,
		fixture.version,
		fixture.digest,
	)
	require.NoError(t, err)
	assert.Equal(t, fixture.version, got.Version)
	assert.Equal(t, fixture.digest, got.Digest)
	assert.Equal(t, []rel.Decision{
		{Tag: "1.2.3", Scope: rel.ScopeExact, Action: rel.ActionCreate},
		{Tag: "1.2", Scope: rel.ScopeMinor, Action: rel.ActionCreate},
		{Tag: "1", Scope: rel.ScopeMajor, Action: rel.ActionCreate},
		{Tag: "latest", Scope: rel.ScopeLatest, Action: rel.ActionCreate},
	}, got.Decisions)
	assert.Equal(t, []rel.Tag{"1.2.3", "1.2", "1", "latest"}, got.Apply())
}

func TestPlanTagsImmutableExactTag(t *testing.T) {
	t.Parallel()

	fixture := newPlanFixture(t)
	reader := regmocks.NewMockStateReader(t)
	expectDigest(reader, fixture.exact, fixture.other)
	expectAbsent(reader, fixture.minor)
	expectAbsent(reader, fixture.major)
	expectAbsent(reader, fixture.latest)

	_, err := puboci.PlanTags(
		context.Background(),
		reader,
		fixture.image,
		fixture.version,
		fixture.digest,
	)
	require.ErrorIs(t, err, rel.ErrImmutableTag)
}

// planFixture holds a candidate release and the references CollectState reads.
type planFixture struct {
	// image is the repository whose tags are planned.
	image puboci.Image
	// version is the candidate release version.
	version rel.Version
	// digest is the candidate image digest.
	digest rel.Digest
	// other is a different digest used for conflict cases.
	other rel.Digest
	// exact is the exact-version tag reference.
	exact puboci.Reference
	// minor is the minor channel tag reference.
	minor puboci.Reference
	// major is the major channel tag reference.
	major puboci.Reference
	// latest is the latest channel tag reference.
	latest puboci.Reference
	// minorChannel is the minor channel key in ChannelState.Channels.
	minorChannel rel.Channel
}

// newPlanFixture constructs a 1.2.3 candidate against ghcr.io/owner/repo.
func newPlanFixture(t *testing.T) planFixture {
	t.Helper()

	image := mustImage(t, "ghcr.io/owner/repo")
	version := rel.Version{Major: 1, Minor: 2, Patch: 3}
	channels := rel.ChannelsFor(version)

	return planFixture{
		image:        image,
		version:      version,
		digest:       mustDigest(t, validDigest),
		other:        mustDigest(t, otherDigest),
		exact:        image.Reference(version.Tag()),
		minor:        image.Reference(channels[0].Tag),
		major:        image.Reference(channels[1].Tag),
		latest:       image.Reference(channels[2].Tag),
		minorChannel: channels[0],
	}
}

// mustImage parses an image name or fails the test.
func mustImage(t *testing.T, value string) puboci.Image {
	t.Helper()

	image, err := puboci.ParseImage(value)
	require.NoError(t, err)

	return image
}

// mustDigest parses a digest or fails the test.
func mustDigest(t *testing.T, value string) rel.Digest {
	t.Helper()

	digest, err := rel.ParseDigest(value)
	require.NoError(t, err)

	return digest
}

// expectAbsent expects Resolve(ref) to report [ErrTagAbsent] once.
func expectAbsent(reader *regmocks.MockStateReader, ref puboci.Reference) {
	reader.EXPECT().Resolve(mock.Anything, ref).Return(rel.Digest(""), puboci.ErrTagAbsent).Once()
}

// expectDigest expects Resolve(ref) to return digest once.
func expectDigest(reader *regmocks.MockStateReader, ref puboci.Reference, digest rel.Digest) {
	reader.EXPECT().Resolve(mock.Anything, ref).Return(digest, nil).Once()
}

// emptyState is absent exact and channel tags for version.
func emptyState(version rel.Version) rel.ChannelState {
	channels := make(map[rel.Channel]rel.TagState, len(rel.ChannelsFor(version)))
	for _, channel := range rel.ChannelsFor(version) {
		channels[channel] = rel.TagState{}
	}

	return rel.ChannelState{Channels: channels}
}

// withExact returns state with a replaced exact tag observation.
func withExact(state rel.ChannelState, exact rel.TagState) rel.ChannelState {
	state.Exact = exact

	return state
}

// withChannel returns state with one replaced channel observation.
func withChannel(state rel.ChannelState, channel rel.Channel, observed rel.TagState) rel.ChannelState {
	state.Channels[channel] = observed

	return state
}

// fmtWrap wraps err so [errors.Is] still matches the sentinel.
func fmtWrap(err error) error {
	return errors.Join(errors.New("missing"), err)
}
