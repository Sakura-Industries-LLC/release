package rel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const otherHex = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestParseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "single letter", input: "v", want: "v"},
		{name: "single digit", input: "1", want: "1"},
		{name: "underscore start", input: "_canary", want: "_canary"},
		{name: "dotted version", input: "1.2.3", want: "1.2.3"},
		{name: "inner hyphen and underscore", input: "release-1_2.3", want: "release-1_2.3"},
		{name: "max length", input: strings.Repeat("a", maxTagLength), want: strings.Repeat("a", maxTagLength)},
		{name: "empty", input: "", wantErr: `tag "" is empty`},
		{name: "leading hyphen", input: "-latest", wantErr: `tag "-latest" has an invalid leading character`},
		{name: "leading dot", input: ".1", wantErr: `tag ".1" has an invalid leading character`},
		{name: "inner slash", input: "rel/1", wantErr: `tag "rel/1" has an invalid character`},
		{
			name:    "too long",
			input:   strings.Repeat("a", maxTagLength+1),
			wantErr: `tag "` + strings.Repeat("a", maxTagLength+1) + `" has length 129, want at most 128`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTag(test.input)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}

func TestChannelsFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version Version
		want    []Channel
	}{
		{
			name:    "zero",
			version: Version{},
			want: []Channel{
				{Scope: ScopeMinor, Tag: "0.0"},
				{Scope: ScopeMajor, Tag: "0"},
				{Scope: ScopeLatest, Tag: "latest"},
			},
		},
		{
			name:    "stable",
			version: Version{Major: 1, Minor: 2, Patch: 3},
			want: []Channel{
				{Scope: ScopeMinor, Tag: "1.2"},
				{Scope: ScopeMajor, Tag: "1"},
				{Scope: ScopeLatest, Tag: "latest"},
			},
		},
		{
			name:    "multi-digit",
			version: Version{Major: 10, Minor: 20, Patch: 30},
			want: []Channel{
				{Scope: ScopeMinor, Tag: "10.20"},
				{Scope: ScopeMajor, Tag: "10"},
				{Scope: ScopeLatest, Tag: "latest"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, ChannelsFor(test.version))
		})
	}
}

func TestPlanTags(t *testing.T) {
	t.Parallel()

	candidate := Version{Major: 1, Minor: 2, Patch: 3}
	digest := mustDigest(t, validDigest)
	other := mustDigest(t, digestPrefix+otherHex)
	olderSameLine := Version{Major: 1, Minor: 2, Patch: 2}
	newerSameLine := Version{Major: 1, Minor: 2, Patch: 4}
	otherMinor := Version{Major: 1, Minor: 3, Patch: 0}
	otherMajor := Version{Major: 2, Minor: 0, Patch: 0}
	olderMajor := Version{Major: 0, Minor: 9, Patch: 0}

	tests := []struct {
		name      string
		version   Version
		digest    Digest
		state     ChannelState
		want      []Decision
		wantApply []Tag
		wantErr   error
	}{
		{
			name:    "exact and channels absent",
			version: candidate,
			digest:  digest,
			state:   emptyState(candidate),
			want: []Decision{
				{Tag: "1.2.3", Scope: ScopeExact, Action: ActionCreate},
				{Tag: "1.2", Scope: ScopeMinor, Action: ActionCreate},
				{Tag: "1", Scope: ScopeMajor, Action: ActionCreate},
				{Tag: "latest", Scope: ScopeLatest, Action: ActionCreate},
			},
			wantApply: []Tag{"1.2.3", "1.2", "1", "latest"},
		},
		{
			name:    "exact already the candidate digest",
			version: candidate,
			digest:  digest,
			state: withExact(emptyState(candidate), TagState{
				Present: true,
				Digest:  digest,
			}),
			want: []Decision{
				{Tag: "1.2.3", Scope: ScopeExact, Action: ActionAccept},
				{Tag: "1.2", Scope: ScopeMinor, Action: ActionCreate},
				{Tag: "1", Scope: ScopeMajor, Action: ActionCreate},
				{Tag: "latest", Scope: ScopeLatest, Action: ActionCreate},
			},
			wantApply: []Tag{"1.2", "1", "latest"},
		},
		{
			name:    "exact on another digest",
			version: candidate,
			digest:  digest,
			state: withExact(emptyState(candidate), TagState{
				Present: true,
				Digest:  other,
			}),
			wantErr: ErrImmutableTag,
		},
		{
			name:    "every channel already the candidate digest",
			version: candidate,
			digest:  digest,
			state: ChannelState{
				Exact: TagState{Present: true, Digest: digest},
				Channels: map[Channel]TagState{
					{Scope: ScopeMinor, Tag: "1.2"}:     {Present: true, Digest: digest},
					{Scope: ScopeMajor, Tag: "1"}:       {Present: true, Digest: digest},
					{Scope: ScopeLatest, Tag: "latest"}: {Present: true, Digest: digest},
				},
			},
			want: []Decision{
				{Tag: "1.2.3", Scope: ScopeExact, Action: ActionAccept},
				{Tag: "1.2", Scope: ScopeMinor, Action: ActionAccept},
				{Tag: "1", Scope: ScopeMajor, Action: ActionAccept},
				{Tag: "latest", Scope: ScopeLatest, Action: ActionAccept},
			},
			wantApply: []Tag{},
		},
		{
			name:    "newer candidate moves every channel",
			version: candidate,
			digest:  digest,
			state:   annotatedState(candidate, other, olderSameLine),
			want: []Decision{
				{Tag: "1.2.3", Scope: ScopeExact, Action: ActionCreate},
				{Tag: "1.2", Scope: ScopeMinor, Action: ActionCreate},
				{Tag: "1", Scope: ScopeMajor, Action: ActionCreate},
				{Tag: "latest", Scope: ScopeLatest, Action: ActionCreate},
			},
			wantApply: []Tag{"1.2.3", "1.2", "1", "latest"},
		},
		{
			name:    "older candidate retains every channel",
			version: candidate,
			digest:  digest,
			state:   annotatedState(candidate, other, newerSameLine),
			want: []Decision{
				{Tag: "1.2.3", Scope: ScopeExact, Action: ActionCreate},
				{Tag: "1.2", Scope: ScopeMinor, Action: ActionRetain},
				{Tag: "1", Scope: ScopeMajor, Action: ActionRetain},
				{Tag: "latest", Scope: ScopeLatest, Action: ActionRetain},
			},
			wantApply: []Tag{"1.2.3"},
		},
		{
			name:    "equal version different digest is corrupt",
			version: candidate,
			digest:  digest,
			state:   annotatedState(candidate, other, candidate),
			wantErr: ErrChannelCorrupt,
		},
		{
			name:    "channel present without version annotation",
			version: candidate,
			digest:  digest,
			state: withChannel(emptyState(candidate), Channel{Scope: ScopeMinor, Tag: "1.2"}, TagState{
				Present: true,
				Digest:  other,
			}),
			wantErr: ErrChannelCorrupt,
		},
		{
			name:    "minor channel outside its minor line",
			version: candidate,
			digest:  digest,
			state: withChannel(emptyState(candidate), Channel{Scope: ScopeMinor, Tag: "1.2"}, TagState{
				Present:    true,
				Digest:     other,
				HasVersion: true,
				Version:    otherMinor,
			}),
			wantErr: ErrChannelCorrupt,
		},
		{
			name:    "major channel outside its major line",
			version: candidate,
			digest:  digest,
			state: withChannel(emptyState(candidate), Channel{Scope: ScopeMajor, Tag: "1"}, TagState{
				Present:    true,
				Digest:     other,
				HasVersion: true,
				Version:    otherMajor,
			}),
			wantErr: ErrChannelCorrupt,
		},
		{
			name:    "latest may cross major lines",
			version: candidate,
			digest:  digest,
			state: withChannel(emptyState(candidate), Channel{Scope: ScopeLatest, Tag: "latest"}, TagState{
				Present:    true,
				Digest:     other,
				HasVersion: true,
				Version:    olderMajor,
			}),
			want: []Decision{
				{Tag: "1.2.3", Scope: ScopeExact, Action: ActionCreate},
				{Tag: "1.2", Scope: ScopeMinor, Action: ActionCreate},
				{Tag: "1", Scope: ScopeMajor, Action: ActionCreate},
				{Tag: "latest", Scope: ScopeLatest, Action: ActionCreate},
			},
			wantApply: []Tag{"1.2.3", "1.2", "1", "latest"},
		},
		{
			name:    "channel missing from state map",
			version: candidate,
			digest:  digest,
			state: ChannelState{
				Channels: map[Channel]TagState{
					{Scope: ScopeMinor, Tag: "1.2"}: {Present: false},
					{Scope: ScopeMajor, Tag: "1"}:   {Present: false},
				},
			},
			wantErr: ErrStateIncomplete,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := PlanTags(test.version, test.digest, test.state)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.version, got.Version)
			assert.Equal(t, test.digest, got.Digest)
			assert.Equal(t, test.want, got.Decisions)
			assert.Equal(t, test.wantApply, got.Apply())
		})
	}
}

func TestPlanTagsRejectsEmptyDigest(t *testing.T) {
	t.Parallel()

	_, err := PlanTags(Version{Major: 1, Minor: 2, Patch: 3}, "", emptyState(Version{Major: 1, Minor: 2, Patch: 3}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest is empty")
}

func TestTagPlanApplySelectsCreateDecisions(t *testing.T) {
	t.Parallel()

	plan := TagPlan{
		Decisions: []Decision{
			{Tag: "1.2.3", Scope: ScopeExact, Action: ActionCreate},
			{Tag: "1.2", Scope: ScopeMinor, Action: ActionAccept},
			{Tag: "1", Scope: ScopeMajor, Action: ActionRetain},
			{Tag: "latest", Scope: ScopeLatest, Action: ActionCreate},
		},
	}

	assert.Equal(t, []Tag{"1.2.3", "latest"}, plan.Apply())
}

// mustDigest parses a digest or fails the test.
func mustDigest(t *testing.T, value string) Digest {
	t.Helper()

	digest, err := ParseDigest(value)
	require.NoError(t, err)

	return digest
}

// emptyState is absent exact and channel tags for v.
func emptyState(v Version) ChannelState {
	channels := make(map[Channel]TagState, channelCount)
	for _, channel := range ChannelsFor(v) {
		channels[channel] = TagState{}
	}

	return ChannelState{Channels: channels}
}

// withExact returns state with a replaced exact tag observation.
func withExact(state ChannelState, exact TagState) ChannelState {
	state.Exact = exact

	return state
}

// withChannel returns state with one replaced channel observation.
func withChannel(state ChannelState, channel Channel, observed TagState) ChannelState {
	state.Channels[channel] = observed

	return state
}

// annotatedState points every channel at version on digest.
func annotatedState(v Version, digest Digest, version Version) ChannelState {
	state := emptyState(v)
	for _, channel := range ChannelsFor(v) {
		state.Channels[channel] = TagState{
			Present:    true,
			Digest:     digest,
			HasVersion: true,
			Version:    version,
		}
	}

	return state
}
