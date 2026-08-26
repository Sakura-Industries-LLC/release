// Package goprof selects canonical Linux binaries from GoReleaser artifacts.json.
//
// ParseArtifacts decodes the pinned GoReleaser record shape. SelectBinaries is
// a pure function that selects every linux/{amd64,arm64} Binary, requires the
// same nonempty name set on both architectures, and sorts architecture-major
// then name-ascending. Paths are relative to the --dist directory basename.
// VerifyBinaries checks that each selected path is lexically confined, a
// regular file, and owner-executable.
package goprof
