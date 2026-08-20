package image

import "fmt"

// Platform is a canonical Linux OCI platform string.
//
// The only constructor is [ParsePlatform]. The zero value is invalid.
type Platform string

// APKArch is a Melange and apko architecture name.
//
// Known values are produced by [Platform.APKArch]. The zero value is invalid.
type APKArch string

const (
	// PlatformAMD64 is linux/amd64.
	PlatformAMD64 Platform = "linux/amd64"
	// PlatformARM64 is linux/arm64.
	PlatformARM64 Platform = "linux/arm64"
	// ArchX8664 is the APK architecture for [PlatformAMD64].
	ArchX8664 APKArch = "x86_64"
	// ArchAArch64 is the APK architecture for [PlatformARM64].
	ArchAArch64 APKArch = "aarch64"
)

// ParsePlatform constructs a [Platform] from a canonical Linux platform string.
//
// Only [PlatformAMD64] and [PlatformARM64] are accepted. Any other value,
// including the empty string, is rejected. Error text names the problem
// and echoes the input.
func ParsePlatform(raw string) (Platform, error) {
	switch Platform(raw) {
	case PlatformAMD64, PlatformARM64:
		return Platform(raw), nil
	default:
		return "", fmt.Errorf("platform %q is not linux/amd64 or linux/arm64", raw)
	}
}

// APKArch returns the APK architecture that p maps onto.
//
// [PlatformAMD64] maps to [ArchX8664]. [PlatformARM64] maps to
// [ArchAArch64]. An unrecognized platform returns the zero value.
func (p Platform) APKArch() APKArch {
	switch p {
	case PlatformAMD64:
		return ArchX8664
	case PlatformARM64:
		return ArchAArch64
	default:
		return ""
	}
}

// String returns the canonical linux/<arch> platform.
func (p Platform) String() string {
	return string(p)
}

// String returns the APK architecture name.
func (a APKArch) String() string {
	return string(a)
}
