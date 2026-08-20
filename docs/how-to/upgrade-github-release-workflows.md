# Upgrade GitHub Release workflows

Use this guide to move a consumer repository from its current workflow revision to a reviewed immutable revision. Do not use a branch or tag as a reusable workflow reference. The [GitHub Release contract](../reference/github-release-contract.md), [OCI image contract](../reference/oci-image-contract.md), and [`release-cli` contract](../reference/release-cli-contract.md) define the current interfaces and publication boundaries.

## Prerequisites

Before changing the consumer repository, confirm that:

- the target `meigma/release` commit has completed review;
- the target commit is available in the public `meigma/release` repository;
- the consumer currently passes its required repository checks;
- `mise`, Git, GitHub CLI, and the repository-approved `actionlint` installation are available locally; and
- you can perform a draft-only release and, if necessary, update its unpublished rehearsal tag.

Record the consumer, the current baseline, and a local checkout of `meigma/release`:

```bash
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
read -r -p 'Current full meigma/release commit SHA: ' CURRENT_RELEASE_REVISION
read -r -p 'Reviewed full meigma/release commit SHA: ' NEW_RELEASE_REVISION
export NEW_RELEASE_REVISION
read -r -p 'Path to the meigma/release checkout: ' RELEASE_CHECKOUT
export RELEASE_CHECKOUT
[[ "$CURRENT_RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]
[[ "$NEW_RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]
test "$NEW_RELEASE_REVISION" != "$CURRENT_RELEASE_REVISION"
test "$(gh api "repos/meigma/release/commits/$NEW_RELEASE_REVISION" --jq .sha)" = \
  "$NEW_RELEASE_REVISION"
test "$(git -C "$RELEASE_CHECKOUT" rev-parse --is-inside-work-tree)" = true
git -C "$RELEASE_CHECKOUT" fetch \
  origin \
  "$CURRENT_RELEASE_REVISION" \
  "$NEW_RELEASE_REVISION"
test "$(git -C "$RELEASE_CHECKOUT" rev-parse "$NEW_RELEASE_REVISION^{commit}")" = \
  "$NEW_RELEASE_REVISION"
```

The commands must succeed. They prevent a shortened, mistyped, unavailable, or locally unresolved revision from entering the caller.

## 1. Assess the contract change

API compare summaries and patches can omit or truncate relevant content. Review a local Git diff between the exact commit objects instead:

```bash
git -C "$RELEASE_CHECKOUT" diff \
  --no-ext-diff \
  --find-renames \
  "$CURRENT_RELEASE_REVISION^{commit}" \
  "$NEW_RELEASE_REVISION^{commit}" \
  -- \
  .github/workflows/go-pre-publish.yml \
  .github/workflows/go-oci-build.yml \
  .github/workflows/publish-github-release.yml \
  .github/workflows/publish-oci-image.yml \
  .github/workflows/release.yml \
  .github/actions/setup-release-cli/action.yml \
  .github/workflows/release-please.yml \
  docs/reference/github-release-contract.md \
  docs/reference/oci-image-contract.md \
  docs/reference/release-cli-contract.md \
  examples/go-release
```

Read both complete target contracts after reviewing the diff:

```bash
git -C "$RELEASE_CHECKOUT" show \
  "$NEW_RELEASE_REVISION:docs/reference/github-release-contract.md"
git -C "$RELEASE_CHECKOUT" show \
  "$NEW_RELEASE_REVISION:docs/reference/oci-image-contract.md"
```

Before adoption, identify changes to:

- reusable workflow inputs, outputs, secrets, and caller permissions;
- the `release-cli` commands, flags, exit codes, and result fields used by the workflows;
- the setup action's acquisition behavior and version and protocol checks;
- checksum signer and attestation identities;
- artifact handoff, payload names, SBOMs, checksums, and publication states;
- consumer source and GoReleaser configuration requirements;
- GitHub App credentials, tag rules, or other external prerequisites;
- runner and mise requirements; and
- required Go, GoReleaser, Syft, Cosign, GitHub CLI, Melange, apko, or ORAS versions.

Stop if the target revision removes a required consumer capability or if any migration or rollback step is unresolved. Do not infer compatibility from an unchanged workflow filename.

## 2. Apply the target contract atomically

At the target revision, moving the GoReleaser invocation into
`release-cli stage --profile go` requires no consumer caller interface change
beyond updating the pinned revision. The command invokes exactly
`goreleaser release --clean --skip=publish`. Keep `release.disable: true` in
`.goreleaser.yaml`; it is a second publication control, and Release Please
continues to own release notes and the initial draft. Existing reusable workflow
inputs and outputs stay unchanged.

In `.github/workflows/release.yml`, replace the current revision with `NEW_RELEASE_REVISION` in all five locations:

1. `uses: meigma/release/.github/workflows/go-pre-publish.yml@...`
2. `uses: meigma/release/.github/workflows/go-oci-build.yml@...`
3. `uses: meigma/release/.github/workflows/publish-oci-image.yml@...`
4. `uses: meigma/release/.github/workflows/publish-github-release.yml@...`
5. `checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@...`

Keep both `publish-image: false` and `publish-release: false` for the upgrade rehearsal. All four reusable workflow references and the checksum signing identity must change in the same pull request and commit. A mixed revision fails a signing boundary or runs contracts that were not reviewed together.

The one full commit SHA selects the workflows, their composite setup action, and
the `release-cli` version used by those workflows. Do not add a separate CLI
version setting or CLI path. A consumer repository automatically installs the
verified CLI release stamped into that revision.

Apply every other target-contract change in that same upgrade:

- update caller permissions, inputs, output consumption, and secrets;
- update source, GoReleaser, asset, and other repository configuration requirements;
- update locked tools as described below; and
- complete required App installation, tag-rule, credential, or organization-setting changes before the upgraded workflow runs.

External prerequisites cannot be committed atomically with repository files. Assign an owner and completion condition for each one, complete it in the documented order, and do not merge or trigger a release while the repository and external states describe different contracts.

Check the edited caller:

```bash
test "$(grep -F -c "$NEW_RELEASE_REVISION" .github/workflows/release.yml)" -eq 5
! grep -F -q "$CURRENT_RELEASE_REVISION" .github/workflows/release.yml
grep -F 'publish-image: false' .github/workflows/release.yml
grep -F 'publish-release: false' .github/workflows/release.yml
```

All four commands must succeed: the caller must contain five target references, no baseline reference, and both disabled publication controls.

Write a migration and rollback checklist in the upgrade pull request. It must record:

- the current and target full commit SHAs;
- every caller interface, permission, source, asset, configuration, and tool change;
- every external prerequisite, its owner, and its observable completion result;
- the expected draft asset and identity checks; and
- the repository and external changes that reverse the entire upgrade before publication.

Do not merge with an unchecked migration item or an unexecutable rollback item.

## 3. Update locked tools only when required

Compare the target contract's repository and toolchain requirements with `mise.toml` and `mise.lock` in the consumer.

If the target contract keeps the current compatible tool versions, leave both files unchanged. Do not regenerate the lock merely because the workflow revision changed.

If the target contract requires different tools or versions:

1. Update only the required declarations in `mise.toml`.
2. Regenerate the supported-platform lock entries:

   ```bash
   mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64
   ```

3. Include `mise.toml` and `mise.lock` in the same upgrade pull request as the caller.

The target workflow revision and the tool lock must reach the default branch together.

## 4. Validate the upgrade pull request

Run the locked tool installation and GoReleaser configuration check:

```bash
mise install --locked
mise exec -- goreleaser check
actionlint .github/workflows/release.yml .github/workflows/release-please.yml
```

All three commands must exit successfully. Use the repository's pinned or otherwise approved `actionlint` installation; do not add an unreviewed download command to the upgrade.

Run the consumer repository's normal local check commands after these release-specific checks. Use the same build, test, lint, and policy entry points required for an ordinary pull request. Do not merge while any required check fails.

Review the final diff and confirm that it contains no moving reusable workflow reference. Submit every repository change in the migration checklist as one pull request. Confirm the external prerequisites in the checklist before merging or triggering the rehearsal.

## 5. Rehearse the target revision

After the upgrade reaches the default branch, perform the draft-only procedure in [Rehearse and recover GitHub Releases](rehearse-and-recover-github-releases.md). Do not enable either publisher until the target revision has populated and verified a draft and produced the expected OCI artifact.

Use the linked guide for its release creation, exact run selection, draft lookup, resumption, and recovery mechanics. During an upgrade rehearsal, the target contract overrides every baseline constant in that guide. In particular, require the target asset names and count, the checksum certificate identity ending in `@$NEW_RELEASE_REVISION`, and the publisher `--signer-digest "$NEW_RELEASE_REVISION"`. Do not reject a target-compliant draft because it differs from the baseline fourteen-asset set or baseline revision.

The rehearsal must produce these observable results:

- Release Please creates the candidate `v*` tag and matching draft;
- the producer and publisher jobs run at `NEW_RELEASE_REVISION`;
- the draft contains exactly the assets allowed by the target contract;
- the checksum manifest validates every listed payload; and
- the release remains a draft.

Use the authenticated GitHub CLI session to download every asset from the exact draft release ID recorded by the rehearsal guide. Run this Bash block from the consumer repository after setting `TAG`, `REPOSITORY`, and `RELEASE_ID`:

```bash
set -euo pipefail
export DRAFT_ASSET_DIR="release-assets-upgrade-${TAG#v}"
test ! -e "$DRAFT_ASSET_DIR"
mkdir "$DRAFT_ASSET_DIR"
gh api --paginate --slurp \
  "repos/$REPOSITORY/releases/$RELEASE_ID/assets?per_page=100" \
  --jq '.[][] | [.id, .name] | @tsv' \
  > "$DRAFT_ASSET_DIR/.assets.tsv"
test -s "$DRAFT_ASSET_DIR/.assets.tsv"
while IFS=$'\t' read -r asset_id asset_name; do
  [[ "$asset_id" =~ ^[0-9]+$ ]]
  [[ "$asset_name" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]]
  gh api \
    -H 'Accept: application/octet-stream' \
    "repos/$REPOSITORY/releases/assets/$asset_id" \
    > "$DRAFT_ASSET_DIR/$asset_name"
done < "$DRAFT_ASSET_DIR/.assets.tsv"
rm -- "$DRAFT_ASSET_DIR/.assets.tsv"
cd "$DRAFT_ASSET_DIR"
test -s checksums.txt
test -s checksums.txt.sigstore.json
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum --check checksums.txt
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 --check checksums.txt
else
  printf 'No SHA-256 checksum command is available.\n' >&2
  exit 1
fi
```

Every listed payload must report `OK`. Verify the target checksum signer identity only after checksum verification succeeds:

```bash
mise exec -- cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@$NEW_RELEASE_REVISION" \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
```

Cosign must exit successfully. An identity containing the current baseline or any other revision is an upgrade failure.

Verify each checksummed payload against the target publisher revision. Set `TAG` and `REPOSITORY` to the candidate values before running this command:

```bash
while IFS= read -r entry; do
  test -n "$entry" || continue
  asset="${entry:66}"
  mise exec -- gh attestation verify "$asset" \
    --repo "$REPOSITORY" \
    --signer-workflow meigma/release/.github/workflows/publish-github-release.yml \
    --signer-digest "$NEW_RELEASE_REVISION" \
    --source-ref "refs/tags/$TAG" \
    --deny-self-hosted-runners
done < checksums.txt
```

Every invocation must exit successfully. `--signer-digest` binds the reusable publisher to the reviewed target commit; the source-ref constraint binds the attestation to the consumer's candidate tag.

After these checks pass, change both `publish-image` and `publish-release` to `true` and resume through the same tag and populated draft as described in the rehearsal guide. The resume run must use `NEW_RELEASE_REVISION` in all five caller locations.

## Roll back before publication

If no candidate tag exists, reverse every repository and external-prerequisite change recorded in the migration checklist. Restore caller interfaces and permissions, workflow references, source and asset configuration, tool declarations and lock entries, App access, tag rules, credentials, and organization settings to their prior states. Apply the repository rollback in one pull request, sequence external rollback steps so the restored workflow remains operable, and run the validation commands again before merging.

If the target revision has populated an unpublished draft:

1. Reverse every repository change in the migration checklist in one rollback commit, including all four `uses:` entries, `checksum-signing-workflow-ref`, caller permissions and interfaces, source and asset configuration, and tool pins and lock entries.
2. Restore every changed external prerequisite to the prior state recorded in the checklist. Sequence those changes so the rollback workflow remains operable, and record each observable restored state.
3. Keep both `publish-image: false` and `publish-release: false`.
4. Run the locked install, GoReleaser check, actionlint, and repository checks against the complete rollback.
5. Move the same unpublished tag to the rollback commit and trigger a new top-level Release run, following the recovery procedure.
6. Verify the restored asset contract, Cosign identity, and GitHub signer digest before enabling publication.

The validated rollback run may replace expected asset names with `--clobber`. If the target revision added asset names that the previous signed manifest does not allow, those names are unexpected during rollback and block upload. Remove them manually from the draft only after confirming that they came from the abandoned target revision. Do not delete and recreate the draft.

## Correct after publication

A public release is no longer a recoverable draft. The publisher rejects it, and there is no workflow rollback that can safely replace its assets or move its tag.

Preserve the public release and tag. Restore or advance the entire workflow contract for future releases in a reviewed pull request, including caller interfaces, permissions, source and asset configuration, tools, and applicable external prerequisites. Run the same validation and draft rehearsal, then publish a corrected new version. Delete or rewrite a public release only through an explicitly authorized release-incident process.
