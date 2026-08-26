package image

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/meigma/release/internal/rel"
)

const (
	// VerifySchema is the versioned image-verify result identifier.
	VerifySchema = "release.dev/image-verify/v2"
	// bytesPerKiB is the number of bytes in a kibibyte.
	bytesPerKiB = 1024
	// kibibytesPerMiB is the number of kibibytes in a mebibyte.
	kibibytesPerMiB = 1024
	// jsonLimitMiB is the JSON document size bound in mebibytes.
	jsonLimitMiB = 4
	// jsonLimitBytes is the maximum encoded JSON document this package buffers.
	//
	// [ReadLayout] applies the bound to index.json and platform manifests.
	// [VerifyLayout] applies it to config blobs. [VerifySBOMs] applies it to
	// SPDX documents. Layer blobs are never read into memory.
	jsonLimitBytes int64 = jsonLimitMiB * bytesPerKiB * kibibytesPerMiB
	// ociSchemaVersion is the OCI image-spec schemaVersion for an index or manifest.
	ociSchemaVersion = 2
	// layoutFileName is the OCI layout marker file.
	layoutFileName = "oci-layout"
	// indexFileName is the OCI image index document.
	indexFileName = "index.json"
	// blobDirectory is the OCI layout blob root.
	blobDirectory = "blobs"
	// blobAlgorithm is the only supported blob digest algorithm.
	blobAlgorithm = "sha256"
	// digestPrefix is the canonical sha256: digest prefix.
	digestPrefix = blobAlgorithm + ":"
	// requiredManifestCount is the closed number of platform manifests.
	requiredManifestCount = 2
	// requiredLayerCount is the number of layers each platform manifest must name.
	requiredLayerCount = 1
)

// Layout is a validated on-disk OCI layout.
type Layout struct {
	// IndexBytes is the exact index.json contents, retained verbatim.
	IndexBytes []byte
	// IndexDigest is SHA-256 over IndexBytes, never over re-marshaled JSON.
	IndexDigest rel.Digest
	// Annotations are the index annotations as they appear on disk.
	Annotations map[string]string
	// Platforms are the index manifests in file order.
	Platforms []LayoutPlatform
}

// LayoutPlatform is one platform manifest plus the blobs it names.
type LayoutPlatform struct {
	// Platform is the Linux OCI platform recorded on the index descriptor.
	Platform Platform
	// Manifest is the digest of the platform manifest blob.
	Manifest rel.Digest
	// Config is the digest of the image config blob.
	Config rel.Digest
	// Layer is the digest of the single layer blob.
	Layer rel.Digest
	// LayerMedia is the layer descriptor media type.
	LayerMedia string
	// Annotations are the manifest annotations as they appear on disk.
	Annotations map[string]string
}

// layoutDescriptor is a size-checked blob named by digest.
type layoutDescriptor struct {
	// digest is the content digest recorded on the parent document.
	digest rel.Digest
	// size is the declared blob size in bytes.
	size int64
	// mediaType is the declared blob media type.
	mediaType string
}

// ReadLayout loads a two-platform OCI layout from fsys.
//
// fsys is a [fs.FS] rooted at the layout directory. A regular oci-layout
// file must exist. index.json is read verbatim; [Layout.IndexDigest] is
// SHA-256 over those exact bytes and is never computed from re-marshaled
// JSON. The index must use schemaVersion 2 and media type
// [ocispec.MediaTypeImageIndex] and must list exactly two manifests whose
// platforms are linux/amd64 and linux/arm64 in any order. Each manifest
// descriptor is validated and its blob must exist as a regular file of the
// declared size. The manifest must use schemaVersion 2 and media type
// [ocispec.MediaTypeImageManifest] and must name exactly one layer. The
// config and layer blobs must exist as regular files of the declared size
// and are not buffered. index.json and manifests are buffered up to
// [jsonLimitBytes].
//
// ReadLayout does not compare annotations, labels, or layer bytes to an
// [ExpectedImage]. Call [VerifyLayout] for those checks.
func ReadLayout(fsys fs.FS) (Layout, error) {
	if fsys == nil {
		return Layout{}, errors.New("layout filesystem is nil")
	}
	if err := requireRegularFile(fsys, layoutFileName); err != nil {
		return Layout{}, err
	}

	indexBytes, err := readJSONDocument(fsys, indexFileName)
	if err != nil {
		return Layout{}, err
	}
	index, digest, err := parseIndex(indexBytes)
	if err != nil {
		return Layout{}, err
	}
	if err := checkIndexPlatforms(index.Manifests); err != nil {
		return Layout{}, err
	}

	platforms := make([]LayoutPlatform, 0, len(index.Manifests))
	for i, desc := range index.Manifests {
		platform, err := readLayoutPlatform(fsys, desc)
		if err != nil {
			return Layout{}, fmt.Errorf("%s manifests[%d]: %w", indexFileName, i, err)
		}
		platforms = append(platforms, platform)
	}

	return Layout{
		IndexBytes:  indexBytes,
		IndexDigest: digest,
		Annotations: cloneAnnotations(index.Annotations),
		Platforms:   platforms,
	}, nil
}

// parseIndex decodes and validates index.json bytes without rewriting them.
func parseIndex(indexBytes []byte) (ocispec.Index, rel.Digest, error) {
	var index ocispec.Index
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		return ocispec.Index{}, "", fmt.Errorf("%s: %w", indexFileName, err)
	}
	if index.SchemaVersion != ociSchemaVersion {
		return ocispec.Index{}, "", fmt.Errorf(
			"%s schemaVersion is %d, want %d",
			indexFileName,
			index.SchemaVersion,
			ociSchemaVersion,
		)
	}
	if index.MediaType != ocispec.MediaTypeImageIndex {
		return ocispec.Index{}, "", fmt.Errorf(
			"%s mediaType is %q, want %q",
			indexFileName,
			index.MediaType,
			ocispec.MediaTypeImageIndex,
		)
	}

	digest, err := digestBytes(indexBytes)
	if err != nil {
		return ocispec.Index{}, "", err
	}

	return index, digest, nil
}

// checkIndexPlatforms requires exactly one linux/amd64 and one linux/arm64 manifest.
func checkIndexPlatforms(manifests []ocispec.Descriptor) error {
	if len(manifests) != requiredManifestCount {
		return fmt.Errorf("%s has %d manifests, want %d", indexFileName, len(manifests), requiredManifestCount)
	}

	seen := make(map[Platform]struct{}, len(manifests))
	for i, desc := range manifests {
		platform, err := platformFromOCI(desc.Platform)
		if err != nil {
			return fmt.Errorf("%s manifests[%d]: %w", indexFileName, i, err)
		}
		if _, exists := seen[platform]; exists {
			return fmt.Errorf("%s lists platform %s more than once", indexFileName, platform)
		}
		seen[platform] = struct{}{}
	}

	return nil
}

// readLayoutPlatform validates one index manifest descriptor and the blobs it names.
func readLayoutPlatform(fsys fs.FS, raw ocispec.Descriptor) (LayoutPlatform, error) {
	platform, err := platformFromOCI(raw.Platform)
	if err != nil {
		return LayoutPlatform{}, err
	}
	manifestDesc, err := parseDescriptor(raw)
	if err != nil {
		return LayoutPlatform{}, err
	}
	err = requireBlob(fsys, manifestDesc)
	if err != nil {
		return LayoutPlatform{}, err
	}

	name, err := blobPath(manifestDesc.digest)
	if err != nil {
		return LayoutPlatform{}, err
	}
	body, err := readJSONDocument(fsys, name)
	if err != nil {
		return LayoutPlatform{}, err
	}

	var manifest ocispec.Manifest
	err = json.Unmarshal(body, &manifest)
	if err != nil {
		return LayoutPlatform{}, fmt.Errorf("%s: %w", name, err)
	}
	if manifest.SchemaVersion != ociSchemaVersion {
		return LayoutPlatform{}, fmt.Errorf(
			"%s schemaVersion is %d, want %d",
			name,
			manifest.SchemaVersion,
			ociSchemaVersion,
		)
	}
	if manifest.MediaType != ocispec.MediaTypeImageManifest {
		return LayoutPlatform{}, fmt.Errorf(
			"%s mediaType is %q, want %q",
			name,
			manifest.MediaType,
			ocispec.MediaTypeImageManifest,
		)
	}
	if len(manifest.Layers) != requiredLayerCount {
		return LayoutPlatform{}, fmt.Errorf(
			"%s has %d layers, want %d",
			name,
			len(manifest.Layers),
			requiredLayerCount,
		)
	}

	configDesc, err := parseDescriptor(manifest.Config)
	if err != nil {
		return LayoutPlatform{}, fmt.Errorf("config: %w", err)
	}
	err = requireBlob(fsys, configDesc)
	if err != nil {
		return LayoutPlatform{}, fmt.Errorf("config: %w", err)
	}

	layerDesc, err := parseDescriptor(manifest.Layers[0])
	if err != nil {
		return LayoutPlatform{}, fmt.Errorf("layers[0]: %w", err)
	}
	err = requireBlob(fsys, layerDesc)
	if err != nil {
		return LayoutPlatform{}, fmt.Errorf("layers[0]: %w", err)
	}

	return LayoutPlatform{
		Platform:    platform,
		Manifest:    manifestDesc.digest,
		Config:      configDesc.digest,
		Layer:       layerDesc.digest,
		LayerMedia:  layerDesc.mediaType,
		Annotations: cloneAnnotations(manifest.Annotations),
	}, nil
}

// platformFromOCI copies a required OCI platform into a [Platform].
func platformFromOCI(platform *ocispec.Platform) (Platform, error) {
	if platform == nil {
		return "", errors.New("platform is missing")
	}
	if platform.OS != "linux" {
		return "", fmt.Errorf("platform os is %q, want linux", platform.OS)
	}
	if platform.Architecture != "amd64" && platform.Architecture != "arm64" {
		return "", fmt.Errorf("platform architecture %q is not amd64 or arm64", platform.Architecture)
	}

	return ParsePlatform(platform.OS + "/" + platform.Architecture)
}

// parseDescriptor validates an OCI descriptor's digest, size, and media type.
func parseDescriptor(desc ocispec.Descriptor) (layoutDescriptor, error) {
	digest, err := rel.ParseDigest(desc.Digest.String())
	if err != nil {
		return layoutDescriptor{}, err
	}
	if desc.Size < 0 {
		return layoutDescriptor{}, fmt.Errorf("descriptor %s size is %d", digest, desc.Size)
	}
	if desc.MediaType == "" {
		return layoutDescriptor{}, fmt.Errorf("descriptor %s media type is empty", digest)
	}

	return layoutDescriptor{digest: digest, size: desc.Size, mediaType: desc.MediaType}, nil
}

// blobPath returns the [fs.FS] slash path of digest under blobs/sha256.
func blobPath(digest rel.Digest) (string, error) {
	parsed, err := rel.ParseDigest(digest.String())
	if err != nil {
		return "", err
	}

	hexPart, found := strings.CutPrefix(parsed.String(), digestPrefix)
	if !found || hexPart == "" {
		return "", fmt.Errorf("digest %q is missing hex", parsed)
	}

	name := path.Join(blobDirectory, blobAlgorithm, hexPart)
	if !fs.ValidPath(name) {
		return "", fmt.Errorf("blob path %q is invalid", name)
	}

	return name, nil
}

// requireBlob requires digest's blob file to be regular and of desc.size.
func requireBlob(fsys fs.FS, desc layoutDescriptor) error {
	name, err := blobPath(desc.digest)
	if err != nil {
		return err
	}
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() != desc.size {
		return fmt.Errorf("%s is %d bytes, declared %d", name, info.Size(), desc.size)
	}

	return nil
}

// requireRegularFile requires name to exist as a regular file.
func requireRegularFile(fsys fs.FS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}

	return nil
}

// readJSONDocument reads a regular JSON file of at most [jsonLimitBytes].
func readJSONDocument(fsys fs.FS, name string) ([]byte, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > jsonLimitBytes {
		return nil, fmt.Errorf("%s is %d bytes, exceeds the %d byte JSON limit", name, info.Size(), jsonLimitBytes)
	}

	file, err := fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, jsonLimitBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("%s: short read", name)
	}

	return data, nil
}

// digestBytes returns the sha256 digest of data.
func digestBytes(data []byte) (rel.Digest, error) {
	sum := sha256.Sum256(data)

	return rel.ParseDigest(digestPrefix + hex.EncodeToString(sum[:]))
}

// cloneAnnotations copies annotations so callers cannot alias the parsed map.
func cloneAnnotations(annotations map[string]string) map[string]string {
	if annotations == nil {
		return map[string]string{}
	}

	return maps.Clone(annotations)
}
