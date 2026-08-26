// Package image stages Linux binaries into signed APK repositories and a locked OCI layout,
// and verifies that layout against the release contract.
//
// [Build] validates staged binary facts, copies them into a scratch workspace,
// verifies each one as a static ELF, and then drives [APKBuilder] and
// [Composer]. [VerifyLayout] reads the on-disk layout byte for byte and checks
// the index, manifests, configs, and layer binary against [ExpectedImage].
// [VerifySBOMs] checks the architecture SPDX documents. [CanonicalDigests]
// hashes work/sources/<arch>/<binary-name>. The package does not import the
// staging projection; callers convert that wire type into [BuildInput].
package image
