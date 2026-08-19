package puboci

import (
	"context"
	"errors"
	"fmt"

	"github.com/meigma/release/internal/rel"
)

const (
	// FinalizeSchema is the versioned OCI finalize-result identifier.
	FinalizeSchema = "release.dev/oci-finalize/v1"
)

// Sentinel errors returned by [Finalize].
var (
	// ErrNotAuthoritative reports that the prepare result is a dry-run document.
	ErrNotAuthoritative = errors.New("prepare result is not authoritative")
	// ErrStateDrift reports that registry state changed since preparation.
	ErrStateDrift = errors.New("registry state drifted since preparation")
)

// FinalizeResult is the versioned document produced by publish oci finalize.
type FinalizeResult struct {
	// Schema identifies the finalize-result version and is always [FinalizeSchema].
	Schema string `json:"schema"`
	// Image is the untagged repository name.
	Image string `json:"image"`
	// Version is the candidate MAJOR.MINOR.PATCH version.
	Version string `json:"version"`
	// IndexDigest is the image index digest.
	IndexDigest string `json:"index_digest"`
	// Applied are the tags written by this run, in plan order.
	Applied []string `json:"applied"`
	// Accepted are the tags already at the candidate digest, in plan order.
	Accepted []string `json:"accepted"`
	// Retained are the channels left on a newer release, in plan order.
	Retained []string `json:"retained"`
}

// FinalizeInput is a validated prepare document and an optional retry sleeper.
type FinalizeInput struct {
	// Prepared is the authoritative document produced by [Prepare].
	Prepared OCIPrepareResult
	// Sleep waits between retryable commit and verification attempts. Nil
	// selects a context-aware timer.
	Sleep SleepFunc
}

// Finalize collects fresh registry state, refuses drift, and commits planned tags.
//
// It rejects a nil context, state reader, or committer and validates
// [FinalizeInput.Prepared] before any registry call. A document with
// Authoritative false fails with [ErrNotAuthoritative]. Fresh state is
// collected through [CollectState]; a serialized plan is never replayed.
// Each expected tag is compared against [OCIPrepareResult.Observed]. An
// unchanged observation is accepted. A tag whose prepared observation
// implied create (absent, or present at an older in-line version) and that
// is now present at the candidate index digest is treated as this
// publication's own partial progress. A prepared retain or accept that
// later sits on the candidate digest is [ErrStateDrift]. Any other change,
// including a disappeared tag or a tag present in only one set, fails with
// [ErrStateDrift] and names the tag, the prepared observation, and the
// fresh one. Tags are then replanned from the
// fresh state. [TagCommitter.Commit] is called once with [rel.TagPlan.Apply]
// and skipped entirely when nothing remains to write. Commit and the
// postcondition reads retry [ErrRetryable] at most four times with 1s, then
// 2s, then 4s of backoff. The exact version tag and every applied tag must
// resolve to the index digest through [StateReader], not the committer's
// word. The result classifies every decision as Applied, Accepted, or
// Retained in plan order. A nil [FinalizeInput.Sleep] uses a context-aware
// timer.
func Finalize(
	ctx context.Context,
	input FinalizeInput,
	state StateReader,
	committer TagCommitter,
) (FinalizeResult, error) {
	if err := validateFinalize(ctx, input, state, committer); err != nil {
		return FinalizeResult{}, err
	}

	image, err := ParseImage(input.Prepared.Image)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("prepare result image: %w", err)
	}
	version, err := rel.ParseVersion(input.Prepared.Version)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("prepare result version: %w", err)
	}
	digest, err := rel.ParseDigest(input.Prepared.IndexDigest)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("prepare result index digest: %w", err)
	}

	sleep := input.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	current, err := CollectState(ctx, state, image, version, digest)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("collect state: %w", err)
	}
	if driftErr := checkFinalizeDrift(input.Prepared.Observed, current, version, digest); driftErr != nil {
		return FinalizeResult{}, driftErr
	}

	plan, err := rel.PlanTags(version, digest, current)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("plan tags: %w", err)
	}

	applied := plan.Apply()
	if len(applied) > 0 {
		if err := withRetry(ctx, sleep, "commit tags", digest, func() error {
			return committer.Commit(ctx, image, digest, applied)
		}); err != nil {
			return FinalizeResult{}, err
		}
	}

	if err := verifyFinalize(ctx, state, image, digest, version.Tag(), applied, sleep); err != nil {
		return FinalizeResult{}, err
	}

	return classifyFinalize(input.Prepared, plan), nil
}

// validateFinalize rejects a nil context, a nil state reader, a nil
// committer, a malformed prepare document, and a non-authoritative result.
func validateFinalize(
	ctx context.Context,
	input FinalizeInput,
	state StateReader,
	committer TagCommitter,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if state == nil {
		return errors.New("state reader is nil")
	}
	if committer == nil {
		return errors.New("tag committer is nil")
	}
	if err := input.Prepared.Validate(); err != nil {
		return err
	}
	if !input.Prepared.Authoritative {
		return ErrNotAuthoritative
	}

	return nil
}

// checkFinalizeDrift compares fresh state against the prepared observations.
//
// Comparison walks the exact tag and each channel from [rel.ChannelsFor] so
// the check is ordered and total. A tag present in one set and missing from
// the other is drift. An unchanged observation is accepted. A tag now at
// digest is accepted only when the prepared observation implied create.
func checkFinalizeDrift(
	observed []TagObservation,
	current rel.ChannelState,
	version rel.Version,
	digest rel.Digest,
) error {
	prepared := make(map[string]TagObservation, len(observed))
	for _, observation := range observed {
		prepared[observation.Tag] = observation
	}

	fresh := map[string]rel.TagState{
		version.Tag().String(): current.Exact,
	}
	for _, channel := range rel.ChannelsFor(version) {
		fresh[channel.Tag.String()] = current.Channels[channel]
	}

	for _, tag := range expectedFinalizeTags(version) {
		preparedObs, havePrepared := prepared[tag]
		freshState, haveFresh := fresh[tag]
		if !havePrepared || !haveFresh {
			return driftError(tag, preparedObs, observeTag(tag, "", freshState))
		}
		if sameObservation(preparedObs, freshState) {
			continue
		}
		if preparedWouldCreate(preparedObs, version, digest) &&
			freshState.Present && freshState.Digest == digest {
			continue
		}

		return driftError(tag, preparedObs, observeTag(tag, rel.Scope(preparedObs.Scope), freshState))
	}

	for _, observation := range observed {
		if _, ok := fresh[observation.Tag]; !ok {
			return driftError(observation.Tag, observation, TagObservation{})
		}
	}

	return nil
}

// expectedFinalizeTags is the exact version tag followed by each channel.
func expectedFinalizeTags(version rel.Version) []string {
	channels := rel.ChannelsFor(version)
	tags := make([]string, 0, exactObservationCount+len(channels))
	tags = append(tags, version.Tag().String())
	for _, channel := range channels {
		tags = append(tags, channel.Tag.String())
	}

	return tags
}

// sameObservation reports whether fresh matches the prepared observation.
func sameObservation(prepared TagObservation, fresh rel.TagState) bool {
	if prepared.Present != fresh.Present {
		return false
	}
	if prepared.Present && prepared.Digest != fresh.Digest.String() {
		return false
	}
	if prepared.Version == "" {
		return !fresh.HasVersion
	}

	return fresh.HasVersion && prepared.Version == fresh.Version.String()
}

// preparedWouldCreate reports whether the prepared observation implied create.
//
// Absent tags and tags present at an older in-line version would have been
// written. A prepared accept, retain, corrupt, or out-of-line observation
// would not, so a later move onto digest is drift rather than convergence.
func preparedWouldCreate(observation TagObservation, version rel.Version, digest rel.Digest) bool {
	if !observation.Present {
		return true
	}
	if observation.Digest == digest.String() {
		return false
	}
	if observation.Version == "" {
		return false
	}
	annotated, err := rel.ParseVersion(observation.Version)
	if err != nil {
		return false
	}
	if !channelInLine(rel.Scope(observation.Scope), version, annotated) {
		return false
	}

	return version.Compare(annotated) > 0
}

// channelInLine reports whether annotated belongs on scope's release line.
func channelInLine(scope rel.Scope, candidate, annotated rel.Version) bool {
	switch scope {
	case rel.ScopeMinor:
		return annotated.Major == candidate.Major && annotated.Minor == candidate.Minor
	case rel.ScopeMajor:
		return annotated.Major == candidate.Major
	case rel.ScopeLatest, rel.ScopeExact:
		return true
	default:
		return false
	}
}

// driftError names the tag, the prepared observation, and the fresh one.
func driftError(tag string, prepared, fresh TagObservation) error {
	return fmt.Errorf(
		"tag %s drifted: prepared %s, now %s: %w",
		tag,
		formatObservation(prepared),
		formatObservation(fresh),
		ErrStateDrift,
	)
}

// formatObservation renders one observation for a drift error.
func formatObservation(observation TagObservation) string {
	if !observation.Present {
		return "absent"
	}
	if observation.Version == "" {
		return "present at " + observation.Digest
	}

	return "present at " + observation.Digest + " version " + observation.Version
}

// verifyFinalize requires the exact tag and every applied tag to resolve to digest.
func verifyFinalize(
	ctx context.Context,
	state StateReader,
	image Image,
	digest rel.Digest,
	exact rel.Tag,
	applied []rel.Tag,
	sleep SleepFunc,
) error {
	if err := verifyTagDigest(ctx, state, image, exact, digest, sleep); err != nil {
		return err
	}
	for _, tag := range applied {
		if tag == exact {
			continue
		}
		if err := verifyTagDigest(ctx, state, image, tag, digest, sleep); err != nil {
			return err
		}
	}

	return nil
}

// verifyTagDigest resolves tag and requires it to equal digest.
func verifyTagDigest(
	ctx context.Context,
	state StateReader,
	image Image,
	tag rel.Tag,
	digest rel.Digest,
	sleep SleepFunc,
) error {
	ref := image.Reference(tag)

	return withRetry(ctx, sleep, "verify tag", digest, func() error {
		resolved, err := state.Resolve(ctx, ref)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", ref, err)
		}
		if resolved != digest {
			return fmt.Errorf("tag %s resolves to %s; expected %s", tag, resolved, digest)
		}

		return nil
	})
}

// classifyFinalize partitions plan decisions into Applied, Accepted, and Retained.
func classifyFinalize(prepared OCIPrepareResult, plan rel.TagPlan) FinalizeResult {
	result := FinalizeResult{
		Schema:      FinalizeSchema,
		Image:       prepared.Image,
		Version:     prepared.Version,
		IndexDigest: prepared.IndexDigest,
		Applied:     []string{},
		Accepted:    []string{},
		Retained:    []string{},
	}
	for _, decision := range plan.Decisions {
		tag := decision.Tag.String()
		switch decision.Action {
		case rel.ActionCreate:
			result.Applied = append(result.Applied, tag)
		case rel.ActionAccept:
			result.Accepted = append(result.Accepted, tag)
		case rel.ActionRetain:
			result.Retained = append(result.Retained, tag)
		}
	}

	return result
}
