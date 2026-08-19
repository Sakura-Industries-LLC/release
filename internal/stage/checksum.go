package stage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"unicode"
)

// checksumLinePrefixLen is the SHA-256 hex digest plus the two-character
// GNU coreutils marker ("  " or " *").
const checksumLinePrefixLen = sha256.Size*2 + 2

// Control file names that live beside claimed payloads.
const (
	// checksumsName is the checksum claim filename.
	checksumsName = "checksums.txt"
	// bundleName is the Sigstore bundle that must accompany the claim.
	bundleName = "checksums.txt.sigstore.json"
	// artifactsName is the GoReleaser artifact inventory filename.
	artifactsName = "artifacts.json"
)

// Digest is a lowercase SHA-256 hex digest.
//
// The only constructor is [ParseDigest], which normalizes uppercase hex and
// rejects any other length or charset. The zero value is invalid.
type Digest string

// AssetName is a flat payload filename from checksums.txt.
//
// The only constructor is [ParseAssetName], which rejects empty names and
// path separators. The zero value is invalid.
type AssetName string

// ChecksumEntry is one validated claim from checksums.txt.
type ChecksumEntry struct {
	// Name is the claimed payload filename.
	Name AssetName
	// Digest is the claimed SHA-256 digest.
	Digest Digest
}

// ChecksumSet is a validated checksums.txt claim.
//
// Values are produced only by [ParseChecksums]. The zero value has no
// entries and is rejected by [VerifyBundle].
type ChecksumSet struct {
	// entries is the ordered list of claimed payloads.
	entries []ChecksumEntry
}

// Report is the successful outcome of staging a Go dist bundle.
type Report struct {
	// Assets is the number of checksummed payloads that matched.
	Assets int
	// Binaries are the selected canonical Linux binaries after verification.
	Binaries []Binary
}

// Binary is a verified canonical Linux binary observed on disk.
type Binary struct {
	// Arch is the GOARCH of the selected binary.
	Arch string
	// Path is the original GoReleaser path, including the --dist basename prefix.
	Path string
	// Mode is the observed permission bits.
	Mode fs.FileMode
}

// ParseDigest constructs a [Digest] from a 64-digit hexadecimal string.
//
// Uppercase hex is normalized to lowercase. Any other length or charset is
// rejected.
func ParseDigest(raw string) (Digest, error) {
	if len(raw) != hex.EncodedLen(sha256.Size) {
		return "", fmt.Errorf("digest %q has length %d, want %d", raw, len(raw), hex.EncodedLen(sha256.Size))
	}
	for _, r := range raw {
		if !isHex(r) {
			return "", fmt.Errorf("digest %q is not hexadecimal", raw)
		}
	}

	return Digest(strings.ToLower(raw)), nil
}

// String returns the lowercase hex digest.
func (d Digest) String() string {
	return string(d)
}

// ParseAssetName constructs an [AssetName] from a flat checksums.txt filename.
//
// Names must be nonempty and must not contain a path separator.
func ParseAssetName(raw string) (AssetName, error) {
	if raw == "" {
		return "", errors.New("asset name is empty")
	}
	if strings.ContainsAny(raw, `/\`) {
		return "", fmt.Errorf("asset name %q contains a path separator", raw)
	}

	return AssetName(raw), nil
}

// String returns the payload filename.
func (n AssetName) String() string {
	return string(n)
}

// Entries returns the claimed payloads in file order.
func (s ChecksumSet) Entries() []ChecksumEntry {
	return slices.Clone(s.entries)
}

// Len returns the number of claimed payloads.
func (s ChecksumSet) Len() int {
	return len(s.entries)
}

// ParseChecksums parses a GNU coreutils sha256sum claim.
//
// Accepted lines are `<64 hex><two spaces><name>` or the binary-marker form
// `<64 hex><space><asterisk><name>`. CRLF is tolerated. Uppercase hex is
// normalized. Empty input, duplicate names, path separators in names,
// malformed digests, and a self-listed checksums.txt are rejected.
func ParseChecksums(r io.Reader) (ChecksumSet, error) {
	if r == nil {
		return ChecksumSet{}, errors.New("checksums reader is nil")
	}

	return parseChecksums(r)
}

// parseChecksums parses after the exported nil check.
func parseChecksums(r io.Reader) (ChecksumSet, error) {
	scanner := bufio.NewScanner(r)
	seen := make(map[AssetName]struct{})
	var entries []ChecksumEntry

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		entry, err := parseChecksumLine(line)
		if err != nil {
			return ChecksumSet{}, fmt.Errorf("checksums.txt line %d: %w", lineNumber, err)
		}
		if _, exists := seen[entry.Name]; exists {
			return ChecksumSet{}, fmt.Errorf("duplicate checksums.txt entry: %s", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return ChecksumSet{}, fmt.Errorf("read checksums.txt: %w", err)
	}
	if len(entries) == 0 {
		return ChecksumSet{}, errors.New("checksums.txt does not list any release payloads")
	}

	return ChecksumSet{entries: entries}, nil
}

// VerifyBundle streams every claimed payload through SHA-256 and requires a
// nonempty regular checksums.txt.sigstore.json.
//
// The first offending asset is named in the error. Payloads are hashed with
// [io.Copy] into [sha256.New]; they are never buffered whole. A nil
// filesystem is rejected.
func VerifyBundle(fsys fs.FS, claim ChecksumSet) error {
	if fsys == nil {
		return errors.New("filesystem is nil")
	}

	return verifyBundle(fsys, claim)
}

// verifyBundle verifies after the exported nil check.
func verifyBundle(fsys fs.FS, claim ChecksumSet) error {
	for _, entry := range claim.entries {
		if err := verifyPayload(fsys, entry); err != nil {
			return err
		}
	}

	return requireRegularNonempty(fsys, bundleName)
}

// parseChecksumLine validates one GNU sha256sum line.
func parseChecksumLine(line string) (ChecksumEntry, error) {
	if len(line) < checksumLinePrefixLen+1 {
		return ChecksumEntry{}, fmt.Errorf("malformed entry %q", line)
	}

	digest, err := ParseDigest(line[:hex.EncodedLen(sha256.Size)])
	if err != nil {
		return ChecksumEntry{}, err
	}

	marker := line[hex.EncodedLen(sha256.Size):checksumLinePrefixLen]
	if marker != "  " && marker != " *" {
		return ChecksumEntry{}, fmt.Errorf("malformed entry %q", line)
	}

	name, err := ParseAssetName(line[checksumLinePrefixLen:])
	if err != nil {
		return ChecksumEntry{}, err
	}
	if name.String() == checksumsName {
		return ChecksumEntry{}, fmt.Errorf("control file %s must not be listed in checksums.txt", checksumsName)
	}

	return ChecksumEntry{Name: name, Digest: digest}, nil
}

// verifyPayload streams one claimed payload through SHA-256.
func verifyPayload(fsys fs.FS, entry ChecksumEntry) error {
	name := entry.Name.String()
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return fmt.Errorf("release payload %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("release payload %s is not a regular file", name)
	}

	file, err := fsys.Open(name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return fmt.Errorf("hash %s: %w", name, err)
	}
	actual := Digest(hex.EncodeToString(sum.Sum(nil)))
	if actual != entry.Digest {
		return fmt.Errorf("release payload %s has digest %s, expected %s", name, actual, entry.Digest)
	}

	return nil
}

// requireRegularNonempty requires name to exist as a nonempty regular file.
func requireRegularNonempty(fsys fs.FS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty", name)
	}

	return nil
}

// isHex reports whether r is an ASCII hexadecimal digit.
func isHex(r rune) bool {
	return unicode.Is(unicode.ASCII_Hex_Digit, r)
}
