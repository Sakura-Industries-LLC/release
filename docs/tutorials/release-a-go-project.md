# Release a Go project

In this tutorial, we will turn the copyable example into a working release for a
small Go command. We will first run the complete pipeline with publication
disabled, inspect the draft and workflow artifacts, and then publish through the
same Release Please tag and draft.

The tutorial uses a command named `hello-release`. For an existing project with
different source and release policies, use [Configure GitHub Releases](../how-to/configure-github-releases.md) instead of copying the sample command.

## Prerequisites

You need:

- a GitHub repository named `hello-release`, with `main` as its default branch,
  and a local checkout of that repository;
- a local checkout of `meigma/release` containing `examples/go-release/`;
- permission to create and merge pull requests in the `hello-release`
  repository;
- help from a Meigma organization owner to grant the Release App and
  organization Actions credentials access to the repository;
- GitHub Actions enabled, with a policy that permits the pinned workflows and
  actions used by the example; and
- Git, GitHub CLI, `mise`, and `jq` installed locally, with GitHub CLI
  authenticated for the repository.

Start in the `hello-release` checkout and record the consumer repository and
example path:

```bash
export RELEASE_EXAMPLE=/absolute/path/to/meigma-release/examples/go-release
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
export DEFAULT_BRANCH="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
test "${REPOSITORY#*/}" = hello-release
test "$DEFAULT_BRANCH" = main
printf 'Consumer: %s\nExample: %s\n' "$REPOSITORY" "$RELEASE_EXAMPLE"
```

The final command prints the `OWNER/hello-release` repository and the absolute
path to `examples/go-release`.

## Copy the example

The tutorial starts with paths that do not already contain release
configuration. Confirm that before copying:

```bash
for path in \
  .github/workflows/release-please.yml \
  .github/workflows/release.yml \
  .goreleaser.yaml \
  apko.yaml \
  melange.yaml \
  .release-please-manifest.json \
  release-please-config.json \
  mise.toml \
  mise.lock \
  go.mod \
  cmd/hello-release; do
  test ! -e "$path"
done
```

The command exits without output. Now copy the release files and sample
command:

```bash
mkdir -p .github/workflows cmd
cp "$RELEASE_EXAMPLE/.github/workflows/release-please.yml" .github/workflows/
cp "$RELEASE_EXAMPLE/.github/workflows/release.yml" .github/workflows/
cp "$RELEASE_EXAMPLE/.goreleaser.yaml" .
cp "$RELEASE_EXAMPLE/apko.yaml" .
cp "$RELEASE_EXAMPLE/melange.yaml" .
cp "$RELEASE_EXAMPLE/.release-please-manifest.json" .
cp "$RELEASE_EXAMPLE/release-please-config.json" .
cp "$RELEASE_EXAMPLE/mise.toml" .
cp "$RELEASE_EXAMPLE/mise.lock" .
cp "$RELEASE_EXAMPLE/go.mod" .
cp -R "$RELEASE_EXAMPLE/cmd/example" cmd/hello-release
```

Check every copied path:

```bash
for path in \
  .github/workflows/release-please.yml \
  .github/workflows/release.yml \
  .goreleaser.yaml \
  apko.yaml \
  melange.yaml \
  .release-please-manifest.json \
  release-please-config.json \
  mise.toml \
  mise.lock \
  go.mod \
  cmd/hello-release/main.go; do
  test -f "$path"
  printf 'copied %s\n' "$path"
done
```

You see one `copied` line for each of the eleven paths.

## Adapt the Go command and packages

Install the locked tools, then set the module path to the GitHub repository:

```bash
mise install --locked
mise exec -- go mod edit -module "github.com/$REPOSITORY"
head -n 1 go.mod
```

`mise install` reports that the locked tools are installed without changing
their requested versions. The final command prints:

```text
module github.com/OWNER/hello-release
```

Replace `OWNER` with the owner shown in `$REPOSITORY`.

Edit `cmd/hello-release/main.go`. Replace the two user-facing occurrences of
`example` with `hello-release`. Keep the `version` and `commit` variables: the
copied GoReleaser configuration sets both at link time.

Edit `.goreleaser.yaml` so its project, build, archive, nFPM, and binary values read:

```yaml
project_name: hello-release

builds:
  - id: hello-release
    main: ./cmd/hello-release
    binary: hello-release

archives:
  - id: hello-release
    ids:
      - hello-release

nfpms:
  - id: hello-release
    ids:
      - hello-release
    vendor: Meigma
    homepage: https://github.com/OWNER/hello-release
    maintainer: Meigma <contact@meigma.dev>
    description: Hello release tutorial command.
    license: LicenseRef-Proprietary

These lines replace the corresponding `example` values; keep the remaining
build, archive, nFPM, checksum, SBOM, signing, changelog, and release settings
from the example. Replace `OWNER` with the owner shown in `$REPOSITORY`. In
particular, keep:

```yaml
changelog:
  disable: true

release:
  disable: true
```

Release Please will own the release notes and initial draft. During the
producer job, `release-cli stage --profile go` invokes exactly
`goreleaser release --clean --skip=publish`; `release.disable: true` is the
second control that prevents GoReleaser publication.

In `release-please-config.json`, replace the package name `example` with
`hello-release`. Keep the initial version at `0.1.0` and the manifest version at
`0.0.0` for this first release.

Run the sample command:

```bash
mise exec -- go run ./cmd/hello-release --version
```

The command prints:

```text
hello-release dev (none)
```

## Adapt the packages and image

Edit `melange.yaml` for the same command:

```yaml
package:
  name: hello-release
  description: Hello release tutorial command.
  vendor: Meigma
  homepage: https://github.com/OWNER/hello-release
  maintainer: Meigma <contact@meigma.dev>
  copyright:
    - license: LicenseRef-Proprietary

pipeline:
  - runs: |
      install -Dm755 -o 0 -g 0 application "${{targets.destdir}}/usr/bin/hello-release"
```

Keep the version variable, target architectures, Wolfi repository and keyring,
and package environment from the example.

Edit `apko.yaml` so the package, entrypoint, and annotations match the command:

```yaml
contents:
  packages:
    - alpine-release
    - ca-certificates-bundle
    - hello-release

entrypoint:
  command: /usr/bin/hello-release

annotations:
  org.opencontainers.image.title: hello-release
  org.opencontainers.image.description: Hello release tutorial command.
  org.opencontainers.image.source: https://github.com/OWNER/hello-release
  org.opencontainers.image.licenses: LicenseRef-Proprietary
```

Keep the `OWNER` replacement consistent with the nFPM and Melange package metadata. Keep the nonroot account,
architectures, certificate settings, and environment from the example.

Validate the completed release configuration:

```bash
mise exec -- goreleaser check
mise exec -- go list ./cmd/...
printf 'https://github.com/%s\n' "$REPOSITORY"
```

`goreleaser check` exits successfully. `go list` prints
`github.com/OWNER/hello-release/cmd/hello-release`, and the final command prints
the source URL now present in `apko.yaml`.

## Grant the release identities access

Complete the App installation and organization credential steps in
[Configure GitHub Releases](../how-to/configure-github-releases.md#1-grant-the-release-app-access):

1. Give the Meigma Release App selected-repository access to `hello-release`.
2. Give the organization variable `MEIGMA_RELEASE_APP_CLIENT_ID` and organization
   secret `MEIGMA_RELEASE_APP_PRIVATE_KEY` selected-repository access to the
   same repository.
3. If a protected-tag ruleset covers `v*`, grant its bypass to the Release App.

The App installation, variable, and secret settings each show
`OWNER/hello-release` in their selected repository list. The private key never
appears in the repository.

Also confirm the image prerequisites in
[Configure OCI image publication](../how-to/configure-oci-images.md#prerequisites).
The release caller grants the publisher `packages: write`; the organization must
have GitHub Packages enabled for the workflow to use that permission.

## Merge the disabled-publication configuration

The copied caller begins in rehearsal mode. Confirm both controls before
committing:

```bash
grep -F 'publish-image: false' .github/workflows/release.yml
grep -F 'publish-release: false' .github/workflows/release.yml
```

Both lines print. Commit the copied and adapted files on a branch, submit them
for review, and merge them through the repository's normal pull request flow:

```bash
git switch -c feat/add-release-workflows
git add \
  .github/workflows/release-please.yml \
  .github/workflows/release.yml \
  .goreleaser.yaml \
  apko.yaml \
  melange.yaml \
  .release-please-manifest.json \
  release-please-config.json \
  mise.toml \
  mise.lock \
  go.mod \
  cmd/hello-release/main.go
git commit -m 'feat: add hello release command'
git push -u origin HEAD
gh pr create --fill
```

GitHub prints the new pull request URL. After its required checks pass, merge it
with the repository's normal reviewed process. The default branch then contains
the command and both release workflows.

## Run the unpublished rehearsal

Treat the first `v0.1.0` candidate as a controlled, unpublished rehearsal tag.
Do not create a draft or push a tag yourself. Release Please owns the release
notes, tag, and initial draft.

Follow [Configure a draft-only run, create the candidate, and inspect the
draft](../how-to/rehearse-and-recover-github-releases.md#1-configure-a-draft-only-run).
Stop after section 3 of that guide. It supplies the exact Release Please, tag,
run-selection, and draft-query commands. Keep the `REPOSITORY`, `TAG`,
`RELEASE_RUN_ID`, and `RELEASE_ID` values from those steps.

At the end of the rehearsal:

- the Release workflow is green;
- the GitHub Release for `$TAG` is still a draft with 26 assets;
- the `release-assets`, `oci-build-inputs`, and `oci-image` workflow artifacts
  exist; and
- no image tag has been written to GHCR.

Open the `release-assets` job log. Its `Stage Go release artifacts` step contains
GoReleaser progress followed by release bundle staging. There is no separate
GoReleaser build step in the workflow: `release-cli stage --profile go` owns the
build and validation.

The `oci-publish` job runs the prepare command in dry-run mode. Its GHCR login,
attestation, and finalize steps are skipped. The `github-release` job populates
and verifies the draft but does not undraft it.

## Inspect the workflow artifacts

Download the authoritative release bundle and OCI image from the exact
rehearsal run:

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

Check the release bundle:

```bash
test "$(find "$INSPECT_DIR/release-assets" -maxdepth 1 -type f -name '*.sbom.json' | wc -l)" -eq 12
test "$(find "$INSPECT_DIR/release-assets" -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l)" -eq 6
test "$(find "$INSPECT_DIR/release-assets" -maxdepth 1 -type f \( -name '*.deb' -o -name '*.rpm' -o -name '*.apk' \) | wc -l)" -eq 6
test -s "$INSPECT_DIR/release-assets/checksums.txt"
test -s "$INSPECT_DIR/release-assets/checksums.txt.sigstore.json"
printf 'release bundle complete\n'
```

The commands print `release bundle complete`.

Check the OCI index and its two platforms:

```bash
test -s "$INSPECT_DIR/oci-image/layout/index.json"
test -s "$INSPECT_DIR/oci-image/image-digest.txt"
test -s "$INSPECT_DIR/oci-image/sboms/sbom-x86_64.spdx.json"
test -s "$INSPECT_DIR/oci-image/sboms/sbom-aarch64.spdx.json"
jq -r '.manifests[] | "\(.platform.os)/\(.platform.architecture)"' \
  "$INSPECT_DIR/oci-image/layout/index.json" |
  sort
```

The final command prints:

```text
linux/amd64
linux/arm64
```

The draft and artifacts now show what the enabled run will publish without
having changed the public release or GHCR tags.

## Enable publication

Resume with the same still-unpublished tag and draft by following
[Resume through the same tag and draft](../how-to/rehearse-and-recover-github-releases.md#4-resume-through-the-same-tag-and-draft).
That procedure changes both publication inputs to `true` in one reviewed commit
and selects the new run for the exact tag and enabling commit. It also preserves
Release Please ownership of the existing draft and release notes.

In the successful resume run, the OCI publisher prepares and signs the image,
the three GitHub attestation steps complete, and finalization applies the image
tags. The GitHub publisher verifies the release bundle, creates the release
attestation, converges the existing draft assets, and makes the draft public
only after the OCI publisher succeeds.

Confirm the release state with the command from the rehearsal guide:

```bash
gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json tagName,isDraft,isPrerelease,publishedAt,url
```

The result reports the same tag, `"isDraft": false`,
`"isPrerelease": false`, a non-null publication time, and a release URL.

GitHub package visibility follows the organization's package-creation setting.
Complete the visibility check and the signature, attestation, checksum, and
runtime verification in [Configure OCI image publication](../how-to/configure-oci-images.md#6-verify-the-published-image) and [Verify the published release](../how-to/configure-github-releases.md#7-verify-the-published-release).

## What you learned

You adapted one release unit, rehearsed it without publication, inspected the
exact artifacts crossing into the publisher jobs, and then published through
the same Release Please tag and draft.

For the complete interfaces, see the [GitHub Release contract](../reference/github-release-contract.md), [OCI image contract](../reference/oci-image-contract.md), and [`release-cli` contract](../reference/release-cli-contract.md). For the security reasoning behind the job and command boundaries, see [Why release trust is split across workflows and the CLI](../explanation/release-trust-boundaries.md).
