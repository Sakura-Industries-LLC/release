package rel

import (
	"errors"
	"fmt"
	"strconv"
)

const (
	// maxTagLength is the OCI distribution-spec maximum tag length.
	maxTagLength = 128
	// channelCount is the number of moving channel tags [ChannelsFor] returns.
	channelCount = 3
	// decisionCount is the exact tag plus every channel from [ChannelsFor].
	decisionCount = 1 + channelCount
	// tagLatest is the moving latest channel tag.
	tagLatest Tag = "latest"
)

// Sentinel errors returned by [PlanTags].
var (
	// ErrImmutableTag reports that an exact version tag already points at
	// another digest.
	ErrImmutableTag = errors.New("immutable tag conflict")
	// ErrChannelCorrupt reports missing version annotation, an out-of-line
	// channel, or equal versions with different digests.
	ErrChannelCorrupt = errors.New("corrupt channel state")
	// ErrStateIncomplete reports that a required channel is missing from
	// [ChannelState.Channels].
	ErrStateIncomplete = errors.New("incomplete channel state")
)

// Tag is a validated OCI image tag.
//
// The only constructor is [ParseTag]. The zero value is invalid.
type Tag string

// ParseTag constructs a [Tag] from an OCI tag string.
//
// The grammar is [A-Za-z0-9_][A-Za-z0-9._-]{0,127}: the first character is
// alphanumeric or underscore, remaining characters may also be dot or hyphen,
// and the total length is 1 through 128.
func ParseTag(value string) (Tag, error) {
	if value == "" {
		return "", fmt.Errorf("tag %q is empty", value)
	}
	if len(value) > maxTagLength {
		return "", fmt.Errorf("tag %q has length %d, want at most %d", value, len(value), maxTagLength)
	}
	if !isTagStart(rune(value[0])) {
		return "", fmt.Errorf("tag %q has an invalid leading character", value)
	}
	for _, r := range value[1:] {
		if !isTagRest(r) {
			return "", fmt.Errorf("tag %q has an invalid character", value)
		}
	}

	return Tag(value), nil
}

// String returns the tag text.
func (t Tag) String() string {
	return string(t)
}

// Scope classifies a planned tag as exact or as a moving channel.
type Scope string

const (
	// ScopeExact is the immutable MAJOR.MINOR.PATCH tag.
	ScopeExact Scope = "exact"
	// ScopeMinor is the MAJOR.MINOR channel.
	ScopeMinor Scope = "minor"
	// ScopeMajor is the MAJOR channel.
	ScopeMajor Scope = "major"
	// ScopeLatest is the latest channel.
	ScopeLatest Scope = "latest"
)

// Channel is one moving tag together with its advancement rule.
type Channel struct {
	// Scope is the channel's advancement rule.
	Scope Scope
	// Tag is the registry tag for this channel.
	Tag Tag
}

// ChannelsFor returns the moving channels for v, in planning order.
//
// The result is always minor "MAJOR.MINOR", major "MAJOR", then latest
// "latest". Tags are formatted from decimal version components or the
// literal "latest", which cannot produce an invalid OCI tag.
func ChannelsFor(v Version) []Channel {
	return []Channel{
		{Scope: ScopeMinor, Tag: decimalTag(v.Major, v.Minor)},
		{Scope: ScopeMajor, Tag: decimalTag(v.Major)},
		{Scope: ScopeLatest, Tag: tagLatest},
	}
}

// TagState is the observed registry state of one tag.
type TagState struct {
	// Present reports whether the tag currently resolves.
	Present bool
	// Digest is the resolved digest. It is meaningful only when Present.
	Digest Digest
	// HasVersion reports whether a version annotation was read.
	HasVersion bool
	// Version is the annotated version. It is meaningful only when HasVersion.
	Version Version
}

// ChannelState is the observed state of the exact tag and every channel.
type ChannelState struct {
	// Exact is the observed state of the candidate's exact version tag.
	Exact TagState
	// Channels is the observed state of each moving channel. Every channel
	// from [ChannelsFor] must be present as a key.
	Channels map[Channel]TagState
}

// Action is the planned outcome for one tag.
type Action string

const (
	// ActionCreate means the tag must be applied to the candidate digest.
	ActionCreate Action = "create"
	// ActionAccept means the tag already resolves to the candidate digest.
	ActionAccept Action = "accept"
	// ActionRetain means the channel stays on a newer release.
	ActionRetain Action = "retain"
)

// Decision is the planned action for one tag.
type Decision struct {
	// Tag is the registry tag this decision applies to.
	Tag Tag
	// Scope is the tag's classification.
	Scope Scope
	// Action is the planned outcome.
	Action Action
}

// TagPlan is the complete set of tag decisions for one candidate.
type TagPlan struct {
	// Version is the candidate release version.
	Version Version
	// Digest is the candidate image digest.
	Digest Digest
	// Decisions are the exact tag and each channel, in planning order.
	Decisions []Decision
}

// Apply returns the tags that must be written, in decision order.
func (p TagPlan) Apply() []Tag {
	tags := make([]Tag, 0, len(p.Decisions))
	for _, decision := range p.Decisions {
		if decision.Action == ActionCreate {
			tags = append(tags, decision.Tag)
		}
	}

	return tags
}

// PlanTags decides which tags a candidate release may apply.
//
// The exact tag is decided first, then each channel from [ChannelsFor] in
// order. A zero digest is rejected. A channel missing from current.Channels
// is [ErrStateIncomplete]. An exact tag on another digest is
// [ErrImmutableTag]. A missing version annotation, an out-of-line channel,
// and equal versions with different digests are [ErrChannelCorrupt].
func PlanTags(v Version, digest Digest, current ChannelState) (TagPlan, error) {
	if digest == "" {
		return TagPlan{}, errors.New("digest is empty")
	}

	decisions := make([]Decision, 0, decisionCount)
	exact, err := planExact(v, digest, current.Exact)
	if err != nil {
		return TagPlan{}, err
	}
	decisions = append(decisions, exact)

	for _, channel := range ChannelsFor(v) {
		state, ok := current.Channels[channel]
		if !ok {
			return TagPlan{}, fmt.Errorf("channel %s is missing: %w", channel.Tag, ErrStateIncomplete)
		}
		decision, err := planChannel(v, digest, channel, state)
		if err != nil {
			return TagPlan{}, err
		}
		decisions = append(decisions, decision)
	}

	return TagPlan{Version: v, Digest: digest, Decisions: decisions}, nil
}

// planExact decides the immutable exact-version tag.
func planExact(v Version, digest Digest, state TagState) (Decision, error) {
	tag := v.Tag()
	if !state.Present {
		return Decision{Tag: tag, Scope: ScopeExact, Action: ActionCreate}, nil
	}
	if state.Digest == digest {
		return Decision{Tag: tag, Scope: ScopeExact, Action: ActionAccept}, nil
	}

	return Decision{}, fmt.Errorf(
		"immutable tag %s resolves to %s; expected %s: %w",
		tag,
		state.Digest,
		digest,
		ErrImmutableTag,
	)
}

// planChannel decides one moving channel tag.
func planChannel(v Version, digest Digest, channel Channel, state TagState) (Decision, error) {
	if !state.Present {
		return Decision{Tag: channel.Tag, Scope: channel.Scope, Action: ActionCreate}, nil
	}
	if state.Digest == digest {
		return Decision{Tag: channel.Tag, Scope: channel.Scope, Action: ActionAccept}, nil
	}
	if !state.HasVersion {
		return Decision{}, fmt.Errorf(
			"channel %s resolves to %s with no version annotation; expected %s: %w",
			channel.Tag,
			state.Digest,
			digest,
			ErrChannelCorrupt,
		)
	}
	if err := checkChannelLine(v, digest, channel, state); err != nil {
		return Decision{}, err
	}

	switch comparison := v.Compare(state.Version); {
	case comparison > 0:
		return Decision{Tag: channel.Tag, Scope: channel.Scope, Action: ActionCreate}, nil
	case comparison < 0:
		return Decision{Tag: channel.Tag, Scope: channel.Scope, Action: ActionRetain}, nil
	default:
		return Decision{}, fmt.Errorf(
			"channel %s has version %s but resolves to %s; expected %s: %w",
			channel.Tag,
			state.Version,
			state.Digest,
			digest,
			ErrChannelCorrupt,
		)
	}
}

// checkChannelLine rejects a channel that points outside its release line.
func checkChannelLine(candidate Version, digest Digest, channel Channel, state TagState) error {
	switch channel.Scope {
	case ScopeMinor:
		if state.Version.Major != candidate.Major || state.Version.Minor != candidate.Minor {
			return fmt.Errorf(
				"channel %s points outside its minor release line: %s resolves to %s; expected %s: %w",
				channel.Tag,
				state.Version,
				state.Digest,
				digest,
				ErrChannelCorrupt,
			)
		}
	case ScopeMajor:
		if state.Version.Major != candidate.Major {
			return fmt.Errorf(
				"channel %s points outside its major release line: %s resolves to %s; expected %s: %w",
				channel.Tag,
				state.Version,
				state.Digest,
				digest,
				ErrChannelCorrupt,
			)
		}
	case ScopeLatest, ScopeExact:
	}

	return nil
}

// decimalTag formats unsigned integers as a dotted OCI tag.
//
// Decimal digits always start with [0-9] and remaining characters are digits
// or dots, so [ParseTag] cannot fail.
func decimalTag(parts ...uint64) Tag {
	tag := Tag(strconv.FormatUint(parts[0], 10))
	for _, part := range parts[1:] {
		tag += Tag("." + strconv.FormatUint(part, 10))
	}

	return tag
}

// isTagStart reports whether r may begin an OCI tag.
func isTagStart(r rune) bool {
	return isASCIILetter(r) || isASCIIDigit(r) || r == '_'
}

// isTagRest reports whether r may appear after the first OCI tag character.
func isTagRest(r rune) bool {
	return isTagStart(r) || r == '.' || r == '-'
}

// isASCIILetter reports whether r is an ASCII letter.
func isASCIILetter(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

// isASCIIDigit reports whether r is an ASCII decimal digit.
func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
