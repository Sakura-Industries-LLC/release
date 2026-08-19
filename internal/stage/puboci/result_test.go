package puboci_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

func TestNewPrepareResult(t *testing.T) {
	t.Parallel()

	fixture := newPrepareFixture(t)
	got := puboci.NewPrepareResult(
		fixture.image,
		fixture.version,
		fixture.index,
		fixture.platforms,
		fixture.state,
		false,
	)

	assert.Equal(t, puboci.OCIPrepareResult{
		Schema:        puboci.PrepareSchema,
		Authoritative: false,
		Image:         "ghcr.io/owner/repo",
		Version:       "1.2.3",
		IndexDigest:   validDigest,
		Platforms: []puboci.AttestationSubject{
			{Platform: "linux/amd64", Digest: validDigest},
			{Platform: "linux/arm64", Digest: otherDigest},
		},
		Observed: []puboci.TagObservation{
			{Tag: "1.2.3", Scope: string(rel.ScopeExact), Present: false},
			{
				Tag:     "1.2",
				Scope:   string(rel.ScopeMinor),
				Present: true,
				Digest:  otherDigest,
				Version: "1.2.2",
			},
			{Tag: "1", Scope: string(rel.ScopeMajor), Present: false},
			{Tag: "latest", Scope: string(rel.ScopeLatest), Present: false},
		},
	}, got)
}

func TestParsePrepareResultRoundTrip(t *testing.T) {
	t.Parallel()

	fixture := newPrepareFixture(t)
	original := puboci.NewPrepareResult(
		fixture.image,
		fixture.version,
		fixture.index,
		fixture.platforms,
		fixture.state,
		false,
	)
	payload, err := json.Marshal(original)
	require.NoError(t, err)

	got, err := puboci.ParsePrepareResult(bytes.NewReader(payload))
	require.NoError(t, err)
	assert.Equal(t, original, got)
	assert.False(t, got.Authoritative)
}

func TestParsePrepareResultRejectsUnknownField(t *testing.T) {
	t.Parallel()

	payload := `{
		"schema":"` + puboci.PrepareSchema + `",
		"authoritative":true,
		"image":"ghcr.io/owner/repo",
		"version":"1.2.3",
		"index_digest":"` + validDigest + `",
		"platforms":[{"platform":"linux/amd64","digest":"` + validDigest + `"}],
		"observed":[],
		"extra":true
	}`

	_, err := puboci.ParsePrepareResult(strings.NewReader(payload))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestParsePrepareResultRejectsOverLimit(t *testing.T) {
	t.Parallel()

	_, err := puboci.ParsePrepareResult(overLimitPrepareReader())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prepare result:")
	assert.NotContains(t, err.Error(), "unsupported")
}

func TestOCIPrepareResultValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult
		wantErr string
	}{
		{
			name: "valid",
		},
		{
			name: "wrong schema",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Schema = "release.dev/oci-prepare/v0"
				return result
			},
			wantErr: `prepare result schema "release.dev/oci-prepare/v0" is unsupported`,
		},
		{
			name: "empty image",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Image = ""
				return result
			},
			wantErr: "prepare result image is empty",
		},
		{
			name: "unparsable version",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Version = "v1.2.3"
				return result
			},
			wantErr: "prepare result version:",
		},
		{
			name: "unparsable index digest",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.IndexDigest = "sha256:abcd"
				return result
			},
			wantErr: "prepare result index digest:",
		},
		{
			name: "empty platforms",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Platforms = nil
				return result
			},
			wantErr: "prepare result has no platforms",
		},
		{
			name: "empty platform",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Platforms[0].Platform = ""
				return result
			},
			wantErr: "prepare result platforms[0] platform is empty",
		},
		{
			name: "bad platform digest",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Platforms[0].Digest = "sha256:abcd"
				return result
			},
			wantErr: "prepare result platforms[0] digest:",
		},
		{
			name: "absent observation with digest",
			mutate: func(result puboci.OCIPrepareResult) puboci.OCIPrepareResult {
				result.Observed[0].Present = false
				result.Observed[0].Digest = validDigest
				return result
			},
			wantErr: `prepare result observed[0] is absent but has digest "` + validDigest + `"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result := validPrepareResult(t)
			if test.mutate != nil {
				result = test.mutate(result)
			}

			err := result.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestParsePrepareResultValidates(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(puboci.OCIPrepareResult{
		Schema:      "release.dev/oci-prepare/v0",
		Image:       "ghcr.io/owner/repo",
		Version:     "1.2.3",
		IndexDigest: validDigest,
		Platforms: []puboci.AttestationSubject{
			{Platform: "linux/amd64", Digest: validDigest},
		},
	})
	require.NoError(t, err)

	_, err = puboci.ParsePrepareResult(bytes.NewReader(payload))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

// prepareFixture holds domain values for [puboci.NewPrepareResult].
type prepareFixture struct {
	// image is the repository being prepared.
	image puboci.Image
	// version is the candidate release version.
	version rel.Version
	// index is the image index digest.
	index rel.Digest
	// platforms are layout platforms in file order.
	platforms []puboci.PlatformImage
	// state has an absent exact tag, a versioned minor, and a missing major.
	state rel.ChannelState
}

// newPrepareFixture constructs a 1.2.3 prepare document input.
func newPrepareFixture(t *testing.T) prepareFixture {
	t.Helper()

	version := rel.Version{Major: 1, Minor: 2, Patch: 3}
	channels := rel.ChannelsFor(version)
	minor := channels[0]
	latest := channels[2]

	return prepareFixture{
		image:   mustImage(t),
		version: version,
		index:   mustDigest(t, validDigest),
		platforms: []puboci.PlatformImage{
			{
				Descriptor: puboci.Descriptor{Digest: mustDigest(t, validDigest)},
				Platform:   puboci.Platform{OS: "linux", Architecture: "amd64"},
			},
			{
				Descriptor: puboci.Descriptor{Digest: mustDigest(t, otherDigest)},
				Platform:   puboci.Platform{OS: "linux", Architecture: "arm64"},
			},
		},
		state: rel.ChannelState{
			Channels: map[rel.Channel]rel.TagState{
				minor: {
					Present:    true,
					Digest:     mustDigest(t, otherDigest),
					HasVersion: true,
					Version:    rel.Version{Major: 1, Minor: 2, Patch: 2},
				},
				latest: {},
			},
		},
	}
}

// validPrepareResult returns a document that passes [puboci.OCIPrepareResult.Validate].
func validPrepareResult(t *testing.T) puboci.OCIPrepareResult {
	t.Helper()

	fixture := newPrepareFixture(t)

	return puboci.NewPrepareResult(
		fixture.image,
		fixture.version,
		fixture.index,
		fixture.platforms,
		fixture.state,
		true,
	)
}

// overLimitPrepareReader streams a prepare document larger than the JSON bound.
func overLimitPrepareReader() io.Reader {
	prefix := `{"schema":"` + puboci.PrepareSchema + `","authoritative":false,"image":"`
	suffix := `","version":"1.2.3","index_digest":"` + validDigest +
		`","platforms":[{"platform":"linux/amd64","digest":"` + validDigest + `"}],"observed":[]}`

	return io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(repeatByteReader('a'), testOverJSONLimitBytes),
		strings.NewReader(suffix),
	)
}

// repeatByteReader yields an endless stream of one byte.
type repeatByteReader byte

// Read fills p with the repeated byte.
func (r repeatByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}

	return len(p), nil
}
