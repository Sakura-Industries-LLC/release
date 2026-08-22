# Add Homebrew and Scoop

Use this guide to publish the GoReleaser-generated cask and Scoop manifest from
one producer into adopter-owned repositories. The publishers open pull requests.
They never merge, approve, or enable auto-merge.

Complete [Adopt the release workflows](adopt-the-release-workflows.md) and
[Install `release-cli`](install-release-cli.md) first. Keep both publishers
disabled until their destination repositories and required checks exist.

## Prepare the adopter-owned App

Install the adopter-owned release App on:

- the producer repository;
- the Homebrew tap; and
- the Scoop bucket.

The App needs `contents: write` and `pull requests: write` on each destination.
The producer needs access to the App client-ID variable and private-key secret.
The tap and bucket do not need those Actions values; their validation workflows
are secret-free.

## Set up the Homebrew tap

### Generate the tap

Choose a public `homebrew-<name>` repository and an absent or empty local
directory:

```bash
export TAP_REPOSITORY=acme/homebrew-tools
export TAP_DIRECTORY="$PWD/homebrew-tools"
release-cli init homebrew-tap \
  --tap "$TAP_REPOSITORY" \
  --output "$TAP_DIRECTORY"
find "$TAP_DIRECTORY" -type f -print | sort
```

The initializer writes exactly:

```text
.github/dependabot.yml
.github/workflows/casks.yml
Casks/.gitkeep
README.md
```

It creates a cask-only tap and does not create `Formula/`. The generated
validation workflow pins `meigma/release` to the full source commit stamped into
the installed CLI. A nonempty output directory is rejected rather than merged.

Create the public repository:

```bash
cd "$TAP_DIRECTORY"
git init
git add .
git commit -m 'chore: initialize Homebrew tap'
git branch -M main
gh repo create "$TAP_REPOSITORY" --public --source=. --remote=origin --push
```

Enable Actions for the tap and allow the pinned `meigma/release` validation
workflow and its pinned actions.

### Generate the cask in the producer

Customize the maintained `homebrew_casks` entry in the producer's
`.goreleaser.yaml`:

```yaml
homebrew_casks:
  - name: widget
    ids:
      - widget
    binaries:
      - widget
    repository:
      owner: acme
      name: homebrew-tools
    homepage: https://github.com/acme/widget
    description: Widget command
    license: MIT
    url:
      template: "https://github.com/acme/widget/releases/download/{{ .Tag }}/{{ .ArtifactName }}"
    skip_upload: true
```

The cask name uses lowercase letters, digits, and interior hyphens. Keep
`skip_upload: true`: GoReleaser generates `dist/homebrew/Casks/widget.rb`, but
`release-cli publish homebrew` owns destination writes.

Customize the maintained `homebrew-publish` job:

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
    uses: meigma/release/.github/workflows/publish-homebrew.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
      tap: acme/homebrew-tools
      cask: widget
      release-app-client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
      publish-homebrew: false
    secrets:
      release-app-private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
```

Replace both revision placeholders with the same full SHA used everywhere else
in the producer. Leave `publish-homebrew: false` until branch protection is
ready.

### Protect and validate the tap

In the tap's **Settings** > **Rules** > **Rulesets**, create an active branch
ruleset for `main` that:

1. requires changes through a pull request;
2. requires `casks / Homebrew cask validation`;
3. blocks force pushes; and
4. blocks branch deletion.

GitHub may not offer the required check until it has run once. If necessary,
enable the publisher for one real release, wait for the new tap pull request
and its validation run, add that check to the ruleset, and only then merge the
pull request.

The validation workflow runs only when `Casks/**/*.rb` changes. Dependabot
changes only `.github/workflows/casks.yml`, so its pull request does not trigger
the required check. After the first cask exists, reproduce an action update on a
maintainer branch and include a semantically neutral comment change in that cask.
Before the first cask, defer the update rather than bypassing the ruleset.

For the first publication, set `publish-homebrew: true` only in a caller that
also sets `publish-image: true` and `publish-release: true`, then create the
stable tag. After the GitHub Release is public, the publisher creates or reuses
the deterministic `release/widget/v<version>` branch and opens one non-draft
pull request. Wait for validation, review the generated cask and URLs, and merge
it manually.

Test the lifecycle on Apple silicon and Intel macOS when both archives are
published:

```bash
brew install --cask acme/tools/widget
widget --version
brew update
brew upgrade --cask widget
widget --version
brew uninstall --cask widget
brew untap acme/tools
```

Publish and merge a later cask before testing `brew upgrade`. A failed producer
job does not merge the pull request. Correct the producer or tap configuration,
then rerun the failed job.

## Set up the Scoop bucket

### Generate the bucket

Choose a public repository and an absent or empty local directory:

```bash
export BUCKET_REPOSITORY=acme/scoop-tools
export BUCKET_DIRECTORY="$PWD/scoop-tools"
release-cli init scoop-bucket \
  --bucket "$BUCKET_REPOSITORY" \
  --output "$BUCKET_DIRECTORY"
find "$BUCKET_DIRECTORY" -type f -print | sort
```

The initializer writes exactly:

```text
.gitattributes
.github/dependabot.yml
.github/workflows/manifests.yml
README.md
```

The bucket uses root `*.json` manifests. `.gitattributes` gives text files CRLF
line endings in Windows checkouts for the pinned Scoop tests. The initializer
does not create a sample manifest.

Create the public repository:

```bash
cd "$BUCKET_DIRECTORY"
git init
git add .
git commit -m 'chore: initialize Scoop bucket'
git branch -M main
gh repo create "$BUCKET_REPOSITORY" --public --source=. --remote=origin --push
```

Enable Actions and allow the generated workflow's pinned `meigma/release`
validation workflow and actions.

### Generate the manifest in the producer

Customize the maintained `scoops` entry:

```yaml
scoops:
  - name: widget
    ids:
      - widget
    repository:
      owner: acme
      name: scoop-tools
    homepage: https://github.com/acme/widget
    description: Widget command
    license: MIT
    url_template: "https://github.com/acme/widget/releases/download/{{ .Tag }}/{{ .ArtifactName }}"
    skip_upload: true
```

Keep `skip_upload: true`. The archive configuration must let GoReleaser select
both Windows AMD64 and ARM64 assets when the producer ships both.

Customize the maintained publisher job:

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
    uses: meigma/release/.github/workflows/publish-scoop.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
      bucket: acme/scoop-tools
      manifest: widget
      release-app-client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
      publish-scoop: false
    secrets:
      release-app-private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
```

Replace both placeholders with the producer's one release-unit SHA. Leave
`publish-scoop: false` until the bucket ruleset is ready.

### Protect and validate the bucket

Create an active branch ruleset for `main` that:

1. requires changes through a pull request;
2. requires `manifests / Scoop manifest validation`;
3. blocks force pushes; and
4. blocks branch deletion.

GitHub may not offer the required check until it has run once. If necessary,
enable the publisher for one real release, wait for the new bucket pull request
and its validation run, add that check to the ruleset, and only then merge the
pull request.

The validation workflow runs on root manifest changes and tests Windows AMD64
and ARM64. Dependabot changes only `.github/workflows/manifests.yml`, so it does
not trigger this required check. After the first manifest exists, reproduce an
action update on a maintainer branch and include a semantically neutral format
change to one root manifest so validation runs. Before the first manifest,
defer that action update rather than bypassing the ruleset.

Set `publish-scoop: true` only in a caller that also sets `publish-image: true`
and `publish-release: true`, then create the stable tag. After the GitHub
Release is public, review the generated root `widget.json`, wait for both
validation jobs, and merge the pull request manually.

Test installation and removal on clean Windows AMD64 and ARM64 systems:

```powershell
scoop bucket add scoop-tools https://github.com/acme/scoop-tools
scoop search widget
scoop install scoop-tools/widget
widget --version
scoop uninstall widget
scoop bucket rm scoop-tools
```

Because this bucket uses root manifests, `scoop bucket list` can report
`Manifests 0`. Use search, install, and the required validation check as the
acceptance signals.

After publishing and merging a later version, test an update from the earlier
bucket commit:

```powershell
scoop bucket add scoop-tools https://github.com/acme/scoop-tools
$bucket = Join-Path $env:USERPROFILE 'scoop\buckets\scoop-tools'
git -C $bucket checkout <PREVIOUS_BUCKET_COMMIT>
scoop install scoop-tools/widget
git -C $bucket checkout main
git -C $bucket pull --ff-only
scoop update widget
widget --version
scoop uninstall widget
scoop bucket rm scoop-tools
```

Run the update lifecycle on both Windows architectures when both archives are
published. A failed publisher does not merge or auto-merge its pull request;
correct the conflict or configuration and rerun the failed job.
