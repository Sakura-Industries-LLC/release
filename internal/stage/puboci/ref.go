package puboci

import (
	"errors"
	"fmt"

	"github.com/meigma/release/internal/rel"
)

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	// MediaType is the OCI media type of the referenced content.
	MediaType string
	// Digest is the content digest.
	Digest rel.Digest
	// Size is the content length in bytes.
	Size int64
}

// DigestRef is an image pinned to a content digest.
type DigestRef struct {
	// Image is the untagged repository name.
	Image Image
	// Digest is the pinned content digest.
	Digest rel.Digest
}

// Validate reports whether d has a media type, a parsable digest, and a non-negative size.
func (d Descriptor) Validate() error {
	if d.MediaType == "" {
		return errors.New("descriptor media type is empty")
	}
	if _, err := rel.ParseDigest(d.Digest.String()); err != nil {
		return fmt.Errorf("descriptor digest: %w", err)
	}
	if d.Size < 0 {
		return errors.New("descriptor size is negative")
	}

	return nil
}

// Pin binds i to digest.
func (i Image) Pin(digest rel.Digest) DigestRef {
	return DigestRef{Image: i, Digest: digest}
}

// String returns image@digest.
func (r DigestRef) String() string {
	return r.Image.String() + "@" + r.Digest.String()
}
