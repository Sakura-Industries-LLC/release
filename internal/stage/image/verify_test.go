package image_test

import (
	"archive/tar"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"
	"testing/fstest"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/image"
)

func TestVerifyLayoutHappyPath(t *testing.T) {
	t.Parallel()

	fixture := newVerifyLayout(t, defaultLayoutSpec())
	expected := expectedImage(t)

	got, err := image.VerifyLayout(fixture.files, expected)
	require.NoError(t, err)
	assert.Equal(t, digestOf(t, fixture.indexBytes), got.IndexDigest())

	result := got.Result(expected)
	assert.Equal(t, image.VerifySchema, result.Schema)
	assert.Equal(t, testVersion, result.Version)
	assert.Equal(t, testBinaryName, result.Binary)
	assert.Equal(t, digestOf(t, fixture.indexBytes).String(), result.IndexDigest)
	require.Len(t, result.Platforms, 2)
	assert.Equal(t, "linux/amd64", result.Platforms[0].Platform)
	assert.Equal(t, "x86_64", result.Platforms[0].Arch)
	assert.Equal(t, fixture.amd64.Manifest.String(), result.Platforms[0].Manifest)
	assert.Equal(t, fixture.amd64.Config.String(), result.Platforms[0].Config)
	assert.Equal(t, fixture.amd64.Layer.String(), result.Platforms[0].Layer)
	assert.Equal(t, digestOf(t, []byte(testAMD64Binary)).String(), result.Platforms[0].BinaryDigest)
	assert.Equal(t, "linux/arm64", result.Platforms[1].Platform)
	assert.Equal(t, "aarch64", result.Platforms[1].Arch)
	assert.Equal(t, digestOf(t, []byte(testARM64Binary)).String(), result.Platforms[1].BinaryDigest)
}

func TestVerifyLayoutAcceptsPlainTarLayer(t *testing.T) {
	t.Parallel()

	fixture := newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
		spec.platforms[0].layers[0].media = ocispec.MediaTypeImageLayer
		spec.platforms[1].layers[0].media = ocispec.MediaTypeImageLayer
	}))

	_, err := image.VerifyLayout(fixture.files, expectedImage(t))
	require.NoError(t, err)
}

func TestVerifyLayoutAcceptsLeadingDotSlash(t *testing.T) {
	t.Parallel()

	fixture := newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
		spec.platforms[0].layers[0].entries[0].name = "./usr/bin/" + testBinaryName
		spec.platforms[1].layers[0].entries[0].name = "./usr/bin/" + testBinaryName
	}))

	_, err := image.VerifyLayout(fixture.files, expectedImage(t))
	require.NoError(t, err)
}

func TestVerifyLayoutIgnoresPrefixedDecoyEntry(t *testing.T) {
	t.Parallel()

	decoy := tarEntry{
		name: "usr/bin/" + testBinaryName + ".bak",
		body: []byte(testAMD64Binary),
		mode: 0o755,
	}
	fixture := newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
		spec.platforms[0].layers[0].entries = append([]tarEntry{decoy}, spec.platforms[0].layers[0].entries...)
		spec.platforms[1].layers[0].entries = append([]tarEntry{{
			name: "usr/bin/" + testBinaryName + ".bak",
			body: []byte(testARM64Binary),
			mode: 0o755,
		}}, spec.platforms[1].layers[0].entries...)
	}))

	got, err := image.VerifyLayout(fixture.files, expectedImage(t))
	require.NoError(t, err)
	assert.Equal(
		t,
		digestOf(t, []byte(testAMD64Binary)).String(),
		got.Result(expectedImage(t)).Platforms[0].BinaryDigest,
	)
}

func TestVerifyLayoutAcceptsFileTypeModeBits(t *testing.T) {
	t.Parallel()

	fixture := newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
		spec.platforms[0].layers[0].raw = true
		spec.platforms[0].layers[0].entries[0].mode = 0o100755
		spec.platforms[1].layers[0].raw = true
		spec.platforms[1].layers[0].entries[0].mode = 0o100755
	}))

	_, err := image.VerifyLayout(fixture.files, expectedImage(t))
	require.NoError(t, err)
}

func TestVerifyLayoutCanonicalOrder(t *testing.T) {
	t.Parallel()

	spec := defaultLayoutSpec()
	spec.platforms[0], spec.platforms[1] = spec.platforms[1], spec.platforms[0]
	fixture := newVerifyLayout(t, spec)

	got, err := image.VerifyLayout(fixture.files, expectedImage(t))
	require.NoError(t, err)
	result := got.Result(expectedImage(t))
	require.Len(t, result.Platforms, 2)
	assert.Equal(t, "linux/amd64", result.Platforms[0].Platform)
	assert.Equal(t, "linux/arm64", result.Platforms[1].Platform)
}

func TestVerifyLayoutErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    fs.FS
		expected func(*testing.T) image.ExpectedImage
		wantErr  string
	}{
		{
			name: "one platform",
			files: newVerifyLayout(
				t,
				mutateSpec(func(spec *layoutSpec) { spec.platforms = spec.platforms[:1] }),
			).files,
			expected: expectedImage,
			wantErr:  "has 1 manifests, want 2",
		},
		{
			name: "three platforms",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				extra := spec.platforms[1]
				extra.arch = "amd64"
				spec.platforms = append(spec.platforms, extra)
			})).files,
			expected: expectedImage,
			wantErr:  "has 3 manifests, want 2",
		},
		{
			name: "non-linux OS",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].os = "windows"
			})).files,
			expected: expectedImage,
			wantErr:  `platform os is "windows", want linux`,
		},
		{
			name: "unexpected architecture",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[1].arch = "riscv64"
			})).files,
			expected: expectedImage,
			wantErr:  `platform architecture "riscv64" is not amd64 or arm64`,
		},
		{
			name: "duplicate architectures",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[1].arch = "amd64"
			})).files,
			expected: expectedImage,
			wantErr:  "lists platform linux/amd64 more than once",
		},
		{
			name: "wrong index media type",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.indexMedia = ocispec.MediaTypeImageManifest
			})).files,
			expected: expectedImage,
			wantErr:  "mediaType is \"" + ocispec.MediaTypeImageManifest + "\"",
		},
		{
			name: "wrong schema version",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.indexSchema = 1
			})).files,
			expected: expectedImage,
			wantErr:  "schemaVersion is 1, want 2",
		},
		{
			name: "missing description",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				delete(spec.annotations, testAnnotationDescription)
			})).files,
			expected: expectedImage,
			wantErr:  "missing annotation " + testAnnotationDescription,
		},
		{
			name: "missing licenses",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				delete(spec.annotations, testAnnotationLicenses)
			})).files,
			expected: expectedImage,
			wantErr:  "missing annotation " + testAnnotationLicenses,
		},
		{
			name: "missing title",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				delete(spec.annotations, testAnnotationTitle)
			})).files,
			expected: expectedImage,
			wantErr:  "missing annotation " + testAnnotationTitle,
		},
		{
			name: "empty description",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.annotations[testAnnotationDescription] = ""
			})).files,
			expected: expectedImage,
			wantErr:  "annotation " + testAnnotationDescription + " is empty",
		},
		{
			name: "empty licenses",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.annotations[testAnnotationLicenses] = ""
			})).files,
			expected: expectedImage,
			wantErr:  "annotation " + testAnnotationLicenses + " is empty",
		},
		{
			name: "empty title",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.annotations[testAnnotationTitle] = ""
			})).files,
			expected: expectedImage,
			wantErr:  "annotation " + testAnnotationTitle + " is empty",
		},
		{
			name: "revision disagrees",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.annotations[testAnnotationRevision] = "deadbeef"
			})).files,
			expected: expectedImage,
			wantErr:  "annotation " + testAnnotationRevision + " is \"deadbeef\"",
		},
		{
			name: "source disagrees",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.annotations[testAnnotationSource] = "https://example.invalid/repo"
			})).files,
			expected: expectedImage,
			wantErr:  "annotation " + testAnnotationSource,
		},
		{
			name: "version disagrees",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.annotations[testAnnotationVersion] = "9.9.9"
			})).files,
			expected: expectedImage,
			wantErr:  "annotation " + testAnnotationVersion + " is \"9.9.9\"",
		},
		{
			name: "manifest annotations differ",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].annotations[testAnnotationTitle] = "other"
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64 manifest " + testAnnotationTitle + " is \"other\"",
		},
		{
			name: "config labels differ",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[1].labels[testAnnotationLicenses] = "MIT"
			})).files,
			expected: expectedImage,
			wantErr:  "linux/arm64 config label " + testAnnotationLicenses + " is \"MIT\"",
		},
		{
			name: "wrong config architecture",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].configArch = "arm64"
			})).files,
			expected: expectedImage,
			wantErr:  `linux/amd64 config architecture is "arm64", want "amd64"`,
		},
		{
			name: "wrong config os",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].configOS = "windows"
			})).files,
			expected: expectedImage,
			wantErr:  `linux/amd64 config os is "windows", want linux`,
		},
		{
			name: "wrong entrypoint",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].entrypoint = []string{"/bin/sh"}
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64 config Entrypoint is",
		},
		{
			name: "multi-element entrypoint",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].entrypoint = []string{"/usr/bin/" + testBinaryName, "--help"}
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64 config Entrypoint is",
		},
		{
			name: "wrong user",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].user = "0"
			})).files,
			expected: expectedImage,
			wantErr:  `linux/amd64 config User is "0", want "65532"`,
		},
		{
			name: "zero layers",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers = nil
			})).files,
			expected: expectedImage,
			wantErr:  "has 0 layers, want 1",
		},
		{
			name: "two layers",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers = append(spec.platforms[0].layers, spec.platforms[0].layers[0])
			})).files,
			expected: expectedImage,
			wantErr:  "has 2 layers, want 1",
		},
		{
			name: "missing descriptor blob",
			files: func() fs.FS {
				fixture := newVerifyLayout(t, defaultLayoutSpec())

				return deleteFile(fixture.files, blobFile(fixture.amd64.Manifest))
			}(),
			expected: expectedImage,
			wantErr:  "blobs/sha256/",
		},
		{
			name: "blob size disagrees",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].manifestSizeDelta = 1
			})).files,
			expected: expectedImage,
			wantErr:  "declared",
		},
		{
			name: "invalid digest string",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].invalidDigest = "sha256:not-a-digest"
			})).files,
			expected: expectedImage,
			wantErr:  "digest",
		},
		{
			name: "mode 0644",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o644
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 0644, want 0755",
		},
		{
			name: "mode 0700",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o700
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 0700, want 0755",
		},
		{
			name: "mode 0777",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o777
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 0777, want 0755",
		},
		{
			name: "mode 04755",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o4755
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 04755, want 0755",
		},
		{
			name: "mode 02755",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o2755
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 02755, want 0755",
		},
		{
			name: "mode 01755",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o1755
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 01755, want 0755",
		},
		{
			name: "mode 06755",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].mode = 0o6755
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " has mode 06755, want 0755",
		},
		{
			name: "hard link instead of regular file",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].typ = tar.TypeLink
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " is type \"hard link\", want a regular file",
		},
		{
			name: "declared size exceeds binary limit",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].raw = true
				spec.platforms[0].layers[0].entries[0].size = 64*1024*1024 + 1
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " is 67108865 bytes, exceeds the 67108864 byte binary limit",
		},
		{
			name: "ownership 1000/1000",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].uid = 1000
				spec.platforms[0].layers[0].entries[0].gid = 1000
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " is owned by 1000/1000, want 0/0",
		},
		{
			name: "symlink instead of regular file",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].typ = tar.TypeSymlink
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " is type \"symlink\", want a regular file",
		},
		{
			name: "directory instead of regular file",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].typ = tar.TypeDir
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: usr/bin/" + testBinaryName + " is type \"directory\", want a regular file",
		},
		{
			name: "missing entry",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].name = "usr/bin/other"
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: layer is missing usr/bin/" + testBinaryName,
		},
		{
			name: "duplicated entry",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries = append(
					spec.platforms[0].layers[0].entries,
					spec.platforms[0].layers[0].entries[0],
				)
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64: layer lists usr/bin/" + testBinaryName + " more than once",
		},
		{
			name: "layer content mismatch",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].entries[0].body = []byte("tampered")
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64 image binary has digest",
		},
		{
			name: "unsupported layer media type",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers[0].media = ocispec.MediaTypeImageLayerZstd
			})).files,
			expected: expectedImage,
			wantErr:  "linux/amd64 layer media type is",
		},
		{
			name:  "empty binary name",
			files: newVerifyLayout(t, defaultLayoutSpec()).files,
			expected: func(t *testing.T) image.ExpectedImage {
				t.Helper()
				expected := expectedImage(t)
				expected.Binary = ""

				return expected
			},
			wantErr: "binary name is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := image.VerifyLayout(test.files, test.expected(t))
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestVerifyLayoutTempDirModes(t *testing.T) {
	t.Parallel()

	root := writeLayoutDir(t, defaultLayoutSpec())
	layoutFS, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, layoutFS.Close())
	})

	_, err = image.VerifyLayout(layoutFS.FS(), expectedImage(t))
	require.NoError(t, err)
}

func TestVerifySBOMsHappyPath(t *testing.T) {
	t.Parallel()

	version, err := rel.ParseVersion(testVersion)
	require.NoError(t, err)

	err = image.VerifySBOMs(validSBOMs(t, testVersion), version, []image.APKArch{image.ArchX8664, image.ArchAArch64})
	require.NoError(t, err)
}

func TestVerifySBOMsIgnoresIndexDocument(t *testing.T) {
	t.Parallel()

	files := validSBOMs(t, testVersion)
	files["sbom-index.spdx.json"] = &fstest.MapFile{Data: []byte(`{"packages":[]}`)}
	version, err := rel.ParseVersion(testVersion)
	require.NoError(t, err)

	err = image.VerifySBOMs(files, version, []image.APKArch{image.ArchX8664, image.ArchAArch64})
	require.NoError(t, err)
}

func TestVerifySBOMsErrors(t *testing.T) {
	t.Parallel()

	version, err := rel.ParseVersion(testVersion)
	require.NoError(t, err)
	arches := []image.APKArch{image.ArchX8664, image.ArchAArch64}

	tests := []struct {
		name    string
		files   fs.FS
		wantErr string
	}{
		{
			name:    "nil filesystem",
			wantErr: "SBOM filesystem is nil",
		},
		{
			name:    "missing SBOM file",
			files:   fstest.MapFS{"sbom-x86_64.spdx.json": {Data: sbomBytes(t, testVersion)}},
			wantErr: "sbom-aarch64.spdx.json",
		},
		{
			name: "missing APPLICATION package",
			files: fstest.MapFS{
				"sbom-x86_64.spdx.json": {
					Data: []byte(`{"packages":[{"primaryPackagePurpose":"LIBRARY","versionInfo":"1.2.3-r0"}]}`),
				},
				"sbom-aarch64.spdx.json": {Data: sbomBytes(t, testVersion)},
			},
			wantErr: "sbom-x86_64.spdx.json has no APPLICATION package at version 1.2.3-r0",
		},
		{
			name: "wrong versionInfo",
			files: fstest.MapFS{
				"sbom-x86_64.spdx.json":  {Data: sbomBytes(t, "9.9.9")},
				"sbom-aarch64.spdx.json": {Data: sbomBytes(t, testVersion)},
			},
			wantErr: "sbom-x86_64.spdx.json has no APPLICATION package at version 1.2.3-r0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := image.VerifySBOMs(test.files, version, arches)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestCanonicalDigests(t *testing.T) {
	t.Parallel()

	amd64 := []byte(testAMD64Binary)
	arm64 := []byte(testARM64Binary)
	work := fstest.MapFS{
		path.Join("sources", "x86_64", "application"):  {Data: amd64},
		path.Join("sources", "aarch64", "application"): {Data: arm64},
	}

	got, err := image.CanonicalDigests(work, []image.APKArch{image.ArchX8664, image.ArchAArch64})
	require.NoError(t, err)
	assert.Equal(t, digestOf(t, amd64), got[image.ArchX8664])
	assert.Equal(t, digestOf(t, arm64), got[image.ArchAArch64])
}

func TestCanonicalDigestsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		work    fs.FS
		arches  []image.APKArch
		wantErr string
	}{
		{
			name:    "nil filesystem",
			arches:  []image.APKArch{image.ArchX8664},
			wantErr: "work filesystem is nil",
		},
		{
			name:    "missing application",
			work:    fstest.MapFS{},
			arches:  []image.APKArch{image.ArchX8664},
			wantErr: "sources/x86_64/application",
		},
		{
			name: "non-regular entry",
			work: fstest.MapFS{
				"sources/x86_64/application": {Mode: fs.ModeDir},
			},
			arches:  []image.APKArch{image.ArchX8664},
			wantErr: "sources/x86_64/application is not a regular file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := image.CanonicalDigests(test.work, test.arches)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

// validSBOMs returns architecture SPDX documents plus an ignored index document.
func validSBOMs(t *testing.T, version string) fstest.MapFS {
	t.Helper()

	return fstest.MapFS{
		"sbom-x86_64.spdx.json":  {Data: sbomBytes(t, version)},
		"sbom-aarch64.spdx.json": {Data: sbomBytes(t, version)},
		"sbom-index.spdx.json":   {Data: []byte(`{"packages":[]}`)},
	}
}

// sbomBytes encodes a one-package SPDX document at version-r0.
func sbomBytes(t *testing.T, version string) []byte {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"packages": []map[string]string{{
			"primaryPackagePurpose": "APPLICATION",
			"versionInfo":           version + "-r0",
		}},
	})
	require.NoError(t, err)

	return data
}

// writeLayoutDir materializes spec under t.TempDir so tar modes stay intact.
func writeLayoutDir(t *testing.T, spec layoutSpec) string {
	t.Helper()

	fixture := newVerifyLayout(t, spec)
	root := t.TempDir()
	for name, file := range fixture.files {
		pathName := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(pathName), 0o755))
		require.NoError(t, os.WriteFile(pathName, file.Data, 0o644))
	}

	return root
}
