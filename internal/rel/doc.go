// Package rel is the pure release model: versions, digests, git tags, OCI tags, and tag plans.
//
// Parse functions construct domain values. [ParseGitTag] accepts a git tag name,
// including a GoReleaser Pro monorepo prefix. [PlanTags] decides which immutable
// exact tag and moving channel tags a candidate release may apply. The package
// performs no I/O and depends only on the standard library.
package rel
