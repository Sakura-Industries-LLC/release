# Rehearse and recover GitHub Releases

Use this guide to populate a draft GitHub Release without publishing it, then resume publication through the same tag and draft. Complete [Configure GitHub Releases](configure-github-releases.md) first. The [GitHub Release contract](../reference/github-release-contract.md) defines the checks that each run enforces.

`FULL_SHA` is the placeholder for the released commit and will be replaced when
this program's final pull request lands.

`release-cli publish github` owns draft discovery, tag and commit binding, expected-asset upload, asset convergence, and the optional undraft operation. It never creates a release, re-drafts a public release, or deletes an asset.

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

At the documented current revision, a successful draft-only run leaves the Release workflow green, the release unpublished with six platform archives, six archive SBOMs, `checksums.txt`, and `checksums.txt.sigstore.json`, and GHCR unchanged. With `publish-release: false`, the workflow runs `release-cli publish github --no-undraft`; the CLI converges and verifies the expected asset set, then confirms that the release remains a draft. The run also retains the verified multi-architecture layout, signed APK repository, apko lock, and image SBOMs in the `oci-image` workflow artifact. When rehearsing another revision, use the target contracts in [Upgrade GitHub Release workflows](upgrade-github-release-workflows.md).

## 3. Inspect the populated draft

Query the authoritative releases collection for the exact tag. This matches the CLI's draft-discovery path and keeps the inspection query aligned with publication:

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

For the documented current revision, the query must select exactly one release with the exact tag, `"draft": true`, `"prerelease": false`, and fourteen assets. Twelve asset names come from `checksums.txt`; the other two are the checksum manifest and its Cosign bundle. For an upgrade rehearsal, require the names and count defined by the target contract instead.

If this rehearsal query reports `"draft": false`, stop. A `publish-release: false` run requires the release to remain a draft, so `publish github --no-undraft` classifies an already-public release as indeterminate even when all assets match. Inspect how the release became public; do not rerun the rehearsal or ask the CLI to re-draft it.

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

The new run builds and signs a new authoritative artifact for `RECOVERY_SHA`. After validating that bundle, `release-cli publish github` accepts only names from the expected closed set and replaces those expected names with `--clobber`. It refuses an unexpected asset and never deletes one.

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

When `publish github` reports an indeterminate release state, it writes this remediation hint to stderr:

```text
inspect the release in GitHub and reconcile manually; do not rerun the publication blindly
```

Follow that hint if the log ends during or after the undraft operation or reports that a draft-only publication found a public release. Inspect the releases collection and asset details with the commands in [Inspect the populated draft](#3-inspect-the-populated-draft):

- If exactly one matching release is still a draft, the failure occurred before a confirmed undraft. After inspecting the state, a rerun is safe when the authoritative artifact remains valid because the CLI reads the tag, release, and assets again and reconciles from that fresh state.
- If exactly one matching release is public and the failed run used `publish-release: true`, compare its asset count, names, states, and digests with the expected bundle. A later publish-enabled CLI invocation reports success without mutation only for an exact match. Any other public state remains indeterminate and requires human handling.
- If exactly one matching release is public and the failed run used `publish-release: false`, the rehearsal did not preserve its draft-only outcome. The state remains indeterminate even when the assets match. Do not rerun or re-draft the release.
- If no release or more than one release carries the tag, stop and resolve that state through the release incident process.

If recovery changes source, workflow configuration, or tool pins, merge that correction, record its commit SHA, and trigger a new run by authorized movement of the unpublished tag to that commit. Then select the run by the exact tag and SHA as shown above. If the tag cannot be moved safely, abandon the incomplete candidate and cut a new one. When repository content is unchanged, the release remains a draft, and upstream build jobs succeeded, rerun only failed jobs with `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY" --failed`; this preserves the authoritative artifacts from the original workflow run. Use a complete rerun only when an upstream artifact must be rebuilt, such as artifact expiry or an artifact-handoff failure.

### Release artifact staging fails

The producer runs `release-cli stage --profile go --dist dist` after GoReleaser
and before either Actions artifact upload. It stops on an invalid checksum
claim or bundle, an invalid Linux binary selection, an escaped path, or a binary
that is not a regular executable file. The failed step writes the offending
artifact diagnostic to stderr.

Use the diagnostic and the [`release-cli` contract](../reference/release-cli-contract.md)
to inspect the generated `dist` files. Correct the source or GoReleaser
configuration instead of bypassing the check. If the correction changes
repository content, merge it and move the unpublished rehearsal tag to the new
commit as described above, or abandon the candidate and cut a new one.

### The matching draft is missing

`release-cli publish github` polls the releases collection for the current tag and reports that no draft release exists if none appears within its bounded discovery budget.

1. Check the Release Please run that created the tag.
2. Query the releases collection with the command in [Inspect the populated draft](#3-inspect-the-populated-draft).
3. Confirm that the App installation and both organization credentials include the consumer repository.
4. Confirm that Release Please created both the exact tag and a draft with that tag.

Do not create an unrelated draft to make the publisher proceed. Release Please owns the release notes, tag, and initial draft. If Release Please created the tag without its draft and cannot reconcile it, remove the incomplete unpublished candidate through an authorized incident process, then cut a new candidate from Release Please.

### The tag moved or resolves to a different commit

`release-cli publish github` requires the workflow's tag to resolve to `github.sha`. It fails when those values differ.

1. Fetch the remote tag and inspect its commit:

   ```bash
   git fetch origin "refs/tags/$TAG:refs/tags/$TAG" --force
   git rev-list -n 1 "$TAG"
   ```

2. Compare the result with the commit shown by the failed Actions run.
3. If the tag was intentionally advanced for resumption, do not rerun the stale draft-only run. Use the new run triggered by the tag update.
4. If the move was unintended, stop publication and follow the repository's release incident process. Do not silently move a published tag backward.

A draft can be recovered after an authorized tag move because the CLI revalidates the tag, commit, unique release, and assets from fresh state. A published tag is immutable operational history; release a corrected version instead.

### Artifact handoff fails

The publisher rejects an invalid artifact ID, an expired artifact, a digest mismatch, or an artifact produced by another workflow run.

If the repository does not need a correction, rerun the complete top-level workflow with `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY"` so it builds a new authoritative artifact; do not substitute an artifact from another run. The producer and publisher must exchange the artifact ID and digest within one run. If the handoff failure requires a source, workflow, or pin correction, merge the correction and move the unpublished tag to that commit to create a new tag-triggered run. The authoritative artifact is retained for seven days; an expired artifact also requires a complete rerun.

### Checksum or Cosign verification fails

For the documented current revision, the publisher requires a nonempty `checksums.txt`, the exact closed payload list, matching payload hashes, a regular Cosign bundle file, issuer `https://token.actions.githubusercontent.com`, and this certificate identity:

```text
https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA
```

For an upgrade rehearsal, replace the current-revision identity with the target value described in [Upgrade GitHub Release workflows](upgrade-github-release-workflows.md).

Do not upload files manually or relax the identity. Correct the producer configuration or its pinned workflow reference, merge that correction, and move the unpublished tag to the correction commit so a new tag-triggered run builds and signs it. If authorized tag movement is unavailable, abandon the candidate and cut a new one. Expected draft assets can be replaced only after the new signed bundle passes validation.

### The draft has unexpected assets

`release-cli publish github` stops before upload when the draft contains an asset name absent from the expected closed set.

1. Inspect the draft in the Releases UI and through the releases API.
2. Determine who uploaded the asset and whether it belongs to the candidate.
3. If the asset is not part of the release contract, remove it manually from the draft with an authorized account.
4. If the asset is required, change the producer so the signed checksum manifest includes it. Merge the correction and move the unpublished tag to the correction commit, or abandon the candidate and cut a new one.

Do not use `--clobber` for an unexpected name. The CLI uses `--clobber` only for the expected closed name set after checksum and signature validation.

### Uploaded asset digest verification fails

`release-cli publish github` waits until every expected asset is uploaded and GitHub reports its digest. It then requires the exact asset count, unique names, and a GitHub-reported SHA-256 digest matching the locally validated bundle.

If no repository content changes, the release is still a draft, and the producer succeeded, rerun only failed jobs with `gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY" --failed`. The CLI re-reads current state and may replace expected names in the same draft. If the failure requires a source, workflow, or pin correction, merge the correction and move the unpublished tag to that commit; otherwise abandon the candidate and cut a new one. If the failure repeats, leave the release as a draft and inspect the release asset state and workflow logs; do not publish through the UI. Manual removal is required only when an unexpected or otherwise unreconcilable asset prevents the CLI from restoring the closed name set.

If the undraft request fails or may have succeeded before a later failure, the CLI cannot prove the final state and reports it as indeterminate. It writes the remediation hint in [Diagnose a failed run](#diagnose-a-failed-run). Inspect the release and assets; do not rerun blindly. For a publish-enabled run, a public release with the exact expected asset set can be reported as success by a later invocation without mutation. If the public asset set differs, the state remains indeterminate. For a draft-only run, every public state is indeterminate, including an exact asset match. The CLI never re-drafts the release.

## When manual cleanup is required

Manual cleanup is required when:

- an unexpected asset must be removed from a draft;
- an incomplete unpublished tag or release prevents Release Please from creating the correct candidate;
- repository rules require an authorized administrator to approve the controlled rehearsal tag move; or
- a draft contains a release association or asset state that the validated `--clobber` path cannot reconcile.

Manual cleanup is not required for an expected asset from the first draft-only run or a failed checksum/signature check. If the release remains a draft, rerun only failed jobs when upstream artifacts remain valid and repository content is unchanged; the CLI reconciles from fresh state. Use a complete rerun for an expired or invalid artifact. For a source, configuration, or pin correction, merge the correction and trigger a new tag run or abandon the candidate.

If a release is already public, do not delete it or move its tag as routine recovery. Preserve the published record and release a corrected version unless the organization declares a separate release incident and explicitly authorizes removal.
