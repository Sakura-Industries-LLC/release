// Package cosign implements [puboci.Signer] and [pubgh.BlobVerifier] by
// invoking the pinned cosign binary.
//
// [New] builds a signer that shells out to `cosign sign --yes --recursive`
// against image@digest. Signing is keyless and recursive: the index and every
// referenced platform manifest are signed. [NewVerifier] builds a verifier
// that shells out to `cosign verify-blob` against a detached Sigstore bundle.
// The adapter performs no registry or policy reasoning of its own: identity
// and issuer come from the request. Keyless credentials use the ambient OIDC
// environment; this package never reads, stores, or logs a key or token.
package cosign
