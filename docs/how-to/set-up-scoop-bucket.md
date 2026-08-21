# Set up a Scoop bucket

Use this guide to create a Scoop bucket and connect a producer repository to the shared publisher. The [`release-cli` contract](../reference/release-cli-contract.md#scoop-bucket-initialization) defines the generated files and failure behavior.

## Prerequisites

Confirm that:

- the producer already builds a Scoop manifest with GoReleaser;
- a released, attestation-verified `release-cli` is installed;
- GitHub CLI is authenticated;
- you can create repositories and rulesets in the target organization; and
- an organization owner can change the Meigma Release App installation and Actions credentials.

Use a public repository. Keep manifests at the repository root because the publisher writes `<manifest>.json` there and the validation workflow inspects root `*.json` files. `scoop bucket list` counts only a `bucket/` subdirectory and therefore reports `Manifests 0` for this layout; use search, install, and the required validation check to verify the bucket.

## 1. Generate the bucket

Choose the repository and an absent or empty local directory:

```bash
export BUCKET_REPOSITORY=acme/scoop-tools
export BUCKET_DIRECTORY="$PWD/scoop-tools"

release-cli init scoop-bucket \
  --bucket "$BUCKET_REPOSITORY" \
  --output "$BUCKET_DIRECTORY"
```

The command writes `.gitattributes`, `.github/workflows/manifests.yml`, `.github/dependabot.yml`, and `README.md`. The reusable validation workflow is pinned to the full source commit of the installed CLI. `.gitattributes` makes text files use CRLF in Windows checkouts, as required by the pinned Scoop bucket tests.

Inspect the output before creating the repository:

```bash
find "$BUCKET_DIRECTORY" -type f -print | sort
```

Do not run the command over an existing checkout. A nonempty output directory is rejected rather than merged or overwritten.

## 2. Create the GitHub repository

Initialize and publish the generated directory:

```bash
cd "$BUCKET_DIRECTORY"
git init
git add .
git commit -m "chore: initialize Scoop bucket"
git branch -M main
gh repo create "$BUCKET_REPOSITORY" --public --source=. --remote=origin --push
```

In the bucket repository's **Settings** > **Actions** > **General**, confirm that Actions is enabled and that the Actions policy permits `meigma/release` and the actions used by its reusable workflow.

## 3. Grant the Release App access

An organization owner must add both the producer and bucket repositories to the Meigma Release App installation:

1. Open the organization settings in GitHub.
2. Open **Third-party access** > **GitHub Apps** > **Installed GitHub Apps**.
3. Configure the Meigma Release App.
4. Under **Repository access**, keep **Only select repositories** selected.
5. Add the producer repository and the new bucket repository, then save.

The App must have `contents: write` and `pull requests: write` for the bucket. The publisher mints a short-lived token scoped to that repository. It does not use a personal access token.

## 4. Grant the producer its Actions credentials

In the organization settings, open **Secrets and variables** > **Actions**:

1. Make the variable `MEIGMA_RELEASE_APP_CLIENT_ID` available to the producer repository.
2. Make the secret `MEIGMA_RELEASE_APP_PRIVATE_KEY` available to the producer repository.
3. Keep both resources limited to selected repositories.

The bucket does not need either credential. Its validation workflow is secret-free.

## 5. Configure manifest generation

Add a `scoops` entry to the producer's `.goreleaser.yaml`. Keep `skip_upload: true`; `release-cli publish scoop`, not GoReleaser, owns the bucket pull request.

```yaml
scoops:
  - name: example
    ids:
      - example
    repository:
      owner: acme
      name: scoop-tools
    homepage: https://github.com/acme/example
    description: Example command
    license: MIT
    url_template: "https://github.com/acme/example/releases/download/{{ .Tag }}/{{ .ArtifactName }}"
    skip_upload: true
```

Replace every example value. The manifest name must use lowercase letters, digits, and interior hyphens. Configure GoReleaser's archive names so the manifest selects both Windows AMD64 and ARM64 assets when the producer ships both architectures.

## 6. Add the publisher job

Add a job after the public GitHub Release job in the producer's tag workflow. Pin `publish-scoop.yml` and `checksum-signing-workflow-ref` to the same full `meigma/release` commit used by the other shared release workflows.

```yaml
  scoop-publish:
    name: Open Scoop bucket pull request
    needs:
      - release-assets
      - github-release
    permissions:
      actions: read
      attestations: read
      contents: read
    uses: meigma/release/.github/workflows/publish-scoop.yml@<FULL_RELEASE_COMMIT>
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@<FULL_RELEASE_COMMIT>
      bucket: acme/scoop-tools
      manifest: example
      release-app-client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
      publish-scoop: true
    secrets:
      release-app-private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
```

Keep `publish-scoop: false` until the bucket settings are ready. The publisher runs only after `github-release` succeeds, so generated manifest URLs point to public release assets.

## 7. Protect the bucket branch

The publisher opens a pull request and never merges it. Protect `main` before merging the first generated manifest:

1. In the bucket repository, open **Settings** > **Rules** > **Rulesets**.
2. Create a branch ruleset targeting the default branch.
3. Require changes through a pull request.
4. Require the `manifests / Scoop manifest validation` status check.
5. Block force pushes and branch deletion.
6. Activate the ruleset.

GitHub may not offer the status check until it has run once. If necessary, enable the publisher for one real release, wait for its bucket pull request and checks, configure the ruleset, and only then merge the pull request.

Dependabot updates `.github/workflows/manifests.yml`, so the manifest-only workflow does not report the required check on those pull requests. Do not bypass branch protection. After the bucket contains a manifest, reproduce the action update on a maintainer branch and include a semantically neutral formatting change to one root manifest so the Scoop validation runs. Before the first manifest exists, defer the action update.

## 8. Verify publication and the Scoop lifecycle

For the first real release:

1. Set `publish-scoop: true` before the release tag is created.
2. Confirm that the GitHub Release becomes public.
3. Confirm that the producer workflow opens one bucket pull request and changes only `<manifest>.json`.
4. Wait for `manifests / Scoop manifest validation` to pass on both Windows AMD64 and ARM64.
5. Review and merge the bucket pull request.
6. On clean Windows AMD64 and ARM64 systems, add the bucket, install the app, run its version command, uninstall it, and remove the bucket.

```powershell
scoop bucket add scoop-tools https://github.com/acme/scoop-tools
scoop install scoop-tools/example
example version
scoop uninstall example
scoop bucket rm scoop-tools
```

Verify updates after publishing and merging the next release. Install the earlier version from the bucket commit that contained it, return the bucket checkout to `main`, then update and uninstall the app:

```powershell
scoop bucket add scoop-tools https://github.com/acme/scoop-tools
$bucket = Join-Path $env:USERPROFILE 'scoop\buckets\scoop-tools'
git -C $bucket checkout <PREVIOUS_BUCKET_COMMIT>
scoop install scoop-tools/example
git -C $bucket checkout main
git -C $bucket pull --ff-only
scoop update example
example version
scoop uninstall example
scoop bucket rm scoop-tools
```

Run the update check on Windows AMD64 and ARM64 when both archives are published. A failed publisher run does not merge or auto-merge a bucket pull request. Correct the producer or bucket configuration, then rerun the failed job.
