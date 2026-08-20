// Package melange implements [image.APKBuilder] by invoking the pinned
// Melange binary.
//
// [New] builds an adapter that shells out to `melange compile`,
// `melange keygen`, and one `melange build` per requested source tree.
// The adapter performs no package or repository reasoning of its own:
// configuration paths, the signing key, provenance, and source trees
// come from the request. Working directory is irrelevant; every path
// the child receives is absolute.
package melange
