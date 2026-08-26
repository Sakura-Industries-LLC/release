package stage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/meigma/release/internal/rel"
)

const (
	// ImageInputsName is the projection filename written into the dist directory.
	ImageInputsName = "oci-build-inputs.json"
	// ImageInputsSchema is the versioned projection identifier.
	ImageInputsSchema = "release.dev/oci-build-inputs/v2"

	// bytesPerKiB is the number of bytes in a kibibyte.
	bytesPerKiB = 1024
	// kibibytesPerMiB is the number of kibibytes in a mebibyte.
	kibibytesPerMiB = 1024
	// jsonLimitMiB is the projection document size bound in mebibytes.
	jsonLimitMiB = 4
	// jsonLimitBytes is the maximum encoded projection this package decodes.
	jsonLimitBytes int64 = jsonLimitMiB * bytesPerKiB * kibibytesPerMiB
	// jsonLimitReadBytes is one past [jsonLimitBytes] so an oversize
	// document can be distinguished from a document that fills the bound.
	jsonLimitReadBytes = jsonLimitBytes + 1

	// platformAMD64 is the linux/amd64 projection platform.
	platformAMD64 = "linux/amd64"
	// platformARM64 is the linux/arm64 projection platform.
	platformARM64 = "linux/arm64"
	// digestPrefix is the canonical SHA-256 algorithm prefix on projection digests.
	digestPrefix = "sha256:"
)

// ImageInputs is the neutral projection of staged facts an image build needs.
type ImageInputs struct {
	// Schema identifies the projection version and is always [ImageInputsSchema].
	Schema string `json:"schema"`
	// Profile is the release profile that produced the staged binaries.
	Profile string `json:"profile"`
	// Binaries are the canonical Linux binary facts, sorted platform-major
	// then name-ascending.
	Binaries []ImageInputBinary `json:"binaries"`
}

// ImageInputBinary is one canonical Linux binary fact.
type ImageInputBinary struct {
	// Platform is the os/architecture pair, either linux/amd64 or linux/arm64.
	Platform string `json:"platform"`
	// Name is the binary filename for this platform.
	Name string `json:"name"`
	// Path is the artifact-root-relative confined path.
	Path string `json:"path"`
	// Digest is the canonical sha256:<hex> digest of the staged binary.
	Digest string `json:"digest"`
}

// NewImageInputs builds a projection from the profile name and staged binaries.
//
// Entries are sorted platform-major (linux/amd64, then linux/arm64), then
// name-ascending, before validation.
func NewImageInputs(profile string, report Report) (ImageInputs, error) {
	binaries := make([]ImageInputBinary, 0, len(report.Binaries))
	for _, binary := range report.Binaries {
		binaries = append(binaries, ImageInputBinary{
			Platform: "linux/" + binary.Arch,
			Name:     binary.Name,
			Path:     binary.RelativePath,
			Digest:   digestPrefix + binary.Digest.String(),
		})
	}
	slices.SortFunc(binaries, compareImageInputBinary)

	inputs := ImageInputs{
		Schema:   ImageInputsSchema,
		Profile:  profile,
		Binaries: binaries,
	}
	if err := inputs.Validate(); err != nil {
		return ImageInputs{}, err
	}

	return inputs, nil
}

// EncodeImageInputs writes compact JSON plus a trailing newline.
func EncodeImageInputs(w io.Writer, inputs ImageInputs) error {
	if w == nil {
		return errors.New("writer is nil")
	}
	if err := inputs.Validate(); err != nil {
		return err
	}

	encoder := json.NewEncoder(w)
	if err := encoder.Encode(inputs); err != nil {
		return fmt.Errorf("encode oci-build-inputs: %w", err)
	}

	return nil
}

// DecodeImageInputs decodes one projection document from r and validates it.
//
// Decoding rejects unknown fields. The document must contain no trailing
// content. Input is bounded to [jsonLimitBytes].
func DecodeImageInputs(r io.Reader) (ImageInputs, error) {
	if r == nil {
		return ImageInputs{}, errors.New("reader is nil")
	}

	limited := &io.LimitedReader{R: r, N: jsonLimitReadBytes}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	var inputs ImageInputs
	err := decoder.Decode(&inputs)
	if limited.N == 0 {
		return ImageInputs{}, fmt.Errorf("oci-build-inputs exceeds the %d MiB JSON limit", jsonLimitMiB)
	}
	if err != nil {
		return ImageInputs{}, fmt.Errorf("oci-build-inputs: %w", err)
	}
	if decoder.More() {
		return ImageInputs{}, errors.New("oci-build-inputs has trailing content")
	}
	if err := inputs.Validate(); err != nil {
		return ImageInputs{}, err
	}

	return inputs, nil
}

// Validate reports whether i is a well-formed projection document.
//
// It is lexical only: it rejects a schema other than [ImageInputsSchema], an
// empty profile, an empty binary list, a platform other than linux/amd64 or
// linux/arm64, a duplicate (platform, name) pair, a name present on only one
// architecture, an empty name, a name containing a path separator, a path
// that is not [filepath.IsLocal], and a digest that [rel.ParseDigest] rejects.
func (i ImageInputs) Validate() error {
	if i.Schema != ImageInputsSchema {
		return fmt.Errorf("oci-build-inputs schema %q is unsupported", i.Schema)
	}
	if i.Profile == "" {
		return errors.New("oci-build-inputs profile is empty")
	}
	if len(i.Binaries) == 0 {
		return errors.New("oci-build-inputs binaries is empty")
	}

	seen := make(map[string]int, len(i.Binaries))
	namesByPlatform := map[string]map[string]struct{}{
		platformAMD64: {},
		platformARM64: {},
	}
	for index, binary := range i.Binaries {
		if err := validateImageInputBinary(index, binary); err != nil {
			return err
		}
		key := binary.Platform + "\x00" + binary.Name
		if previous, exists := seen[key]; exists {
			return fmt.Errorf(
				"oci-build-inputs binaries[%d] duplicates platform %q name %q from binaries[%d]",
				index,
				binary.Platform,
				binary.Name,
				previous,
			)
		}
		seen[key] = index
		namesByPlatform[binary.Platform][binary.Name] = struct{}{}
	}

	return requireMatchingImageInputNames(namesByPlatform)
}

// validateImageInputBinary reports lexical problems on one projection binary.
func validateImageInputBinary(index int, binary ImageInputBinary) error {
	switch binary.Platform {
	case platformAMD64, platformARM64:
	default:
		return fmt.Errorf(
			"oci-build-inputs binaries[%d] platform %q is not linux/amd64 or linux/arm64",
			index,
			binary.Platform,
		)
	}
	if binary.Name == "" {
		return fmt.Errorf("oci-build-inputs binaries[%d] name is empty", index)
	}
	if strings.ContainsAny(binary.Name, `/\`) {
		return fmt.Errorf("oci-build-inputs binaries[%d] name %q contains a path separator", index, binary.Name)
	}
	if !filepath.IsLocal(binary.Path) {
		return fmt.Errorf("oci-build-inputs binaries[%d] path %q is not confined", index, binary.Path)
	}
	if _, err := rel.ParseDigest(binary.Digest); err != nil {
		return fmt.Errorf("oci-build-inputs binaries[%d] digest: %w", index, err)
	}

	return nil
}

// requireMatchingImageInputNames requires every name on both architectures.
func requireMatchingImageInputNames(namesByPlatform map[string]map[string]struct{}) error {
	union := make(map[string]struct{})
	for _, names := range namesByPlatform {
		for name := range names {
			union[name] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(union))
	for name := range union {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)

	for _, platform := range []string{platformAMD64, platformARM64} {
		have := namesByPlatform[platform]
		for _, name := range ordered {
			if _, ok := have[name]; !ok {
				return fmt.Errorf("oci-build-inputs is missing %s binary %q", platform, name)
			}
		}
	}

	return nil
}

// compareImageInputBinary orders entries platform-major, then name-ascending.
func compareImageInputBinary(a, b ImageInputBinary) int {
	if a.Platform != b.Platform {
		return strings.Compare(a.Platform, b.Platform)
	}

	return strings.Compare(a.Name, b.Name)
}
