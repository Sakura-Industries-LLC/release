# Rehearse and recover GitHub Releases

Use this guide to populate a draft GitHub Release without publishing it, then resume publication through the same tag and draft. Complete [Configure GitHub Releases](configure-github-releases.md) first. The [GitHub Release contract](../reference/github-release-contract.md) defines the checks that each run enforces.

## Prerequisites

Before starting a rehearsal, confirm that:

- the release configuration and organization credentials are present on the default branch;
- the Meigma Release App has selected-repository access;
- any protected `v*` tag rule permits the App to create release tags;
- the candidate version has no existing public release; and
- you can update the rehearsal tag if resumption requires moving it to a recovery commit.

Tag updates are a separate permission from App-created tag creation. If a ruleset prevents you from updating a rehearsal tag, do not weaken the production rule solely for this procedure. Perform the rehearsal in a disposable repository or use an organization-approved break-glass process.

Record the consumer repository:

```bash
gh auth status
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
export DEFAULT_BRANCH="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
```

## 1. Configure a draft-only run

In `.github/workflows/release.yml`, disable both publishers:

```yaml
publish-image: false
publish-release: false
```

The copyable example already uses both values. Merge the change into the default branch before Release Please creates the candidate tag. The tag must contain the rehearsal caller; changing an untagged branch after the tag exists does not change that run.

Confirm the value on the default branch:

```bash
git fetch origin "$DEFAULT_BRANCH"
git show "origin/$DEFAULT_BRANCH:.github/workflows/release.yml" |
  grep -E 'publish-(image|release): false'
```

The command must print both disabled inputs. Do not start the release if either is missing.

## 2. Create the candidate tag and draft

Release Please needs a releasable Conventional Commit after the version recorded in `.release-please-manifest.json`. When that condition is met, dispatch it:

```bash
gh workflow run release-please.yml \
  --repo "$REPOSITORY" \
  --ref "$DEFAULT_BRANCH"
gh run list \
  --repo "$REPOSITORY" \
  --workflow release-please.yml \
  --limit 5
```

Review and squash-merge the Release Please pull request. The subsequent Release Please run creates the `v*` tag and matching draft through the Release App. The tag triggers the Release workflow.

Set `TAG` to the exact tag created by Release Please. The unmodified new example creates `v0.1.0` first:

```bash
export TAG=v0.1.0
git fetch origin "refs/tags/$TAG:refs/tags/$TAG"
export TAG_SHA="$(git rev-list -n 1 "$TAG")"
gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$TAG_SHA" \
  --event push \
  --limit 100 \
  --json databaseId,headBranch,headSha,event,status,url
```

After the exact tag and commit appear, assert that the query selects one run and watch it:

```bash
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
gh run watch "$RELEASE_RUN_ID" \
  --repo "$REPOSITORY" \
  --compact \
  --exit-status
```

At the documented current revision, a successful draft-only run leaves the Release workflow green, the release unpublished with six platform archives, six archive SBOMs, `checksums.txt`, and `checksums.txt.sigstore.json`, and GHCR unchanged. The run also retains the verified multi-architecture layout, signed APK repository, apko lock, and image SBOMs in the `oci-image` workflow artifact. When rehearsing another revision, use the target contracts in [Upgrade GitHub Release workflows](upgrade-github-release-workflows.md).

## 3. Inspect the populated draft

Query the authoritative releases collection for the exact tag. This matches the publisher's draft-discovery path and keeps the inspection query aligned with the workflow:

```bash
test "$(gh api --paginate --slurp \
  "repos/$REPOSITORY/releases?per_page=100" \
  --jq "[.[][] | select(.tag_name == \"$TAG\")] | length")" -eq 1
export RELEASE_ID="$(gh api --paginate --slurp \
  "repos/$REPOSITORY/releases?per_page=100" \
  --jq "[.[][] | select(.tag_name == \"$TAG\")][0].id")"
gh api "repos/$REPOSITORY/releases/$RELEASE_ID" \
  --jq '{id, tag_name, draft, prerelease, assets: [.assets[].name]}'
```

For the documented current revision, the query must select exactly one release with the exact tag, `"draft": true`, `"prerelease": false`, and fourteen assets. Twelve asset names come from `checksums.txt`; the other two are the checksum manifest and its Cosign bundle. For an upgrade rehearsal, require the names and count defined by the target contract instead.

Use the paginated releases API above or the repository's Releases UI while the release remains a draft. This procedure does not use by-tag CLI commands for draft discovery.

Before resuming, inspect the Release workflow log and the draft asset list. Do not manually publish the draft. The final workflow must perform digest verification immediately before publication.

## 4. Resume through the same tag and draft

Change both publication controls in `.github/workflows/release.yml`:

```yaml
publish-image: true
publish-release: true
```

Submit and merge that change. Fetch the resulting default-branch commit and record it:

```bash
git fetch origin "$DEFAULT_BRANCH" --tags
export RECOVERY_SHA="$(git rev-parse "origin/$DEFAULT_BRANCH")"
printf 'Recovery commit: %s\n' "$RECOVERY_SHA"
```

The `push`-on-tag caller has no manual dispatch input. A rerun of the original Actions run would use the original tagged workflow with both publishers disabled. To exercise the updated caller, move the same rehearsal tag to the recovery commit and push that tag update:

```bash
git tag --force "$TAG" "$RECOVERY_SHA"
git push --force origin "refs/tags/$TAG"
```

Use this tag move only for the controlled unpublished rehearsal. Never move a tag for a published release.

After the run for the exact tag and `RECOVERY_SHA` appears, assert that the query selects one run and watch it:

```bash
gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$RECOVERY_SHA" \
  --event push \
  --limit 100 \
  --json databaseId,headBranch,headSha,event,status,url
test "$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$RECOVERY_SHA" \
  --event push \
  --limit 100 \
  --json databaseId \
  --jq 'length')" -eq 1
export RESUME_RUN_ID="$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$RECOVERY_SHA" \
  --event push \
  --limit 100 \
  --json databaseId \
  --jq '.[0].databaseId')"
gh run watch "$RESUME_RUN_ID" \
  --repo "$REPOSITORY" \
  --compact \
  --exit-status
```

The new run builds and signs a new authoritative artifact for `RECOVERY_SHA`. After validating that bundle, the publisher allows only names from its signed checksum manifest and replaces those expected names with `--clobber`. It does not accept an unexpected asset.

Confirm that publication reused the same release ID and changed its state:

```bash
export FINAL_RELEASE_ID="$(gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json databaseId \
  --jq .databaseId)"
test "$FINAL_RELEASE_ID" = "$RELEASE_ID"
gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json tagName,isDraft,isPrerelease,publishedAt,url
```

The ID comparison must succeed. The final result must report `"isDraft": false` and the original tag name. Run the checksum, Cosign identity, and GitHub attestation verification commands in [Configure GitHub Releases](configure-github-releases.md#7-verify-the-published-release) against the final assets.

## Diagnose a failed run

Resolve the commit currently named by the unpublished tag, then select the failed push run for that exact tag and commit:

```bash
git fetch origin "refs/tags/$TAG:refs/tags/$TAG" --force
export FAILED_SHA="$(git rev-list -n 1 "$TAG")"
gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$FAILED_SHA" \
  --event push \
  --status failure \
  --limit 100 \
  --json databaseId,headBranch,headSha,event,status,conclusion,url
test "$(gh run list \
  --repo "$REPOSITORY" \
  --workflow release.yml \
  --branch "$TAG" \
  --commit "$FAILED_SHA" \
  --event push \
  --status failure \
  --limit 100 \
  --json databaseId \
  --jq 'length')" -eq 1
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
gh run view "$FAILED_RUN_ID" \
  --repo "$REPOSITORY" \
  --log-failed
```

Keep the release as a draft while diagnosing any failure below.

If recovery changes source, workflow configuration, or tool pins, merge that correction, record its commit SHA, and trigger a new run by authorized movement of the unpublished tag to that commit. Then select the run by the exact tag and SHA as shown above. If the tag cannot be moved safely, abandon the incomplete candidate and cut a new one. When repository content is unchanged and upstream build jobs succeeded, rerun only failed jobs with `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY" --failed`; this preserves the authoritative artifacts from the original workflow run. Use a complete rerun only when an upstream artifact must be rebuilt, such as artifact expiry or an artifact-handoff failure.

### The matching draft is missing

The publisher polls the releases collection for the current tag and then reports `No GitHub Release found` if none appears.

1. Check the Release Please run that created the tag.
2. Query the releases collection with the command in [Inspect the populated draft](#3-inspect-the-populated-draft).
3. Confirm that the App installation and both organization credentials include the consumer repository.
4. Confirm that Release Please created both the exact tag and a draft with that tag.

Do not create an unrelated draft to make the publisher proceed. Release Please owns the release notes, tag, and initial draft. If Release Please created the tag without its draft and cannot reconcile it, remove the incomplete unpublished candidate through an authorized incident process, then cut a new candidate from Release Please.

### The tag moved or resolves to a different commit

The publisher requires the workflow's tag to resolve to `github.sha`. It fails when those values differ.

1. Fetch the remote tag and inspect its commit:

   ```bash
   git fetch origin "refs/tags/$TAG:refs/tags/$TAG" --force
   git rev-list -n 1 "$TAG"
   ```

2. Compare the result with the commit shown by the failed Actions run.
3. If the tag was intentionally advanced for resumption, do not rerun the stale draft-only run. Use the new run triggered by the tag update.
4. If the move was unintended, stop publication and follow the repository's release incident process. Do not silently move a published tag backward.

A draft can be recovered after an authorized tag move because the workflow revalidates the tag and release ID. A published tag is immutable operational history; release a corrected version instead.

### Artifact handoff fails

The publisher rejects an invalid artifact ID, an expired artifact, a digest mismatch, or an artifact produced by another workflow run.

If the repository does not need a correction, rerun the complete top-level workflow with `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY"` so it builds a new authoritative artifact; do not substitute an artifact from another run. The producer and publisher must exchange the artifact ID and digest within one run. If the handoff failure requires a source, workflow, or pin correction, merge the correction and move the unpublished tag to that commit to create a new tag-triggered run. The authoritative artifact is retained for seven days; an expired artifact also requires a complete rerun.

### Checksum or Cosign verification fails

For the documented current revision, the publisher requires a nonempty `checksums.txt`, the exact closed payload list, matching payload hashes, a regular Cosign bundle file, issuer `https://token.actions.githubusercontent.com`, and this certificate identity:

```text
https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@fb8c8098ff27968fb3070e928c00e925f38c698e
```

For an upgrade rehearsal, replace the current-revision identity with the target value described in [Upgrade GitHub Release workflows](upgrade-github-release-workflows.md).

Do not upload files manually or relax the identity. Correct the producer configuration or its pinned workflow reference, merge that correction, and move the unpublished tag to the correction commit so a new tag-triggered run builds and signs it. If authorized tag movement is unavailable, abandon the candidate and cut a new one. Expected draft assets can be replaced only after the new signed bundle passes validation.

### The draft has unexpected assets

The publisher stops before upload when the draft contains an asset name absent from the signed checksum manifest.

1. Inspect the draft in the Releases UI and through the releases API.
2. Determine who uploaded the asset and whether it belongs to the candidate.
3. If the asset is not part of the release contract, remove it manually from the draft with an authorized account.
4. If the asset is required, change the producer so the signed checksum manifest includes it. Merge the correction and move the unpublished tag to the correction commit, or abandon the candidate and cut a new one.

Do not use `--clobber` for an unexpected name. The workflow uses `--clobber` only for the expected closed name set after checksum and signature validation.

### Uploaded asset digest verification fails

The publisher waits until every expected asset is uploaded and GitHub reports its digest. It then requires the exact asset count, unique names, and a GitHub-reported SHA-256 digest matching the locally validated bundle.

If no repository content changes and the producer succeeded, rerun only failed jobs with `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY" --failed`. This reuses the validated artifact and may replace its expected names in the same draft. If the failure requires a source, workflow, or pin correction, merge the correction and move the unpublished tag to that commit; otherwise abandon the candidate and cut a new one. If the failure repeats, leave the release as a draft and inspect the release asset state and workflow logs; do not publish through the UI. Manual removal is required only when an unexpected or otherwise unreconcilable asset prevents the workflow from restoring the closed name set.

If the final API call changed the release to non-draft before a later check failed, the workflow cannot roll that state back. Confirm the state with `gh release view "$TAG" --repo "$REPOSITORY" --json isDraft,publishedAt,url`. A subsequent run will reject the public release because it is no longer a draft; preserve it and cut a corrected version unless the organization authorizes a release-incident removal.

## When manual cleanup is required

Manual cleanup is required when:

- an unexpected asset must be removed from a draft;
- an incomplete unpublished tag or release prevents Release Please from creating the correct candidate;
- repository rules require an authorized administrator to approve the controlled rehearsal tag move; or
- a draft contains a release association or asset state that the validated `--clobber` path cannot reconcile.

Manual cleanup is not required for an expected asset from the first draft-only run or a failed checksum/signature check. Rerun only failed jobs when upstream artifacts remain valid and repository content is unchanged. Use a complete rerun for an expired or invalid artifact. For a source, configuration, or pin correction, merge the correction and trigger a new tag run or abandon the candidate.

If a release is already public, do not delete it or move its tag as routine recovery. Preserve the published record and release a corrected version unless the organization declares a separate release incident and explicitly authorizes removal.
