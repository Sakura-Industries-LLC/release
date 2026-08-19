package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	cosignmocks "github.com/meigma/release/internal/adapter/cosign/mocks"
	regmocks "github.com/meigma/release/internal/adapter/reg/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// prepareOCISchemaVersion is the OCI image-spec schemaVersion used in fixtures.
	prepareOCISchemaVersion = 2
	// prepareLayoutJSON is a valid oci-layout marker document.
	prepareLayoutJSON = `{"imageLayoutVersion":"1.0.0"}`
	// prepareAMD64Config is a distinct amd64 config blob.
	prepareAMD64Config = `{"architecture":"amd64","os":"linux"}`
	// prepareARM64Config is a distinct arm64 config blob.
	prepareARM64Config = `{"architecture":"arm64","os":"linux"}`
	// prepareAMD64Layer is a distinct amd64 layer blob.
	prepareAMD64Layer = "amd64-layer"
	// prepareARM64Layer is a distinct arm64 layer blob.
	prepareARM64Layer = "arm64-layer"
	// prepareCosignPath is the RELEASE_COSIGN_PATH fixture.
	prepareCosignPath = "/opt/cosign"
	// prepareCommand is the envelope command path for publish oci prepare.
	prepareCommand = "publish oci prepare"
)

func TestPublishOCIPrepareMissingValuesAreUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing layout",
			args: []string{
				"publish", "oci", "prepare",
				"--image", tagsImage,
				"--version", "1.2.3",
				"--digest", tagsDigest,
			},
			want: "--layout is required",
		},
		{
			name: "missing digest",
			args: []string{
				"publish", "oci", "prepare",
				"--layout", "/unused",
				"--image", tagsImage,
				"--version", "1.2.3",
			},
			want: "--digest is required",
		},
		{
			name: "malformed digest",
			args: []string{
				"publish", "oci", "prepare",
				"--layout", "/unused",
				"--image", tagsImage,
				"--version", "1.2.3",
				"--digest", "not-a-digest",
			},
			want: "digest",
		},
		{
			name: "malformed version",
			args: []string{
				"publish", "oci", "prepare",
				"--layout", "/unused",
				"--image", tagsImage,
				"--version", "v1.2.3",
				"--digest", tagsDigest,
			},
			want: "v prefix",
		},
		{
			name: "malformed image",
			args: []string{
				"publish", "oci", "prepare",
				"--layout", "/unused",
				"--image", "GHCR.IO/OWNER/REPO",
				"--version", "1.2.3",
				"--digest", tagsDigest,
			},
			want: "uppercase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executePrepareFactory(t, nil, tt.args, trackingPrepareFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Empty(t, stdout)
			assert.Contains(t, err.Error(), tt.want)
			assert.False(t, called)
		})
	}
}

func TestPublishOCIPrepareJSONConfigFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing layout",
			args: []string{
				"publish", "oci", "prepare",
				"--json",
				"--image", tagsImage,
				"--version", "1.2.3",
				"--digest", tagsDigest,
			},
			want: "--layout is required",
		},
		{
			name: "missing digest",
			args: []string{
				"publish", "oci", "prepare",
				"--json",
				"--layout", "/unused",
				"--image", tagsImage,
				"--version", "1.2.3",
			},
			want: "--digest is required",
		},
		{
			name: "malformed digest",
			args: []string{
				"publish", "oci", "prepare",
				"--json",
				"--layout", "/unused",
				"--image", tagsImage,
				"--version", "1.2.3",
				"--digest", "not-a-digest",
			},
			want: "digest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executePrepareFactory(t, nil, tt.args, trackingPrepareFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.False(t, called)
			assertPrepareFailureEnvelope(t, stdout, tt.want)
		})
	}
}

func TestPublishOCIPrepareInvalidLayoutPath(t *testing.T) {
	t.Parallel()

	fileLayout := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(fileLayout, []byte("file"), 0o644))

	tests := []struct {
		name   string
		layout string
	}{
		{
			name:   "missing directory",
			layout: filepath.Join(t.TempDir(), "missing"),
		},
		{
			name:   "path is a file",
			layout: fileLayout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executePrepareFactory(t, nil, []string{
				"publish", "oci", "prepare",
				"--json",
				"--layout", tt.layout,
				"--image", tagsImage,
				"--version", "1.2.3",
				"--digest", tagsDigest,
			}, trackingPrepareFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 1, cli.ExitCode(err))
			assert.Contains(t, err.Error(), "open layout")
			assert.Contains(t, err.Error(), tt.layout)
			assert.False(t, called)
			assertPrepareFailureEnvelope(t, stdout, "open layout")
		})
	}
}

func TestPublishOCIPrepareDryRunJSON(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	readerCalled := false
	writerCalled := false

	stdout, err := executePrepareFactory(t, nil, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--dry-run",
		"--json",
	}, prepareFactories{
		newReader: func(cli.RegistryConfig) (puboci.StateReader, error) {
			readerCalled = true
			return absentReader(t), nil
		},
		newPusher: func(cli.RegistryConfig) (puboci.ContentPusher, error) {
			writerCalled = true
			return unusedPusher(t), nil
		},
		newSigner: func(string) (puboci.Signer, error) {
			writerCalled = true
			return unusedSigner(t), nil
		},
	})
	require.NoError(t, err)
	require.True(t, readerCalled)
	assert.False(t, writerCalled)

	result := decodePrepareResult(t, stdout)
	assert.False(t, result.Authoritative)
	assert.Equal(t, layout.Index.Digest.String(), result.IndexDigest)
	assert.Equal(t, []puboci.AttestationSubject{
		{Platform: "linux/amd64", Digest: layout.Platforms[0].Descriptor.Digest.String()},
		{Platform: "linux/arm64", Digest: layout.Platforms[1].Descriptor.Digest.String()},
	}, result.Platforms)
}

func TestPublishOCIPrepareJSONSuccess(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	image := mustImage(t)
	pusher := unusedPusher(t)
	signer := unusedSigner(t)
	expectSuccessfulPrepare(t, image, layout, pusher, signer)

	stdout, stderr, err := executePrepare(t, map[string]string{
		"GITHUB_TOKEN": tagsToken,
	}, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--json",
	}, preparePorts{
		reader: absentReader(t),
		pusher: pusher,
		signer: signer,
	})
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, tagsToken)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, prepareCommand, envelope.Command)
	assert.True(t, envelope.OK)

	result := decodePrepareResult(t, stdout)
	assert.True(t, result.Authoritative)
	assert.Equal(t, puboci.PrepareSchema, result.Schema)
	assert.Equal(t, tagsImage, result.Image)
	assert.Equal(t, "1.2.3", result.Version)
	assert.Equal(t, layout.Index.Digest.String(), result.IndexDigest)
	assert.Equal(t, []puboci.AttestationSubject{
		{Platform: "linux/amd64", Digest: layout.Platforms[0].Descriptor.Digest.String()},
		{Platform: "linux/arm64", Digest: layout.Platforms[1].Descriptor.Digest.String()},
	}, result.Platforms)
	assert.Equal(t, []puboci.TagObservation{
		{Tag: "1.2.3", Scope: string(rel.ScopeExact), Present: false},
		{Tag: "1.2", Scope: string(rel.ScopeMinor), Present: false},
		{Tag: "1", Scope: string(rel.ScopeMajor), Present: false},
		{Tag: "latest", Scope: string(rel.ScopeLatest), Present: false},
	}, result.Observed)
}

func TestPublishOCIPrepareSilentSuccess(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	image := mustImage(t)
	pusher := unusedPusher(t)
	signer := unusedSigner(t)
	expectSuccessfulPrepare(t, image, layout, pusher, signer)

	stdout, stderr, err := executePrepare(t, nil, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
	}, preparePorts{
		reader: absentReader(t),
		pusher: pusher,
		signer: signer,
	})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestPublishOCIPreparePublicationFailure(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	image := mustImage(t)
	first := layout.Blobs[0]
	pusher := unusedPusher(t)
	pusher.EXPECT().
		PushBlob(mock.Anything, image, first, mock.Anything).
		Return(errors.New("blob rejected")).
		Once()

	stdout, _, err := executePrepare(t, map[string]string{
		"GITHUB_TOKEN": tagsToken,
	}, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--json",
	}, preparePorts{
		reader: absentReader(t),
		pusher: pusher,
		signer: unusedSigner(t),
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "push blob")
	assert.NotContains(t, err.Error(), tagsToken)
	assert.NotContains(t, stdout, tagsToken)
	assertPrepareFailureEnvelope(t, stdout, "push blob")
}

func TestPublishOCIPrepareRegistryConfig(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	image, err := puboci.ParseImage("127.0.0.1:5000/owner/repo")
	require.NoError(t, err)
	reader := absentReader(t)
	pusher := unusedPusher(t)
	signer := unusedSigner(t)
	expectSuccessfulPrepare(t, image, layout, pusher, signer)

	var gotReader cli.RegistryConfig
	var gotPusher cli.RegistryConfig
	var gotPath string
	stdout, err := executePrepareFactory(t, map[string]string{
		"GITHUB_TOKEN":        tagsToken,
		"GITHUB_ACTOR":        "octocat",
		"RELEASE_COSIGN_PATH": prepareCosignPath,
	}, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", "127.0.0.1:5000/owner/repo",
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--plain-http",
		"--json",
	}, prepareFactories{
		newReader: func(config cli.RegistryConfig) (puboci.StateReader, error) {
			gotReader = config
			return reader, nil
		},
		newPusher: func(config cli.RegistryConfig) (puboci.ContentPusher, error) {
			gotPusher = config
			return pusher, nil
		},
		newSigner: func(path string) (puboci.Signer, error) {
			gotPath = path
			return signer, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, gotReader, gotPusher)
	assert.Equal(t, "octocat", gotReader.Credentials.Username)
	assert.Equal(t, tagsToken, gotReader.Credentials.Password.Reveal())
	assert.True(t, gotReader.PlainHTTP)
	assert.Equal(t, prepareCosignPath, gotPath)
	assert.NotContains(t, stdout, tagsToken)
	assert.True(t, decodePrepareResult(t, stdout).Authoritative)
}

func TestPublishOCIPrepareDryRunEnvYesIsUsage(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	called := false
	stdout, err := executePrepareFactory(t, map[string]string{
		"RELEASE_DRY_RUN": "yes",
	}, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--json",
	}, trackingPrepareFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.False(t, called)
	assert.Contains(t, err.Error(), "RELEASE_DRY_RUN")
	assertPrepareFailureEnvelope(t, stdout, "RELEASE_DRY_RUN")
}

func TestPublishOCIPrepareDryRunEnvTrue(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	readerCalled := false
	writerCalled := false
	stdout, err := executePrepareFactory(t, map[string]string{
		"RELEASE_DRY_RUN": "true",
	}, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--json",
	}, prepareFactories{
		newReader: func(cli.RegistryConfig) (puboci.StateReader, error) {
			readerCalled = true
			return absentReader(t), nil
		},
		newPusher: func(cli.RegistryConfig) (puboci.ContentPusher, error) {
			writerCalled = true
			return unusedPusher(t), nil
		},
		newSigner: func(string) (puboci.Signer, error) {
			writerCalled = true
			return unusedSigner(t), nil
		},
	})
	require.NoError(t, err)
	require.True(t, readerCalled)
	assert.False(t, writerCalled)
	assert.False(t, decodePrepareResult(t, stdout).Authoritative)
}

func TestPublishOCIPreparePlainHTTPRefusedForGHCR(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	called := false
	stdout, err := executePrepareFactory(t, nil, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--plain-http",
		"--json",
	}, trackingPrepareFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.False(t, called)
	assert.Contains(t, err.Error(), "--plain-http")
	assertPrepareFailureEnvelope(t, stdout, "--plain-http")
}

func TestPublishOCIPrepareSymlinkEscape(t *testing.T) {
	t.Parallel()

	layoutDir, layout := writeTwoPlatformLayout(t)
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	blobName, err := puboci.BlobPath(layout.Blobs[0].Digest)
	require.NoError(t, err)
	blobPath := filepath.Join(layoutDir, filepath.FromSlash(blobName))
	require.NoError(t, os.Remove(blobPath))
	require.NoError(t, os.Symlink(outside, blobPath))

	stdout, _, err := executePrepare(t, nil, []string{
		"publish", "oci", "prepare",
		"--layout", layoutDir,
		"--image", tagsImage,
		"--version", "1.2.3",
		"--digest", layout.Index.Digest.String(),
		"--json",
	}, preparePorts{
		reader: unusedReader(t),
		pusher: unusedPusher(t),
		signer: unusedSigner(t),
	})
	require.Error(t, err)
	assert.NotEqual(t, 0, cli.ExitCode(err))
	assert.Equal(t, 1, countJSONDocuments(stdout))
}

// preparePorts is the injected prepare command ports.
type preparePorts struct {
	// reader is the registry read port.
	reader puboci.StateReader
	// pusher is the registry write port.
	pusher puboci.ContentPusher
	// signer is the Cosign signing port.
	signer puboci.Signer
}

// prepareFactories constructs prepare ports from resolved configuration.
type prepareFactories struct {
	// newReader constructs the registry read port.
	newReader func(cli.RegistryConfig) (puboci.StateReader, error)
	// newPusher constructs the registry write port.
	newPusher func(cli.RegistryConfig) (puboci.ContentPusher, error)
	// newSigner constructs the Cosign signing port.
	newSigner func(string) (puboci.Signer, error)
}

// trackingPrepareFactories records whether any prepare factory was invoked.
func trackingPrepareFactories(t *testing.T, called *bool) prepareFactories {
	t.Helper()

	return prepareFactories{
		newReader: func(cli.RegistryConfig) (puboci.StateReader, error) {
			*called = true
			return unusedReader(t), nil
		},
		newPusher: func(cli.RegistryConfig) (puboci.ContentPusher, error) {
			*called = true
			return unusedPusher(t), nil
		},
		newSigner: func(string) (puboci.Signer, error) {
			*called = true
			return unusedSigner(t), nil
		},
	}
}

// unusedPusher returns a generated mock that fails if the port is called.
func unusedPusher(t *testing.T) *regmocks.MockContentPusher {
	t.Helper()

	return regmocks.NewMockContentPusher(t)
}

// unusedSigner returns a generated mock that fails if the port is called.
func unusedSigner(t *testing.T) *cosignmocks.MockSigner {
	t.Helper()

	return cosignmocks.NewMockSigner(t)
}

// expectSuccessfulPrepare expects the full push, verify, and sign sequence.
func expectSuccessfulPrepare(
	t *testing.T,
	image puboci.Image,
	layout puboci.Layout,
	pusher *regmocks.MockContentPusher,
	signer *cosignmocks.MockSigner,
) {
	t.Helper()

	for _, blob := range layout.Blobs {
		pusher.EXPECT().
			PushBlob(mock.Anything, image, blob, mock.Anything).
			Return(nil).
			Once()
	}
	for _, platform := range layout.Platforms {
		pusher.EXPECT().
			PushManifest(mock.Anything, image, platform.Descriptor, mock.Anything).
			Return(nil).
			Once()
	}
	pusher.EXPECT().
		PushManifest(mock.Anything, image, layout.Index, mock.Anything).
		Return(nil).
		Once()
	pusher.EXPECT().
		Verify(mock.Anything, image.Pin(layout.Index.Digest)).
		Return(nil).
		Once()
	for _, platform := range layout.Platforms {
		pusher.EXPECT().
			Verify(mock.Anything, image.Pin(platform.Descriptor.Digest)).
			Return(nil).
			Once()
	}
	signer.EXPECT().
		SignRecursive(mock.Anything, image.Pin(layout.Index.Digest)).
		Return(nil).
		Once()
}

// executePrepare runs publish oci prepare with injected ports.
func executePrepare(
	t *testing.T,
	env map[string]string,
	args []string,
	ports preparePorts,
) (string, string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		StateReader:   ports.reader,
		ContentPusher: ports.pusher,
		Signer:        ports.signer,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executePrepareFactory runs publish oci prepare with observing factories.
func executePrepareFactory(
	t *testing.T,
	env map[string]string,
	args []string,
	factories prepareFactories,
) (string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: &strings.Builder{},
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		NewStateReader:   factories.newReader,
		NewContentPusher: factories.newPusher,
		NewSigner:        factories.newSigner,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), err
}

// decodePrepareResult unmarshals the envelope result as [puboci.OCIPrepareResult].
func decodePrepareResult(t *testing.T, stdout string) puboci.OCIPrepareResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result puboci.OCIPrepareResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertPrepareFailureEnvelope checks stdout is one ok:false prepare envelope.
func assertPrepareFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, prepareCommand, envelope.Command)
	assert.False(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
	assert.NotContains(t, stdout, tagsToken)
}

// writeTwoPlatformLayout writes a linux/amd64 then linux/arm64 OCI layout.
func writeTwoPlatformLayout(t *testing.T) (string, puboci.Layout) {
	t.Helper()

	amd64Config := prepareDescriptor(t, ocispec.MediaTypeImageConfig, []byte(prepareAMD64Config))
	arm64Config := prepareDescriptor(t, ocispec.MediaTypeImageConfig, []byte(prepareARM64Config))
	amd64Layer := prepareDescriptor(t, ocispec.MediaTypeImageLayer, []byte(prepareAMD64Layer))
	arm64Layer := prepareDescriptor(t, ocispec.MediaTypeImageLayer, []byte(prepareARM64Layer))
	amd64ManifestBytes := prepareManifestBytes(t, amd64Config, amd64Layer)
	arm64ManifestBytes := prepareManifestBytes(t, arm64Config, arm64Layer)
	amd64Manifest := prepareDescriptor(t, ocispec.MediaTypeImageManifest, amd64ManifestBytes)
	arm64Manifest := prepareDescriptor(t, ocispec.MediaTypeImageManifest, arm64ManifestBytes)
	indexBytes, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: prepareOCISchemaVersion},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			prepareOCIDescriptor(amd64Manifest, &ocispec.Platform{OS: "linux", Architecture: "amd64"}),
			prepareOCIDescriptor(arm64Manifest, &ocispec.Platform{OS: "linux", Architecture: "arm64"}),
		},
	})
	require.NoError(t, err)

	root := t.TempDir()
	writeLayoutFile(t, root, "oci-layout", []byte(prepareLayoutJSON))
	writeLayoutFile(t, root, "index.json", indexBytes)
	writeLayoutBlob(t, root, amd64Manifest.Digest, amd64ManifestBytes)
	writeLayoutBlob(t, root, arm64Manifest.Digest, arm64ManifestBytes)
	writeLayoutBlob(t, root, amd64Config.Digest, []byte(prepareAMD64Config))
	writeLayoutBlob(t, root, arm64Config.Digest, []byte(prepareARM64Config))
	writeLayoutBlob(t, root, amd64Layer.Digest, []byte(prepareAMD64Layer))
	writeLayoutBlob(t, root, arm64Layer.Digest, []byte(prepareARM64Layer))

	layout, err := puboci.ReadLayout(os.DirFS(root))
	require.NoError(t, err)

	return root, layout
}

// writeLayoutFile creates path under root with data.
func writeLayoutFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, name), data, 0o644))
}

// writeLayoutBlob writes digest's blob bytes under the OCI layout root.
func writeLayoutBlob(t *testing.T, root string, digest rel.Digest, data []byte) {
	t.Helper()

	name, err := puboci.BlobPath(digest)
	require.NoError(t, err)
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// prepareManifestBytes marshals a one-layer image manifest.
func prepareManifestBytes(t *testing.T, config, layer puboci.Descriptor) []byte {
	t.Helper()

	data, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: prepareOCISchemaVersion},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    prepareOCIDescriptor(config, nil),
		Layers:    []ocispec.Descriptor{prepareOCIDescriptor(layer, nil)},
	})
	require.NoError(t, err)

	return data
}

// prepareDescriptor returns the descriptor of data at mediaType.
func prepareDescriptor(t *testing.T, mediaType string, data []byte) puboci.Descriptor {
	t.Helper()

	sum := sha256.Sum256(data)
	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
	require.NoError(t, err)

	return puboci.Descriptor{
		MediaType: mediaType,
		Digest:    digest,
		Size:      int64(len(data)),
	}
}

// prepareOCIDescriptor converts desc into an OCI descriptor with an optional platform.
func prepareOCIDescriptor(desc puboci.Descriptor, platform *ocispec.Platform) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: desc.MediaType,
		Digest:    godigest.Digest(desc.Digest.String()),
		Size:      desc.Size,
		Platform:  platform,
	}
}

// mustImage parses the fixture image name.
func mustImage(t *testing.T) puboci.Image {
	t.Helper()

	image, err := puboci.ParseImage(tagsImage)
	require.NoError(t, err)

	return image
}
