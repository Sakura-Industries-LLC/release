package puboci_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"

	godigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// testOCISchemaVersion is the OCI image-spec schemaVersion used in fixtures.
	testOCISchemaVersion = 2
	// testLayoutJSON is a valid oci-layout marker document.
	testLayoutJSON = `{"imageLayoutVersion":"1.0.0"}`
	// testAMD64Config is a distinct amd64 config blob.
	testAMD64Config = `{"architecture":"amd64","os":"linux"}`
	// testARM64Config is a distinct arm64 config blob.
	testARM64Config = `{"architecture":"arm64","os":"linux"}`
	// testAMD64Layer is a distinct amd64 layer blob.
	testAMD64Layer = "amd64-layer"
	// testARM64Layer is a distinct arm64 layer blob.
	testARM64Layer = "arm64-layer"
	// testSharedLayer is a layer blob referenced by both platforms.
	testSharedLayer = "shared-layer"
	// testBytesPerKiB is the number of bytes in a kibibyte.
	testBytesPerKiB = 1024
	// testKibibytesPerMiB is the number of kibibytes in a mebibyte.
	testKibibytesPerMiB = 1024
	// testJSONLimitMiB matches the package JSON document bound.
	testJSONLimitMiB = 4
	// testJSONLimitBytes is the maximum encoded JSON document ReadLayout buffers.
	testJSONLimitBytes int64 = testJSONLimitMiB * testBytesPerKiB * testKibibytesPerMiB
	// testOverJSONLimitBytes is one byte past the JSON document bound.
	testOverJSONLimitBytes = testJSONLimitBytes + 1
)

func TestReadLayoutTwoPlatforms(t *testing.T) {
	t.Parallel()

	fixture := newTwoPlatformLayout(t, []byte(testAMD64Layer), []byte(testARM64Layer))

	got, err := puboci.ReadLayout(fixture.files)
	require.NoError(t, err)

	assert.Equal(t, ocispec.MediaTypeImageIndex, got.Index.MediaType)
	assert.Equal(t, digestOf(t, fixture.indexBytes), got.Index.Digest)
	assert.Equal(t, int64(len(fixture.indexBytes)), got.Index.Size)
	assert.Equal(t, fixture.indexBytes, got.IndexBytes)
	assert.Equal(t, []puboci.PlatformImage{fixture.amd64, fixture.arm64}, got.Platforms)
	assert.Equal(t, fixture.blobs, got.Blobs)
}

func TestReadLayoutSharedLayer(t *testing.T) {
	t.Parallel()

	shared := []byte(testSharedLayer)
	fixture := newTwoPlatformLayout(t, shared, shared)

	got, err := puboci.ReadLayout(fixture.files)
	require.NoError(t, err)

	assert.Equal(t, fixture.blobs, got.Blobs)
	require.Len(t, got.Blobs, 3)
	assert.Equal(t, digestOf(t, shared), got.Blobs[1].Digest)
}

func TestReadLayoutErrors(t *testing.T) {
	t.Parallel()

	valid := newTwoPlatformLayout(t, []byte(testAMD64Layer), []byte(testARM64Layer))
	manifest := descriptorFor(t, ocispec.MediaTypeImageManifest, []byte(`{"schemaVersion":2}`))
	missing := puboci.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    mustDigest(t, validDigest),
		Size:      1,
	}
	oversized := manifest
	oversized.Size++

	tests := []struct {
		name    string
		files   fs.FS
		wantErr string
	}{
		{
			name:    "missing oci-layout",
			files:   fstest.MapFS{indexFileName(): {Data: valid.indexBytes}},
			wantErr: "oci-layout",
		},
		{
			name:    "missing index.json",
			files:   fstest.MapFS{layoutFileName(): {Data: []byte(testLayoutJSON)}},
			wantErr: "index.json",
		},
		{
			name: "malformed JSON",
			files: fstest.MapFS{
				layoutFileName(): {Data: []byte(testLayoutJSON)},
				indexFileName():  {Data: []byte("{")},
			},
			wantErr: "index.json",
		},
		{
			name: "wrong schemaVersion",
			files: fstest.MapFS{
				layoutFileName(): {Data: []byte(testLayoutJSON)},
				indexFileName(): {Data: encodeIndex(t, ocispec.Index{
					Versioned: specs.Versioned{SchemaVersion: 1},
					MediaType: ocispec.MediaTypeImageIndex,
					Manifests: []ocispec.Descriptor{ociDescriptor(manifest, linuxAMD64())},
				})},
			},
			wantErr: "schemaVersion is 1, want 2",
		},
		{
			name: "wrong index media type",
			files: fstest.MapFS{
				layoutFileName(): {Data: []byte(testLayoutJSON)},
				indexFileName(): {Data: encodeIndex(t, ocispec.Index{
					Versioned: specs.Versioned{SchemaVersion: testOCISchemaVersion},
					MediaType: ocispec.MediaTypeImageManifest,
					Manifests: []ocispec.Descriptor{ociDescriptor(manifest, linuxAMD64())},
				})},
			},
			wantErr: "mediaType is \"" + ocispec.MediaTypeImageManifest + "\"",
		},
		{
			name: "zero manifests",
			files: fstest.MapFS{
				layoutFileName(): {Data: []byte(testLayoutJSON)},
				indexFileName(): {Data: encodeIndex(t, ocispec.Index{
					Versioned: specs.Versioned{SchemaVersion: testOCISchemaVersion},
					MediaType: ocispec.MediaTypeImageIndex,
					Manifests: []ocispec.Descriptor{},
				})},
			},
			wantErr: "has no manifests",
		},
		{
			name: "missing manifest blob",
			files: fstest.MapFS{
				layoutFileName(): {Data: []byte(testLayoutJSON)},
				indexFileName(): {Data: encodeIndex(t, ocispec.Index{
					Versioned: specs.Versioned{SchemaVersion: testOCISchemaVersion},
					MediaType: ocispec.MediaTypeImageIndex,
					Manifests: []ocispec.Descriptor{ociDescriptor(missing, linuxAMD64())},
				})},
			},
			wantErr: blobFile(t, missing.Digest),
		},
		{
			name: "declared size differs",
			files: fstest.MapFS{
				layoutFileName(): {Data: []byte(testLayoutJSON)},
				indexFileName(): {
					Data: encodeIndexFrom(t, indexEntry{desc: oversized, platform: linuxAMD64()}),
				},
				blobFile(t, manifest.Digest): {Data: []byte(`{"schemaVersion":2}`)},
			},
			wantErr: "declared",
		},
		{
			name:    "conflicting blob size",
			files:   conflictingLayerLayout(t),
			wantErr: "conflicting descriptors",
		},
		{
			name:    "missing platform",
			files:   missingPlatformLayout(t, manifest),
			wantErr: "platform is missing",
		},
		{
			name:    "oversized index.json",
			files:   oversizedIndexLayout(t),
			wantErr: "exceeds the",
		},
		{
			name:    "oversized platform manifest",
			files:   oversizedManifestLayout(t, manifest),
			wantErr: "exceeds the",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := puboci.ReadLayout(test.files)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestBlobPath(t *testing.T) {
	t.Parallel()

	digest := mustDigest(t, validDigest)
	got, err := puboci.BlobPath(digest)
	require.NoError(t, err)
	assert.Equal(t, "blobs/sha256/"+validHex(), got)
	assert.True(t, fs.ValidPath(got))

	_, err = puboci.BlobPath("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest")
}

// layoutFixture is a two-platform OCI layout and the descriptors it contains.
type layoutFixture struct {
	// files is the extracted layout directory.
	files fstest.MapFS
	// indexBytes is the exact index.json contents.
	indexBytes []byte
	// amd64 is the first platform listed by the index.
	amd64 puboci.PlatformImage
	// arm64 is the second platform listed by the index.
	arm64 puboci.PlatformImage
	// blobs is the expected first-seen config and layer push order.
	blobs []puboci.Descriptor
}

// newTwoPlatformLayout builds linux/amd64 then linux/arm64 with the given layers.
func newTwoPlatformLayout(t *testing.T, amd64Layer, arm64Layer []byte) layoutFixture {
	t.Helper()

	amd64Config := descriptorFor(t, ocispec.MediaTypeImageConfig, []byte(testAMD64Config))
	arm64Config := descriptorFor(t, ocispec.MediaTypeImageConfig, []byte(testARM64Config))
	amd64LayerDesc := descriptorFor(t, ocispec.MediaTypeImageLayer, amd64Layer)
	arm64LayerDesc := descriptorFor(t, ocispec.MediaTypeImageLayer, arm64Layer)
	amd64Manifest := manifestFor(amd64Config, amd64LayerDesc)
	arm64Manifest := manifestFor(arm64Config, arm64LayerDesc)
	amd64ManifestBytes := encodeManifest(t, amd64Manifest)
	arm64ManifestBytes := encodeManifest(t, arm64Manifest)
	amd64Image := puboci.PlatformImage{
		Descriptor: descriptorFor(t, ocispec.MediaTypeImageManifest, amd64ManifestBytes),
		Platform:   puboci.Platform{OS: "linux", Architecture: "amd64"},
	}
	arm64Image := puboci.PlatformImage{
		Descriptor: descriptorFor(t, ocispec.MediaTypeImageManifest, arm64ManifestBytes),
		Platform:   puboci.Platform{OS: "linux", Architecture: "arm64"},
	}
	indexBytes := encodeIndexFrom(t,
		indexEntry{desc: amd64Image.Descriptor, platform: linuxAMD64()},
		indexEntry{desc: arm64Image.Descriptor, platform: linuxARM64()},
	)

	files := fstest.MapFS{
		layoutFileName(): {Data: []byte(testLayoutJSON)},
		indexFileName():  {Data: indexBytes},
		blobFile(t, amd64Image.Descriptor.Digest): {Data: amd64ManifestBytes},
		blobFile(t, arm64Image.Descriptor.Digest): {Data: arm64ManifestBytes},
		blobFile(t, amd64Config.Digest):           {Data: []byte(testAMD64Config)},
		blobFile(t, arm64Config.Digest):           {Data: []byte(testARM64Config)},
		blobFile(t, amd64LayerDesc.Digest):        {Data: amd64Layer},
	}
	if amd64LayerDesc.Digest != arm64LayerDesc.Digest {
		files[blobFile(t, arm64LayerDesc.Digest)] = &fstest.MapFile{Data: arm64Layer}
	}

	blobs := []puboci.Descriptor{amd64Config, amd64LayerDesc}
	if arm64Config.Digest != amd64Config.Digest {
		blobs = append(blobs, arm64Config)
	}
	if arm64LayerDesc.Digest != amd64LayerDesc.Digest {
		blobs = append(blobs, arm64LayerDesc)
	}

	return layoutFixture{
		files:      files,
		indexBytes: indexBytes,
		amd64:      amd64Image,
		arm64:      arm64Image,
		blobs:      blobs,
	}
}

// conflictingLayerLayout is a two-platform layout whose shared layer digest
// is declared with two different sizes.
func conflictingLayerLayout(t *testing.T) fstest.MapFS {
	t.Helper()

	shared := []byte(testSharedLayer)
	amd64Config := descriptorFor(t, ocispec.MediaTypeImageConfig, []byte(testAMD64Config))
	arm64Config := descriptorFor(t, ocispec.MediaTypeImageConfig, []byte(testARM64Config))
	sharedLayer := descriptorFor(t, ocispec.MediaTypeImageLayer, shared)
	conflictLayer := sharedLayer
	conflictLayer.Size++
	amd64ManifestBytes := encodeManifest(t, manifestFor(amd64Config, sharedLayer))
	arm64ManifestBytes := encodeManifest(t, manifestFor(arm64Config, conflictLayer))
	amd64Manifest := descriptorFor(t, ocispec.MediaTypeImageManifest, amd64ManifestBytes)
	arm64Manifest := descriptorFor(t, ocispec.MediaTypeImageManifest, arm64ManifestBytes)

	return fstest.MapFS{
		layoutFileName(): {Data: []byte(testLayoutJSON)},
		indexFileName(): {Data: encodeIndexFrom(t,
			indexEntry{desc: amd64Manifest, platform: linuxAMD64()},
			indexEntry{desc: arm64Manifest, platform: linuxARM64()},
		)},
		blobFile(t, amd64Manifest.Digest): {Data: amd64ManifestBytes},
		blobFile(t, arm64Manifest.Digest): {Data: arm64ManifestBytes},
		blobFile(t, amd64Config.Digest):   {Data: []byte(testAMD64Config)},
		blobFile(t, arm64Config.Digest):   {Data: []byte(testARM64Config)},
		blobFile(t, sharedLayer.Digest):   {Data: shared},
	}
}

// missingPlatformLayout is an index whose only manifest has no platform.
func missingPlatformLayout(t *testing.T, manifest puboci.Descriptor) fstest.MapFS {
	t.Helper()

	return fstest.MapFS{
		layoutFileName(): {Data: []byte(testLayoutJSON)},
		indexFileName(): {Data: encodeIndex(t, ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: testOCISchemaVersion},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: []ocispec.Descriptor{ociDescriptor(manifest, nil)},
		})},
	}
}

// oversizedIndexLayout reports an index.json larger than the JSON bound.
func oversizedIndexLayout(t *testing.T) fs.FS {
	t.Helper()

	return withReportedSize(fstest.MapFS{
		layoutFileName(): {Data: []byte(testLayoutJSON)},
		indexFileName():  {Data: []byte(`{}`)},
	}, indexFileName(), testOverJSONLimitBytes)
}

// oversizedManifestLayout points a valid index at a too-large platform manifest.
func oversizedManifestLayout(t *testing.T, manifest puboci.Descriptor) fs.FS {
	t.Helper()

	desc := manifest
	desc.Size = testOverJSONLimitBytes

	return withReportedSize(fstest.MapFS{
		layoutFileName(): {Data: []byte(testLayoutJSON)},
		indexFileName(): {
			Data: encodeIndexFrom(t, indexEntry{desc: desc, platform: linuxAMD64()}),
		},
		blobFile(t, desc.Digest): {Data: []byte(`{"schemaVersion":2}`)},
	}, blobFile(t, desc.Digest), testOverJSONLimitBytes)
}

// withReportedSize reports size for name without allocating that many bytes.
func withReportedSize(base fstest.MapFS, name string, size int64) fs.FS {
	return sizedFS{MapFS: base, sizes: map[string]int64{name: size}}
}

// sizedFS is a MapFS that lies about selected file sizes.
type sizedFS struct {
	// MapFS is the underlying layout files.
	fstest.MapFS

	// sizes overrides Stat size for named files.
	sizes map[string]int64
}

// Open returns a file whose Stat reports any overridden size.
func (s sizedFS) Open(name string) (fs.File, error) {
	file, err := s.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	if size, ok := s.sizes[name]; ok {
		return sizedFile{File: file, size: size}, nil
	}

	return file, nil
}

// Stat reports any overridden size for name.
func (s sizedFS) Stat(name string) (fs.FileInfo, error) {
	info, err := s.MapFS.Stat(name)
	if err != nil {
		return nil, err
	}
	if size, ok := s.sizes[name]; ok {
		return sizedInfo{FileInfo: info, size: size}, nil
	}

	return info, nil
}

// sizedFile is a file whose Stat reports a fixed size.
type sizedFile struct {
	// File is the opened underlying file.
	fs.File

	// size is the reported content length.
	size int64
}

// Stat returns the overridden size.
func (f sizedFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}

	return sizedInfo{FileInfo: info, size: f.size}, nil
}

// sizedInfo is a FileInfo with a replaced Size.
type sizedInfo struct {
	// FileInfo is the underlying file metadata.
	fs.FileInfo

	// size is the reported content length.
	size int64
}

// Size returns the overridden content length.
func (i sizedInfo) Size() int64 {
	return i.size
}

// indexEntry is one index manifest descriptor and its platform.
type indexEntry struct {
	// desc is the platform manifest descriptor.
	desc puboci.Descriptor
	// platform is the OS and architecture recorded on that descriptor.
	platform *ocispec.Platform
}

// encodeIndexFrom marshals an index listing the given platform manifests.
func encodeIndexFrom(t *testing.T, entries ...indexEntry) []byte {
	t.Helper()

	manifests := make([]ocispec.Descriptor, 0, len(entries))
	for _, entry := range entries {
		manifests = append(manifests, ociDescriptor(entry.desc, entry.platform))
	}

	return encodeIndex(t, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: testOCISchemaVersion},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	})
}

// encodeIndex marshals index as JSON.
func encodeIndex(t *testing.T, index ocispec.Index) []byte {
	t.Helper()

	data, err := json.Marshal(index)
	require.NoError(t, err)

	return data
}

// encodeManifest marshals manifest as JSON.
func encodeManifest(t *testing.T, manifest ocispec.Manifest) []byte {
	t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	return data
}

// manifestFor returns a one-layer image manifest.
func manifestFor(config puboci.Descriptor, layer puboci.Descriptor) ocispec.Manifest {
	return ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: testOCISchemaVersion},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ociDescriptor(config, nil),
		Layers:    []ocispec.Descriptor{ociDescriptor(layer, nil)},
	}
}

// ociDescriptor converts desc into an OCI descriptor with an optional platform.
func ociDescriptor(desc puboci.Descriptor, platform *ocispec.Platform) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: desc.MediaType,
		Digest:    godigest.Digest(desc.Digest.String()),
		Size:      desc.Size,
		Platform:  platform,
	}
}

// descriptorFor returns the descriptor of data at mediaType.
func descriptorFor(t *testing.T, mediaType string, data []byte) puboci.Descriptor {
	t.Helper()

	return puboci.Descriptor{
		MediaType: mediaType,
		Digest:    digestOf(t, data),
		Size:      int64(len(data)),
	}
}

// digestOf returns the sha256 digest of data.
func digestOf(t *testing.T, data []byte) rel.Digest {
	t.Helper()

	sum := sha256.Sum256(data)

	return mustDigest(t, "sha256:"+hex.EncodeToString(sum[:]))
}

// blobFile returns the layout path of digest.
func blobFile(t *testing.T, digest rel.Digest) string {
	t.Helper()

	name, err := puboci.BlobPath(digest)
	require.NoError(t, err)

	return name
}

// linuxAMD64 is the linux/amd64 platform.
func linuxAMD64() *ocispec.Platform {
	return &ocispec.Platform{OS: "linux", Architecture: "amd64"}
}

// linuxARM64 is the linux/arm64 platform.
func linuxARM64() *ocispec.Platform {
	return &ocispec.Platform{OS: "linux", Architecture: "arm64"}
}

// layoutFileName is the OCI layout marker path.
func layoutFileName() string {
	return "oci-layout"
}

// indexFileName is the OCI index path.
func indexFileName() string {
	return "index.json"
}

// validHex is the hex part of validDigest.
func validHex() string {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
