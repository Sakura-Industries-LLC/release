package image_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	godigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/image"
)

const (
	// testDescription is a nonempty index description annotation.
	testDescription = "Exercise the Meigma release pipeline."
	// testLicenses is a nonempty index licenses annotation.
	testLicenses = "LicenseRef-Proprietary"
	// testTitle is a nonempty index title annotation.
	testTitle = testBinaryName
	// testCreated is an index created annotation the verifier ignores.
	testCreated = "2024-01-02T03:04:05Z"
	// testLayoutJSON is a valid oci-layout marker document.
	testLayoutJSON = `{"imageLayoutVersion":"1.0.0"}`
	// testAMD64Binary is distinct linux/amd64 layer payload.
	testAMD64Binary = "amd64-canonical-binary"
	// testARM64Binary is distinct linux/arm64 layer payload.
	testARM64Binary = "arm64-canonical-binary"
	// testOCISchemaVersion is the OCI image-spec schemaVersion used in fixtures.
	testOCISchemaVersion = 2
	// testAnnotationDescription is the OCI description annotation key.
	testAnnotationDescription = "org.opencontainers.image.description"
	// testAnnotationLicenses is the OCI licenses annotation key.
	testAnnotationLicenses = "org.opencontainers.image.licenses"
	// testAnnotationRevision is the OCI revision annotation key.
	testAnnotationRevision = "org.opencontainers.image.revision"
	// testAnnotationSource is the OCI source annotation key.
	testAnnotationSource = "org.opencontainers.image.source"
	// testAnnotationTitle is the OCI title annotation key.
	testAnnotationTitle = "org.opencontainers.image.title"
	// testAnnotationVersion is the OCI version annotation key.
	testAnnotationVersion = "org.opencontainers.image.version"
	// testAnnotationCreated is the OCI created annotation key.
	testAnnotationCreated = "org.opencontainers.image.created"
)

func TestReadLayoutTwoPlatforms(t *testing.T) {
	t.Parallel()

	fixture := newVerifyLayout(t, defaultLayoutSpec())

	got, err := image.ReadLayout(fixture.files)
	require.NoError(t, err)

	assert.Equal(t, fixture.indexBytes, got.IndexBytes)
	assert.Equal(t, digestOf(t, fixture.indexBytes), got.IndexDigest)
	assert.Equal(t, defaultAnnotations(), got.Annotations)
	require.Len(t, got.Platforms, 2)
	assert.Equal(t, image.PlatformAMD64, got.Platforms[0].Platform)
	assert.Equal(t, image.PlatformARM64, got.Platforms[1].Platform)
	assert.Equal(t, fixture.amd64.Manifest, got.Platforms[0].Manifest)
	assert.Equal(t, fixture.arm64.Manifest, got.Platforms[1].Manifest)
	assert.Equal(t, fixture.amd64.Config, got.Platforms[0].Config)
	assert.Equal(t, fixture.arm64.Config, got.Platforms[1].Config)
	assert.Equal(t, fixture.amd64.Layer, got.Platforms[0].Layer)
	assert.Equal(t, fixture.arm64.Layer, got.Platforms[1].Layer)
	assert.Equal(t, ocispec.MediaTypeImageLayerGzip, got.Platforms[0].LayerMedia)
	assert.Equal(t, defaultAnnotations(), got.Platforms[0].Annotations)
}

func TestReadLayoutPreservesIndexOrder(t *testing.T) {
	t.Parallel()

	spec := defaultLayoutSpec()
	spec.platforms[0], spec.platforms[1] = spec.platforms[1], spec.platforms[0]
	fixture := newVerifyLayout(t, spec)

	got, err := image.ReadLayout(fixture.files)
	require.NoError(t, err)
	require.Len(t, got.Platforms, 2)
	assert.Equal(t, image.PlatformARM64, got.Platforms[0].Platform)
	assert.Equal(t, image.PlatformAMD64, got.Platforms[1].Platform)
}

func TestReadLayoutIndexDigestIsByteExact(t *testing.T) {
	t.Parallel()

	fixture := newVerifyLayout(t, defaultLayoutSpec())
	original, err := image.ReadLayout(fixture.files)
	require.NoError(t, err)

	changed := cloneMapFS(fixture.files)
	changed["index.json"] = &fstest.MapFile{Data: append(slices.Clone(fixture.indexBytes), '\n')}

	got, err := image.ReadLayout(changed)
	require.NoError(t, err)
	assert.NotEqual(t, original.IndexDigest, got.IndexDigest)
	assert.Equal(t, digestOf(t, changed["index.json"].Data), got.IndexDigest)
	assert.Equal(t, changed["index.json"].Data, got.IndexBytes)
}

func TestReadLayoutErrors(t *testing.T) {
	t.Parallel()

	valid := newVerifyLayout(t, defaultLayoutSpec())

	tests := []struct {
		name    string
		files   fs.FS
		wantErr string
	}{
		{
			name:    "nil filesystem",
			wantErr: "layout filesystem is nil",
		},
		{
			name:    "missing oci-layout",
			files:   fstest.MapFS{"index.json": {Data: valid.indexBytes}},
			wantErr: "oci-layout",
		},
		{
			name: "missing index.json",
			files: fstest.MapFS{
				"oci-layout": {Data: []byte(testLayoutJSON)},
			},
			wantErr: "index.json",
		},
		{
			name: "one platform",
			files: newVerifyLayout(
				t,
				mutateSpec(func(spec *layoutSpec) { spec.platforms = spec.platforms[:1] }),
			).files,
			wantErr: "has 1 manifests, want 2",
		},
		{
			name: "three platforms",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				extra := spec.platforms[1]
				extra.arch = "amd64"
				spec.platforms = append(spec.platforms, extra)
			})).files,
			wantErr: "has 3 manifests, want 2",
		},
		{
			name: "non-linux OS",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].os = "windows"
			})).files,
			wantErr: `platform os is "windows", want linux`,
		},
		{
			name: "unexpected architecture",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[1].arch = "riscv64"
			})).files,
			wantErr: `platform architecture "riscv64" is not amd64 or arm64`,
		},
		{
			name: "duplicate architectures",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[1].arch = "amd64"
			})).files,
			wantErr: "lists platform linux/amd64 more than once",
		},
		{
			name: "wrong index media type",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.indexMedia = ocispec.MediaTypeImageManifest
			})).files,
			wantErr: "mediaType is \"" + ocispec.MediaTypeImageManifest + "\"",
		},
		{
			name: "wrong schema version",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.indexSchema = 1
			})).files,
			wantErr: "schemaVersion is 1, want 2",
		},
		{
			name: "zero layers",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers = nil
			})).files,
			wantErr: "has 0 layers, want 1",
		},
		{
			name: "two layers",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].layers = append(spec.platforms[0].layers, spec.platforms[0].layers[0])
			})).files,
			wantErr: "has 2 layers, want 1",
		},
		{
			name:    "missing descriptor blob",
			files:   deleteFile(valid.files, blobFile(valid.amd64.Manifest)),
			wantErr: blobFile(valid.amd64.Manifest),
		},
		{
			name: "blob size disagrees",
			files: newVerifyLayout(
				t,
				mutateSpec(func(spec *layoutSpec) { spec.platforms[0].manifestSizeDelta = 1 }),
			).files,
			wantErr: "declared",
		},
		{
			name: "invalid digest string",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].invalidDigest = "sha256:not-a-digest"
			})).files,
			wantErr: "digest",
		},
		{
			name: "wrong manifest media type",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].manifestMedia = ocispec.MediaTypeImageIndex
			})).files,
			wantErr: "mediaType is \"" + ocispec.MediaTypeImageIndex + "\"",
		},
		{
			name: "wrong manifest schema version",
			files: newVerifyLayout(t, mutateSpec(func(spec *layoutSpec) {
				spec.platforms[0].manifestSchema = 1
			})).files,
			wantErr: "schemaVersion is 1, want 2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := image.ReadLayout(test.files)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

// layoutSpec is the mutable description of a two-platform OCI layout fixture.
type layoutSpec struct {
	// indexSchema is the index schemaVersion.
	indexSchema int
	// indexMedia is the index mediaType.
	indexMedia string
	// annotations are the index annotations.
	annotations map[string]string
	// platforms are the index manifests in file order.
	platforms []platformSpec
}

// platformSpec is one platform manifest, config, and layer.
type platformSpec struct {
	// os is the index descriptor platform OS.
	os string
	// arch is the index descriptor platform architecture.
	arch string
	// manifestSchema is the manifest schemaVersion.
	manifestSchema int
	// manifestMedia is the manifest mediaType.
	manifestMedia string
	// annotations are the manifest annotations.
	annotations map[string]string
	// configArch is the config architecture field.
	configArch string
	// configOS is the config os field.
	configOS string
	// entrypoint is the config Entrypoint.
	entrypoint []string
	// user is the config User.
	user string
	// labels are the config Labels.
	labels map[string]string
	// layers are the manifest layers in file order.
	layers []layerSpec
	// manifestSizeDelta adds to the index descriptor size without changing bytes.
	manifestSizeDelta int64
	// invalidDigest replaces the index descriptor digest when set.
	invalidDigest string
}

// layerSpec is one layer blob and the tar entries it contains.
type layerSpec struct {
	// media is the layer descriptor media type.
	media string
	// entries are the tar members, in walk order.
	entries []tarEntry
	// raw writes ustar headers by hand so a declared Size override survives.
	raw bool
}

// tarEntry is one tar member used to build a layer blob.
type tarEntry struct {
	// name is the tar header name.
	name string
	// body is the regular-file payload.
	body []byte
	// mode is the permission bits.
	mode int64
	// uid is the numeric owner.
	uid int
	// gid is the numeric group.
	gid int
	// typ is the tar Typeflag; zero means a regular file.
	typ byte
	// size is the declared tar Size. Zero means len(body).
	size int64
}

// verifyLayout is a built OCI layout and the digests it contains.
type verifyLayout struct {
	// files is the extracted layout directory.
	files fstest.MapFS
	// indexBytes is the exact index.json contents.
	indexBytes []byte
	// amd64 is the linux/amd64 platform recorded by the builder.
	amd64 image.LayoutPlatform
	// arm64 is the linux/arm64 platform recorded by the builder.
	arm64 image.LayoutPlatform
}

// defaultLayoutSpec returns a valid two-platform gzip layout.
func defaultLayoutSpec() layoutSpec {
	annotations := defaultAnnotations()

	return layoutSpec{
		indexSchema: testOCISchemaVersion,
		indexMedia:  ocispec.MediaTypeImageIndex,
		annotations: annotations,
		platforms: []platformSpec{
			defaultPlatformSpec("amd64", []byte(testAMD64Binary), annotations),
			defaultPlatformSpec("arm64", []byte(testARM64Binary), annotations),
		},
	}
}

// defaultPlatformSpec returns one valid platform with a gzip layer.
func defaultPlatformSpec(arch string, binary []byte, annotations map[string]string) platformSpec {
	return platformSpec{
		os:             "linux",
		arch:           arch,
		manifestSchema: testOCISchemaVersion,
		manifestMedia:  ocispec.MediaTypeImageManifest,
		annotations:    cloneStringMap(annotations),
		configArch:     arch,
		configOS:       "linux",
		entrypoint:     []string{"/usr/bin/" + testBinaryName},
		user:           "65532",
		labels:         cloneStringMap(annotations),
		layers: []layerSpec{{
			media: ocispec.MediaTypeImageLayerGzip,
			entries: []tarEntry{{
				name: "usr/bin/" + testBinaryName,
				body: binary,
				mode: 0o755,
			}},
		}},
	}
}

// defaultAnnotations returns the seven index annotations a real apko layout carries.
func defaultAnnotations() map[string]string {
	return map[string]string{
		testAnnotationDescription: testDescription,
		testAnnotationLicenses:    testLicenses,
		testAnnotationRevision:    testCommit,
		testAnnotationSource:      testSourceURL,
		testAnnotationTitle:       testTitle,
		testAnnotationVersion:     testVersion,
		testAnnotationCreated:     testCreated,
	}
}

// mutateSpec copies [defaultLayoutSpec] and applies edit.
func mutateSpec(edit func(*layoutSpec)) layoutSpec {
	spec := defaultLayoutSpec()
	edit(&spec)

	return spec
}

// newVerifyLayout encodes spec as an on-disk OCI layout.
func newVerifyLayout(t *testing.T, spec layoutSpec) verifyLayout {
	t.Helper()

	files := fstest.MapFS{
		"oci-layout": {Data: []byte(testLayoutJSON)},
	}
	manifests := make([]ocispec.Descriptor, 0, len(spec.platforms))
	var amd64, arm64 image.LayoutPlatform
	for _, platform := range spec.platforms {
		built := addPlatform(t, files, platform)
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    godigest.Digest(built.Manifest.String()),
			Size:      blobSize(t, files, built.Manifest) + platform.manifestSizeDelta,
			Platform:  &ocispec.Platform{OS: platform.os, Architecture: platform.arch},
		}
		if platform.invalidDigest != "" {
			desc.Digest = godigest.Digest(platform.invalidDigest)
		}
		manifests = append(manifests, desc)
		switch platform.arch {
		case "amd64":
			amd64 = built
		case "arm64":
			arm64 = built
		}
	}

	indexBytes, err := json.Marshal(ocispec.Index{
		Versioned:   specs.Versioned{SchemaVersion: spec.indexSchema},
		MediaType:   spec.indexMedia,
		Manifests:   manifests,
		Annotations: spec.annotations,
	})
	require.NoError(t, err)
	files["index.json"] = &fstest.MapFile{Data: indexBytes}

	return verifyLayout{files: files, indexBytes: indexBytes, amd64: amd64, arm64: arm64}
}

// addPlatform writes one platform's manifest, config, and layer blobs.
func addPlatform(t *testing.T, files fstest.MapFS, spec platformSpec) image.LayoutPlatform {
	t.Helper()

	layers := make([]ocispec.Descriptor, 0, len(spec.layers))
	var layerDigest rel.Digest
	var layerMedia string
	for _, layer := range spec.layers {
		body := encodeLayer(t, layer)
		desc := putBlob(t, files, layer.media, body)
		layers = append(layers, desc)
		parsed, err := rel.ParseDigest(desc.Digest.String())
		require.NoError(t, err)
		layerDigest = parsed
		layerMedia = layer.media
	}

	configBytes, err := json.Marshal(ocispec.Image{
		Platform: ocispec.Platform{
			Architecture: spec.configArch,
			OS:           spec.configOS,
		},
		Config: ocispec.ImageConfig{
			User:       spec.user,
			Entrypoint: spec.entrypoint,
			Labels:     spec.labels,
		},
	})
	require.NoError(t, err)
	configDesc := putBlob(t, files, ocispec.MediaTypeImageConfig, configBytes)
	configDigest, err := rel.ParseDigest(configDesc.Digest.String())
	require.NoError(t, err)

	manifestBytes, err := json.Marshal(ocispec.Manifest{
		Versioned:   specs.Versioned{SchemaVersion: spec.manifestSchema},
		MediaType:   spec.manifestMedia,
		Config:      configDesc,
		Layers:      layers,
		Annotations: spec.annotations,
	})
	require.NoError(t, err)
	manifestDesc := putBlob(t, files, ocispec.MediaTypeImageManifest, manifestBytes)
	manifestDigest, err := rel.ParseDigest(manifestDesc.Digest.String())
	require.NoError(t, err)

	platform, err := image.ParsePlatform("linux/" + spec.arch)
	if err != nil {
		platform = image.Platform("linux/" + spec.arch)
	}

	return image.LayoutPlatform{
		Platform:    platform,
		Manifest:    manifestDigest,
		Config:      configDigest,
		Layer:       layerDigest,
		LayerMedia:  layerMedia,
		Annotations: cloneStringMap(spec.annotations),
	}
}

// encodeLayer encodes entries as a tar or gzip-compressed tar blob.
func encodeLayer(t *testing.T, layer layerSpec) []byte {
	t.Helper()

	var payload []byte
	if layer.raw {
		payload = encodeRawTar(t, layer.entries)
	} else {
		payload = encodeTar(t, layer.entries)
	}

	if layer.media == ocispec.MediaTypeImageLayerGzip || strings.HasSuffix(layer.media, "+gzip") {
		var compressed bytes.Buffer
		gzipWriter := gzip.NewWriter(&compressed)
		_, err := gzipWriter.Write(payload)
		require.NoError(t, err)
		require.NoError(t, gzipWriter.Close())

		return compressed.Bytes()
	}

	return payload
}

// encodeTar writes entries with [tar.Writer].
func encodeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for _, entry := range entries {
		header := tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Uid:      entry.uid,
			Gid:      entry.gid,
			Size:     declaredSize(entry),
			Typeflag: entry.typ,
		}
		if header.Typeflag == 0 {
			header.Typeflag = tar.TypeReg
		}
		if header.Typeflag != tar.TypeReg {
			header.Size = 0
		}
		require.NoError(t, writer.WriteHeader(&header))
		if header.Typeflag == tar.TypeReg {
			_, err := writer.Write(entry.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())

	return buf.Bytes()
}

// encodeRawTar writes ustar headers without normalizing Typeflag or Size.
func encodeRawTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	for _, entry := range entries {
		_, err := buf.Write(ustarHeader(t, entry))
		require.NoError(t, err)
		if isRegularTarType(entry.typ) {
			_, err = buf.Write(entry.body)
			require.NoError(t, err)
			pad := (512 - len(entry.body)%512) % 512
			_, err = buf.Write(make([]byte, pad))
			require.NoError(t, err)
		}
	}
	_, err := buf.Write(make([]byte, 1024))
	require.NoError(t, err)

	return buf.Bytes()
}

// ustarHeader encodes one 512-byte ustar header for entry.
func ustarHeader(t *testing.T, entry tarEntry) []byte {
	t.Helper()

	require.LessOrEqual(t, len(entry.name), 100)
	block := make([]byte, 512)
	copy(block[0:], entry.name)
	copy(block[100:], fmt.Sprintf("%07o", entry.mode))
	copy(block[108:], fmt.Sprintf("%07o", entry.uid))
	copy(block[116:], fmt.Sprintf("%07o", entry.gid))
	copy(block[124:], fmt.Sprintf("%011o", declaredSize(entry)))
	copy(block[136:], fmt.Sprintf("%011o", 0))
	copy(block[148:], "        ")
	typ := entry.typ
	if typ == 0 {
		typ = tar.TypeReg
	}
	block[156] = typ
	copy(block[257:], "ustar")
	copy(block[263:], "00")
	var sum int
	for _, b := range block {
		sum += int(b)
	}
	copy(block[148:], fmt.Sprintf("%06o", sum))
	block[154] = 0
	block[155] = ' '

	return block
}

// declaredSize is the tar Size written for entry.
func declaredSize(entry tarEntry) int64 {
	if entry.size != 0 {
		return entry.size
	}

	return int64(len(entry.body))
}

// isRegularTarType reports whether typ carries a regular-file payload.
func isRegularTarType(typ byte) bool {
	return typ == 0 || typ == tar.TypeReg
}

// putBlob stores data under blobs/sha256 and returns its descriptor.
func putBlob(t *testing.T, files fstest.MapFS, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()

	digest := digestOf(t, data)
	files[blobFile(digest)] = &fstest.MapFile{Data: data}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    godigest.Digest(digest.String()),
		Size:      int64(len(data)),
	}
}

// blobSize returns the stored size of digest in files.
func blobSize(t *testing.T, files fstest.MapFS, digest rel.Digest) int64 {
	t.Helper()

	file, ok := files[blobFile(digest)]
	require.True(t, ok)

	return int64(len(file.Data))
}

// blobFile returns the layout path of digest.
func blobFile(digest rel.Digest) string {
	return path.Join("blobs/sha256", strings.TrimPrefix(digest.String(), "sha256:"))
}

// deleteFile returns a copy of files without name.
func deleteFile(files fstest.MapFS, name string) fstest.MapFS {
	out := cloneMapFS(files)
	delete(out, name)

	return out
}

// cloneMapFS copies files into a new map.
func cloneMapFS(files fstest.MapFS) fstest.MapFS {
	out := make(fstest.MapFS, len(files))
	for name, file := range files {
		copied := *file
		copied.Data = slices.Clone(file.Data)
		out[name] = &copied
	}

	return out
}

// cloneStringMap copies values into a new map.
func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	maps.Copy(out, values)

	return out
}

// expectedImage returns the [image.ExpectedImage] that matches a default layout.
func expectedImage(t *testing.T) image.ExpectedImage {
	t.Helper()

	version, err := rel.ParseVersion(testVersion)
	require.NoError(t, err)

	return image.ExpectedImage{
		Version:  version,
		Binaries: []string{testBinaryName},
		Revision: testCommit,
		Source:   testSourceURL,
		Canonical: map[image.APKArch]map[string]rel.Digest{
			image.ArchX8664:   {testBinaryName: digestOf(t, []byte(testAMD64Binary))},
			image.ArchAArch64: {testBinaryName: digestOf(t, []byte(testARM64Binary))},
		},
	}
}
