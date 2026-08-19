package puboci_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

func TestDigestRefString(t *testing.T) {
	t.Parallel()

	image := mustImage(t)
	digest := mustDigest(t, validDigest)
	ref := image.Pin(digest)

	assert.Equal(t, image, ref.Image)
	assert.Equal(t, digest, ref.Digest)
	assert.Equal(t, "ghcr.io/owner/repo@"+validDigest, ref.String())
}

func TestImagePin(t *testing.T) {
	t.Parallel()

	image := mustImage(t)
	digest := mustDigest(t, validDigest)

	assert.Equal(t, puboci.DigestRef{Image: image, Digest: digest}, image.Pin(digest))
}

func TestDescriptorValidate(t *testing.T) {
	t.Parallel()

	valid := puboci.Descriptor{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    mustDigest(t, validDigest),
		Size:      0,
	}

	tests := []struct {
		name    string
		desc    puboci.Descriptor
		wantErr string
	}{
		{
			name: "valid zero size",
			desc: valid,
		},
		{
			name: "empty media type",
			desc: puboci.Descriptor{
				Digest: valid.Digest,
				Size:   1,
			},
			wantErr: "descriptor media type is empty",
		},
		{
			name: "empty digest",
			desc: puboci.Descriptor{
				MediaType: valid.MediaType,
				Size:      1,
			},
			wantErr: "descriptor digest:",
		},
		{
			name: "malformed digest",
			desc: puboci.Descriptor{
				MediaType: valid.MediaType,
				Digest:    rel.Digest("sha256:abcd"),
				Size:      1,
			},
			wantErr: "descriptor digest:",
		},
		{
			name: "negative size",
			desc: puboci.Descriptor{
				MediaType: valid.MediaType,
				Digest:    valid.Digest,
				Size:      -1,
			},
			wantErr: "descriptor size is negative",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.desc.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}
