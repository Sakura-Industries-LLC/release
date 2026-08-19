// Package ghrel implements [pubgh.ReleaseReader] and [pubgh.Publisher] with go-github.
//
// [New] accepts an already-authenticated [github.Client] so token text never
// enters domain values or error strings. [NewAuthenticated] builds that
// client from a [rel.Secret] for the public API or a GITHUB_API_URL
// enterprise/stub base. FindDraft paginates repository releases once and
// returns the unique tag match, WaitAssets paginates release assets once
// and returns the current view, Get maps one release, and Publish undrafts
// a release by sending draft=false only. Polling lives in [pubgh.Publish].
// API failures are classified as absent, auth, retryable, or malformed.
package ghrel
