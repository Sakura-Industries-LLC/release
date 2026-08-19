// Package cosign implements [puboci.Signer] by invoking the pinned cosign binary.
//
// [New] builds a signer that shells out to `cosign sign --yes --recursive`
// against image@digest. Signing is keyless and recursive: the index and every
// referenced platform manifest are signed. The adapter performs no registry
// reasoning of its own. Keyless signing uses the ambient OIDC environment;
// this package never reads, stores, or logs a key or token.
package cosign
