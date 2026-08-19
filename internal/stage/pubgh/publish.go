package pubgh

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage"
)

// ReleaseReader observes GitHub Releases and their assets.
//
// Implementations must not create, undraft, or delete a release, and must
// not delete an asset. FindDraft and WaitAssets perform one paginated
// snapshot each; they take no poll budget. [Publish] owns the 24×5s draft
// budget and the 12×1s asset-convergence budget.
type ReleaseReader interface {
	// FindDraft returns the unique release whose tag_name equals tag.
	//
	// The adapter lists once. Zero matches is [ErrNoDraft] immediately.
	// More than one match is [ErrAmbiguousRelease]. Otherwise it returns
	// the release with its real Draft flag; it does not wait for a draft
	// and does not refuse a public release. [Publish] retries [ErrNoDraft]
	// under [PublishInput.Draft].
	FindDraft(ctx context.Context, repository Repository, tag rel.Tag) (Release, error)
	// WaitAssets returns the current assets on release after one list pass.
	//
	// The adapter does not judge readiness, count, or digests. [Publish]
	// re-reads under [PublishInput.Asset] until the expected set matches.
	// Implementations must not mutate the release.
	WaitAssets(ctx context.Context, repository Repository, release ReleaseID) (AssetsView, error)
	// Get returns the current release metadata for id.
	Get(ctx context.Context, repository Repository, release ReleaseID) (Release, error)
}

// AssetReplacer uploads the expected local assets onto a draft release.
//
// Clobber semantics live in the adapter. Unexpected existing assets must
// already have been refused by [Publish] before Replace is called.
type AssetReplacer interface {
	// Replace uploads expected onto the release identified by tag.
	Replace(ctx context.Context, repository Repository, tag rel.Tag, expected []AssetPath) error
}

// Publisher leaves draft state on a populated release.
type Publisher interface {
	// Publish marks the release public. It must not re-draft a public
	// release and must not create a release.
	Publish(ctx context.Context, repository Repository, release ReleaseID) error
}

// RefResolver resolves a git tag to the commit it currently names.
type RefResolver interface {
	// Resolve returns the commit object ID named by tag.
	Resolve(ctx context.Context, tag rel.Tag) (CommitSHA, error)
}

// PublishInput is the closed input to [Publish].
type PublishInput struct {
	// Repository is the GitHub owner/name that owns the release.
	Repository Repository
	// Tag is the git tag bound to the draft release.
	Tag rel.Tag
	// Commit is the workflow's github.sha. The tag must resolve to it.
	Commit CommitSHA
	// Expected is the closed bundle rebuilt from the distribution
	// directory. Payload and control names carry their hex digests.
	Expected Bundle
	// Assets are the local paths to upload, in [Bundle] order.
	Assets []AssetPath
	// Undraft is false for --no-undraft: converge and stop while still
	// a draft.
	Undraft bool
	// Draft is the draft-discovery budget. The zero value selects
	// [DefaultDraftPolicy].
	Draft PollPolicy
	// Asset is the asset-convergence budget. The zero value selects
	// [DefaultAssetPolicy].
	Asset PollPolicy
	// Sleep waits between retryable GitHub calls and between poll
	// attempts. Nil selects a context-aware timer.
	Sleep SleepFunc
}

// PublishResult is the JSON document produced by a successful [Publish].
type PublishResult struct {
	// ReleaseID is the GitHub Release identifier.
	ReleaseID int64 `json:"release_id"`
	// Tag is the git tag bound to the release.
	Tag string `json:"tag"`
	// URL is the GitHub html_url of the release.
	URL string `json:"url"`
	// Draft reports the release draft state after the run.
	Draft bool `json:"draft"`
	// Assets are the converged asset names, sorted.
	Assets []string `json:"assets"`
}

// Publish binds a tag to github.sha, uploads the expected assets onto the
// matching draft, converges them, and optionally leaves draft state.
//
// The order is fail-closed at every step:
//
//  1. Validate the input and reject a nil context or port.
//  2. Resolve the tag and fail unless it equals [PublishInput.Commit].
//  3. Find the draft. Absence after the budget is [ErrNoDraft]. More than
//     one release for the tag is [ErrAmbiguousRelease].
//  4. A non-draft release is a rerun after a completed publication. If
//     Undraft is false, that is [ErrIndeterminate]: a draft-only
//     publication was requested but the release is already public. If
//     Undraft is true and the assets match the expected set exactly
//     (same names and count, every asset uploaded with a nonempty
//     digest, every digest equal to "sha256:" plus the expected hex),
//     return success with Draft false. Any other difference is
//     [ErrIndeterminate]. This branch never creates, re-drafts,
//     uploads, or deletes.
//  5. Read assets once before uploading and refuse any existing name
//     outside the expected set with [ErrUnexpectedAsset]. Unexpected
//     assets are never deleted.
//  6. Replace every expected asset path. Clobber lives in the adapter.
//  7. Converge under the asset policy. Incomplete readiness (count
//     below expected, missing digest, or a state other than uploaded)
//     is retried. A duplicate name, unexpected name, count above
//     expected, or digest mismatch fails immediately.
//  8. If Undraft, publish and require the final Get to report Draft
//     false. A Publish, Get, or draft-state failure after the undraft
//     call is [ErrIndeterminate] because the remote release may
//     already have left draft. Otherwise require the release to still
//     be a draft.
//  9. Return the release URL and the sorted converged asset names.
//
// Transient failures classified [ErrRetryable] are retried with the same
// bounded helper used by [VerifyHandoff] (four attempts, 1s/2s/4s). Tag
// and SHA mismatch, unexpected assets, and digest mismatches are never
// retried. A cancelled [context.Context] fails immediately.
func Publish(
	ctx context.Context,
	input PublishInput,
	reader ReleaseReader,
	replacer AssetReplacer,
	publisher Publisher,
	resolver RefResolver,
) (PublishResult, error) {
	if err := validatePublish(ctx, input, reader, replacer, publisher, resolver); err != nil {
		return PublishResult{}, err
	}
	sleep := input.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	input.Draft = resolvePolicy(input.Draft, DefaultDraftPolicy())
	input.Asset = resolvePolicy(input.Asset, DefaultAssetPolicy())

	return publish(ctx, input, reader, replacer, publisher, resolver, sleep)
}

// validatePublish rejects a nil context, a nil port, and a zero input.
func validatePublish(
	ctx context.Context,
	input PublishInput,
	reader ReleaseReader,
	replacer AssetReplacer,
	publisher Publisher,
	resolver RefResolver,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if reader == nil {
		return errors.New("release reader is nil")
	}
	if replacer == nil {
		return errors.New("asset replacer is nil")
	}
	if publisher == nil {
		return errors.New("publisher is nil")
	}
	if resolver == nil {
		return errors.New("ref resolver is nil")
	}
	if input.Repository.Owner == "" || input.Repository.Name == "" {
		return errors.New("publish repository is empty")
	}
	if input.Tag == "" {
		return errors.New("publish tag is empty")
	}
	if input.Commit == "" {
		return errors.New("publish commit is empty")
	}
	if len(input.Expected.Names()) == 0 {
		return errors.New("publish expected bundle is empty")
	}
	if len(input.Assets) != len(input.Expected.Names()) {
		return fmt.Errorf(
			"publish asset paths: got %d, want %d",
			len(input.Assets),
			len(input.Expected.Names()),
		)
	}

	return nil
}

// publish runs the ordered state machine after exported guards.
func publish(
	ctx context.Context,
	input PublishInput,
	reader ReleaseReader,
	replacer AssetReplacer,
	publisher Publisher,
	resolver RefResolver,
	sleep SleepFunc,
) (PublishResult, error) {
	if err := ctx.Err(); err != nil {
		return PublishResult{}, fmt.Errorf("publish: %w", err)
	}

	resolved, err := resolveCommit(ctx, resolver, input.Tag, sleep)
	if err != nil {
		return PublishResult{}, err
	}
	if resolved != input.Commit {
		return PublishResult{}, fmt.Errorf(
			"tag %s resolves to %s, expected %s",
			input.Tag,
			resolved,
			input.Commit,
		)
	}

	found, err := findDraft(ctx, reader, input, sleep)
	if err != nil {
		return PublishResult{}, err
	}
	if !found.Draft {
		if !input.Undraft {
			return PublishResult{}, fmt.Errorf(
				"%w: draft-only publication requested but release %s is already public",
				ErrIndeterminate,
				found.ID,
			)
		}

		return acceptPublished(ctx, reader, input, found, sleep)
	}

	pre, err := readAssets(ctx, reader, input, found.ID, sleep)
	if err != nil {
		return PublishResult{}, err
	}
	if refuseErr := refuseUnexpected(pre, input.Expected); refuseErr != nil {
		return PublishResult{}, refuseErr
	}

	if replaceErr := replaceAssets(ctx, replacer, input, sleep); replaceErr != nil {
		return PublishResult{}, replaceErr
	}

	view, err := convergeAssets(ctx, reader, input, found.ID, sleep)
	if err != nil {
		return PublishResult{}, err
	}

	final, err := finalizeRelease(ctx, reader, publisher, input, found.ID, sleep)
	if err != nil {
		return PublishResult{}, err
	}

	return publishResult(final, view), nil
}

// resolveCommit returns the commit named by tag, retrying [ErrRetryable].
func resolveCommit(ctx context.Context, resolver RefResolver, tag rel.Tag, sleep SleepFunc) (CommitSHA, error) {
	var sha CommitSHA
	err := retryOp(ctx, sleep, fmt.Sprintf("resolve tag %s", tag), func() error {
		got, callErr := resolver.Resolve(ctx, tag)
		if callErr != nil {
			return callErr
		}
		sha = got

		return nil
	})
	if err != nil {
		return "", err
	}

	return sha, nil
}

// findDraft locates the unique release for the tag under the draft policy.
func findDraft(ctx context.Context, reader ReleaseReader, input PublishInput, sleep SleepFunc) (Release, error) {
	var found Release
	err := poll(ctx, sleep, input.Draft, func() error {
		var snapshot Release
		callErr := retryOp(ctx, sleep, fmt.Sprintf("find draft for %s", input.Tag), func() error {
			got, innerErr := reader.FindDraft(ctx, input.Repository, input.Tag)
			if innerErr != nil {
				return innerErr
			}
			snapshot = got

			return nil
		})
		if callErr != nil {
			return callErr
		}
		found = snapshot

		return nil
	})
	if err != nil {
		return Release{}, err
	}

	return found, nil
}

// acceptPublished handles a non-draft release found for the tag.
//
// Exact means the observed assets have the same names and count as the
// expected bundle, every asset is uploaded with a nonempty digest, and
// every digest equals "sha256:" plus the expected hex. Anything else is
// [ErrIndeterminate]. [publish] has already refused this branch when
// Undraft is false. The branch never mutates: no upload, no undraft,
// no re-draft, no deletion.
func acceptPublished(
	ctx context.Context,
	reader ReleaseReader,
	input PublishInput,
	found Release,
	sleep SleepFunc,
) (PublishResult, error) {
	view, err := readAssets(ctx, reader, input, found.ID, sleep)
	if err != nil {
		return PublishResult{}, fmt.Errorf("%w: %w", ErrIndeterminate, err)
	}
	if err := validateConverged(view, input.Expected); err != nil {
		return PublishResult{}, fmt.Errorf("%w: %w", ErrIndeterminate, err)
	}

	return publishResult(found, view), nil
}

// readAssets fetches one asset snapshot, retrying [ErrRetryable].
func readAssets(
	ctx context.Context,
	reader ReleaseReader,
	input PublishInput,
	id ReleaseID,
	sleep SleepFunc,
) (AssetsView, error) {
	var view AssetsView
	err := retryOp(ctx, sleep, fmt.Sprintf("wait assets for release %s", id), func() error {
		got, callErr := reader.WaitAssets(ctx, input.Repository, id)
		if callErr != nil {
			return callErr
		}
		view = got

		return nil
	})
	if err != nil {
		return AssetsView{}, err
	}

	return view, nil
}

// convergeAssets polls until the assets match expected or the budget ends.
func convergeAssets(
	ctx context.Context,
	reader ReleaseReader,
	input PublishInput,
	id ReleaseID,
	sleep SleepFunc,
) (AssetsView, error) {
	var view AssetsView
	err := poll(ctx, sleep, input.Asset, func() error {
		got, callErr := readAssets(ctx, reader, input, id, sleep)
		if callErr != nil {
			return callErr
		}
		view = got

		return validateConverged(got, input.Expected)
	})
	if err != nil {
		return AssetsView{}, err
	}

	return view, nil
}

// refuseUnexpected fails when any observed asset name is outside expected.
func refuseUnexpected(view AssetsView, expected Bundle) error {
	allowed := expectedNameSet(expected)
	for _, asset := range view.Assets {
		if _, ok := allowed[asset.Name]; !ok {
			return fmt.Errorf("%w: %s", ErrUnexpectedAsset, asset.Name)
		}
	}

	return nil
}

// replaceAssets uploads every expected path, retrying [ErrRetryable].
func replaceAssets(ctx context.Context, replacer AssetReplacer, input PublishInput, sleep SleepFunc) error {
	return retryOp(ctx, sleep, fmt.Sprintf("replace assets for %s", input.Tag), func() error {
		return replacer.Replace(ctx, input.Repository, input.Tag, input.Assets)
	})
}

// validateConverged requires the observed assets to match expected exactly.
//
// Exact means: no duplicate names, the count equals the expected count,
// every asset is in the expected name set, every asset has state
// "uploaded" and a nonempty digest, and every digest equals
// "sha256:" plus the expected hex. Count below expected, digest-presence,
// and state mismatches are [incompleteError] so the poll loop can retry
// them. Duplicate names, unexpected names, a count above expected, and
// digest-value mismatches are terminal and are never retried.
func validateConverged(view AssetsView, expected Bundle) error {
	entries := expectedEntries(expected)
	if len(view.Assets) > len(entries) {
		return fmt.Errorf("asset count %d, want %d", len(view.Assets), len(entries))
	}
	if len(view.Assets) < len(entries) {
		return incompleteError{msg: fmt.Sprintf("asset count %d, want %d", len(view.Assets), len(entries))}
	}

	seen := make(map[string]struct{}, len(view.Assets))
	for _, asset := range view.Assets {
		if _, dup := seen[asset.Name]; dup {
			return fmt.Errorf("duplicate asset name %s", asset.Name)
		}
		seen[asset.Name] = struct{}{}
		want, ok := entries[asset.Name]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnexpectedAsset, asset.Name)
		}
		if asset.State != uploadedState {
			return incompleteError{
				msg: fmt.Sprintf("asset %s state %q, want %q", asset.Name, asset.State, uploadedState),
			}
		}
		if asset.Digest == "" {
			return incompleteError{msg: fmt.Sprintf("asset %s has no digest", asset.Name)}
		}
		if asset.Digest != githubDigest(want) {
			return fmt.Errorf("asset %s digest %s, want %s", asset.Name, asset.Digest, githubDigest(want))
		}
	}

	return nil
}

// finalizeRelease undrafts when requested and checks the final draft flag.
func finalizeRelease(
	ctx context.Context,
	reader ReleaseReader,
	publisher Publisher,
	input PublishInput,
	id ReleaseID,
	sleep SleepFunc,
) (Release, error) {
	if input.Undraft {
		err := retryOp(ctx, sleep, fmt.Sprintf("publish release %s", id), func() error {
			return publisher.Publish(ctx, input.Repository, id)
		})
		if err != nil {
			return Release{}, fmt.Errorf(
				"%w: publish of release %s may have applied: %w",
				ErrIndeterminate,
				id,
				err,
			)
		}
	}

	final, err := getRelease(ctx, reader, input.Repository, id, sleep)
	if err != nil {
		if input.Undraft {
			return Release{}, fmt.Errorf("%w: %w", ErrIndeterminate, err)
		}

		return Release{}, err
	}
	if input.Undraft && final.Draft {
		return Release{}, fmt.Errorf("%w: release %s is still a draft after publish", ErrIndeterminate, id)
	}
	if !input.Undraft && !final.Draft {
		return Release{}, fmt.Errorf("%w: release %s left draft state", ErrIndeterminate, id)
	}

	return final, nil
}

// getRelease fetches current release metadata, retrying [ErrRetryable].
func getRelease(
	ctx context.Context,
	reader ReleaseReader,
	repository Repository,
	id ReleaseID,
	sleep SleepFunc,
) (Release, error) {
	var found Release
	err := retryOp(ctx, sleep, fmt.Sprintf("get release %s", id), func() error {
		got, callErr := reader.Get(ctx, repository, id)
		if callErr != nil {
			return callErr
		}
		found = got

		return nil
	})
	if err != nil {
		return Release{}, err
	}

	return found, nil
}

// publishResult builds the JSON document from the final release and assets.
func publishResult(found Release, view AssetsView) PublishResult {
	names := make([]string, 0, len(view.Assets))
	for _, asset := range view.Assets {
		names = append(names, asset.Name)
	}
	slices.Sort(names)

	return PublishResult{
		ReleaseID: found.ID.Int64(),
		Tag:       found.Tag.String(),
		URL:       found.URL,
		Draft:     found.Draft,
		Assets:    names,
	}
}

// expectedNameSet is the closed set of expected asset names.
func expectedNameSet(expected Bundle) map[string]struct{} {
	names := expected.Names()
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}

	return set
}

// expectedEntries maps each expected asset name to its hex digest.
func expectedEntries(expected Bundle) map[string]stage.Digest {
	entries := make(map[string]stage.Digest, len(expected.Payloads)+len(expected.Controls))
	for _, entry := range expected.Payloads {
		entries[entry.Name] = entry.Digest
	}
	for _, entry := range expected.Controls {
		entries[entry.Name] = entry.Digest
	}

	return entries
}

// githubDigest formats a bundle hex digest as GitHub's sha256:<hex> form.
func githubDigest(digest stage.Digest) string {
	return digestPrefix + digest.String()
}

// poll repeats op until it succeeds or the policy budget expires.
//
// [ErrNoDraft] and [incompleteError] are retried. Other errors fail
// immediately. The policy wait runs after every unsuccessful attempt,
// including the last, so a fully exhausted budget records
// policy.Attempts waits.
func poll(ctx context.Context, sleep SleepFunc, policy PollPolicy, op func() error) error {
	var lastErr error
	for range policy.Attempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isPollMiss(err) {
			return err
		}
		if err := sleep(ctx, policy.Wait); err != nil {
			return err
		}
	}
	if lastErr == nil {
		return errors.New("poll budget exhausted")
	}

	return lastErr
}

// isPollMiss reports whether err should consume another poll attempt.
func isPollMiss(err error) bool {
	if errors.Is(err, ErrNoDraft) {
		return true
	}
	var incomplete incompleteError

	return errors.As(err, &incomplete)
}

// incompleteError is a pollable asset-convergence miss.
type incompleteError struct {
	// msg names what has not yet converged.
	msg string
}

// Error returns the miss description.
func (e incompleteError) Error() string {
	return e.msg
}
