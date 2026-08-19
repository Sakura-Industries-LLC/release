// Package goprof selects canonical Linux binaries from GoReleaser artifacts.json.
//
// ParseArtifacts decodes the pinned GoReleaser record shape. SelectBinaries is
// a pure function that requires exactly one linux/amd64 Binary and one
// linux/arm64 Binary. Paths are relative to the --dist directory basename.
// VerifyBinaries checks that each selected path is lexically confined, a
// regular file, and owner-executable.
package goprof
