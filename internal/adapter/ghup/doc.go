// Package ghup implements [pubgh.AssetReplacer] by invoking the gh CLI.
//
// [New] builds a replacer that shells out to
// `gh release upload <tag> <path>... --repo <owner>/<name> --clobber` as an
// explicit argument slice. --clobber replaces an existing release asset of
// the same name. This adapter never deletes an asset it was not given: names
// outside the expected set are the caller's problem to refuse.
//
// The App token is applied only as GH_TOKEN in the child environment. It is
// never placed on argv, stored in a formattable field, or included in returned
// errors.
package ghup
