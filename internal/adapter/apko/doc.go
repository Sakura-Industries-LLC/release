// Package apko implements [image.Composer] by invoking the pinned apko
// binary.
//
// [New] builds an adapter that shells out to `apko lock` and then
// `apko build` in the request working directory. The adapter performs
// no lockfile or layout reasoning of its own: configuration, repository,
// keyring, lock, SBOM, layout, reference, architectures, and annotations
// come from the request. Both invocations run with [exec.Cmd.Dir] set to
// request.Dir; every other path the child receives is request-relative.
package apko
