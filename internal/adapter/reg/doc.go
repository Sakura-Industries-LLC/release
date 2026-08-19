// Package reg implements [puboci.StateReader], [puboci.ContentPusher], and
// [puboci.TagCommitter] with oras-go.
//
// [New] builds a registry client for tag reads, digest-addressed writes, and
// serial tag commits. Token text is applied only when building a per-request
// authenticated transport and is never stored in a formattable field or
// included in returned errors. Resolve, Version, PushBlob, PushManifest,
// Verify, and Commit classify registry failures as absent, auth, retryable, or
// corrupt. An already-present blob or manifest is success. [Client.Commit]
// applies tags one at a time and verifies each write before the next starts.
package reg
