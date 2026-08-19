// Package gitx implements [pubgh.RefResolver] by invoking the git binary.
//
// [New] builds a resolver that shells out to `git rev-list -n 1 <tag>`
// against a checkout. The command resolves the tag to the commit it
// points at, which is the binding between the release tag and
// github.sha. The adapter performs no GitHub API calls and never
// creates, moves, or deletes a tag.
package gitx
