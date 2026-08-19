// Package ghact implements [pubgh.ArtifactMeta] with go-github.
//
// [New] accepts an already-authenticated [github.Client] so token text never
// enters domain values or error strings. [NewAuthenticated] builds that
// client for the public API or a GITHUB_API_URL enterprise/stub base. Get
// maps GitHub artifact metadata onto [pubgh.ArtifactMetadata] and classifies
// API failures as absent, auth, retryable, or malformed.
package ghact
