package puboci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/meigma/release/internal/rel"
)

const (
	// bytesPerKiB is the number of bytes in a kibibyte.
	bytesPerKiB = 1024
	// kibibytesPerMiB is the number of kibibytes in a mebibyte.
	kibibytesPerMiB = 1024
	// jsonLimitMiB is the JSON document size bound in mebibytes.
	jsonLimitMiB = 4
	// jsonLimitBytes is the maximum encoded JSON document this package buffers.
	//
	// [ReadLayout] applies the bound to index.json and platform manifests.
	// [ParsePrepareResult] applies it to a prepare-result document. Layer
	// and config blobs are never read into memory.
	jsonLimitBytes int64 = jsonLimitMiB * bytesPerKiB * kibibytesPerMiB
	// ociSchemaVersion is the OCI image-spec schemaVersion for an index.
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
)

// Platform is an OCI image OS and architecture pair.
type Platform struct {
	// OS is the image operating system, such as linux.
	OS string
	// Architecture is the image CPU architecture, such as amd64.
	Architecture string
}

// PlatformImage is one platform manifest listed by an image index.
type PlatformImage struct {
	// Descriptor is the index's descriptor for this platform manifest.
	Descriptor Descriptor
	// Platform is the OS and architecture recorded on that descriptor.
	Platform Platform
}

// Layout is a validated local OCI image layout rooted at oci-image/layout.
type Layout struct {
	// Index is the descriptor of the exact index.json bytes.
	Index Descriptor
	// IndexBytes is the exact index.json contents, retained for push.
	IndexBytes []byte
	// Platforms are the index manifests in file order.
	Platforms []PlatformImage
	// Blobs are unique config and layer descriptors in first-seen push order.
	Blobs []Descriptor
}

// String returns os/architecture.
func (p Platform) String() string {
	return p.OS + "/" + p.Architecture
}

// ReadLayout loads an extracted oci-image/layout directory from fsys.
//
// fsys is a [fs.FS] rooted at the layout directory. A regular oci-layout
// file must exist. index.json is read verbatim; its descriptor digest is
// SHA-256 over those exact bytes and its size is their length. The index
// must use schemaVersion 2 and media type [ocispec.MediaTypeImageIndex] and
// must list at least one manifest. Each manifest descriptor is validated
// and must name a platform with a non-empty OS and architecture. Its blob
// must exist as a regular file of the declared size, and its config and
// layer blobs are collected the same way. Duplicate digests keep the
// first descriptor; a later descriptor with a different size or media
// type is an error. Layer and config blobs are never buffered.
// index.json and manifests are buffered up to [jsonLimitBytes].
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
	index, indexDesc, err := parseIndex(indexBytes)
	if err != nil {
		return Layout{}, err
	}

	blobs := newBlobCollector()
	platforms := make([]PlatformImage, 0, len(index.Manifests))
	for i, desc := range index.Manifests {
		platform, err := readPlatform(fsys, desc, blobs)
		if err != nil {
			return Layout{}, fmt.Errorf("%s manifests[%d]: %w", indexFileName, i, err)
		}
		platforms = append(platforms, platform)
	}

	return Layout{
		Index:      indexDesc,
		IndexBytes: indexBytes,
		Platforms:  platforms,
		Blobs:      blobs.blobs,
	}, nil
}

// BlobPath returns the [fs.FS] slash path of digest under blobs/sha256.
//
// The digest must parse as sha256:<64 hex>. The result is valid for
// [fs.ValidPath].
func BlobPath(digest rel.Digest) (string, error) {
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

// blobCollector records unique config and layer descriptors in first-seen order.
type blobCollector struct {
	// blobs is the push-order list of unique descriptors.
	blobs []Descriptor
	// seen maps digest to the first descriptor recorded for it.
	seen map[rel.Digest]Descriptor
}

// newBlobCollector constructs an empty first-seen blob list.
func newBlobCollector() *blobCollector {
	return &blobCollector{seen: make(map[rel.Digest]Descriptor)}
}

// parseIndex decodes and validates index.json bytes without rewriting them.
func parseIndex(indexBytes []byte) (ocispec.Index, Descriptor, error) {
	var index ocispec.Index
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		return ocispec.Index{}, Descriptor{}, fmt.Errorf("%s: %w", indexFileName, err)
	}
	if index.SchemaVersion != ociSchemaVersion {
		return ocispec.Index{}, Descriptor{}, fmt.Errorf(
			"%s schemaVersion is %d, want %d",
			indexFileName,
			index.SchemaVersion,
			ociSchemaVersion,
		)
	}
	if index.MediaType != ocispec.MediaTypeImageIndex {
		return ocispec.Index{}, Descriptor{}, fmt.Errorf(
			"%s mediaType is %q, want %q",
			indexFileName,
			index.MediaType,
			ocispec.MediaTypeImageIndex,
		)
	}
	if len(index.Manifests) == 0 {
		return ocispec.Index{}, Descriptor{}, fmt.Errorf("%s has no manifests", indexFileName)
	}

	digest, err := digestBytes(indexBytes)
	if err != nil {
		return ocispec.Index{}, Descriptor{}, err
	}

	return index, Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest,
		Size:      int64(len(indexBytes)),
	}, nil
}

// readPlatform validates one index manifest descriptor and collects its blobs.
func readPlatform(fsys fs.FS, raw ocispec.Descriptor, blobs *blobCollector) (PlatformImage, error) {
	desc, err := descriptorFromOCI(raw)
	if err != nil {
		return PlatformImage{}, err
	}
	platform, err := requiredPlatform(raw.Platform)
	if err != nil {
		return PlatformImage{}, err
	}
	if blobErr := requireBlob(fsys, desc); blobErr != nil {
		return PlatformImage{}, blobErr
	}

	name, err := BlobPath(desc.Digest)
	if err != nil {
		return PlatformImage{}, err
	}
	body, err := readJSONDocument(fsys, name)
	if err != nil {
		return PlatformImage{}, err
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return PlatformImage{}, fmt.Errorf("%s: %w", name, err)
	}
	if err := collectBlobs(fsys, manifest, blobs); err != nil {
		return PlatformImage{}, err
	}

	return PlatformImage{Descriptor: desc, Platform: platform}, nil
}

// collectBlobs records a manifest's config and layer descriptors in push order.
func collectBlobs(fsys fs.FS, manifest ocispec.Manifest, blobs *blobCollector) error {
	config, err := descriptorFromOCI(manifest.Config)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := addUniqueBlob(fsys, blobs, config); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	for i, layer := range manifest.Layers {
		desc, err := descriptorFromOCI(layer)
		if err != nil {
			return fmt.Errorf("layers[%d]: %w", i, err)
		}
		if err := addUniqueBlob(fsys, blobs, desc); err != nil {
			return fmt.Errorf("layers[%d]: %w", i, err)
		}
	}

	return nil
}

// addUniqueBlob records desc if new and requires its blob file on first sight.
func addUniqueBlob(fsys fs.FS, blobs *blobCollector, desc Descriptor) error {
	if existing, ok := blobs.seen[desc.Digest]; ok {
		if existing.Size != desc.Size || existing.MediaType != desc.MediaType {
			return fmt.Errorf("digest %s has conflicting descriptors", desc.Digest)
		}

		return nil
	}
	if err := requireBlob(fsys, desc); err != nil {
		return err
	}
	blobs.seen[desc.Digest] = desc
	blobs.blobs = append(blobs.blobs, desc)

	return nil
}

// requireBlob requires digest's blob file to be regular and of desc.Size.
func requireBlob(fsys fs.FS, desc Descriptor) error {
	name, err := BlobPath(desc.Digest)
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
	if info.Size() != desc.Size {
		return fmt.Errorf("%s is %d bytes, declared %d", name, info.Size(), desc.Size)
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

// descriptorFromOCI converts an OCI descriptor after validating it.
func descriptorFromOCI(desc ocispec.Descriptor) (Descriptor, error) {
	digest, err := rel.ParseDigest(desc.Digest.String())
	if err != nil {
		return Descriptor{}, err
	}

	out := Descriptor{MediaType: desc.MediaType, Digest: digest, Size: desc.Size}
	if err := out.Validate(); err != nil {
		return Descriptor{}, err
	}

	return out, nil
}

// requiredPlatform copies a required OCI platform into a [Platform].
func requiredPlatform(platform *ocispec.Platform) (Platform, error) {
	if platform == nil {
		return Platform{}, errors.New("platform is missing")
	}
	if platform.OS == "" {
		return Platform{}, errors.New("platform os is empty")
	}
	if platform.Architecture == "" {
		return Platform{}, errors.New("platform architecture is empty")
	}

	return Platform{OS: platform.OS, Architecture: platform.Architecture}, nil
}

// digestBytes returns the sha256 digest of data.
func digestBytes(data []byte) (rel.Digest, error) {
	sum := sha256.Sum256(data)

	return rel.ParseDigest(digestPrefix + hex.EncodeToString(sum[:]))
}
