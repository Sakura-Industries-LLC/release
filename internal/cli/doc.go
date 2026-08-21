// Package cli implements the release-cli Cobra command tree.
//
// NewRootCommand builds a fresh command with injected streams and an optional
// [LookupEnv] seam. The tree exposes stage, plan tags, publish oci prepare,
// publish oci finalize, publish github, publish homebrew, publish scoop,
// verify bundle, verify handoff, and version. Flags override RELEASE_*
// [cobra.Flag.Changed]; there is no config file. ExitCode maps errors onto
// the process contract: 0 success, 1 a release-contract, verification, or
// command failure, 2 usage or configuration error.
package cli
