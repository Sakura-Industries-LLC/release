// Package puboci reads a local OCI layout and prepares digest-addressed publication.
//
// [ReadLayout] loads an extracted oci-image/layout directory. [Prepare] plans
// tags through [StateReader], pushes content through [ContentPusher], and
// signs the published index through [Signer]. Tagging is not part of this
// package yet.
package puboci
