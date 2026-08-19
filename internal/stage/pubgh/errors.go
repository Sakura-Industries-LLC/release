package pubgh

import "errors"

// Sentinel errors classified for [Publish].
var (
	// ErrNoDraft reports that no release for the tag appeared before the
	// draft poll budget expired.
	ErrNoDraft = errors.New("no draft release for the tag")
	// ErrAmbiguousRelease reports that the tag resolves to more than one
	// GitHub Release.
	ErrAmbiguousRelease = errors.New("tag does not uniquely resolve to one release")
	// ErrUnexpectedAsset reports that the release already contains an asset
	// name outside the expected closed set. Unexpected assets are refused
	// and are never deleted.
	ErrUnexpectedAsset = errors.New("release contains an unexpected asset")
	// ErrIndeterminate reports that a non-draft release exists for the tag
	// but its assets do not match the expected closed set exactly, so
	// [Publish] cannot tell whether a prior run completed successfully.
	ErrIndeterminate = errors.New("release state is indeterminate")
)
