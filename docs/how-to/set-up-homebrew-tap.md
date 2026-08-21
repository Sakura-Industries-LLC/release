# Set up a Homebrew tap

Use this guide to create a cask-only Homebrew tap and connect a producer repository to the shared publisher. The [`release-cli` contract](../reference/release-cli-contract.md#homebrew-tap-initialization) defines the generated files and failure behavior.

## Prerequisites

Confirm that:

- the producer already builds a Homebrew cask with GoReleaser;
- a released, attestation-verified `release-cli` is installed;
- GitHub CLI is authenticated;
- you can create repositories and rulesets in the target organization; and
- an organization owner can change the Meigma Release App installation and Actions credentials.

Use a public repository named `homebrew-<name>`. Homebrew users will install casks from `owner/<name>`.

## 1. Generate the tap

Choose the repository and an absent or empty local directory:

```bash
export TAP_REPOSITORY=acme/homebrew-tools
export TAP_DIRECTORY="$PWD/homebrew-tools"

release-cli init homebrew-tap \
  --tap "$TAP_REPOSITORY" \
  --output "$TAP_DIRECTORY"
```

The command writes `.github/workflows/casks.yml`, `.github/dependabot.yml`, `Casks/.gitkeep`, and `README.md`. It does not create a `Formula/` directory. The reusable validation workflow is pinned to the full source commit of the installed CLI.

Inspect the output before creating the repository:

```bash
find "$TAP_DIRECTORY" -type f -print | sort
```

Do not run the command over an existing checkout. A nonempty output directory is rejected rather than merged or overwritten.

## 2. Create the GitHub repository

Initialize and publish the generated directory:

```bash
cd "$TAP_DIRECTORY"
git init
git add .
git commit -m "chore: initialize Homebrew tap"
git branch -M main
gh repo create "$TAP_REPOSITORY" --public --source=. --remote=origin --push
```

In the tap repository's **Settings** > **Actions** > **General**, confirm that Actions is enabled and that the Actions policy permits `meigma/release` and the actions used by its reusable workflow.

## 3. Grant the Release App access

An organization owner must add both the producer and tap repositories to the Meigma Release App installation:

1. Open the organization settings in GitHub.
2. Open **Third-party access** > **GitHub Apps** > **Installed GitHub Apps**.
3. Configure the Meigma Release App.
4. Under **Repository access**, keep **Only select repositories** selected.
5. Add the producer repository and the new tap repository, then save.

The App must have `contents: write` and `pull requests: write` for the tap. The publisher mints a short-lived token scoped to that repository. It does not use a personal access token.

## 4. Grant the producer its Actions credentials

In the organization settings, open **Secrets and variables** > **Actions**:

1. Make the variable `MEIGMA_RELEASE_APP_CLIENT_ID` available to the producer repository.
2. Make the secret `MEIGMA_RELEASE_APP_PRIVATE_KEY` available to the producer repository.
3. Keep both resources limited to selected repositories.

The tap does not need either credential. Its validation workflow is secret-free.

## 5. Configure cask generation

Add a `homebrew_casks` entry to the producer's `.goreleaser.yaml`. Keep `skip_upload: true`; `release-cli publish homebrew`, not GoReleaser, owns the tap pull request.

```yaml
homebrew_casks:
  - name: example
    ids:
      - example
    binaries:
      - example
    repository:
      owner: acme
      name: homebrew-tools
    homepage: https://github.com/acme/example
    description: Example command
    license: MIT
    url:
      template: "https://github.com/acme/example/releases/download/{{ .Tag }}/{{ .ArtifactName }}"
    skip_upload: true
```

Replace every example value. The cask name must use lowercase letters, digits, and interior hyphens.

## 6. Add the publisher job

Add a job after the public GitHub Release job in the producer's tag workflow. Pin `publish-homebrew.yml` and `checksum-signing-workflow-ref` to the same full `meigma/release` commit used by the other shared release workflows.

```yaml
  homebrew-publish:
    name: Open Homebrew tap pull request
    needs:
      - release-assets
      - github-release
    permissions:
      actions: read
      attestations: read
      contents: read
    uses: meigma/release/.github/workflows/publish-homebrew.yml@<FULL_RELEASE_COMMIT>
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@<FULL_RELEASE_COMMIT>
      tap: acme/homebrew-tools
      cask: example
      release-app-client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
      publish-homebrew: true
    secrets:
      release-app-private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
```

Keep `publish-homebrew: false` until the tap settings are ready. The publisher runs only after `github-release` succeeds, so generated cask URLs point to public release assets.

## 7. Protect the tap branch

The publisher opens a pull request and never merges it. Protect `main` before merging the first generated cask:

1. In the tap repository, open **Settings** > **Rules** > **Rulesets**.
2. Create a branch ruleset targeting the default branch.
3. Require changes through a pull request.
4. Require the `casks / Homebrew cask validation` status check.
5. Block force pushes and branch deletion.
6. Activate the ruleset.

GitHub may not offer the status check until it has run once. If necessary, enable the publisher for one real release, wait for its tap pull request and checks, configure the ruleset, and only then merge the pull request.

## 8. Verify the first publication

For the first real release:

1. Set `publish-homebrew: true` before the release tag is created.
2. Confirm that the GitHub Release becomes public.
3. Confirm that the producer workflow opens one tap pull request.
4. Wait for `casks / Homebrew cask validation` to pass.
5. Review and merge the tap pull request.
6. Install and remove the cask on both Apple silicon and Intel macOS where those archives are published.

```bash
brew install --cask acme/tools/example
example version
brew uninstall --cask example
```

A failed publisher run does not merge or auto-merge a tap pull request. Correct the producer or tap configuration, then rerun the failed job.
