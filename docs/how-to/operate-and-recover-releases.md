# Operate and recover releases

Use this guide to rehearse, publish, retry, recover, and upgrade an adopted
release unit. Complete [Adopt the release workflows](adopt-the-release-workflows.md)
first.

A public release or exact OCI version is not rolled back in place. Recovery
before publication can reuse an unpublished tag and draft. Recovery after
publication uses a corrective stable version.

## Rehearse a stable candidate

Set every remote publisher to `false` in `.github/workflows/release.yml`:

```yaml
publish-image: false
publish-release: false
publish-homebrew: false
publish-scoop: false
publish-package-repository: false
```

Merge that configuration before Release Please creates the tag. Confirm the two
core controls on the default branch:

```bash
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
export DEFAULT_BRANCH="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
git fetch origin "$DEFAULT_BRANCH"
git show "origin/$DEFAULT_BRANCH:.github/workflows/release.yml" |
  grep -E 'publish-(image|release): false'
```

Dispatch Release Please when the repository contains a releasable Conventional
Commit after the manifest version:

```bash
gh workflow run release-please.yml --repo "$REPOSITORY" --ref "$DEFAULT_BRANCH"
gh run list --repo "$REPOSITORY" --workflow release-please.yml --limit 5
```

Review and merge the Release Please pull request. Its subsequent run creates the
stable `vMAJOR.MINOR.PATCH` tag and one matching draft through the adopter-owned
App. Do not create the tag or draft manually.

Record the tag, its commit, and the one tag-triggered run:

```bash
export TAG=v1.2.3
git fetch origin "refs/tags/$TAG:refs/tags/$TAG"
export TAG_SHA="$(git rev-list -n 1 "$TAG")"
test "$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$TAG_SHA" \
  --event push \
  --limit 100 \
  --json databaseId \
  --jq 'length')" -eq 1
export RELEASE_RUN_ID="$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$TAG_SHA" \
  --event push \
  --limit 100 \
  --json databaseId \
  --jq '.[0].databaseId')"
gh run watch "$RELEASE_RUN_ID" --repo "$REPOSITORY" --compact --exit-status
```

The disabled run still builds and validates the release bundle and OCI image.
The OCI publisher runs `publish oci prepare --dry-run` without a registry write.
The GitHub publisher verifies and populates the draft with `--no-undraft`.
Homebrew, Scoop, and package-repository jobs skip before token creation or a
destination request.

## Inspect the draft and artifacts

Query the releases collection because the candidate is still a draft:

```bash
test "$(gh api --paginate --slurp \
  "repos/$REPOSITORY/releases?per_page=100" \
  --jq "[.[][] | select(.tag_name == \"$TAG\")] | length")" -eq 1
export RELEASE_ID="$(gh api --paginate --slurp \
  "repos/$REPOSITORY/releases?per_page=100" \
  --jq "[.[][] | select(.tag_name == \"$TAG\")][0].id")"
gh api "repos/$REPOSITORY/releases/$RELEASE_ID" \
  --jq '{id, tag_name, draft, prerelease, assets: [.assets[] | {name, state, digest}]}'
```

Require one non-prerelease draft. For the maintained Go profile, the public
closed set has 26 files: six archives, six native packages, twelve SBOMs,
`checksums.txt`, and `checksums.txt.sigstore.json`.

Download the artifacts from this exact run:

```bash
export INSPECT_DIR="rehearsal-${TAG#v}"
test ! -e "$INSPECT_DIR"
mkdir -p "$INSPECT_DIR/release-assets" "$INSPECT_DIR/oci-image"
gh run download "$RELEASE_RUN_ID" \
  --repo "$REPOSITORY" \
  --name release-assets \
  --dir "$INSPECT_DIR/release-assets"
gh run download "$RELEASE_RUN_ID" \
  --repo "$REPOSITORY" \
  --name oci-image \
  --dir "$INSPECT_DIR/oci-image"
```

The maintained all-destination example's `release-assets` artifact also
contains one Homebrew cask and one Scoop manifest. These two controls are bound
by the Actions artifact digest but are not checksummed, attested, or uploaded to
the GitHub Release.

Verify the release payload and OCI layout:

```bash
cd "$INSPECT_DIR/release-assets"
sha256sum --check checksums.txt
cd - >/dev/null
test -s "$INSPECT_DIR/oci-image/image-digest.txt"
test -s "$INSPECT_DIR/oci-image/layout/index.json"
jq -r '.manifests[] | "\(.platform.os)/\(.platform.architecture)"' \
  "$INSPECT_DIR/oci-image/layout/index.json" | sort
```

On macOS, use `shasum -a 256 --check checksums.txt`. Review the job logs,
generated Homebrew and Scoop controls, SBOMs, signatures, and expected
destination names. Do not publish the draft through the GitHub UI.

## Resume the same unpublished candidate

Change `publish-image` and `publish-release` to `true` in one reviewed commit.
Enable Homebrew, Scoop, or native package publication only if each destination's
external setup is complete.

A rerun of the first Actions run still uses the tagged caller with publishers
disabled. To use the enabling commit, move the same unpublished rehearsal tag:

```bash
git fetch origin "$DEFAULT_BRANCH" --tags
export RECOVERY_SHA="$(git rev-parse "origin/$DEFAULT_BRANCH")"
git tag --force "$TAG" "$RECOVERY_SHA"
git push --force origin "refs/tags/$TAG"
```

Use this operation only for a controlled unpublished rehearsal. Select and
watch the new run by the exact tag and recovery commit:

```bash
export RESUME_RUN_COUNT=0
until test "$RESUME_RUN_COUNT" -gt 0; do
  export RESUME_RUN_COUNT="$(gh run list \
    --repo "$REPOSITORY" \
    --workflow release.yml \
    --branch "$TAG" \
    --commit "$RECOVERY_SHA" \
    --event push \
    --limit 100 \
    --json databaseId \
    --jq 'length')"
  test "$RESUME_RUN_COUNT" -gt 0 || sleep 2
done
test "$RESUME_RUN_COUNT" -eq 1
export RESUME_RUN_ID="$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$RECOVERY_SHA" \
  --event push \
  --limit 100 \
  --json databaseId \
  --jq '.[0].databaseId')"
gh run watch "$RESUME_RUN_ID" --repo "$REPOSITORY" --compact --exit-status
```

Confirm that the same release ID became public:

```bash
test "$(gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json databaseId \
  --jq .databaseId)" = "$RELEASE_ID"
gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json tagName,isDraft,isPrerelease,publishedAt,url
```

Verify the public release and digest-pinned image with the commands in
[Release your first Go application](../tutorials/release-your-first-go-application.md#verify-the-release-and-image).

## Choose the correct retry

Use the smallest retry that preserves the authoritative artifact and reviewed
content:

| Condition | Action |
| --- | --- |
| Repository content is unchanged, the candidate remains a draft, and upstream artifacts are valid | `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY" --failed` |
| A transient failure requires the complete graph but no content change | `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY"` |
| An Actions artifact expired or its handoff is invalid | Rerun the complete graph to build a new artifact. |
| Source, workflow configuration, signer pin, or tool lock changes | Merge the correction and move the unpublished tag to that commit, or abandon the candidate. |
| The release may already be public | Inspect remote state before any rerun. |
| The release is public and incorrect | Preserve it and publish a corrective version. |

Select a failed run by tag and commit, not by recency alone:

```bash
git fetch origin "refs/tags/$TAG:refs/tags/$TAG" --force
export FAILED_SHA="$(git rev-list -n 1 "$TAG")"
export FAILED_RUN_ID="$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$FAILED_SHA" \
  --event push \
  --status failure \
  --limit 100 \
  --json databaseId \
  --jq '.[0].databaseId')"
test -n "$FAILED_RUN_ID"
gh run view "$FAILED_RUN_ID" --repo "$REPOSITORY" --log-failed
```

## Diagnose publisher failures

### Release Please or draft discovery

If no matching draft exists:

1. inspect the Release Please run that created the tag;
2. confirm the App installation, variable, secret, and protected-tag bypass;
3. query the paginated releases collection for the exact tag; and
4. confirm that Release Please created one tag and one draft.

Do not create an unrelated draft to satisfy the publisher. If Release Please
cannot reconcile an incomplete unpublished candidate, remove it only through an
authorized incident process and cut a new candidate.

### Build, signing, or staging

`release-cli stage --profile go` invokes GoReleaser before validating the
bundle. Correct the source or `.goreleaser.yaml` when staging reports an invalid
checksum, missing canonical Linux binary, dynamic executable, escaped path, or
irregular file.

When macOS signing is enabled, confirm all five Apple credentials and inspect
Quill's rejection or timeout. When native package signing is enabled, confirm
all four credentials, owner-only key files, nFPM ID `release`, and the fixed key
expressions. Do not disable signing to make a producer eligible for a native
repository whose policy requires signed RPM and APK packages.

### Artifact handoff

A publisher rejects an artifact from another run, an expired artifact, a
non-positive ID, or a GitHub digest mismatch. Do not substitute an artifact
from another run. Rerun the complete top-level workflow when a new authoritative
artifact is required.

### Checksum or signer verification

Require the one release-unit identity:

```text
https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@<full-release-unit-sha>
```

Do not relax the issuer, change the identity to a branch or tag, or upload files
manually. Correct every reusable workflow reference and signer field together,
then build a new artifact from the correction commit.

### Unexpected GitHub Release assets

The GitHub publisher refuses an asset name outside the signed closed set and
never deletes it. Inspect the draft and determine its origin.

- If the asset does not belong to the release contract, an authorized operator
  must remove it from the draft before a retry.
- If it belongs in the release, change the producer so `checksums.txt` includes
  it, merge the correction, and run from the correction commit.

`--clobber` is used only for expected names after closed-set verification. It is
not an override for unexpected files.

### Undraft or final-state uncertainty

An undraft request has no rollback. If the request fails, a final read fails, or
the observed state is unexpected, `release-cli` reports an indeterminate state.
Inspect the release and all asset names, states, and digests before a rerun.

A later publish-enabled invocation can report success without mutation only
when an already-public release exactly matches the expected closed set. A
public result from a draft-only invocation remains indeterminate even when the
assets match. The CLI never re-drafts a release.

### OCI prepare, attestation, or finalize

The phase determines the possible registry state:

- failed preparation can leave untagged digest-addressed content;
- failed signing or attestation can leave partial trust metadata but no new
  candidate tag;
- failed finalization can apply only a prefix of the planned tags; and
- the dependent GitHub Release remains a draft while OCI publication fails.

Rerun failed jobs when the same authoritative image artifact remains valid.
Prepare reads registry state again, signing and attestations converge, and
finalize accepts candidate tags already applied before applying the remaining
eligible tags. Duplicate valid signatures or attestations can result.

Never hand-replay a saved prepare envelope. It records an earlier registry
observation and is not a durable receipt. If finalize reports drift, inspect the
named tags and current digests before another attempt.

### Homebrew or Scoop

The caller schedules these publishers after the GitHub Release job. Enable them
only when that job also has `publish-release: true`; Homebrew and Scoop do not
independently check whether the release is public. They refuse a conflicting
destination branch, multiple matching pull requests, a same-or-newer
destination version with different content, or changes outside the one
generated file.

Inspect the deterministic `release/<name>/v<version>` branch and pull request.
Correct the producer control or destination conflict, then rerun the failed job.
Do not force-update the branch, bypass validation, enable auto-merge, or merge
an unreviewed control.

### Native package repository

The central receiver can fail on release closed-set verification, explicit
checksum or attestation signer policy, GitHub digests, package metadata, native
RPM/APK signatures, existing immutable R2 objects, aggregate signing, or local
and public client installation.

Fix the failed prerequisite and replay the same `{repository, tag}` request.
Matching objects are skipped and replaceable metadata is regenerated. Do not
give a producer R2 credentials, bypass signature verification, or delete an
immutable object to make a replay succeed.

## Upgrade the release unit atomically

Before editing a producer, record the current and target full SHAs and review a
local diff between those exact commits. Include reusable workflows, the setup
action, example, two references, and any release-manifest stamp that changed.

Apply the target contract in one pull request:

1. replace every reusable `uses:` revision;
2. replace every `checksum-signing-workflow-ref` revision;
3. update explicit package policy `checksum_identity` values for the producer;
4. apply changed inputs, outputs, secrets, caller permissions, source contract,
   asset contract, and tool declarations;
5. regenerate `mise.lock` only when tool declarations changed; and
6. complete external App, ruleset, credential, key, destination, or environment
   prerequisites before triggering a candidate.

Keep all publishers disabled during the upgrade rehearsal. Confirm that the
caller contains one target workflow ref and no old revision:

```bash
! grep -F -q "$CURRENT_RELEASE_REVISION" .github/workflows/release.yml
refs="$(grep -Eo '@[0-9a-f]{40}' .github/workflows/release.yml | sort -u)"
test "$refs" = "@$NEW_RELEASE_REVISION"
grep -F 'publish-image: false' .github/workflows/release.yml
grep -F 'publish-release: false' .github/workflows/release.yml
```

Rehearse the target revision. The target [release system reference](../reference/release-system.md)
overrides old asset counts, identities, and interfaces. Enable publishers only
after the target draft, checksum identity, GitHub signer digest, and OCI
artifact pass review.

## Roll back before publication

If no candidate tag exists, reverse the complete repository change in one pull
request and restore every external prerequisite in an order that leaves the old
workflow operable.

If the target revision populated an unpublished draft:

1. restore all workflow refs, signer identities, permissions, interfaces,
   source configuration, and tool pins together;
2. restore external App, ruleset, key, credential, and environment state;
3. keep publishers disabled;
4. move the unpublished tag to the rollback commit; and
5. verify the restored draft contract before publication.

A previous contract can classify names introduced by the target revision as
unexpected. After confirming their origin, an authorized operator may remove
those names from the draft. Do not delete and recreate the draft.

## Correct after publication

Never move the tag, replace a public release asset, overwrite an exact OCI tag,
or rewrite immutable native package objects as routine recovery. Preserve the
published record, correct the complete release unit for future runs, rehearse a
new stable version, and publish that corrective release. Destructive removal
belongs only to a separately authorized release-incident process.
