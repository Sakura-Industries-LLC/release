package rel

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// versionComponents is the number of dotted parts in a stable triple.
const versionComponents = 3

// Version is a canonical stable MAJOR.MINOR.PATCH triple.
//
// The only constructor is [ParseVersion]. The zero value is 0.0.0, which is
// a valid version.
type Version struct {
	// Major is the leftmost version component.
	Major uint64
	// Minor is the middle version component.
	Minor uint64
	// Patch is the rightmost version component.
	Patch uint64
}

// ParseVersion constructs a [Version] from a stable MAJOR.MINOR.PATCH string.
//
// The grammar is exactly three decimal components with no leading zeros
// (except the value 0), no v prefix, no sign, no prerelease, and no build
// metadata. A component that does not fit in uint64 is rejected. Error text
// names the problem and echoes the input.
func ParseVersion(value string) (Version, error) {
	if strings.TrimSpace(value) == "" {
		return Version{}, fmt.Errorf("version %q is empty", value)
	}
	if strings.HasPrefix(value, "v") || strings.HasPrefix(value, "V") {
		return Version{}, fmt.Errorf("version %q has a v prefix", value)
	}
	if err := rejectVersionDecorators(value); err != nil {
		return Version{}, err
	}
	parts := strings.Split(value, ".")
	if len(parts) != versionComponents {
		return Version{}, fmt.Errorf(
			"version %q has %d components, want %d",
			value,
			len(parts),
			versionComponents,
		)
	}

	major, err := parseVersionComponent(value, "major", parts[0])
	if err != nil {
		return Version{}, err
	}
	minor, err := parseVersionComponent(value, "minor", parts[1])
	if err != nil {
		return Version{}, err
	}
	patch, err := parseVersionComponent(value, "patch", parts[2])
	if err != nil {
		return Version{}, err
	}

	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// Compare reports the order of v and other.
//
// It returns -1 if v is less, 0 if they are equal, and +1 if v is greater.
// Components are compared as major, then minor, then patch.
func (v Version) Compare(other Version) int {
	if result := cmp.Compare(v.Major, other.Major); result != 0 {
		return result
	}
	if result := cmp.Compare(v.Minor, other.Minor); result != 0 {
		return result
	}

	return cmp.Compare(v.Patch, other.Patch)
}

// String returns the canonical MAJOR.MINOR.PATCH form.
func (v Version) String() string {
	return strconv.FormatUint(v.Major, 10) + "." +
		strconv.FormatUint(v.Minor, 10) + "." +
		strconv.FormatUint(v.Patch, 10)
}

// Tag returns the exact-version registry tag, which equals [Version.String].
//
// A decimal triple always starts with a digit and contains only digits and
// dots, so the result is a valid OCI tag.
func (v Version) Tag() Tag {
	return Tag(v.String())
}

// rejectVersionDecorators rejects prerelease and build-metadata suffixes.
//
// A leading + or - on a component is left for [parseVersionComponent] so
// signed numbers stay distinct from SemVer suffixes.
func rejectVersionDecorators(value string) error {
	if plus := strings.IndexByte(value, '+'); plus >= 0 && !isComponentStart(value, plus) {
		return fmt.Errorf("version %q has build metadata", value)
	}
	if hyphen := strings.IndexByte(value, '-'); hyphen >= 0 && !isComponentStart(value, hyphen) {
		return fmt.Errorf("version %q has a prerelease suffix", value)
	}

	return nil
}

// isComponentStart reports whether index is the first character of a dotted
// component, including the start of the string.
func isComponentStart(value string, index int) bool {
	return index == 0 || value[index-1] == '.'
}

// parseVersionComponent parses one decimal component of a stable version.
func parseVersionComponent(input, name, part string) (uint64, error) {
	if part == "" {
		return 0, fmt.Errorf("version %q has an empty %s component", input, name)
	}
	if part[0] == '+' || part[0] == '-' {
		return 0, fmt.Errorf("version %q has a signed %s component", input, name)
	}
	if len(part) > 1 && part[0] == '0' {
		return 0, fmt.Errorf("version %q has a leading zero in the %s component", input, name)
	}
	if strings.Contains(part, "+") {
		return 0, fmt.Errorf("version %q has build metadata", input)
	}
	if strings.Contains(part, "-") {
		return 0, fmt.Errorf("version %q has a prerelease suffix", input)
	}
	for _, r := range part {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("version %q has a non-numeric %s component", input, name)
		}
	}

	value, err := strconv.ParseUint(part, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("version %q has a %s component that exceeds uint64", input, name)
	}

	return value, nil
}
