package rel

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	// maxGitTagLength is the maximum byte length of a git tag name.
	maxGitTagLength = 255
)

// GitTag is a validated git tag name.
//
// The only constructor is [ParseGitTag]. The zero value is invalid.
type GitTag string

// ParseGitTag constructs a [GitTag] from a git tag name.
//
// The grammar is a nonempty string of at most 255 bytes with no whitespace
// or control characters, no `..`, no `~^:?*[\`, no leading `-` or `/`, no
// trailing `/` or `.lock`, and no `//`. A `/` may group components, as in
// GoReleaser Pro monorepo.tag_prefix tags such as cli/v0.1.4.
func ParseGitTag(value string) (GitTag, error) {
	if value == "" {
		return "", fmt.Errorf("git tag %q is empty", value)
	}
	if len(value) > maxGitTagLength {
		return "", fmt.Errorf("git tag %q has length %d, want at most %d", value, len(value), maxGitTagLength)
	}
	if err := checkGitTagForm(value); err != nil {
		return "", err
	}

	return GitTag(value), nil
}

// Version returns the stable version encoded in the last path segment of t.
//
// The last `/`-separated segment is taken, one optional leading `v` is
// stripped, and the remainder is parsed with [ParseVersion].
func (t GitTag) Version() (Version, error) {
	segment := string(t)
	if index := strings.LastIndexByte(segment, '/'); index >= 0 {
		segment = segment[index+1:]
	}

	return ParseVersion(strings.TrimPrefix(segment, "v"))
}

// String returns the git tag name.
func (t GitTag) String() string {
	return string(t)
}

// checkGitTagForm reports grammar violations other than emptiness and length.
func checkGitTagForm(value string) error {
	if value[0] == '-' || value[0] == '/' {
		return fmt.Errorf("git tag %q has an invalid leading character", value)
	}
	if strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") {
		return fmt.Errorf("git tag %q has an invalid trailing component", value)
	}
	if strings.Contains(value, "..") || strings.Contains(value, "//") {
		return fmt.Errorf("git tag %q has an invalid character sequence", value)
	}
	for _, r := range value {
		if !isGitTagRune(r) {
			return fmt.Errorf("git tag %q has an invalid character", value)
		}
	}

	return nil
}

// isGitTagRune reports whether r may appear in a git tag name.
func isGitTagRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsControl(r) {
		return false
	}
	switch r {
	case '~', '^', ':', '?', '*', '[', '\\':
		return false
	}

	return true
}
