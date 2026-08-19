// Package puboci reads a local OCI layout and publishes digest-addressed content and tags.
//
// [ReadLayout] loads an extracted oci-image/layout directory. [Prepare] plans
// tags through [StateReader], pushes content through [ContentPusher], and
// signs the published index through [Signer]. [Finalize] collects fresh
// registry state, refuses drift from the prepare observations, and commits
// tags through [TagCommitter] only after those checks succeed.
package puboci
