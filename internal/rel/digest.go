package rel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// digestPrefix is the canonical OCI SHA-256 algorithm prefix.
const digestPrefix = "sha256:"

// Digest is a lowercase sha256:<64 hex> image digest.
//
// The only constructor is [ParseDigest], which requires the sha256: prefix
// and normalizes uppercase hex. The zero value is invalid.
type Digest string

// ParseDigest constructs a [Digest] from a sha256:<hex> string.
//
// The sha256: prefix is required. The hex part must be exactly
// [hex.EncodedLen] of [sha256.Size] hexadecimal digits. Uppercase hex is
// normalized to lowercase. Any other prefix, length, or charset is rejected.
func ParseDigest(value string) (Digest, error) {
	if value == "" {
		return "", fmt.Errorf("digest %q is empty", value)
	}

	hexPart, found := strings.CutPrefix(value, digestPrefix)
	if !found {
		return "", digestPrefixError(value)
	}
	if len(hexPart) != hex.EncodedLen(sha256.Size) {
		return "", fmt.Errorf(
			"digest %q has %d hex digits, want %d",
			value,
			len(hexPart),
			hex.EncodedLen(sha256.Size),
		)
	}
	for _, r := range hexPart {
		if !isHex(r) {
			return "", fmt.Errorf("digest %q is not hexadecimal", value)
		}
	}

	return Digest(digestPrefix + strings.ToLower(hexPart)), nil
}

// String returns the canonical sha256:<hex> digest.
func (d Digest) String() string {
	return string(d)
}

// digestPrefixError names a missing or unexpected algorithm prefix.
func digestPrefixError(value string) error {
	algorithm, rest, found := strings.Cut(value, ":")
	if found && algorithm != "" && rest != "" {
		return fmt.Errorf("digest %q has prefix %q, want %q", value, algorithm+":", digestPrefix)
	}

	return fmt.Errorf("digest %q is missing the %s prefix", value, digestPrefix)
}

// isHex reports whether r is an ASCII hexadecimal digit.
func isHex(r rune) bool {
	return unicode.Is(unicode.ASCII_Hex_Digit, r)
}
