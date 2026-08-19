package puboci

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/meigma/release/internal/rel"
)

const (
	// minImageParts is host plus at least one path element.
	minImageParts = 2
	// schemeMarker separates a URL scheme from the rest of a reference.
	schemeMarker = "://"
)

// Sentinel errors classified for registry reads.
var (
	// ErrTagAbsent reports that a tag does not resolve in the registry.
	ErrTagAbsent = errors.New("registry tag not found")
	// ErrRetryable reports a transient registry failure. [CollectState] does
	// not retry; the adapter classifies the failure and the caller decides
	// whether to try again.
	ErrRetryable = errors.New("retryable registry error")
	// ErrCorruptState reports that a present tag's version annotation is
	// missing or not a stable version.
	ErrCorruptState = errors.New("corrupt registry state")
)

// Image is a lowercase untagged registry repository name.
//
// The only constructor is [ParseImage]. The zero value is invalid.
type Image string

// ParseImage constructs an [Image] from a lowercase registry reference.
//
// The grammar is host/path[/path...] with no scheme, tag, or digest. The host
// is the text before the first slash and may include a port. At least one path
// element is required. Empty input, uppercase letters, a scheme, a :tag suffix
// on the last element, an @digest, leading or trailing slashes, empty path
// elements, and spaces are rejected.
func ParseImage(value string) (Image, error) {
	if value == "" {
		return "", fmt.Errorf("image %q is empty", value)
	}
	if strings.ContainsFunc(value, unicode.IsSpace) {
		return "", fmt.Errorf("image %q contains a space", value)
	}
	if value != strings.ToLower(value) {
		return "", fmt.Errorf("image %q contains an uppercase letter", value)
	}
	if strings.Contains(value, schemeMarker) {
		return "", fmt.Errorf("image %q has a scheme", value)
	}
	if strings.Contains(value, "@") {
		return "", fmt.Errorf("image %q has a digest", value)
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("image %q has a leading slash", value)
	}
	if strings.HasSuffix(value, "/") {
		return "", fmt.Errorf("image %q has a trailing slash", value)
	}

	parts := strings.Split(value, "/")
	if len(parts) < minImageParts {
		return "", fmt.Errorf("image %q has no path", value)
	}
	if slices.Contains(parts, "") {
		return "", fmt.Errorf("image %q has an empty path element", value)
	}
	if strings.Contains(parts[len(parts)-1], ":") {
		return "", fmt.Errorf("image %q has a tag", value)
	}

	return Image(value), nil
}

// String returns the untagged repository name.
func (i Image) String() string {
	return string(i)
}

// Reference binds i to tag.
func (i Image) Reference(tag rel.Tag) Reference {
	return Reference{Image: i, Tag: tag}
}

// Reference is an image together with one tag.
type Reference struct {
	// Image is the untagged repository name.
	Image Image
	// Tag is the exact or channel tag.
	Tag rel.Tag
}

// String returns image:tag.
func (r Reference) String() string {
	return r.Image.String() + ":" + r.Tag.String()
}

// StateReader reads current registry tag state.
//
// Implementations classify not-found as [ErrTagAbsent], transient failures as
// [ErrRetryable], and unusable version annotations as [ErrCorruptState].
type StateReader interface {
	// Resolve returns the digest currently pointed at by ref.
	//
	// An error wrapping [ErrTagAbsent] means the tag is not present.
	Resolve(ctx context.Context, ref Reference) (rel.Digest, error)

	// Version returns the org.opencontainers.image.version annotation at ref.
	//
	// Callers invoke Version only when ref is present and resolves to a
	// digest other than the candidate. An error wrapping [ErrCorruptState]
	// means the annotation is missing or not a stable version.
	Version(ctx context.Context, ref Reference) (rel.Version, error)
}

// CollectState reads the exact tag and every moving channel for version.
//
// A nil context or reader is rejected. A cancelled context fails before the
// port is called. [ErrTagAbsent] from [StateReader.Resolve] is treated as an
// absent tag. Any other Resolve or Version error is wrapped with the
// reference and returned. Version is called only when a channel tag is present
// and its digest differs from digest, so a present differing tag never returns
// with HasVersion false. Transient failures classified as [ErrRetryable] are
// not retried. CollectState performs no registry writes.
func CollectState(
	ctx context.Context,
	reader StateReader,
	image Image,
	version rel.Version,
	digest rel.Digest,
) (rel.ChannelState, error) {
	if ctx == nil {
		return rel.ChannelState{}, errors.New("context is nil")
	}
	if reader == nil {
		return rel.ChannelState{}, errors.New("state reader is nil")
	}
	if err := ctx.Err(); err != nil {
		return rel.ChannelState{}, fmt.Errorf("collect state: %w", err)
	}

	exact, err := resolveState(ctx, reader, image.Reference(version.Tag()))
	if err != nil {
		return rel.ChannelState{}, err
	}

	channels := rel.ChannelsFor(version)
	observed := make(map[rel.Channel]rel.TagState, len(channels))
	for _, channel := range channels {
		ref := image.Reference(channel.Tag)
		state, err := resolveState(ctx, reader, ref)
		if err != nil {
			return rel.ChannelState{}, err
		}
		if state.Present && state.Digest != digest {
			annotated, err := reader.Version(ctx, ref)
			if err != nil {
				return rel.ChannelState{}, fmt.Errorf("version %s: %w", ref, err)
			}
			state.HasVersion = true
			state.Version = annotated
		}
		observed[channel] = state
	}

	return rel.ChannelState{Exact: exact, Channels: observed}, nil
}

// PlanTags collects registry state and decides which tags the candidate may apply.
//
// It is [CollectState] followed by [rel.PlanTags]. A planner failure is
// returned unchanged so callers can inspect the [rel] sentinels. PlanTags
// performs no registry writes and does not retry transient failures.
func PlanTags(
	ctx context.Context,
	reader StateReader,
	image Image,
	version rel.Version,
	digest rel.Digest,
) (rel.TagPlan, error) {
	current, err := CollectState(ctx, reader, image, version, digest)
	if err != nil {
		return rel.TagPlan{}, err
	}

	return rel.PlanTags(version, digest, current)
}

// resolveState reads one tag and treats [ErrTagAbsent] as not present.
func resolveState(ctx context.Context, reader StateReader, ref Reference) (rel.TagState, error) {
	resolved, err := reader.Resolve(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrTagAbsent) {
			return rel.TagState{}, nil
		}

		return rel.TagState{}, fmt.Errorf("resolve %s: %w", ref, err)
	}

	return rel.TagState{Present: true, Digest: resolved}, nil
}
