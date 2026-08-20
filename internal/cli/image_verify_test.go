package cli_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage/image"
)

const (
	// imageVerifyCommand is the envelope command path for image verify.
	imageVerifyCommand = "image verify"
	// imageVerifyDigestFile is the output-relative index digest artifact.
	imageVerifyDigestFile = "image-digest.txt"
	// imageVerifyAMD64Binary is the staged amd64 application fixture.
	imageVerifyAMD64Binary = "amd64-application"
	// imageVerifyARM64Binary is the staged arm64 application fixture.
	imageVerifyARM64Binary = "arm64-application"
	// imageVerifyCreated is the index created annotation fixture.
	imageVerifyCreated = "2024-01-02T03:04:05Z"
	// imageVerifyDescription is a nonempty image description annotation.
	imageVerifyDescription = "Release CLI"
	// imageVerifyLicenses is a nonempty image licenses annotation.
	imageVerifyLicenses = "Apache-2.0"
	// imageVerifyTitle is a nonempty image title annotation.
	imageVerifyTitle = "release-cli"
	// imageVerifyOCISchemaVersion is the OCI image-spec schemaVersion used in fixtures.
	imageVerifyOCISchemaVersion = 2
	// imageVerifyLayoutJSON is a valid oci-layout marker document.
	imageVerifyLayoutJSON = `{"imageLayoutVersion":"1.0.0"}`
	// imageVerifyFileMode is the mode of regular fixture files.
	imageVerifyFileMode = 0o644
	// imageVerifyExecMode is the mode of staged application files and the layer entry.
	imageVerifyExecMode = 0o755
	// imageVerifyDirMode is the mode of fixture directories.
	imageVerifyDirMode = 0o755
)

func TestImageVerifyJSONSuccess(t *testing.T) {
	t.Parallel()

	tree := writeImageVerifyTree(t)
	stdout, stderr, err := executeImageVerify(t, imageVerifyEnv(), []string{
		"image", "verify",
		"--json",
		"--output", tree.output,
		"--work", tree.work,
		"--binary", imageBinaryName,
		"--version", imageVersion,
	})
	require.NoError(t, err)
	assert.Empty(t, stderr)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, imageVerifyCommand, envelope.Command)
	assert.True(t, envelope.OK)

	result := decodeImageVerifyResult(t, stdout)
	assert.Equal(t, image.VerifySchema, result.Schema)
	assert.Equal(t, imageVersion, result.Version)
	assert.Equal(t, imageBinaryName, result.Binary)
	assert.Equal(t, tree.indexDigest, result.IndexDigest)
	assert.Equal(t, []image.VerifiedPlatform{
		{
			Platform:     "linux/amd64",
			Arch:         "x86_64",
			Manifest:     tree.amd64.manifest,
			Config:       tree.amd64.config,
			Layer:        tree.amd64.layer,
			BinaryDigest: tree.amd64.binary,
		},
		{
			Platform:     "linux/arm64",
			Arch:         "aarch64",
			Manifest:     tree.arm64.manifest,
			Config:       tree.arm64.config,
			Layer:        tree.arm64.layer,
			BinaryDigest: tree.arm64.binary,
		},
	}, result.Platforms)
	assert.Equal(t, tree.indexDigest+"\n", readImageDigestFile(t, tree.output))
}

func TestImageVerifySilentSuccess(t *testing.T) {
	t.Parallel()

	tree := writeImageVerifyTree(t)
	stdout, stderr, err := executeImageVerify(t, imageVerifyEnv(), []string{
		"image", "verify",
		"--output", tree.output,
		"--work", tree.work,
		"--binary", imageBinaryName,
		"--version", imageVersion,
	})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, tree.indexDigest+"\n", readImageDigestFile(t, tree.output))
}

func TestImageVerifyMissingValuesAreUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		args func(output, work string) []string
		want string
	}{
		{
			name: "missing output",
			env:  imageVerifyEnv(),
			args: func(_, work string) []string {
				return []string{
					"image", "verify",
					"--work", work,
					"--binary", imageBinaryName,
					"--version", imageVersion,
				}
			},
			want: "--output is required",
		},
		{
			name: "missing work",
			env:  imageVerifyEnv(),
			args: func(output, _ string) []string {
				return []string{
					"image", "verify",
					"--output", output,
					"--binary", imageBinaryName,
					"--version", imageVersion,
				}
			},
			want: "--work is required",
		},
		{
			name: "missing binary",
			env:  imageVerifyEnv(),
			args: func(output, work string) []string {
				return []string{
					"image", "verify",
					"--output", output,
					"--work", work,
					"--version", imageVersion,
				}
			},
			want: "binary name is empty",
		},
		{
			name: "unresolvable version",
			env:  imageVerifyEnv(),
			args: func(output, work string) []string {
				return []string{
					"image", "verify",
					"--output", output,
					"--work", work,
					"--binary", imageBinaryName,
				}
			},
			want: "--version is required when GITHUB_REF_NAME is unset",
		},
		{
			name: "missing GITHUB_SHA",
			env:  omitEnv(imageVerifyEnv(), "GITHUB_SHA"),
			args: func(output, work string) []string {
				return []string{
					"image", "verify",
					"--output", output,
					"--work", work,
					"--binary", imageBinaryName,
					"--version", imageVersion,
				}
			},
			want: "GITHUB_SHA is required",
		},
		{
			name: "missing GITHUB_SERVER_URL",
			env:  omitEnv(imageVerifyEnv(), "GITHUB_SERVER_URL"),
			args: func(output, work string) []string {
				return []string{
					"image", "verify",
					"--output", output,
					"--work", work,
					"--binary", imageBinaryName,
					"--version", imageVersion,
				}
			},
			want: "GITHUB_SERVER_URL is required",
		},
		{
			name: "missing GITHUB_REPOSITORY",
			env:  omitEnv(imageVerifyEnv(), "GITHUB_REPOSITORY"),
			args: func(output, work string) []string {
				return []string{
					"image", "verify",
					"--output", output,
					"--work", work,
					"--binary", imageBinaryName,
					"--version", imageVersion,
				}
			},
			want: "GITHUB_REPOSITORY is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			output := t.TempDir()
			work := t.TempDir()
			stdout, err := executeImageVerifyStdout(t, tt.env, tt.args(output, work))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Empty(t, stdout)
			assert.Contains(t, err.Error(), tt.want)
			assertPathAbsent(t, filepath.Join(output, imageVerifyDigestFile))
		})
	}
}

func TestImageVerifyInvalidBinaryIsUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		binary string
		want   string
	}{
		{
			name:   "empty binary",
			binary: "",
			want:   "binary name is empty",
		},
		{
			name:   "path separator",
			binary: "usr/bin/tool",
			want:   `binary name "usr/bin/tool" contains a path separator`,
		},
		{
			name:   "dot-dot",
			binary: "..",
			want:   `binary name ".." is not a filename`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			output := t.TempDir()
			work := t.TempDir()
			stdout, err := executeImageVerifyStdout(t, imageVerifyEnv(), []string{
				"image", "verify",
				"--json",
				"--output", output,
				"--work", work,
				"--binary", tt.binary,
				"--version", imageVersion,
			})
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, err.Error(), tt.want)
			assertImageVerifyFailureEnvelope(t, stdout, tt.want)
			assertPathAbsent(t, filepath.Join(output, imageVerifyDigestFile))
		})
	}
}

func TestImageVerifyLayoutFailure(t *testing.T) {
	t.Parallel()

	tree := writeImageVerifyTree(t)

	require.NoError(t, os.Remove(filepath.Join(tree.output, "layout", "oci-layout")))

	stdout, err := executeImageVerifyStdout(t, imageVerifyEnv(), []string{
		"image", "verify",
		"--json",
		"--output", tree.output,
		"--work", tree.work,
		"--binary", imageBinaryName,
		"--version", imageVersion,
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assertImageVerifyFailureEnvelope(t, stdout, "oci-layout")
	assertPathAbsent(t, filepath.Join(tree.output, imageVerifyDigestFile))
}

func TestImageVerifySBOMFailure(t *testing.T) {
	t.Parallel()

	tree := writeImageVerifyTree(t)
	writeFile(t, filepath.Join(tree.output, "sboms", "sbom-x86_64.spdx.json"), `{"packages":[]}`)

	stdout, err := executeImageVerifyStdout(t, imageVerifyEnv(), []string{
		"image", "verify",
		"--json",
		"--output", tree.output,
		"--work", tree.work,
		"--binary", imageBinaryName,
		"--version", imageVersion,
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assertImageVerifyFailureEnvelope(t, stdout, "sbom-x86_64.spdx.json")
	assertPathAbsent(t, filepath.Join(tree.output, imageVerifyDigestFile))
}

func TestImageVerifyMissingLayoutOrSBOMs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		drop string
		want string
	}{
		{name: "missing layout", drop: "layout", want: "layout"},
		{name: "missing sboms", drop: "sboms", want: "sboms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tree := writeImageVerifyTree(t)
			require.NoError(t, os.RemoveAll(filepath.Join(tree.output, tt.drop)))

			stdout, err := executeImageVerifyStdout(t, imageVerifyEnv(), []string{
				"image", "verify",
				"--json",
				"--output", tree.output,
				"--work", tree.work,
				"--binary", imageBinaryName,
				"--version", imageVersion,
			})
			require.Error(t, err)
			assert.Equal(t, 1, cli.ExitCode(err))
			assertImageVerifyFailureEnvelope(t, stdout, tt.want)
			assertPathAbsent(t, filepath.Join(tree.output, imageVerifyDigestFile))
		})
	}
}

// imageVerifyTree is a synthetic output and work pair that satisfies image verify.
type imageVerifyTree struct {
	// output is the authoritative artifact output root.
	output string
	// work is the scratch workspace.
	work string
	// indexDigest is SHA-256 over the exact index.json bytes.
	indexDigest string
	// amd64 is the linux/amd64 platform fixture.
	amd64 imageVerifyPlatform
	// arm64 is the linux/arm64 platform fixture.
	arm64 imageVerifyPlatform
}

// imageVerifyPlatform holds one platform's recorded digests.
type imageVerifyPlatform struct {
	// manifest is the platform manifest digest.
	manifest string
	// config is the image config digest.
	config string
	// layer is the single layer digest.
	layer string
	// binary is the staged application digest.
	binary string
}

// writeImageVerifyTree writes a two-platform layout, SBOMs, and staged binaries.
func writeImageVerifyTree(t *testing.T) imageVerifyTree {
	t.Helper()

	output := t.TempDir()
	work := t.TempDir()
	amd64Binary := []byte(imageVerifyAMD64Binary)
	arm64Binary := []byte(imageVerifyARM64Binary)
	writeImageVerifyApplication(t, work, "x86_64", amd64Binary)
	writeImageVerifyApplication(t, work, "aarch64", arm64Binary)

	annotations := imageVerifyAnnotations()
	amd64 := writeImageVerifyPlatform(t, output, "amd64", amd64Binary, annotations)
	arm64 := writeImageVerifyPlatform(t, output, "arm64", arm64Binary, annotations)
	indexDigest := writeImageVerifyIndex(t, output, amd64, arm64, annotations)
	writeImageVerifySBOMs(t, output)

	return imageVerifyTree{
		output:      output,
		work:        work,
		indexDigest: indexDigest,
		amd64:       amd64.recorded,
		arm64:       arm64.recorded,
	}
}

// imageVerifyWritten is one platform's blobs plus the index descriptor.
type imageVerifyWritten struct {
	// descriptor is the index manifest descriptor, including platform.
	descriptor ocispec.Descriptor
	// recorded is the digest set asserted in the --json result.
	recorded imageVerifyPlatform
}

// writeImageVerifyPlatform writes one platform's config, layer, and manifest blobs.
func writeImageVerifyPlatform(
	t *testing.T,
	output, architecture string,
	binary []byte,
	annotations map[string]string,
) imageVerifyWritten {
	t.Helper()

	configBytes, err := json.Marshal(ocispec.Image{
		Platform: ocispec.Platform{Architecture: architecture, OS: "linux"},
		Config: ocispec.ImageConfig{
			User:       "65532",
			Entrypoint: []string{"/usr/bin/" + imageBinaryName},
			Labels:     annotations,
		},
	})
	require.NoError(t, err)
	layerBytes := tarLayerFile(t, "usr/bin/"+imageBinaryName, binary)
	config := imageVerifyDescriptor(t, ocispec.MediaTypeImageConfig, configBytes)
	layer := imageVerifyDescriptor(t, ocispec.MediaTypeImageLayer, layerBytes)
	manifestBytes, err := json.Marshal(ocispec.Manifest{
		Versioned:   specs.Versioned{SchemaVersion: imageVerifyOCISchemaVersion},
		MediaType:   ocispec.MediaTypeImageManifest,
		Config:      config,
		Layers:      []ocispec.Descriptor{layer},
		Annotations: annotations,
	})
	require.NoError(t, err)
	manifest := imageVerifyDescriptor(t, ocispec.MediaTypeImageManifest, manifestBytes)
	writeImageVerifyBlob(t, output, config.Digest, configBytes)
	writeImageVerifyBlob(t, output, layer.Digest, layerBytes)
	writeImageVerifyBlob(t, output, manifest.Digest, manifestBytes)
	manifest.Platform = &ocispec.Platform{OS: "linux", Architecture: architecture}

	return imageVerifyWritten{
		descriptor: manifest,
		recorded: imageVerifyPlatform{
			manifest: manifest.Digest.String(),
			config:   config.Digest.String(),
			layer:    layer.Digest.String(),
			binary:   sha256Digest(binary),
		},
	}
}

// writeImageVerifyIndex writes oci-layout and index.json and returns the index digest.
func writeImageVerifyIndex(
	t *testing.T,
	output string,
	amd64, arm64 imageVerifyWritten,
	annotations map[string]string,
) string {
	t.Helper()

	indexAnnotations := maps.Clone(annotations)

	indexAnnotations[ocispec.AnnotationCreated] = imageVerifyCreated
	indexBytes, err := json.Marshal(ocispec.Index{
		Versioned:   specs.Versioned{SchemaVersion: imageVerifyOCISchemaVersion},
		MediaType:   ocispec.MediaTypeImageIndex,
		Manifests:   []ocispec.Descriptor{amd64.descriptor, arm64.descriptor},
		Annotations: indexAnnotations,
	})
	require.NoError(t, err)
	layoutDir := filepath.Join(output, "layout")
	require.NoError(t, os.MkdirAll(layoutDir, imageVerifyDirMode))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(layoutDir, "oci-layout"), []byte(imageVerifyLayoutJSON), imageVerifyFileMode),
	)
	require.NoError(t, os.WriteFile(filepath.Join(layoutDir, "index.json"), indexBytes, imageVerifyFileMode))

	return sha256Digest(indexBytes)
}

// writeImageVerifySBOMs writes architecture SPDX documents and an ignored index SBOM.
func writeImageVerifySBOMs(t *testing.T, output string) {
	t.Helper()

	sbom := imageVerifySBOM(imageVersion)
	writeFile(t, filepath.Join(output, "sboms", "sbom-x86_64.spdx.json"), sbom)
	writeFile(t, filepath.Join(output, "sboms", "sbom-aarch64.spdx.json"), sbom)
	writeFile(t, filepath.Join(output, "sboms", "sbom-index.spdx.json"), `{"packages":[]}`)
}

// writeImageVerifyApplication writes work/sources/<arch>/application.
func writeImageVerifyApplication(t *testing.T, work, arch string, binary []byte) {
	t.Helper()

	path := filepath.Join(work, "sources", arch, "application")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), imageVerifyDirMode))
	require.NoError(t, os.WriteFile(path, binary, imageVerifyExecMode))
}

// writeImageVerifyBlob writes digest's bytes under output/layout/blobs/sha256.
func writeImageVerifyBlob(t *testing.T, output string, digest godigest.Digest, data []byte) {
	t.Helper()

	hexPart, found := strings.CutPrefix(digest.String(), "sha256:")
	require.True(t, found)
	path := filepath.Join(output, "layout", "blobs", "sha256", hexPart)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), imageVerifyDirMode))
	require.NoError(t, os.WriteFile(path, data, imageVerifyFileMode))
}

// imageVerifyDescriptor returns the descriptor of data at mediaType.
func imageVerifyDescriptor(t *testing.T, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()

	sum := sha256.Sum256(data)

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    godigest.Digest("sha256:" + hex.EncodeToString(sum[:])),
		Size:      int64(len(data)),
	}
}

// tarLayerFile returns an uncompressed tar whose only entry is name.
func tarLayerFile(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     imageVerifyExecMode,
		Size:     int64(len(content)),
	}))
	_, err := writer.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return buf.Bytes()
}

// imageVerifyAnnotations returns the six annotations checked on the index and manifests.
func imageVerifyAnnotations() map[string]string {
	return map[string]string{
		ocispec.AnnotationDescription: imageVerifyDescription,
		ocispec.AnnotationLicenses:    imageVerifyLicenses,
		ocispec.AnnotationRevision:    imageSHA,
		ocispec.AnnotationSource:      imageServer + "/" + imageRepo,
		ocispec.AnnotationTitle:       imageVerifyTitle,
		ocispec.AnnotationVersion:     imageVersion,
	}
}

// imageVerifySBOM returns a minimal SPDX document for version.
func imageVerifySBOM(version string) string {
	return `{"packages":[{"primaryPackagePurpose":"APPLICATION","versionInfo":"` + version + `-r0"}]}`
}

// imageVerifyEnv returns the required Actions environment for image verify.
func imageVerifyEnv() map[string]string {
	return map[string]string{
		"GITHUB_SERVER_URL": imageServer,
		"GITHUB_REPOSITORY": imageRepo,
		"GITHUB_SHA":        imageSHA,
	}
}

// executeImageVerify runs image verify and captures stdout and stderr.
func executeImageVerify(t *testing.T, env map[string]string, args []string) (string, string, error) {
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
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executeImageVerifyStdout runs image verify and returns stdout.
func executeImageVerifyStdout(t *testing.T, env map[string]string, args []string) (string, error) {
	t.Helper()

	stdout, _, err := executeImageVerify(t, env, args)

	return stdout, err
}

// decodeImageVerifyResult unmarshals the envelope result as [image.VerifyResult].
func decodeImageVerifyResult(t *testing.T, stdout string) image.VerifyResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result image.VerifyResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertImageVerifyFailureEnvelope checks stdout is one ok:false image-verify envelope.
func assertImageVerifyFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, imageVerifyCommand, envelope.Command)
	assert.False(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
}

// readImageDigestFile returns the exact contents of image-digest.txt.
func readImageDigestFile(t *testing.T, output string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(output, imageVerifyDigestFile))
	require.NoError(t, err)

	return string(data)
}
