package stage_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage"
)

const (
	validImageDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherImageDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	testBytesPerKiB        = 1024
	testKibibytesPerMiB    = 1024
	testJSONLimitMiB       = 4
	testJSONLimitBytes     = testJSONLimitMiB * testBytesPerKiB * testKibibytesPerMiB
	testOverJSONLimitBytes = testJSONLimitBytes + 1
)

func TestEncodeDecodeImageInputsRoundTrip(t *testing.T) {
	t.Parallel()

	original := validImageInputs()
	var encoded bytes.Buffer
	require.NoError(t, stage.EncodeImageInputs(&encoded, original))
	assert.True(t, strings.HasSuffix(encoded.String(), "\n"))

	got, err := stage.DecodeImageInputs(&encoded)
	require.NoError(t, err)
	assert.Equal(t, original, got)
}

func TestNewImageInputs(t *testing.T) {
	t.Parallel()

	got, err := stage.NewImageInputs("go", stage.Report{
		Binaries: []stage.Binary{
			{
				Arch:         "amd64",
				Path:         "dist/release-cli_linux_amd64_v1/release-cli",
				RelativePath: "release-cli_linux_amd64_v1/release-cli",
				Name:         "release-cli",
				Digest:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
			{
				Arch:         "arm64",
				Path:         "dist/release-cli_linux_arm64_v8.0/release-cli",
				RelativePath: "release-cli_linux_arm64_v8.0/release-cli",
				Name:         "release-cli",
				Digest:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, validImageInputs(), got)
}

func TestNewImageInputsSortsPlatformMajorThenName(t *testing.T) {
	t.Parallel()

	got, err := stage.NewImageInputs("go", stage.Report{
		Binaries: []stage.Binary{
			{
				Arch:         "arm64",
				Path:         "dist/server_linux_arm64/incus-server",
				RelativePath: "server_linux_arm64/incus-server",
				Name:         "incus-server",
				Digest:       "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			},
			{
				Arch:         "amd64",
				Path:         "dist/server_linux_amd64/incus-server",
				RelativePath: "server_linux_amd64/incus-server",
				Name:         "incus-server",
				Digest:       "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
			{
				Arch:         "arm64",
				Path:         "dist/agent_linux_arm64/incus-agent",
				RelativePath: "agent_linux_arm64/incus-agent",
				Name:         "incus-agent",
				Digest:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
			{
				Arch:         "amd64",
				Path:         "dist/agent_linux_amd64/incus-agent",
				RelativePath: "agent_linux_amd64/incus-agent",
				Name:         "incus-agent",
				Digest:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []stage.ImageInputBinary{
		{
			Platform: "linux/amd64",
			Name:     "incus-agent",
			Path:     "agent_linux_amd64/incus-agent",
			Digest:   validImageDigest,
		},
		{
			Platform: "linux/amd64",
			Name:     "incus-server",
			Path:     "server_linux_amd64/incus-server",
			Digest:   "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
		{
			Platform: "linux/arm64",
			Name:     "incus-agent",
			Path:     "agent_linux_arm64/incus-agent",
			Digest:   otherImageDigest,
		},
		{
			Platform: "linux/arm64",
			Name:     "incus-server",
			Path:     "server_linux_arm64/incus-server",
			Digest:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		},
	}, got.Binaries)
}

func TestDecodeImageInputsRejectsUnknownField(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(validImageInputs())
	require.NoError(t, err)
	payload = append(payload[:len(payload)-1], []byte(`,"extra":true}`)...)

	_, err = stage.DecodeImageInputs(bytes.NewReader(payload))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestDecodeImageInputsRejectsOverLimit(t *testing.T) {
	t.Parallel()

	_, err := stage.DecodeImageInputs(overLimitImageInputsReader())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the 4 MiB JSON limit")
}

func TestDecodeImageInputsRejectsTrailingJSONValue(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(validImageInputs())
	require.NoError(t, err)

	_, err = stage.DecodeImageInputs(bytes.NewReader(append(payload, []byte(`{}`)...)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trailing content")
}

func TestDecodeImageInputsRejectsTrailingBytesPastBound(t *testing.T) {
	t.Parallel()

	payload := paddedImageInputsJSON(t, testJSONLimitBytes)
	stream := io.MultiReader(bytes.NewReader(payload), strings.NewReader("{}"))

	_, err := stage.DecodeImageInputs(stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trailing content")
}

func TestDecodeImageInputsAcceptsExactLimit(t *testing.T) {
	t.Parallel()

	payload := paddedImageInputsJSON(t, testJSONLimitBytes)
	got, err := stage.DecodeImageInputs(bytes.NewReader(payload))
	require.NoError(t, err)
	assert.Equal(t, stage.ImageInputsSchema, got.Schema)
	assert.Len(
		t,
		got.Profile,
		len(validImageInputs().Profile)+testJSONLimitBytes-len(mustMarshalImageInputs(t, validImageInputs())),
	)
}

func TestImageInputsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(inputs stage.ImageInputs) stage.ImageInputs
		wantErr string
	}{
		{
			name: "valid",
		},
		{
			name: "schema mismatch",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Schema = "release.dev/oci-build-inputs/v0"
				return inputs
			},
			wantErr: `oci-build-inputs schema "release.dev/oci-build-inputs/v0" is unsupported`,
		},
		{
			name: "empty binaries",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries = nil
				return inputs
			},
			wantErr: "oci-build-inputs binaries is empty",
		},
		{
			name: "duplicate platform and name",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries[1].Platform = "linux/amd64"
				return inputs
			},
			wantErr: `oci-build-inputs binaries[1] duplicates platform "linux/amd64" name "release-cli" from binaries[0]`,
		},
		{
			name: "unknown platform",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries[1].Platform = "linux/s390x"
				return inputs
			},
			wantErr: `oci-build-inputs binaries[1] platform "linux/s390x" is not linux/amd64 or linux/arm64`,
		},
		{
			name: "asymmetric name set",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries[1].Name = "other"
				return inputs
			},
			wantErr: `oci-build-inputs is missing linux/amd64 binary "other"`,
		},
		{
			name: "absolute path",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries[0].Path = "/etc/passwd"
				return inputs
			},
			wantErr: `oci-build-inputs binaries[0] path "/etc/passwd" is not confined`,
		},
		{
			name: "escaping path",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries[0].Path = "../secret"
				return inputs
			},
			wantErr: `oci-build-inputs binaries[0] path "../secret" is not confined`,
		},
		{
			name: "malformed digest",
			mutate: func(inputs stage.ImageInputs) stage.ImageInputs {
				inputs.Binaries[0].Digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				return inputs
			},
			wantErr: "oci-build-inputs binaries[0] digest:",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			inputs := validImageInputs()
			if test.mutate != nil {
				inputs = test.mutate(inputs)
			}

			err := inputs.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestDecodeImageInputsValidates(t *testing.T) {
	t.Parallel()

	inputs := validImageInputs()
	inputs.Schema = "release.dev/oci-build-inputs/v0"
	payload, err := json.Marshal(inputs)
	require.NoError(t, err)

	_, err = stage.DecodeImageInputs(bytes.NewReader(payload))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

// validImageInputs returns a document that passes [stage.ImageInputs.Validate].
func validImageInputs() stage.ImageInputs {
	return stage.ImageInputs{
		Schema:  stage.ImageInputsSchema,
		Profile: "go",
		Binaries: []stage.ImageInputBinary{
			{
				Platform: "linux/amd64",
				Name:     "release-cli",
				Path:     "release-cli_linux_amd64_v1/release-cli",
				Digest:   validImageDigest,
			},
			{
				Platform: "linux/arm64",
				Name:     "release-cli",
				Path:     "release-cli_linux_arm64_v8.0/release-cli",
				Digest:   otherImageDigest,
			},
		},
	}
}

// overLimitImageInputsReader streams a projection larger than the JSON bound.
func overLimitImageInputsReader() io.Reader {
	prefix := `{"schema":"` + stage.ImageInputsSchema + `","profile":"`
	suffix := `","binaries":[` +
		`{"platform":"linux/amd64","name":"release-cli","path":"a/b","digest":"` + validImageDigest + `"},` +
		`{"platform":"linux/arm64","name":"release-cli","path":"c/d","digest":"` + otherImageDigest + `"}]}`

	return io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(repeatByteReader('a'), testOverJSONLimitBytes),
		strings.NewReader(suffix),
	)
}

// paddedImageInputsJSON returns a valid projection whose encoded size is size.
func paddedImageInputsJSON(t *testing.T, size int) []byte {
	t.Helper()

	base := mustMarshalImageInputs(t, validImageInputs())
	require.Less(t, len(base), size)

	inputs := validImageInputs()
	inputs.Profile += strings.Repeat("a", size-len(base))
	payload := mustMarshalImageInputs(t, inputs)
	require.Len(t, payload, size)

	return payload
}

// mustMarshalImageInputs marshals inputs or fails the test.
func mustMarshalImageInputs(t *testing.T, inputs stage.ImageInputs) []byte {
	t.Helper()

	payload, err := json.Marshal(inputs)
	require.NoError(t, err)

	return payload
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
