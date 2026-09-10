// Package arch holds the behavior tests for the Forgejo Arch transport.
//
// The transport is a shell script,
// .github/actions/publish-forgejo-arch/publish-arch.sh, so that an operator
// rehearsing a publication runs the same code a release runs. These tests
// drive that script against a fake registry and hold it to the rules the
// publisher depends on: which package set it accepts, what it uploads, what
// it requires the registry to hold afterwards, what it does when the registry
// already holds a package under the same key, and where the publisher
// credential is allowed to travel.
package arch
