package puboci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/meigma/release/internal/rel"
)

const (
	// PrepareSchema is the versioned OCI prepare-result identifier.
	PrepareSchema = "release.dev/oci-prepare/v1"
	// exactObservationCount is the exact-version tag that precedes channel observations.
	exactObservationCount = 1
)

// AttestationSubject is one platform manifest that later attestation names.
type AttestationSubject struct {
	// Platform is the os/architecture pair, such as linux/amd64.
	Platform string `json:"platform"`
	// Digest is the platform manifest digest.
	Digest string `json:"digest"`
}

// TagObservation is the registry state of one exact or channel tag.
type TagObservation struct {
	// Tag is the registry tag.
	Tag string `json:"tag"`
	// Scope is exact, minor, major, or latest.
	Scope string `json:"scope"`
	// Present reports whether the tag currently resolves.
	Present bool `json:"present"`
	// Digest is the resolved digest. It is omitted when the tag is absent.
	Digest string `json:"digest,omitempty"`
	// Version is the annotated version. It is omitted when none was read.
	Version string `json:"version,omitempty"`
}

// OCIPrepareResult is the versioned document produced by publish oci prepare.
type OCIPrepareResult struct {
	// Schema identifies the prepare-result version and is always [PrepareSchema].
	Schema string `json:"schema"`
	// Authoritative is false for --dry-run and true after a real prepare.
	Authoritative bool `json:"authoritative"`
	// Image is the untagged repository name.
	Image string `json:"image"`
	// Version is the candidate MAJOR.MINOR.PATCH version.
	Version string `json:"version"`
	// IndexDigest is the image index digest.
	IndexDigest string `json:"index_digest"`
	// Platforms are the layout's platform manifests in layout order.
	Platforms []AttestationSubject `json:"platforms"`
	// Observed is the exact tag, then minor, major, and latest.
	Observed []TagObservation `json:"observed"`
}

// NewPrepareResult renders a prepare document from domain values.
//
// Platforms stay in the order supplied by the layout. Observed is the exact
// tag followed by each channel from [rel.ChannelsFor]. A channel missing
// from state.Channels is recorded as absent.
func NewPrepareResult(
	image Image,
	version rel.Version,
	index rel.Digest,
	platforms []PlatformImage,
	state rel.ChannelState,
	authoritative bool,
) OCIPrepareResult {
	subjects := make([]AttestationSubject, 0, len(platforms))
	for _, platform := range platforms {
		subjects = append(subjects, AttestationSubject{
			Platform: platform.Platform.String(),
			Digest:   platform.Descriptor.Digest.String(),
		})
	}

	channels := rel.ChannelsFor(version)
	observed := make([]TagObservation, 0, exactObservationCount+len(channels))
	observed = append(observed, observeTag(version.Tag().String(), rel.ScopeExact, state.Exact))
	for _, channel := range channels {
		observed = append(observed, observeTag(channel.Tag.String(), channel.Scope, state.Channels[channel]))
	}

	return OCIPrepareResult{
		Schema:        PrepareSchema,
		Authoritative: authoritative,
		Image:         image.String(),
		Version:       version.String(),
		IndexDigest:   index.String(),
		Platforms:     subjects,
		Observed:      observed,
	}
}

// ParsePrepareResult decodes one prepare document from r and validates it.
//
// Decoding rejects unknown fields. Documents are limited to [jsonLimitBytes].
func ParsePrepareResult(r io.Reader) (OCIPrepareResult, error) {
	if r == nil {
		return OCIPrepareResult{}, errors.New("reader is nil")
	}

	decoder := json.NewDecoder(io.LimitReader(r, jsonLimitBytes))
	decoder.DisallowUnknownFields()

	var result OCIPrepareResult
	if err := decoder.Decode(&result); err != nil {
		return OCIPrepareResult{}, fmt.Errorf("prepare result: %w", err)
	}
	if err := result.Validate(); err != nil {
		return OCIPrepareResult{}, err
	}

	return result, nil
}

// Validate reports whether r is a well-formed prepare document.
//
// It rejects a schema other than [PrepareSchema], an empty image, an
// unparsable version or index digest, an empty platform list, a platform
// subject with an empty platform or an unparsable digest, and an absent
// tag observation that still carries a digest.
func (r OCIPrepareResult) Validate() error {
	if r.Schema != PrepareSchema {
		return fmt.Errorf("prepare result schema %q is unsupported", r.Schema)
	}
	if r.Image == "" {
		return errors.New("prepare result image is empty")
	}
	if _, err := rel.ParseVersion(r.Version); err != nil {
		return fmt.Errorf("prepare result version: %w", err)
	}
	if _, err := rel.ParseDigest(r.IndexDigest); err != nil {
		return fmt.Errorf("prepare result index digest: %w", err)
	}
	if len(r.Platforms) == 0 {
		return errors.New("prepare result has no platforms")
	}
	for i, platform := range r.Platforms {
		if platform.Platform == "" {
			return fmt.Errorf("prepare result platforms[%d] platform is empty", i)
		}
		if _, err := rel.ParseDigest(platform.Digest); err != nil {
			return fmt.Errorf("prepare result platforms[%d] digest: %w", i, err)
		}
	}
	for i, observation := range r.Observed {
		if !observation.Present && observation.Digest != "" {
			return fmt.Errorf("prepare result observed[%d] is absent but has digest %q", i, observation.Digest)
		}
	}

	return nil
}

// observeTag renders one tag's observed registry state.
func observeTag(tag string, scope rel.Scope, state rel.TagState) TagObservation {
	observation := TagObservation{
		Tag:     tag,
		Scope:   string(scope),
		Present: state.Present,
	}
	if state.Present {
		observation.Digest = state.Digest.String()
	}
	if state.HasVersion {
		observation.Version = state.Version.String()
	}

	return observation
}
