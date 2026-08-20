package image

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/meigma/release/internal/rel"
)

const (
	// annotationDescription is the OCI description annotation key.
	annotationDescription = "org.opencontainers.image.description"
	// annotationLicenses is the OCI licenses annotation key.
	annotationLicenses = "org.opencontainers.image.licenses"
	// annotationSource is the OCI source annotation key.
	annotationSource = "org.opencontainers.image.source"
	// annotationTitle is the OCI title annotation key.
	annotationTitle = "org.opencontainers.image.title"
	// expectedConfigUser is the numeric non-root user written into every image config.
	expectedConfigUser = "65532"
	// sbomVersionSuffix is the Melange package revision suffix on SPDX versionInfo.
	sbomVersionSuffix = "-r0"
	// applicationPurpose is the SPDX primaryPackagePurpose of the staged binary.
	applicationPurpose = "APPLICATION"
	// layerMediaTar is the uncompressed OCI layer media type.
	layerMediaTar = ocispec.MediaTypeImageLayer
	// layerMediaGzip is the gzip-compressed OCI layer media type.
	layerMediaGzip = ocispec.MediaTypeImageLayerGzip
	// gzipMediaSuffix is the media-type suffix that selects gzip decompression.
	gzipMediaSuffix = "+gzip"
	// tarModeBits is the low twelve bits of a tar Mode: permission plus
	// setuid, setgid, and sticky. File-type bits above this mask are ignored.
	tarModeBits int64 = 0o7777
	// maxBinaryBytes is the maximum usr/bin/<binary> payload hashed from a layer.
	//
	// A real layer is about 12 MiB. This bound is well above a release-cli
	// binary and fails closed on a decompression bomb that declares a huge
	// tar Size. The layer stream is never buffered.
	maxBinaryBytes int64 = 64 * bytesPerKiB * kibibytesPerMiB
)

// checkedAnnotationKeys returns the six org.opencontainers.image.* keys
// compared across the index, each manifest, and each config's labels.
func checkedAnnotationKeys() []string {
	return []string{
		annotationDescription,
		annotationLicenses,
		annotationRevision,
		annotationSource,
		annotationTitle,
		annotationVersion,
	}
}

// ExpectedImage is the provenance and staged-binary facts [VerifyLayout] checks.
type ExpectedImage struct {
	// Version is the candidate MAJOR.MINOR.PATCH release.
	Version rel.Version
	// Binary is the filename that must appear at /usr/bin/<binary> in each layer.
	Binary string
	// Revision is the expected org.opencontainers.image.revision, typically GITHUB_SHA.
	Revision string
	// Source is the expected org.opencontainers.image.source URL.
	Source string
	// Canonical maps each APK architecture to the digest of sources/<arch>/application.
	Canonical map[APKArch]rel.Digest
}

// VerifiedImage is a layout that satisfied [VerifyLayout].
type VerifiedImage struct {
	// digest is SHA-256 over the exact index.json bytes.
	digest rel.Digest
	// platforms are the verified platforms in canonical order.
	platforms []verifiedPlatform
}

// verifiedPlatform is one platform's verified blob and binary facts.
type verifiedPlatform struct {
	// platform is the Linux OCI platform.
	platform Platform
	// arch is the APK architecture that platform maps onto.
	arch APKArch
	// manifest is the digest of the platform manifest blob.
	manifest rel.Digest
	// config is the digest of the image config blob.
	config rel.Digest
	// layer is the digest of the single layer blob.
	layer rel.Digest
	// binaryDigest is the streamed SHA-256 of usr/bin/<binary> in that layer.
	binaryDigest rel.Digest
}

// VerifiedPlatform is one platform recorded by [VerifyResult].
type VerifiedPlatform struct {
	// Platform is the Linux OCI platform, such as linux/amd64.
	Platform string `json:"platform"`
	// Arch is the APK architecture, such as x86_64.
	Arch string `json:"arch"`
	// Manifest is the digest of the platform manifest blob.
	Manifest string `json:"manifest"`
	// Config is the digest of the image config blob.
	Config string `json:"config"`
	// Layer is the digest of the single layer blob.
	Layer string `json:"layer"`
	// BinaryDigest is the streamed SHA-256 of usr/bin/<binary> in that layer.
	BinaryDigest string `json:"binary_digest"`
}

// VerifyResult is the versioned document produced by [VerifiedImage.Result].
type VerifyResult struct {
	// Schema identifies the image-verify result version and is always [VerifySchema].
	Schema string `json:"schema"`
	// Version is the candidate MAJOR.MINOR.PATCH version.
	Version string `json:"version"`
	// Binary is the staged binary filename shared by every platform.
	Binary string `json:"binary"`
	// IndexDigest is SHA-256 over the exact index.json bytes.
	IndexDigest string `json:"index_digest"`
	// Platforms are the verified platforms in canonical order: linux/amd64, linux/arm64.
	Platforms []VerifiedPlatform `json:"platforms"`
}

// sbomDocument is the SPDX fields [VerifySBOMs] inspects.
type sbomDocument struct {
	// Packages are the SPDX packages listed by the document.
	Packages []sbomPackage `json:"packages"`
}

// sbomPackage is one SPDX package entry.
type sbomPackage struct {
	// PrimaryPackagePurpose is the SPDX package purpose, such as APPLICATION.
	PrimaryPackagePurpose string `json:"primaryPackagePurpose"`
	// VersionInfo is the package version string, such as 1.2.3-r0.
	VersionInfo string `json:"versionInfo"`
}

// VerifyLayout checks a two-platform OCI layout against expected.
//
// fsys is a [fs.FS] rooted at the layout directory. Cheap structural
// checks run first through [ReadLayout]: oci-layout exists, index.json
// parses with schemaVersion 2 and media type [ocispec.MediaTypeImageIndex],
// the sorted platform architecture set is amd64 and arm64, every platform
// OS is linux, and each manifest names exactly one layer whose blobs exist
// at the declared size. Annotation checks then require the six
// org.opencontainers.image.{description,licenses,revision,source,title,version}
// keys on the index; description, licenses, and title must be nonempty;
// revision, source, and version must equal expected. Each platform's
// manifest annotations and config labels must equal those six index
// values. Each config must have architecture equal to the descriptor
// architecture, os linux, Entrypoint ["/usr/bin/<expected.Binary>"], and
// User "65532". The single layer is then streamed, never buffered: gzip
// when the media type ends in +gzip, otherwise plain tar. The entry
// usr/bin/<expected.Binary> (a leading "./" is accepted) must be a regular
// file whose Mode low twelve bits are exactly 0755, with uid/gid 0/0, and
// a declared tar Size of at most
// [maxBinaryBytes]; its content is hashed through SHA-256 with [io.CopyN]
// bounded by that Size and must equal expected.Canonical for that
// platform's APK architecture. A missing, duplicate, non-regular,
// oversized, or mismatched entry is a verification failure that names
// the platform. Layer media types other than the OCI tar and tar+gzip
// types are rejected.
//
// VerifyLayout does not inspect SBOMs and does not write image-digest.txt.
// Call [VerifySBOMs] and the command layer for those.
func VerifyLayout(fsys fs.FS, expected ExpectedImage) (VerifiedImage, error) {
	if err := validateExpected(expected); err != nil {
		return VerifiedImage{}, err
	}

	layout, err := ReadLayout(fsys)
	if err != nil {
		return VerifiedImage{}, err
	}
	if err := checkIndexAnnotations(layout.Annotations, expected); err != nil {
		return VerifiedImage{}, err
	}

	verified := make([]verifiedPlatform, 0, len(layout.Platforms))
	for _, platform := range layout.Platforms {
		got, err := verifyPlatform(fsys, platform, layout.Annotations, expected)
		if err != nil {
			return VerifiedImage{}, err
		}
		verified = append(verified, got)
	}
	slices.SortFunc(verified, compareVerifiedPlatform)

	return VerifiedImage{digest: layout.IndexDigest, platforms: verified}, nil
}

// IndexDigest returns SHA-256 over the exact index.json bytes.
func (image VerifiedImage) IndexDigest() rel.Digest {
	return image.digest
}

// Result returns the versioned [VerifyResult] document for expected.
//
// Platforms are listed in canonical order: linux/amd64, then linux/arm64.
func (image VerifiedImage) Result(expected ExpectedImage) VerifyResult {
	platforms := make([]VerifiedPlatform, 0, len(image.platforms))
	for _, platform := range image.platforms {
		platforms = append(platforms, VerifiedPlatform{
			Platform:     platform.platform.String(),
			Arch:         platform.arch.String(),
			Manifest:     platform.manifest.String(),
			Config:       platform.config.String(),
			Layer:        platform.layer.String(),
			BinaryDigest: platform.binaryDigest.String(),
		})
	}

	return VerifyResult{
		Schema:      VerifySchema,
		Version:     expected.Version.String(),
		Binary:      expected.Binary,
		IndexDigest: image.digest.String(),
		Platforms:   platforms,
	}
}

// VerifySBOMs requires one architecture SPDX document per arch.
//
// fsys is a [fs.FS] rooted at the sboms directory. For each architecture,
// sbom-<arch>.spdx.json must be a regular JSON document of at most
// [jsonLimitBytes] that contains at least one packages[] entry with
// primaryPackagePurpose APPLICATION and versionInfo "<version>-r0".
// sbom-index.spdx.json is ignored. Decode uses a local struct; there is
// no SPDX dependency. A missing file or a document that fails the
// APPLICATION package check is a verification failure that names the file.
func VerifySBOMs(fsys fs.FS, version rel.Version, arches []APKArch) error {
	if fsys == nil {
		return errors.New("SBOM filesystem is nil")
	}
	if len(arches) == 0 {
		return errors.New("SBOM architecture list is empty")
	}

	wantVersion := version.String() + sbomVersionSuffix
	for _, arch := range arches {
		if arch == "" {
			return errors.New("SBOM architecture is empty")
		}
		name := "sbom-" + arch.String() + ".spdx.json"
		if err := checkSBOM(fsys, name, wantVersion); err != nil {
			return err
		}
	}

	return nil
}

// CanonicalDigests streams sources/<arch>/application through SHA-256.
//
// work is a [fs.FS] rooted at the scratch workspace. Each architecture is
// hashed independently and never buffered. A missing path or a non-regular
// entry is an error that names the file. The returned map is keyed by the
// supplied architectures in the order they were hashed.
func CanonicalDigests(work fs.FS, arches []APKArch) (map[APKArch]rel.Digest, error) {
	if work == nil {
		return nil, errors.New("work filesystem is nil")
	}
	if len(arches) == 0 {
		return nil, errors.New("canonical architecture list is empty")
	}

	digests := make(map[APKArch]rel.Digest, len(arches))
	for _, arch := range arches {
		if arch == "" {
			return nil, errors.New("canonical architecture is empty")
		}
		name := path.Join(sourcesDir, arch.String(), applicationFile)
		digest, err := hashRegularFile(work, name)
		if err != nil {
			return nil, err
		}
		digests[arch] = digest
	}

	return digests, nil
}

// validateExpected rejects incomplete [ExpectedImage] facts.
func validateExpected(expected ExpectedImage) error {
	if expected.Binary == "" {
		return errors.New("binary name is empty")
	}
	if strings.ContainsAny(expected.Binary, `/\`) {
		return fmt.Errorf("binary name %q contains a path separator", expected.Binary)
	}
	if expected.Revision == "" {
		return errors.New("revision is empty")
	}
	if expected.Source == "" {
		return errors.New("source is empty")
	}
	if expected.Canonical == nil {
		return errors.New("canonical digest map is nil")
	}

	return nil
}

// checkIndexAnnotations requires the six OCI keys and the expected provenance.
func checkIndexAnnotations(annotations map[string]string, expected ExpectedImage) error {
	for _, key := range checkedAnnotationKeys() {
		if _, ok := annotations[key]; !ok {
			return fmt.Errorf("%s is missing annotation %s", indexFileName, key)
		}
	}
	if annotations[annotationDescription] == "" {
		return fmt.Errorf("%s annotation %s is empty", indexFileName, annotationDescription)
	}
	if annotations[annotationLicenses] == "" {
		return fmt.Errorf("%s annotation %s is empty", indexFileName, annotationLicenses)
	}
	if annotations[annotationTitle] == "" {
		return fmt.Errorf("%s annotation %s is empty", indexFileName, annotationTitle)
	}
	if annotations[annotationRevision] != expected.Revision {
		return fmt.Errorf(
			"%s annotation %s is %q, want %q",
			indexFileName,
			annotationRevision,
			annotations[annotationRevision],
			expected.Revision,
		)
	}
	if annotations[annotationSource] != expected.Source {
		return fmt.Errorf(
			"%s annotation %s is %q, want %q",
			indexFileName,
			annotationSource,
			annotations[annotationSource],
			expected.Source,
		)
	}
	if annotations[annotationVersion] != expected.Version.String() {
		return fmt.Errorf(
			"%s annotation %s is %q, want %q",
			indexFileName,
			annotationVersion,
			annotations[annotationVersion],
			expected.Version,
		)
	}

	return nil
}

// verifyPlatform checks one platform's annotations, config, and layer binary.
func verifyPlatform(
	fsys fs.FS,
	platform LayoutPlatform,
	indexAnnotations map[string]string,
	expected ExpectedImage,
) (verifiedPlatform, error) {
	if err := checkEqualAnnotations(platform.Platform, "manifest", platform.Annotations, indexAnnotations); err != nil {
		return verifiedPlatform{}, err
	}

	config, err := readImageConfig(fsys, platform)
	if err != nil {
		return verifiedPlatform{}, err
	}
	err = checkImageConfig(platform, config, indexAnnotations, expected)
	if err != nil {
		return verifiedPlatform{}, err
	}

	canonical, ok := expected.Canonical[platform.Platform.APKArch()]
	if !ok {
		return verifiedPlatform{}, fmt.Errorf(
			"%s is missing a canonical digest for %s",
			platform.Platform,
			platform.Platform.APKArch(),
		)
	}
	binaryDigest, err := verifyLayerBinary(fsys, platform, expected.Binary, canonical)
	if err != nil {
		return verifiedPlatform{}, err
	}

	return verifiedPlatform{
		platform:     platform.Platform,
		arch:         platform.Platform.APKArch(),
		manifest:     platform.Manifest,
		config:       platform.Config,
		layer:        platform.Layer,
		binaryDigest: binaryDigest,
	}, nil
}

// checkEqualAnnotations requires subject's six checked keys to equal the index.
func checkEqualAnnotations(platform Platform, subject string, got, want map[string]string) error {
	for _, key := range checkedAnnotationKeys() {
		if got[key] != want[key] {
			return fmt.Errorf(
				"%s %s %s is %q, want %q",
				platform,
				subject,
				key,
				got[key],
				want[key],
			)
		}
	}

	return nil
}

// readImageConfig loads and decodes the platform config blob.
func readImageConfig(fsys fs.FS, platform LayoutPlatform) (ocispec.Image, error) {
	name, err := blobPath(platform.Config)
	if err != nil {
		return ocispec.Image{}, fmt.Errorf("%s config: %w", platform.Platform, err)
	}
	body, err := readJSONDocument(fsys, name)
	if err != nil {
		return ocispec.Image{}, fmt.Errorf("%s config: %w", platform.Platform, err)
	}

	var config ocispec.Image
	if err := json.Unmarshal(body, &config); err != nil {
		return ocispec.Image{}, fmt.Errorf("%s config %s: %w", platform.Platform, name, err)
	}

	return config, nil
}

// checkImageConfig requires architecture, os, Entrypoint, User, and labels.
func checkImageConfig(
	platform LayoutPlatform,
	config ocispec.Image,
	indexAnnotations map[string]string,
	expected ExpectedImage,
) error {
	wantArch := strings.TrimPrefix(platform.Platform.String(), "linux/")
	if config.Architecture != wantArch {
		return fmt.Errorf(
			"%s config architecture is %q, want %q",
			platform.Platform,
			config.Architecture,
			wantArch,
		)
	}
	if config.OS != "linux" {
		return fmt.Errorf("%s config os is %q, want linux", platform.Platform, config.OS)
	}

	wantEntrypoint := []string{"/usr/bin/" + expected.Binary}
	if !slices.Equal(config.Config.Entrypoint, wantEntrypoint) {
		return fmt.Errorf(
			"%s config Entrypoint is %q, want %q",
			platform.Platform,
			config.Config.Entrypoint,
			wantEntrypoint,
		)
	}
	if config.Config.User != expectedConfigUser {
		return fmt.Errorf(
			"%s config User is %q, want %q",
			platform.Platform,
			config.Config.User,
			expectedConfigUser,
		)
	}

	return checkEqualAnnotations(platform.Platform, "config label", config.Config.Labels, indexAnnotations)
}

// verifyLayerBinary streams the layer and hashes usr/bin/<binary>.
func verifyLayerBinary(
	fsys fs.FS,
	platform LayoutPlatform,
	binary string,
	canonical rel.Digest,
) (rel.Digest, error) {
	if err := checkLayerMedia(platform); err != nil {
		return "", err
	}

	name, err := blobPath(platform.Layer)
	if err != nil {
		return "", fmt.Errorf("%s layer: %w", platform.Platform, err)
	}
	file, err := fsys.Open(name)
	if err != nil {
		return "", fmt.Errorf("%s open layer %s: %w", platform.Platform, name, err)
	}
	defer file.Close()
	stream := io.Reader(file)
	if strings.HasSuffix(platform.LayerMedia, gzipMediaSuffix) {
		reader, gzipErr := gzip.NewReader(file)
		if gzipErr != nil {
			return "", fmt.Errorf("%s layer %s: gzip: %w", platform.Platform, name, gzipErr)
		}
		defer reader.Close()
		stream = reader
	}

	digest, err := hashBinaryEntry(stream, binary)
	if err != nil {
		return "", fmt.Errorf("%s: %w", platform.Platform, err)
	}
	if digest != canonical {
		return "", fmt.Errorf(
			"%s image binary has digest %s, expected %s",
			platform.Platform,
			digest,
			canonical,
		)
	}

	return digest, nil
}

// checkLayerMedia rejects a layer media type that is neither tar nor tar+gzip.
func checkLayerMedia(platform LayoutPlatform) error {
	switch platform.LayerMedia {
	case layerMediaTar, layerMediaGzip:
		return nil
	default:
		return fmt.Errorf(
			"%s layer media type is %q, want %q or %q",
			platform.Platform,
			platform.LayerMedia,
			layerMediaTar,
			layerMediaGzip,
		)
	}
}

// hashBinaryEntry finds usr/bin/<binary> once and hashes its content.
//
// The payload is copied with [io.CopyN] using the tar header Size, which
// must be between 0 and [maxBinaryBytes] inclusive. The layer stream is
// never buffered.
func hashBinaryEntry(stream io.Reader, binary string) (rel.Digest, error) {
	want := path.Join("usr/bin", binary)
	reader := tar.NewReader(stream)
	found := false
	var digest rel.Digest

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read layer: %w", err)
		}
		if !isBinaryEntry(header.Name, want) {
			continue
		}
		if found {
			return "", fmt.Errorf("layer lists %s more than once", want)
		}
		hashed, hashErr := hashMatchedEntry(reader, header, want)
		if hashErr != nil {
			return "", hashErr
		}
		digest = hashed
		found = true
	}
	if !found {
		return "", fmt.Errorf("layer is missing %s", want)
	}

	return digest, nil
}

// hashMatchedEntry validates header and streams the regular-file payload.
func hashMatchedEntry(reader *tar.Reader, header *tar.Header, want string) (rel.Digest, error) {
	if err := checkBinaryHeader(header, want); err != nil {
		return "", err
	}

	sum := sha256.New()
	written, err := io.CopyN(sum, reader, header.Size)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", want, err)
	}
	if written != header.Size {
		return "", fmt.Errorf("hash %s: short read", want)
	}

	digest, err := rel.ParseDigest(digestPrefix + hex.EncodeToString(sum.Sum(nil)))
	if err != nil {
		return "", fmt.Errorf("digest %s: %w", want, err)
	}

	return digest, nil
}

// checkBinaryHeader requires a regular 0755 file owned by 0/0 within [maxBinaryBytes].
//
// The tar reader normalizes the historic NUL typeflag to [tar.TypeReg]
// before [tar.Reader.Next] returns, so only TypeReg reaches this check.
// The Mode comparison uses the low twelve bits so file-type bits are
// ignored and setuid, setgid, and sticky still fail.
func checkBinaryHeader(header *tar.Header, want string) error {
	if header.Typeflag != tar.TypeReg {
		return fmt.Errorf("%s is type %q, want a regular file", want, tarTypeName(header.Typeflag))
	}
	if header.Mode&tarModeBits != int64(fileModeExecutable) {
		return fmt.Errorf("%s has mode %#o, want %#o", want, header.Mode&tarModeBits, fileModeExecutable)
	}
	if header.Uid != 0 || header.Gid != 0 {
		return fmt.Errorf("%s is owned by %d/%d, want 0/0", want, header.Uid, header.Gid)
	}
	if header.Size < 0 {
		return fmt.Errorf("%s has negative size %d", want, header.Size)
	}
	if header.Size > maxBinaryBytes {
		return fmt.Errorf("%s is %d bytes, exceeds the %d byte binary limit", want, header.Size, maxBinaryBytes)
	}

	return nil
}

// isBinaryEntry reports whether name is usr/bin/<binary> with an optional "./".
func isBinaryEntry(name, want string) bool {
	cleaned := strings.TrimPrefix(name, "./")

	return cleaned == want
}

// tarTypeName is a short label for a tar Typeflag used in error text.
func tarTypeName(flag byte) string {
	switch flag {
	case tar.TypeDir:
		return "directory"
	case tar.TypeSymlink:
		return "symlink"
	case tar.TypeLink:
		return "hard link"
	default:
		return string(flag)
	}
}

// checkSBOM requires one APPLICATION package at wantVersion in name.
func checkSBOM(fsys fs.FS, name, wantVersion string) error {
	body, err := readJSONDocument(fsys, name)
	if err != nil {
		return err
	}

	var doc sbomDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for _, pkg := range doc.Packages {
		if pkg.PrimaryPackagePurpose == applicationPurpose && pkg.VersionInfo == wantVersion {
			return nil
		}
	}

	return fmt.Errorf("%s has no APPLICATION package at version %s", name, wantVersion)
}

// hashRegularFile streams name through SHA-256 without buffering it.
func hashRegularFile(fsys fs.FS, name string) (rel.Digest, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", name)
	}

	file, err := fsys.Open(name)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", name, err)
	}

	return rel.ParseDigest(digestPrefix + hex.EncodeToString(sum.Sum(nil)))
}

// compareVerifiedPlatform orders platforms as linux/amd64 then linux/arm64.
func compareVerifiedPlatform(a, b verifiedPlatform) int {
	return strings.Compare(a.platform.String(), b.platform.String())
}
