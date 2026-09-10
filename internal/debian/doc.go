// Package debian holds the behavior tests for the Forgejo Debian transport.
//
// The transport is a shell script,
// .github/actions/publish-forgejo-debian/publish-debian.sh, so that an
// operator rehearsing a publication runs the same code a release runs. These
// tests drive that script against a fake registry and hold it to the rules the
// publisher depends on: which package set it accepts, what it uploads, what it
// does when the registry already holds a file under the same key, and where
// the publisher credential is allowed to travel.
package debian
