// Package pubgh verifies GitHub Actions artifact handoff metadata.
//
// [VerifyHandoff] is a pure check over the [ArtifactMeta] port. It confirms
// that an artifact exists, belongs to the expected workflow run, has not
// expired, and reports the caller-supplied digest. It does not download the
// artifact and does not recompute the Actions ZIP transport digest.
package pubgh
