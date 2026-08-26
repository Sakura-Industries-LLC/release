package goprof

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// binaryType is GoReleaser's compiled-binary artifact type.
	binaryType = "Binary"
	// linuxOS is the required GOOS for canonical binaries.
	linuxOS = "linux"
	// ownerExecute is the owner-execute permission bit.
	ownerExecute = 0o100
)

// Record is one GoReleaser artifacts.json entry.
//
// Fields are bare strings because this type mirrors GoReleaser's wire format
// at the decode boundary. Validation happens in [SelectBinaries], not here.
// A zero Record is an empty decoded object and is ignored by selection.
type Record struct {
	// Type is the GoReleaser artifact type, for example Binary or Archive.
	Type string `json:"type"`
	// GOOS is the target operating system.
	GOOS string `json:"goos"`
	// GOARCH is the target architecture.
	GOARCH string `json:"goarch"`
	// Path is the GoReleaser-written path, prefixed with the --dist basename.
	Path string `json:"path"`
	// Name is the artifact filename.
	Name string `json:"name"`
}

// RootName is the basename of the --dist directory.
//
// GoReleaser prefixes artifact paths with this name. The zero value is
// invalid; construct with [ParseRootName].
type RootName string

// Arch is a selected Linux GOARCH.
//
// Only amd64 and arm64 are valid after [SelectBinaries]. The zero value is
// invalid; construct with [ParseArch].
type Arch string

// ArtifactPath is a GoReleaser-written artifact path.
//
// It includes the [RootName] prefix. The zero value is invalid; construct
// with [ParseArtifactPath].
type ArtifactPath string

// RelativePath is an artifact path confined under the dist root for [io/fs.FS].
//
// The zero value is invalid; construct with [ParseRelativePath].
type RelativePath string

// BinaryName is the filename of a selected Linux binary.
//
// The only constructor is [ParseBinaryName]. The zero value is invalid.
type BinaryName string

// CanonicalBinary is a selected linux/{amd64,arm64} Binary record.
//
// Values are produced only by [SelectBinaries]. A zero CanonicalBinary is
// invalid and must not be verified.
type CanonicalBinary struct {
	// Arch is the selected GOARCH.
	Arch Arch
	// Path is the original GoReleaser path, including the --dist basename prefix.
	Path ArtifactPath
	// RelativePath is Path with the leading root name stripped for [io/fs.FS] lookup.
	RelativePath RelativePath
	// Name is the binary filename from this record.
	Name BinaryName
}

// ParseRootName constructs a [RootName] from a directory basename.
//
// Names must be nonempty, must not contain a path separator, and must
// not be "." or "..".
func ParseRootName(raw string) (RootName, error) {
	if raw == "" || raw == "." || raw == string(filepath.Separator) {
		return "", fmt.Errorf("dist root name %q is empty", raw)
	}
	if raw == ".." {
		return "", fmt.Errorf("dist root name %q is not a basename", raw)
	}
	if strings.ContainsAny(raw, `/\`) {
		return "", fmt.Errorf("dist root name %q is not a basename", raw)
	}

	return RootName(raw), nil
}

// String returns the directory basename.
func (n RootName) String() string {
	return string(n)
}

// ParseArch constructs an [Arch] from a required Linux GOARCH.
func ParseArch(raw string) (Arch, error) {
	if !slices.Contains(requiredArchs(), raw) {
		return "", fmt.Errorf("unsupported architecture %q", raw)
	}

	return Arch(raw), nil
}

// String returns the GOARCH value.
func (a Arch) String() string {
	return string(a)
}

// ParseArtifactPath constructs an [ArtifactPath] from a nonempty path.
func ParseArtifactPath(raw string) (ArtifactPath, error) {
	if raw == "" {
		return "", errors.New("artifact path is empty")
	}

	return ArtifactPath(raw), nil
}

// String returns the original GoReleaser path.
func (p ArtifactPath) String() string {
	return string(p)
}

// ParseRelativePath constructs a confined [RelativePath].
func ParseRelativePath(raw string) (RelativePath, error) {
	if err := confine(raw); err != nil {
		return "", err
	}

	return RelativePath(raw), nil
}

// String returns the dist-root-relative path.
func (p RelativePath) String() string {
	return string(p)
}

// ParseBinaryName constructs a [BinaryName] from an artifact filename.
//
// Names must be nonempty, must not contain a path separator, and must not
// be "." or "..".
func ParseBinaryName(raw string) (BinaryName, error) {
	if raw == "" {
		return "", errors.New("binary name is empty")
	}
	if raw == "." || raw == ".." {
		return "", fmt.Errorf("binary name %q is not a filename", raw)
	}
	if strings.ContainsAny(raw, `/\`) {
		return "", fmt.Errorf("binary name %q contains a path separator", raw)
	}

	return BinaryName(raw), nil
}

// String returns the binary filename.
func (n BinaryName) String() string {
	return string(n)
}

// requiredArchs returns the closed set of Linux GOARCH values that must
// each appear with the same nonempty binary name set.
func requiredArchs() []string {
	return []string{"amd64", "arm64"}
}

// ParseArtifacts decodes a GoReleaser artifacts.json document.
//
// Unknown fields are ignored. The document must be a JSON array. A nil reader
// is rejected.
func ParseArtifacts(r io.Reader) ([]Record, error) {
	if r == nil {
		return nil, errors.New("artifacts reader is nil")
	}

	return parseArtifacts(r)
}

// parseArtifacts decodes after the exported nil check.
func parseArtifacts(r io.Reader) ([]Record, error) {
	decoder := json.NewDecoder(r)
	var records []Record
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode artifacts.json: %w", err)
	}

	return records, nil
}

// SelectBinaries returns every linux Binary for the required architectures.
//
// Each linux/{amd64,arm64} Binary is selected. A duplicate (architecture, name)
// pair fails with a diagnostic that names the pair. Paths must be relative to
// root, the basename of the --dist directory (GoReleaser writes "<dir>/...").
// The name set must be nonempty and identical across both architectures; a
// name present on only one architecture is an error that names it. The result
// is sorted architecture-major (amd64, then arm64), then name-ascending.
func SelectBinaries(records []Record, root RootName) ([]CanonicalBinary, error) {
	if root == "" {
		return nil, errors.New("dist root name is empty")
	}
	selected := make(map[archName]CanonicalBinary)
	var found []string

	for _, record := range records {
		if record.Type != binaryType || record.GOOS != linuxOS {
			continue
		}
		next, err := selectLinuxBinary(record, root, selected, found)
		if err != nil {
			return nil, err
		}
		found = next
	}

	return assembleBinaries(selected, found)
}

// archName is a selected architecture and binary filename pair.
type archName struct {
	// arch is the Linux GOARCH.
	arch Arch
	// name is the binary filename.
	name BinaryName
}

// selectLinuxBinary records one linux Binary for its (architecture, name) pair.
//
// found is the architectures seen so far, including this record's GOARCH.
func selectLinuxBinary(
	record Record,
	root RootName,
	selected map[archName]CanonicalBinary,
	found []string,
) ([]string, error) {
	found = append(found, record.GOARCH)
	arch, err := ParseArch(record.GOARCH)
	if err != nil {
		return found, fmt.Errorf(
			"unexpected linux/%s Binary record; found %s",
			record.GOARCH,
			joinArchs(found),
		)
	}
	name, err := ParseBinaryName(record.Name)
	if err != nil {
		return found, err
	}
	key := archName{arch: arch, name: name}
	if _, exists := selected[key]; exists {
		return found, fmt.Errorf(
			"duplicate linux/%s Binary record for %q; found %s",
			record.GOARCH,
			name,
			joinArchs(found),
		)
	}
	relative, err := rootRelative(record.Path, root)
	if err != nil {
		return found, err
	}
	artifactPath, err := ParseArtifactPath(record.Path)
	if err != nil {
		return found, err
	}
	selected[key] = CanonicalBinary{
		Arch:         arch,
		Path:         artifactPath,
		RelativePath: relative,
		Name:         name,
	}

	return found, nil
}

// assembleBinaries requires a nonempty identical name set on both architectures.
func assembleBinaries(selected map[archName]CanonicalBinary, found []string) ([]CanonicalBinary, error) {
	required := requiredArchs()
	namesByArch := make(map[Arch]map[BinaryName]struct{}, len(required))
	for _, raw := range required {
		namesByArch[Arch(raw)] = make(map[BinaryName]struct{})
	}
	for key := range selected {
		namesByArch[key.arch][key.name] = struct{}{}
	}

	union := make(map[BinaryName]struct{})
	for _, names := range namesByArch {
		for name := range names {
			union[name] = struct{}{}
		}
	}
	if len(union) == 0 {
		return nil, fmt.Errorf(
			"missing linux Binary record for %s; found %s",
			joinArchs(required),
			joinArchs(found),
		)
	}

	var names []string
	for name := range union {
		names = append(names, name.String())
	}
	slices.Sort(names)

	for _, raw := range required {
		arch := Arch(raw)
		have := namesByArch[arch]
		var missing []string
		for _, name := range names {
			if _, ok := have[BinaryName(name)]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf(
				"missing linux/%s Binary record for %s; found %s",
				arch,
				joinArchs(missing),
				joinArchs(found),
			)
		}
	}

	out := make([]CanonicalBinary, 0, len(selected))
	for _, raw := range required {
		arch := Arch(raw)
		for _, name := range names {
			out = append(out, selected[archName{arch: arch, name: BinaryName(name)}])
		}
	}

	return out, nil
}

// VerifyBinaries checks each selected path against fsys.
//
// Every path must be lexically confined under the dist root, a regular file,
// and have the owner-execute bit set. The caller supplies [os.OpenRoot] of
// the dist directory as [fs.FS]. A nil filesystem is rejected.
func VerifyBinaries(fsys fs.FS, binaries []CanonicalBinary) error {
	if fsys == nil {
		return errors.New("filesystem is nil")
	}

	return verifyBinaries(fsys, binaries)
}

// verifyBinaries checks binaries after the exported nil check.
func verifyBinaries(fsys fs.FS, binaries []CanonicalBinary) error {
	for _, binary := range binaries {
		if err := verifyBinary(fsys, binary); err != nil {
			return err
		}
	}

	return nil
}

// verifyBinary checks lexical confinement, regularity, and the execute bit.
func verifyBinary(fsys fs.FS, binary CanonicalBinary) error {
	if err := confine(binary.RelativePath.String()); err != nil {
		return fmt.Errorf("%s: %w", binary.Path, err)
	}

	info, err := fs.Stat(fsys, binary.RelativePath.String())
	if err != nil {
		return fmt.Errorf("%s: %w", binary.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", binary.Path)
	}
	if info.Mode().Perm()&ownerExecute == 0 {
		return fmt.Errorf("%s is not executable", binary.Path)
	}

	return nil
}

// rootRelative strips the leading "<root>/" prefix and confines the remainder.
func rootRelative(raw string, root RootName) (RelativePath, error) {
	prefix := root.String() + "/"
	relative, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return "", fmt.Errorf("artifact path %q is not %s/-relative", raw, root)
	}

	return ParseRelativePath(relative)
}

// confine rejects empty, absolute, and lexically escaping relative paths.
func confine(relative string) error {
	if relative == "" || !filepath.IsLocal(relative) {
		if relative != "" &&
			(filepath.Clean(relative) == ".." || strings.HasPrefix(filepath.ToSlash(filepath.Clean(relative)), "../")) {
			return fmt.Errorf("path %q escapes the dist root", relative)
		}

		return fmt.Errorf("path %q is not confined under the dist root", relative)
	}

	return nil
}

// joinArchs formats architecture names for diagnostics.
func joinArchs(archs []string) string {
	if len(archs) == 0 {
		return "none"
	}

	return strings.Join(archs, ", ")
}
