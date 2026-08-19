// Package cli implements the release-cli Cobra command tree.
//
// NewRootCommand builds a fresh command with injected streams and an optional
// [LookupEnv] seam. The tree exposes stage, verify handoff, and version.
// Flags override RELEASE_* environment variables via [cobra.Flag.Changed];
// there is no config file. ExitCode maps errors onto the process contract:
// 0 success, 1 verification failure, 2 usage or configuration error.
package cli
