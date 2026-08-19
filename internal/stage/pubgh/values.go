package pubgh

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/meigma/release/internal/rel"
)

const (
	// commitSHALen is the length of a Git commit object ID.
	commitSHALen = 40
	// uploadedState is the GitHub asset state that means the file is ready.
	uploadedState = "uploaded"
	// defaultDraftAttempts is the GR-06 listReleases retry budget.
	defaultDraftAttempts = 24
	// defaultDraftWait is the GR-06 pause between draft lookups.
	defaultDraftWait = 5 * time.Second
	// defaultAssetAttempts is the GR-16 listReleaseAssets retry budget.
	defaultAssetAttempts = 12
	// defaultAssetWait is the GR-16 pause between asset lookups.
	defaultAssetWait = time.Second
)

// ReleaseID is a positive GitHub Release identifier.
//
// The only constructors are [ParseReleaseID] and [ReleaseIDFromInt].
// The zero value is invalid.
type ReleaseID int64

// CommitSHA is a 40-digit lowercase Git commit object ID.
//
// The only constructor is [ParseCommitSHA]. The zero value is invalid.
type CommitSHA string

// AssetPath is a local filesystem path handed to [AssetReplacer].
type AssetPath string

// Release is one GitHub Release observed through [ReleaseReader].
type Release struct {
	// ID is the GitHub Release identifier.
	ID ReleaseID
	// Tag is the git tag bound to the release.
	Tag rel.Tag
	// Draft reports whether the release is still a draft.
	Draft bool
	// URL is the GitHub html_url of the release.
	URL string
}

// Asset is one GitHub Release asset observed through [ReleaseReader].
type Asset struct {
	// Name is the asset file name on the release.
	Name string
	// Digest is the GitHub-reported digest, "sha256:<64 hex>". It is empty
	// until GitHub finishes processing the upload.
	Digest string
	// State is the GitHub-reported asset state. It is "uploaded" when the
	// asset is ready.
	State string
}

// AssetsView is the set of assets currently attached to a release.
type AssetsView struct {
	// Assets are the GitHub-reported release assets, in API order.
	Assets []Asset
}

// PollPolicy is a bounded lookup budget.
//
// A zero Attempts or Wait field is replaced by the matching field from
// [DefaultDraftPolicy] or [DefaultAssetPolicy].
type PollPolicy struct {
	// Attempts is the maximum number of lookups, including the first.
	Attempts int
	// Wait is the pause after each failed lookup, including the last.
	Wait time.Duration
}

// ParseReleaseID constructs a [ReleaseID] from a decimal string.
func ParseReleaseID(raw string) (ReleaseID, error) {
	value, err := parseSafeID("release id", raw)
	if err != nil {
		return 0, err
	}

	return ReleaseID(value), nil
}

// ReleaseIDFromInt constructs a [ReleaseID] from a positive safe integer.
func ReleaseIDFromInt(value int64) (ReleaseID, error) {
	if err := checkSafeID("release id", value); err != nil {
		return 0, err
	}

	return ReleaseID(value), nil
}

// Int64 returns the identifier as int64.
func (id ReleaseID) Int64() int64 {
	return int64(id)
}

// String returns the decimal identifier.
func (id ReleaseID) String() string {
	return strconv.FormatInt(int64(id), 10)
}

// ParseCommitSHA constructs a [CommitSHA] from a 40-digit hex string.
//
// Surrounding space is trimmed. Uppercase hex is normalized to lowercase.
func ParseCommitSHA(raw string) (CommitSHA, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("commit sha is empty")
	}
	lowered := strings.ToLower(trimmed)
	if len(lowered) != commitSHALen {
		return "", fmt.Errorf("commit sha %q has length %d, want %d", lowered, len(lowered), commitSHALen)
	}
	for _, r := range lowered {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return "", fmt.Errorf("commit sha %q is not hexadecimal", lowered)
		}
	}

	return CommitSHA(lowered), nil
}

// String returns the lowercase commit object ID.
func (s CommitSHA) String() string {
	return string(s)
}

// String returns the filesystem path.
func (p AssetPath) String() string {
	return string(p)
}

// DefaultDraftPolicy returns the GR-06 draft-discovery budget: 24 attempts,
// 5s apart.
func DefaultDraftPolicy() PollPolicy {
	return PollPolicy{Attempts: defaultDraftAttempts, Wait: defaultDraftWait}
}

// DefaultAssetPolicy returns the GR-16 asset-convergence budget: 12 attempts,
// 1s apart.
func DefaultAssetPolicy() PollPolicy {
	return PollPolicy{Attempts: defaultAssetAttempts, Wait: defaultAssetWait}
}

// resolvePolicy replaces non-positive fields with the matching fallback field.
func resolvePolicy(policy, fallback PollPolicy) PollPolicy {
	if policy.Attempts <= 0 {
		policy.Attempts = fallback.Attempts
	}
	if policy.Wait <= 0 {
		policy.Wait = fallback.Wait
	}

	return policy
}
