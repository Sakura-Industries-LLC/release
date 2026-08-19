package puboci_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	regmocks "github.com/meigma/release/internal/adapter/reg/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const thirdDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestFinalizeHappyPath(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	expectEmptyRegistry(tc.reader, tc.plan)
	expectCommit(tc, []rel.Tag{"1.2.3", "1.2", "1", "latest"})
	expectVerifyTags(tc.reader, tc.plan, tc.plan.exact, tc.plan.minor, tc.plan.major, tc.plan.latest)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, []string{"1.2.3", "1.2", "1", "latest"}, nil, nil), got)
}

func TestFinalizeRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := newFinalizeTest(t)
	badSchema := valid.input.Prepared
	badSchema.Schema = "release.dev/oci-prepare/v0"
	badDigest := valid.input.Prepared
	badDigest.IndexDigest = "not-a-digest"
	emptyPlatforms := valid.input.Prepared
	emptyPlatforms.Platforms = nil
	notAuthoritative := valid.input.Prepared
	notAuthoritative.Authoritative = false

	tests := []struct {
		name      string
		ctx       context.Context
		input     puboci.FinalizeInput
		state     puboci.StateReader
		committer puboci.TagCommitter
		wantErr   string
		wantIs    error
	}{
		{
			name:      "nil context",
			input:     valid.input,
			state:     valid.reader,
			committer: valid.committer,
			wantErr:   "context is nil",
		},
		{
			name:      "nil state reader",
			ctx:       context.Background(),
			input:     valid.input,
			committer: valid.committer,
			wantErr:   "state reader is nil",
		},
		{
			name:    "nil committer",
			ctx:     context.Background(),
			input:   valid.input,
			state:   valid.reader,
			wantErr: "tag committer is nil",
		},
		{
			name:      "bad schema",
			ctx:       context.Background(),
			input:     puboci.FinalizeInput{Prepared: badSchema, Sleep: instantSleep},
			state:     valid.reader,
			committer: valid.committer,
			wantErr:   "unsupported",
		},
		{
			name:      "bad digest",
			ctx:       context.Background(),
			input:     puboci.FinalizeInput{Prepared: badDigest, Sleep: instantSleep},
			state:     valid.reader,
			committer: valid.committer,
			wantErr:   "index digest",
		},
		{
			name:      "empty platforms",
			ctx:       context.Background(),
			input:     puboci.FinalizeInput{Prepared: emptyPlatforms, Sleep: instantSleep},
			state:     valid.reader,
			committer: valid.committer,
			wantErr:   "no platforms",
		},
		{
			name:      "not authoritative",
			ctx:       context.Background(),
			input:     puboci.FinalizeInput{Prepared: notAuthoritative, Sleep: instantSleep},
			state:     valid.reader,
			committer: valid.committer,
			wantIs:    puboci.ErrNotAuthoritative,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := puboci.Finalize(test.ctx, test.input, test.state, test.committer)
			require.Error(t, err)
			if test.wantIs != nil {
				require.ErrorIs(t, err, test.wantIs)
			}
			if test.wantErr != "" {
				assert.Contains(t, err.Error(), test.wantErr)
			}
			assert.Equal(t, puboci.FinalizeResult{}, got)
		})
	}
}

func TestFinalizeDrift(t *testing.T) {
	t.Parallel()

	older := rel.Version{Major: 1, Minor: 2, Patch: 2}
	changed := rel.Version{Major: 1, Minor: 2, Patch: 1}

	tests := []struct {
		name    string
		prepare func(tc *finalizeTest) rel.ChannelState
		mutate  func(prepared *puboci.OCIPrepareResult)
		fresh   func(tc *finalizeTest, reader *regmocks.MockStateReader)
		tag     string
	}{
		{
			name: "channel moved to a third digest",
			prepare: func(tc *finalizeTest) rel.ChannelState {
				return withLatest(emptyState(tc.plan.version), rel.TagState{
					Present:    true,
					Digest:     tc.plan.other,
					HasVersion: true,
					Version:    older,
				})
			},
			fresh: func(tc *finalizeTest, reader *regmocks.MockStateReader) {
				expectAbsent(reader, tc.plan.exact)
				expectAbsent(reader, tc.plan.minor)
				expectAbsent(reader, tc.plan.major)
				expectDigest(reader, tc.plan.latest, mustDigest(t, thirdDigest))
				reader.EXPECT().
					Version(mock.Anything, tc.plan.latest).
					Return(older, nil).
					Once()
			},
			tag: "latest",
		},
		{
			name: "channel version annotation changed",
			prepare: func(tc *finalizeTest) rel.ChannelState {
				return withLatest(emptyState(tc.plan.version), rel.TagState{
					Present:    true,
					Digest:     tc.plan.other,
					HasVersion: true,
					Version:    older,
				})
			},
			fresh: func(tc *finalizeTest, reader *regmocks.MockStateReader) {
				expectAbsent(reader, tc.plan.exact)
				expectAbsent(reader, tc.plan.minor)
				expectAbsent(reader, tc.plan.major)
				expectDigest(reader, tc.plan.latest, tc.plan.other)
				reader.EXPECT().
					Version(mock.Anything, tc.plan.latest).
					Return(changed, nil).
					Once()
			},
			tag: "latest",
		},
		{
			name: "tag disappeared",
			prepare: func(tc *finalizeTest) rel.ChannelState {
				return withLatest(emptyState(tc.plan.version), rel.TagState{
					Present:    true,
					Digest:     tc.plan.other,
					HasVersion: true,
					Version:    older,
				})
			},
			fresh: func(tc *finalizeTest, reader *regmocks.MockStateReader) {
				expectEmptyRegistry(reader, tc.plan)
			},
			tag: "latest",
		},
		{
			name: "observed latest entry removed",
			prepare: func(tc *finalizeTest) rel.ChannelState {
				return emptyState(tc.plan.version)
			},
			mutate: func(prepared *puboci.OCIPrepareResult) {
				prepared.Observed = prepared.Observed[:len(prepared.Observed)-1]
			},
			fresh: func(tc *finalizeTest, reader *regmocks.MockStateReader) {
				expectEmptyRegistry(reader, tc.plan)
			},
			tag: "latest",
		},
		{
			name: "exact tag moved off the candidate digest",
			prepare: func(tc *finalizeTest) rel.ChannelState {
				return withExact(emptyState(tc.plan.version), rel.TagState{
					Present: true,
					Digest:  tc.plan.digest,
				})
			},
			fresh: func(tc *finalizeTest, reader *regmocks.MockStateReader) {
				expectDigest(reader, tc.plan.exact, tc.plan.other)
				expectAbsent(reader, tc.plan.minor)
				expectAbsent(reader, tc.plan.major)
				expectAbsent(reader, tc.plan.latest)
			},
			tag: "1.2.3",
		},
		{
			name: "retained latest moved onto the candidate digest",
			prepare: func(tc *finalizeTest) rel.ChannelState {
				return withLatest(emptyState(tc.plan.version), rel.TagState{
					Present:    true,
					Digest:     tc.plan.other,
					HasVersion: true,
					Version:    rel.Version{Major: 1, Minor: 2, Patch: 4},
				})
			},
			fresh: func(tc *finalizeTest, reader *regmocks.MockStateReader) {
				expectAbsent(reader, tc.plan.exact)
				expectAbsent(reader, tc.plan.minor)
				expectAbsent(reader, tc.plan.major)
				expectDigest(reader, tc.plan.latest, tc.plan.digest)
			},
			tag: "latest",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tc := newFinalizeTest(t)
			tc.input.Prepared = newFinalizePrepare(t, tc.plan, test.prepare(tc))
			if test.mutate != nil {
				test.mutate(&tc.input.Prepared)
			}
			test.fresh(tc, tc.reader)

			got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
			require.ErrorIs(t, err, puboci.ErrStateDrift)
			assert.Contains(t, err.Error(), test.tag)
			assert.Contains(t, err.Error(), "prepared")
			assert.Contains(t, err.Error(), "now")
			assert.Equal(t, puboci.FinalizeResult{}, got)
		})
	}
}

func TestFinalizeRerunConvergence(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	expectDigest(tc.reader, tc.plan.exact, tc.plan.digest)
	expectAbsent(tc.reader, tc.plan.minor)
	expectAbsent(tc.reader, tc.plan.major)
	expectAbsent(tc.reader, tc.plan.latest)
	expectCommit(tc, []rel.Tag{"1.2", "1", "latest"})
	expectVerifyTags(tc.reader, tc.plan, tc.plan.exact, tc.plan.minor, tc.plan.major, tc.plan.latest)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, []string{"1.2", "1", "latest"}, []string{"1.2.3"}, nil), got)
}

func TestFinalizeHalfCompleted(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	expectDigest(tc.reader, tc.plan.exact, tc.plan.digest)
	expectDigest(tc.reader, tc.plan.minor, tc.plan.digest)
	expectAbsent(tc.reader, tc.plan.major)
	expectAbsent(tc.reader, tc.plan.latest)
	expectCommit(tc, []rel.Tag{"1", "latest"})
	expectVerifyTags(tc.reader, tc.plan, tc.plan.exact, tc.plan.major, tc.plan.latest)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, []string{"1", "latest"}, []string{"1.2.3", "1.2"}, nil), got)
}

func TestFinalizeRetainsNewerLatest(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	newer := rel.Version{Major: 1, Minor: 2, Patch: 4}
	prepared := withLatest(emptyState(tc.plan.version), rel.TagState{
		Present:    true,
		Digest:     tc.plan.other,
		HasVersion: true,
		Version:    newer,
	})
	tc.input.Prepared = newFinalizePrepare(t, tc.plan, prepared)
	expectAbsent(tc.reader, tc.plan.exact)
	expectAbsent(tc.reader, tc.plan.minor)
	expectAbsent(tc.reader, tc.plan.major)
	expectDigest(tc.reader, tc.plan.latest, tc.plan.other)
	tc.reader.EXPECT().
		Version(mock.Anything, tc.plan.latest).
		Return(newer, nil).
		Once()
	expectCommit(tc, []rel.Tag{"1.2.3", "1.2", "1"})
	expectVerifyTags(tc.reader, tc.plan, tc.plan.exact, tc.plan.minor, tc.plan.major)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, []string{"1.2.3", "1.2", "1"}, nil, []string{"latest"}), got)
}

func TestFinalizeImmutableExactTag(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	prepared := withExact(emptyState(tc.plan.version), rel.TagState{
		Present: true,
		Digest:  tc.plan.other,
	})
	tc.input.Prepared = newFinalizePrepare(t, tc.plan, prepared)
	expectDigest(tc.reader, tc.plan.exact, tc.plan.other)
	expectAbsent(tc.reader, tc.plan.minor)
	expectAbsent(tc.reader, tc.plan.major)
	expectAbsent(tc.reader, tc.plan.latest)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.ErrorIs(t, err, rel.ErrImmutableTag)
	assert.Equal(t, puboci.FinalizeResult{}, got)
}

func TestFinalizeNothingToApply(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	expectDigest(tc.reader, tc.plan.exact, tc.plan.digest)
	expectDigest(tc.reader, tc.plan.minor, tc.plan.digest)
	expectDigest(tc.reader, tc.plan.major, tc.plan.digest)
	expectDigest(tc.reader, tc.plan.latest, tc.plan.digest)
	expectVerifyTags(tc.reader, tc.plan, tc.plan.exact)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, nil, []string{"1.2.3", "1.2", "1", "latest"}, nil), got)
}

func TestFinalizeCommitFailure(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	expectEmptyRegistry(tc.reader, tc.plan)
	tc.committer.EXPECT().
		Commit(mock.Anything, tc.plan.image, tc.plan.digest, []rel.Tag{"1.2.3", "1.2", "1", "latest"}).
		Return(errors.New("tag rejected")).
		Once()

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit tags")
	assert.Contains(t, err.Error(), "tag rejected")
	assert.Equal(t, puboci.FinalizeResult{}, got)
}

func TestFinalizeRetryableCommitThenSucceeds(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)
	tags := []rel.Tag{"1.2.3", "1.2", "1", "latest"}
	tc.committer.EXPECT().
		Commit(mock.Anything, tc.plan.image, tc.plan.digest, tags).
		Return(puboci.ErrRetryable).
		Once()
	tc.committer.EXPECT().
		Commit(mock.Anything, tc.plan.image, tc.plan.digest, tags).
		Return(nil).
		Once()
	expectVerifyTags(tc.reader, tc.plan, tc.plan.exact, tc.plan.minor, tc.plan.major, tc.plan.latest)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, []string{"1.2.3", "1.2", "1", "latest"}, nil, nil), got)
	assert.Equal(t, []time.Duration{time.Second}, waits)
}

func TestFinalizeRetryableCommitExhausted(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)
	tc.committer.EXPECT().
		Commit(mock.Anything, tc.plan.image, tc.plan.digest, []rel.Tag{"1.2.3", "1.2", "1", "latest"}).
		Return(puboci.ErrRetryable).
		Times(4)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.ErrorIs(t, err, puboci.ErrRetryable)
	assert.Contains(t, err.Error(), "commit tags")
	assert.Contains(t, err.Error(), "after 4 attempts")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, waits)
	assert.Equal(t, puboci.FinalizeResult{}, got)
}

func TestFinalizeVerificationMismatch(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	expectEmptyRegistry(tc.reader, tc.plan)
	expectCommit(tc, []rel.Tag{"1.2.3", "1.2", "1", "latest"})
	expectDigest(tc.reader, tc.plan.exact, tc.plan.other)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1.2.3")
	assert.Contains(t, err.Error(), tc.plan.other.String())
	assert.Contains(t, err.Error(), tc.plan.digest.String())
	assert.Equal(t, puboci.FinalizeResult{}, got)
}

func TestFinalizeRetryableVerifyThenSucceeds(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)
	expectCommit(tc, []rel.Tag{"1.2.3", "1.2", "1", "latest"})
	tc.reader.EXPECT().
		Resolve(mock.Anything, tc.plan.exact).
		Return(rel.Digest(""), puboci.ErrRetryable).
		Once()
	expectDigest(tc.reader, tc.plan.exact, tc.plan.digest)
	expectVerifyTags(tc.reader, tc.plan, tc.plan.minor, tc.plan.major, tc.plan.latest)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.NoError(t, err)
	assert.Equal(t, wantFinalizeResult(tc, []string{"1.2.3", "1.2", "1", "latest"}, nil, nil), got)
	assert.Equal(t, []time.Duration{time.Second}, waits)
}

func TestFinalizeRetryableVerifyExhausted(t *testing.T) {
	t.Parallel()

	tc := newFinalizeTest(t)
	var waits []time.Duration
	tc.input.Sleep = recordSleep(&waits)
	expectEmptyRegistry(tc.reader, tc.plan)
	expectCommit(tc, []rel.Tag{"1.2.3", "1.2", "1", "latest"})
	tc.reader.EXPECT().
		Resolve(mock.Anything, tc.plan.exact).
		Return(rel.Digest(""), puboci.ErrRetryable).
		Times(4)

	got, err := puboci.Finalize(context.Background(), tc.input, tc.reader, tc.committer)
	require.ErrorIs(t, err, puboci.ErrRetryable)
	assert.Contains(t, err.Error(), "verify tag")
	assert.Contains(t, err.Error(), "after 4 attempts")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, waits)
	assert.Equal(t, puboci.FinalizeResult{}, got)
}

// finalizeTest is one Finalize invocation and the collaborators it needs.
type finalizeTest struct {
	// plan is the repository and tags CollectState reads.
	plan planFixture
	// input is the candidate finalize request.
	input puboci.FinalizeInput
	// reader is the registry state port.
	reader *regmocks.MockStateReader
	// committer is the serial tag-write port.
	committer *regmocks.MockTagCommitter
}

// newFinalizeTest builds an authoritative first-publication finalize request.
func newFinalizeTest(t *testing.T) *finalizeTest {
	t.Helper()

	plan := newPlanFixture(t)

	return &finalizeTest{
		plan: plan,
		input: puboci.FinalizeInput{
			Prepared: newFinalizePrepare(t, plan, emptyState(plan.version)),
			Sleep:    instantSleep,
		},
		reader:    regmocks.NewMockStateReader(t),
		committer: regmocks.NewMockTagCommitter(t),
	}
}

// newFinalizePrepare renders an authoritative prepare document for plan and state.
func newFinalizePrepare(t *testing.T, plan planFixture, state rel.ChannelState) puboci.OCIPrepareResult {
	t.Helper()

	return puboci.NewPrepareResult(
		plan.image,
		plan.version,
		plan.digest,
		[]puboci.PlatformImage{
			{
				Descriptor: puboci.Descriptor{Digest: plan.digest},
				Platform:   puboci.Platform{OS: "linux", Architecture: "amd64"},
			},
		},
		state,
		true,
	)
}

// expectCommit expects one serial Commit of tags in plan order.
func expectCommit(tc *finalizeTest, tags []rel.Tag) {
	tc.committer.EXPECT().
		Commit(mock.Anything, tc.plan.image, tc.plan.digest, tags).
		Return(nil).
		Once()
}

// expectVerifyTags expects each ref to resolve to the candidate digest once.
func expectVerifyTags(reader *regmocks.MockStateReader, plan planFixture, refs ...puboci.Reference) {
	for _, ref := range refs {
		expectDigest(reader, ref, plan.digest)
	}
}

// wantFinalizeResult is the document Finalize should emit for tc.
func wantFinalizeResult(tc *finalizeTest, applied, accepted, retained []string) puboci.FinalizeResult {
	if applied == nil {
		applied = []string{}
	}
	if accepted == nil {
		accepted = []string{}
	}
	if retained == nil {
		retained = []string{}
	}

	return puboci.FinalizeResult{
		Schema:      puboci.FinalizeSchema,
		Image:       tc.plan.image.String(),
		Version:     tc.plan.version.String(),
		IndexDigest: tc.plan.digest.String(),
		Applied:     applied,
		Accepted:    accepted,
		Retained:    retained,
	}
}

// withLatest returns state with a replaced latest-channel observation.
func withLatest(state rel.ChannelState, observed rel.TagState) rel.ChannelState {
	for channel := range state.Channels {
		if channel.Scope == rel.ScopeLatest {
			state.Channels[channel] = observed

			return state
		}
	}

	return state
}
