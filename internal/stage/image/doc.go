// Package image stages Linux binaries into signed APK repositories and a locked OCI layout.
//
// [Build] is the engine: it validates staged binary facts, copies them into a
// scratch workspace, verifies each one as a static ELF, and then drives
// [APKBuilder] and [Composer]. The package does not import the staging
// projection; callers convert that wire type into [BuildInput].
package image
