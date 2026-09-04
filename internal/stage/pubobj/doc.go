// Package pubobj publishes a verified closed release bundle to a private
// S3-compatible object store.
//
// [Publish] is convergent and immutable: an existing object with the same
// digest is left unchanged, an existing object with a different digest is
// refused, and a missing object is streamed up. Store errors are not retried;
// a later invocation converges from the current remote digests. The
// [ObjectStore] port reuses [pkgrepo.StoredContent] and [pkgrepo.UploadRequest]
// so one S3-compatible adapter can serve both the package repository and
// release downloads.
package pubobj
