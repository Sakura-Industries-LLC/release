# Configure GitHub Releases

Use this guide to add the shared Meigma Go release workflows to a repository. The [GitHub Release contract](../reference/github-release-contract.md) defines the reusable workflow inputs, permissions, artifacts, and failure behavior.

`FULL_SHA` is the placeholder for the released commit and will be replaced when
this program's final pull request lands.

## Prerequisites

Before you change the repository, confirm that:

- the repository contains a Go command and uses `main` as its default branch, or you know which branch value to replace in the example;
- GitHub Actions is enabled and the repository's Actions policy permits calls to `meigma/release` and the pinned actions used by the shared workflows;
- you can create and merge pull requests in the consumer repository;
- an organization owner can manage the `meigma-release` GitHub App installation and organization Actions credentials;
- `mise`, Git, and GitHub CLI are installed locally; and
- GitHub CLI is authenticated for the consumer repository.

From the consumer repository, record its name and check authentication:

```bash
gh auth status
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
export DEFAULT_BRANCH="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
printf 'Repository: %s\nDefault branch: %s\n' "$REPOSITORY" "$DEFAULT_BRANCH"
```

The example workflows target `main`. If the command prints another default branch, replace `main` in `.github/workflows/release-please.yml` before you merge the configuration.

## 1. Grant the Release App access

An organization owner must complete this step before either release workflow runs.

1. Open the Meigma organization settings in GitHub.
2. Open **Third-party access** > **GitHub Apps** > **Installed GitHub Apps**.
3. Configure the Meigma Release App.
4. Under **Repository access**, keep **Only select repositories** selected and add the consumer repository.
5. Save the installation.

The installation must show the consumer repository in its selected repository list. Do not change the installation to **All repositories**.

If a repository or organization ruleset restricts creation of `v*` tags, add the Meigma Release App as a bypass actor for that restriction. Keep the rule enabled for other actors.

These are organization administration operations. GitHub CLI has no purpose-built command for changing an App installation's selected repositories, and an API request requires installation-management authorization. The commands in this guide therefore do not attempt that change. An organization owner must use the GitHub settings UI or an independently authorized administrative process.

## 2. Grant the organization credentials

In the Meigma organization settings, open **Secrets and variables** > **Actions**.

1. Create or update the organization variable `MEIGMA_RELEASE_APP_CLIENT_ID`.
2. Set its value to the Meigma Release App client ID.
3. Set its repository access to **Selected repositories** and add the consumer repository.
4. Create or update the organization secret `MEIGMA_RELEASE_APP_PRIVATE_KEY` with the App private key.
5. Set the secret's repository access to **Selected repositories** and add the same consumer repository.

The variable and secret must each show the consumer repository in their selected repository list. GitHub never returns an Actions secret's stored value, so verification is limited to its name, visibility, selected repository access, and a workflow that successfully creates an App token. Do not print the private key or add it as a repository file.

The publisher workflow uses the client ID and private key with `actions/create-github-app-token`. It passes only the resulting short-lived installation token to `release-cli publish github` through `RELEASE_APP_TOKEN`. The CLI holds the token as a redacted secret; it never receives the App private key or mints a token.

Organization owners can administer organization variables and secrets through GitHub CLI only when their token has the required organization scopes and role. This guide uses the UI because repository-level authorization alone cannot perform or verify these organization-level writes.

## 3. Copy the release files

From a checkout of `meigma/release`, copy the release infrastructure from `examples/go-release/` into the consumer repository. Preserve the relative paths. Do not copy `examples/go-release/README.md`.

Set `CONSUMER` to the consumer checkout. Before copying, check whether any destination path already exists. If it does, stop and merge the example's release settings into that file; do not overwrite repository configuration. When the destination paths are absent, copy the files:

```bash
export CONSUMER=/absolute/path/to/consumer
mkdir -p "$CONSUMER/.github/workflows"
cp examples/go-release/.github/workflows/release-please.yml "$CONSUMER/.github/workflows/"
cp examples/go-release/.github/workflows/release.yml "$CONSUMER/.github/workflows/"
cp examples/go-release/.goreleaser.yaml "$CONSUMER/"
cp examples/go-release/apko.yaml "$CONSUMER/"
cp examples/go-release/melange.yaml "$CONSUMER/"
cp examples/go-release/.release-please-manifest.json "$CONSUMER/"
cp examples/go-release/release-please-config.json "$CONSUMER/"
cp examples/go-release/mise.toml "$CONSUMER/"
cp examples/go-release/mise.lock "$CONSUMER/"
```

The example contains release infrastructure only. It does not define pull request checks, branch protection, code review, or the repository's complete CI policy.

For a new empty repository that will use the complete minimal command, also copy:

```bash
mkdir -p "$CONSUMER/cmd/example"
cp examples/go-release/go.mod "$CONSUMER/"
cp examples/go-release/cmd/example/main.go "$CONSUMER/cmd/example/"
```

Do not overwrite an existing repository's `go.mod` or command source. Adapt its real command to the copied release configuration instead.

## 4. Replace project-specific values

In the copied files, replace the example values with values from the consumer repository:

- In `.goreleaser.yaml`, replace project name, build ID, archive ID, command path, and binary name `example` with the consumer's values.
- Keep the `main.version` and `main.commit` linker variable names only if the command's `main` package defines both variables and uses them for `--version`. Otherwise, change the ldflags to the consumer command's real linker variables.
- If you copied the sample source, replace module path `example.com/meigma/release-consumer` and the literal command name and output in `cmd/example/main.go`.
- In `release-please-config.json`, replace package name `example` and choose the intended first release in `initial-version`.
- In `.release-please-manifest.json`, keep `0.0.0` only for a repository that has never released. For an existing project, set `.` to its latest released version without the `v` prefix.
- In `.github/workflows/release-please.yml`, replace `main` if the consumer's default branch is different.
- In `melange.yaml` and `apko.yaml`, replace the package name, command path, description, license, source URL, and image annotations as described in [Configure OCI image publication](configure-oci-images.md).

Do not replace these shared contract values:

- reusable workflow revision `FULL_SHA`;
- `checksum-signing-workflow-ref` value `meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA`;
- variable name `MEIGMA_RELEASE_APP_CLIENT_ID`; or
- secret name `MEIGMA_RELEASE_APP_PRIVATE_KEY`.

To change the immutable revision later, follow [Upgrade GitHub Release workflows](upgrade-github-release-workflows.md). Update all reusable workflow references and the checksum signing identity together; do not edit one reference in isolation.

Keep `checksum-signing-workflow-ref` in `owner/repository/workflow@revision` form without a URL prefix. The publisher adds `https://github.com/` and passes the resulting exact certificate identity to `release-cli verify bundle` with `--identity`. For example, the documented input becomes `--identity https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA`.

The copied GoReleaser configuration builds Darwin, Linux, and Windows archives
for amd64 and arm64. Confirm that the consumer command supports those targets
before releasing it. Keep both `changelog.disable: true` and
`release.disable: true`: Release Please owns the release notes and initial
draft, and GoReleaser must not publish a release.

The producer obtains `release-cli` through the shared setup action and runs
`release-cli stage --profile go --dist dist` before uploading either Actions
artifact. The `stage` command builds the release bundle by invoking exactly
`goreleaser release --clean --skip=publish`, then validates and projects the
result. `release.disable: true` and `--skip=publish` are independent publication
controls. Leave the optional `cli-path` input unset in a consumer repository.
It is an unsupported escape hatch for this repository's dogfood release and for
callers that own the workflow-to-binary pairing.

After downloading the authoritative artifact, the publisher runs `release-cli verify bundle`. The command verifies the local closed file set before it verifies the detached Sigstore bundle against the exact certificate identity. The workflow then creates the GitHub build-provenance attestation with `dist/checksums.txt` and runs `release-cli publish github --dist dist --json`. The CLI rebuilds the expected names and digests from the verified local bundle, reconciles the matching draft, uploads expected names, and verifies GitHub's asset states and digests. Keep this verify, attest, then publish order. For the reasoning behind these separate responsibilities, see [Why release trust is split across workflows and the CLI](../explanation/release-trust-boundaries.md).

The copied release caller sets both `publish-image: false` and `publish-release: false`. Keep both values for the first rehearsal. With `publish-release: false`, the workflow passes `--no-undraft`; the CLI converges the populated draft and stops without making it public. Before a public release, change both inputs to `true` and merge the change before Release Please creates the tag. The [rehearsal and recovery guide](rehearse-and-recover-github-releases.md) gives the safer first-run sequence.

## 5. Generate and validate the tool lock

Run the following commands from the consumer repository:

```bash
mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64
mise install --locked
mise exec -- goreleaser check
mise exec -- go list ./cmd/...
```

`mise lock` must leave `mise.lock` with entries for the pinned Go, GoReleaser, Syft, Cosign, GitHub CLI, Melange, and apko tools. `mise install --locked` must complete without changing a requested version, and `goreleaser check` must accept `.goreleaser.yaml`. Confirm that `go list` includes the command path configured in `.goreleaser.yaml`.

Commit both `mise.toml` and the generated `mise.lock` with the other release files. Submit the change through the repository's normal pull request review and squash-merge process.

After the configuration reaches the default branch, confirm that GitHub recognizes both workflows:

```bash
gh workflow view release-please.yml --repo "$REPOSITORY"
gh workflow view release.yml --repo "$REPOSITORY"
```

Each command must print the corresponding workflow instead of reporting that the workflow was not found.

## 6. Run Release Please

Before continuing with a public release, confirm that `.github/workflows/release.yml` on the default branch contains both `publish-image: true` and `publish-release: true`. Release Please also needs at least one releasable Conventional Commit after the version recorded in `.release-please-manifest.json`. Do not create an empty release commit to satisfy this condition.

When a releasable change is present, dispatch Release Please and inspect its run:

```bash
gh workflow run release-please.yml --repo "$REPOSITORY" --ref "$DEFAULT_BRANCH"
gh run list \
  --repo "$REPOSITORY" \
  --workflow release-please.yml \
  --limit 5
```

The successful run creates or updates one Release Please pull request. Find it by its workflow-managed label:

```bash
export RELEASE_PR="$(gh pr list \
  --repo "$REPOSITORY" \
  --label 'autorelease: pending' \
  --json number \
  --jq '.[0].number')"
test -n "$RELEASE_PR"
gh pr view "$RELEASE_PR" --repo "$REPOSITORY"
```

Review the version, changelog, and manifest changes. If they are correct and required checks pass, squash-merge the release pull request:

```bash
gh pr merge "$RELEASE_PR" \
  --repo "$REPOSITORY" \
  --squash \
  --delete-branch
```

The merge triggers Release Please again. A successful run creates a `v*` tag and a matching draft GitHub Release through the Release App. The App-created tag then triggers `.github/workflows/release.yml`.

Inspect both workflows:

```bash
gh run list --repo "$REPOSITORY" --workflow release-please.yml --limit 5
gh run list --repo "$REPOSITORY" --workflow release.yml --limit 5
```

With both publishers enabled, the Release workflow builds the authoritative archives and OCI image, verifies their handoffs, and publishes and signs the GHCR image. For the GitHub Release, the publisher runs `release-cli verify bundle`, creates the release attestation with `dist/checksums.txt`, and then runs `release-cli publish github`. The CLI binds the tag to the workflow commit, uploads only expected assets with clobber semantics, verifies the exact asset set and GitHub-reported digests, and makes the draft public as its last mutation. It never creates or re-drafts a release and never deletes an asset.

For an unmodified new example, the first tag is `v0.1.0`. For another repository, set `TAG` to the exact tag shown by the successful Release Please run:

```bash
export TAG=v0.1.0
gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json tagName,isDraft,isPrerelease,publishedAt,url
```

The final result must report the expected tag, `"isDraft": false`, and `"isPrerelease": false`. The release contains six platform archives, six archive SBOMs, `checksums.txt`, and `checksums.txt.sigstore.json`.

To stop before publication and inspect the populated draft, follow [Rehearse and recover GitHub Releases](rehearse-and-recover-github-releases.md).

## 7. Verify the published release

Create a new directory and download the exact release:

```bash
export ASSET_DIR="release-assets-${TAG#v}"
test ! -e "$ASSET_DIR"
mkdir "$ASSET_DIR"
gh release download "$TAG" \
  --repo "$REPOSITORY" \
  --dir "$ASSET_DIR"
cd "$ASSET_DIR"
```

On macOS, verify every payload named by the checksum manifest:

```bash
shasum -a 256 --check checksums.txt
```

On Linux, use:

```bash
sha256sum --check checksums.txt
```

Every listed archive and SBOM must report `OK`.

Verify that the checksum manifest was signed by the canonical reusable pre-publish workflow revision:

```bash
mise exec -- cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
```

Cosign must exit successfully with the certificate identity and issuer constraints in place.

Finally, verify the GitHub build-provenance attestation for every checksummed payload:

```bash
while IFS= read -r entry; do
  test -n "$entry" || continue
  asset="${entry:66}"
  mise exec -- gh attestation verify "$asset" \
    --repo "$REPOSITORY" \
    --signer-workflow meigma/release/.github/workflows/publish-github-release.yml \
    --signer-digest FULL_SHA \
    --source-ref "refs/tags/$TAG" \
    --deny-self-hosted-runners
done < checksums.txt
```

Each invocation must exit successfully. The signer workflow names the reusable publisher rather than the consumer caller, and `--signer-digest` binds it to the canonical revision. GitHub attestations cover the twelve payloads in `checksums.txt`; the checksum manifest and Cosign bundle are control files and are not attestation subjects.
