// Package pubgh verifies GitHub Actions artifact handoff metadata and
// signed release bundles.
//
// [VerifyHandoff] is a pure check over the [ArtifactMeta] port. It confirms
// that an artifact exists, belongs to the expected workflow run, has not
// expired, and reports the caller-supplied digest. It does not download the
// artifact and does not recompute the Actions ZIP transport digest.
//
// [VerifyBundle] reconciles a distribution directory against its
// checksums.txt claim and then verifies the detached Sigstore bundle
// through [BlobVerifier]. Local checks run before the signature check.
package pubgh
