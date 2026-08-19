// Package reg implements [puboci.StateReader] and [puboci.ContentPusher] with oras-go.
//
// [New] builds a registry client for tag reads and digest-addressed writes.
// Token text is applied only when building a per-request authenticated
// transport and is never stored in a formattable field or included in returned
// errors. The client never creates, moves, or deletes a tag. Resolve, Version,
// PushBlob, PushManifest, and Verify classify registry failures as absent,
// auth, retryable, or corrupt. An already-present blob or manifest is success.
package reg
