# GitHub release contract reference

This page defines the cross-repository contract for the reusable Go producer and GitHub Release publisher at revision `FULL_SHA`. The placeholder will be replaced with the released commit when this program's final pull request lands.

For configuration steps, see [Configure GitHub releases](../how-to/configure-github-releases.md). For draft rehearsals and recovery steps, see [Rehearse and recover GitHub releases](../how-to/rehearse-and-recover-github-releases.md). The [`release-cli` contract](release-cli-contract.md) defines the command, output, and exit-code surface used by the producer. The [OCI image contract](oci-image-contract.md) defines the image builder and publisher that gate the complete delivery caller. To adopt another immutable revision, see [Upgrade GitHub release workflows](../how-to/upgrade-github-release-workflows.md). A complete consumer repository is available in the [Go release example](../../examples/go-release/).

## Canonical workflow references

The complete caller pins all four reusable workflows to one full revision. The GitHub Release path directly calls the producer and GitHub publisher:

```yaml
uses: meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA
```

```yaml
uses: meigma/release/.github/workflows/publish-github-release.yml@FULL_SHA
```

The checksum signer identity input must name the same producer workflow revision:

```yaml
checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA
```

## Caller contract

The supported caller runs on creation or movement of a `v*` tag. Every reusable workflow rejects a non-tag ref. Tag deletion events must not start the producer job. The GitHub Release publisher waits for successful image build and publication so a registry failure leaves the release draft unpublished.

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

permissions: {}

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false

jobs:
  release-assets:
    name: Build release assets
    if: github.event.deleted == false
    permissions:
      attestations: read
      contents: read
      id-token: write
    uses: meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA

  oci-image:
    name: Build OCI image
    needs: release-assets
    permissions:
      actions: read
      contents: read
    uses: meigma/release/.github/workflows/go-oci-build.yml@FULL_SHA
    with:
      artifact-id: ${{ needs.release-assets.outputs.oci-input-artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.oci-input-artifact-digest }}

  oci-publish:
    name: Publish OCI image
    needs: oci-image
    permissions:
      actions: read
      artifact-metadata: write
      attestations: write
      contents: read
      id-token: write
      packages: write
    uses: meigma/release/.github/workflows/publish-oci-image.yml@FULL_SHA
    with:
      artifact-id: ${{ needs.oci-image.outputs.artifact-id }}
      artifact-digest: ${{ needs.oci-image.outputs.artifact-digest }}
      image-digest: ${{ needs.oci-image.outputs.image-digest }}
      publish-image: true

  github-release:
    name: Publish GitHub Release
    needs:
      - release-assets
      - oci-image
      - oci-publish
    permissions:
      actions: read
      artifact-metadata: write
      attestations: write
      contents: read
      id-token: write
    uses: meigma/release/.github/workflows/publish-github-release.yml@FULL_SHA
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA
      require-oci-image: true
      oci-image-reference: ${{ needs.oci-publish.outputs.image-reference }}
      release-app-client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
      publish-release: true
    secrets:
      release-app-private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
```

The top-level `permissions: {}` prevents permissions from being granted implicitly. Each called job grants its reusable workflow only the permissions listed above. A called workflow cannot elevate permissions beyond those granted by its caller.

The caller concurrency key serializes runs for the same workflow and tag. `cancel-in-progress: false` prevents a later run for that tag from canceling an earlier run. The OCI publisher adds repository-wide serialization across different release tags so shared channel tags cannot race.

## Reusable workflow interfaces

### `go-pre-publish.yml`

The Go producer accepts one optional input and no secrets.

| Input | Type | Required | Default | Value |
| --- | --- | --- | --- | --- |
| `cli-path` | string | No | Empty | Unsupported path to a caller-supplied `release-cli` binary. The caller owns the workflow-to-binary pairing. Normal consumers omit this input. |

The producer loads `setup-release-cli` from the same pinned release revision
with `uses: $/.github/actions/setup-release-cli`. The caller does not pin the
action or CLI separately.

| Output | Value |
| --- | --- |
| `artifact-id` | ID returned by `actions/upload-artifact` for the `release-assets` artifact. |
| `artifact-url` | URL returned for the `release-assets` artifact. |
| `artifact-digest` | SHA-256 digest returned for the `release-assets` artifact. |

The job requires these caller permissions:

| Permission | Access | Use |
| --- | --- | --- |
| `attestations` | `read` | Verify the downloaded `release-cli` archive attestation during setup. |
| `contents` | `read` | Check out the consumer repository and its tag history. |
| `id-token` | `write` | Obtain the OIDC identity used by keyless Cosign signing. |

The workflow runs on `ubuntu-24.04` with a 20-minute timeout. It declares `permissions: {}` at workflow scope, so the caller must grant the job permissions explicitly. Its `release-assets` artifact is retained for seven days and is uploaded with compression disabled.

### `publish-github-release.yml`

| Input | Type | Required | Default | Value |
| --- | --- | --- | --- | --- |
| `artifact-id` | string | Yes | None | Positive integer ID from `go-pre-publish.yml`. |
| `artifact-digest` | string | Yes | None | Expected SHA-256 digest from `go-pre-publish.yml`. The comparison accepts the digest with or without a `sha256:` prefix. |
| `cli-path` | string | No | Empty | Unsupported path to a caller-supplied `release-cli` binary. The caller owns the workflow-to-binary pairing. Normal consumers omit this input. |
| `checksum-signing-workflow-ref` | string | Yes | None | Exact owner, repository, workflow path, and revision used as the checksum certificate identity after the `https://github.com/` prefix is added. |
| `release-app-client-id` | string | Yes | None | Client ID used to mint the Release App installation token. |
| `publish-release` | boolean | No | `true` | Whether to change the populated draft to a non-draft release after verification. |
| `require-oci-image` | boolean | No | `false` | Whether public GitHub Release publication requires a validated digest-pinned GHCR image reference for the caller repository. |
| `oci-image-reference` | string | No | Empty | `ghcr.io/<lowercase-owner>/<lowercase-repository>@sha256:<digest>` returned by the successful OCI publisher. |

| Secret | Required | Value |
| --- | --- | --- |
| `release-app-private-key` | Yes | Private key used with `release-app-client-id` to mint the Release App installation token. |

| Output | Value |
| --- | --- |
| `attestation-url` | URL returned by the GitHub build-provenance attestation step. |
| `release-url` | HTML URL of the populated release, whether it remains a draft or is published. |

The publisher job requires these caller permissions:

| Permission | Access | Use |
| --- | --- | --- |
| `actions` | `read` | Read and download the authoritative Actions artifact. |
| `artifact-metadata` | `write` | Write metadata used by GitHub artifact attestations. |
| `attestations` | `write` | Create GitHub build-provenance attestations. |
| `contents` | `read` | Check out the consumer repository at the tag. Draft and release operations use the App token instead. |
| `id-token` | `write` | Obtain the OIDC identity for GitHub build-provenance attestations. |

The workflow runs on `ubuntu-24.04` with a 10-minute timeout. The reusable workflow declares `permissions: {}` at workflow scope; the caller must grant the job permissions explicitly.

After the tag gate, checkout, tool setup, and Release App token step, the publisher's relevant sequence is:

1. If `cli-path` is nonempty, place the same-run dogfood binary at that path.
2. Run `setup-release-cli` from the same pinned release revision.
3. Run `release-cli verify handoff --artifact-id <n> --digest <sha256:...>`.
4. Find the matching draft release and verify its tag.
5. Download the artifact with the SHA-pinned `actions/download-artifact` step and `digest-mismatch: error`.

## Versioning and credentials

The current versioning workflow runs Release Please on pushes to `main` and on `workflow_dispatch`. It declares `permissions: {}` at workflow scope. Its job declares `contents: write`, `pull-requests: write`, and `issues: write`, then passes a Release App installation token to `googleapis/release-please-action`.

The supported Release Please configuration has these release-boundary values:

| Setting | Current value | Contract effect |
| --- | --- | --- |
| `release-type` | `go` | Applies Release Please's Go versioning strategy. |
| Manifest version | `0.0.0` | Records that no release has been published. |
| `initial-version` | `0.1.0` | Selects the first proposed release version. |
| `include-v-in-tag` | `true` | Produces tags accepted by the caller's `v*` filter. |
| `include-component-in-tag` | `false` | Produces an unscoped version tag. |
| `force-tag-creation` | `true` | Creates the release tag when the release is cut. |
| `draft` | `true` | Creates the draft required by the publisher. |
| `bump-minor-pre-major` | `true` | Uses a minor bump for pre-1.0 features. |
| `bump-patch-for-minor-pre-major` | `true` | Uses a patch bump for pre-1.0 fixes. |

The initial version and pre-1.0 bump rules are current versioning policy, not reusable-workflow defaults. The publisher's hard requirement is a matching draft and tag; it does not calculate a version or create either object.

Both versioning and publication use these organization-level credential identifiers:

- Variable: `MEIGMA_RELEASE_APP_CLIENT_ID`
- Secret: `MEIGMA_RELEASE_APP_PRIVATE_KEY`

The Meigma Release GitHub App must be installed on the consumer repository. Release publication requests an installation token with `contents: write`. Release Please also uses the App to update release pull requests and create the draft release and tag. If a repository protects `v*` tags, its rules must allow this App to bypass tag-creation restrictions. The App-created tag is the event that starts the release caller.

## Tag and draft invariants

The publisher proceeds only when all of these conditions hold:

- `github.ref_type` is `tag`.
- The artifact ID is a positive safe integer.
- The artifact has not expired.
- The artifact belongs to the current workflow run.
- The artifact's GitHub-reported digest matches `artifact-digest`.
- A GitHub Release exists whose `tag_name` equals `github.ref_name`.
- The matching release is still a draft.
- `git rev-list -n 1 <tag>` equals `github.sha` for the run.
- Before upload, the tag resolves uniquely to the previously selected release ID.

The publisher polls the release list up to 24 times, waiting five seconds after an unsuccessful lookup. It fails instead of creating a missing draft. It also fails if the release is already published or if the tag resolves to a different commit than the run.

## Repository and toolchain contract

The producer checks out the consumer repository with full history and runs GoReleaser in that repository. The consumer therefore supplies the Go module, command source, `.goreleaser.yaml`, `mise.toml`, and `mise.lock` used for its build.

The repository must declare and lock these mise tool identifiers:

- `go`
- `aqua:goreleaser/goreleaser`
- `aqua:anchore/syft`
- `aqua:sigstore/cosign`
- `aqua:cli/cli`

The producer installs the first four tools. The publisher installs GitHub CLI and Cosign. Both workflows set `MISE_EXEC_AUTO_INSTALL=false` and invoke their managed tools through `mise exec`; undeclared tools are not installed as a fallback. The producer also sets `GOTOOLCHAIN=local` and verifies that `go`, `goreleaser`, `syft`, and `cosign` resolve to their mise-managed executables. The setup action separately requires the runner's `gh` command with attestation support and fails closed if either is unavailable.

The canonical workflows install mise `2026.8.8`. These repository pins are the current known-compatible baseline, not versions selected automatically by the reusable workflows:

| Tool | Current repository pin |
| --- | --- |
| Go | `1.26.6` |
| GoReleaser | `2.17.1` |
| Syft | `1.51.0` |
| Cosign | `3.1.3` |
| GitHub CLI | `2.97.0` |

The lock must contain entries that mise can install on the `ubuntu-24.04` runner. The workflows use the versions selected by the consumer repository's locked mise configuration.

## GoReleaser contract

The reusable producer runs this command in the consumer repository:

```text
goreleaser release --clean --skip=publish
```

A compatible `.goreleaser.yaml` uses schema version 2 and writes the release bundle under `dist`. The supported Go profile has these requirements:

- Build Darwin, Linux, and Windows binaries for `amd64` and `arm64` with `CGO_ENABLED=0`.
- Package Darwin and Linux binaries as `tar.gz`; package Windows binaries as `zip`.
- Name archives `<project>_<version>_<os>_<arch>` before the format extension.
- Build with `-trimpath` and linker flags `-s -w -buildid=`.
- Populate `main.version` from `{{ .Version }}` and `main.commit` from `{{ .FullCommit }}`.
- Set `mod_timestamp` to `{{ .CommitTimestamp }}`.
- Use GoReleaser's module-proxy mode and the local Go toolchain. The current profile sets `GOPROXY=https://proxy.golang.org,direct` and `GOSUMDB=sum.golang.org` for module resolution.
- Emit one archive SBOM per archive through GoReleaser's `artifacts: archive` SBOM configuration.
- Write the SHA-256 manifest as `checksums.txt`.
- Sign `checksums.txt` with `cosign sign-blob --bundle=${signature} ${artifact} --yes` and name the bundle `checksums.txt.sigstore.json`.
- Disable GoReleaser changelog generation and the GoReleaser release pipe. Release Please owns release notes and the draft; the reusable publisher owns asset upload and publication.

The command also supplies `--skip=publish`. `release.disable: true` is the repository requirement; the command-line skip is a second boundary against GoReleaser publication.

The project name, command path, and binary name are consumer values. The [copyable example](../../examples/go-release/) uses `example`, `./cmd/example`, and `example`. They are not inputs to the reusable workflow.

This repository's own project and binary name is `release-cli`, so its released
archive names start with `release-cli_`; for example,
`release-cli_<version>_linux_amd64.tar.gz`. Consumer repositories continue to
use their own project and binary names.

## Authoritative artifact and asset contract

The producer uploads one Actions artifact named `release-assets`. Its upload set is limited to:

```text
dist/*.tar.gz
dist/*.zip
dist/*.sbom.json
dist/checksums.txt
dist/checksums.txt.sigstore.json
```

For the supported three-operating-system, two-architecture Go profile, this is six archives, six archive SBOMs, `checksums.txt`, and `checksums.txt.sigstore.json`: fourteen files in total.

Before upload, the producer obtains `release-cli` through the shared setup action and runs `release-cli stage --profile go --dist dist`. The command verifies every payload listed in `checksums.txt`, requires a nonempty regular `checksums.txt.sigstore.json`, and verifies the two canonical Linux binaries described in the [`release-cli` contract](release-cli-contract.md).

The publisher's artifact handoff has three independent owners:

1. `release-cli verify handoff` verifies the GitHub API metadata tuple before download: the artifact exists, belongs to the current workflow run, has not expired, and has a GitHub-reported digest that matches the caller-supplied digest after normalization.
2. The SHA-pinned `actions/download-artifact` step, configured with `digest-mismatch: error`, verifies the transport digest of the artifact ZIP.
3. Later `release-cli` content commands verify the extracted content.

`release-cli verify handoff` does not download the artifact and never reproduces the Actions ZIP digest.

`checksums.txt` is the authoritative payload list. It may end with a newline; every entry line must contain a 64-digit hexadecimal SHA-256 digest, a standard text or binary marker, and a flat filename matching this character set:

```text
[A-Za-z0-9][A-Za-z0-9._+-]*
```

The bundle verifier enforces these rules:

- The manifest contains at least one payload.
- Every payload name is unique.
- Payloads are regular files. Directories and symbolic links are rejected.
- Every listed payload exists and matches its recorded SHA-256 digest.
- `checksums.txt` and `checksums.txt.sigstore.json` are control files and cannot list themselves as payloads.
- The downloaded `dist` directory contains exactly the listed payloads and the two control files. Any other entry is rejected.

The publisher uploads the listed payloads plus the two control files. The GitHub Release must end with exactly that closed name set. Duplicate names, missing names, unexpected names, non-uploaded asset states, missing GitHub digests, or digest differences cause failure.

GitHub build-provenance attestations use `dist/checksums.txt` as `subject-checksums`. The checksummed archives and SBOMs are attestation subjects. The checksum manifest and its Cosign bundle are uploaded control files, not entries in their own manifest.

## Trust identities

The checksum signature is accepted only when Cosign verifies all of the following:

| Field | Required value |
| --- | --- |
| Certificate identity | `https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@FULL_SHA` |
| Certificate OIDC issuer | `https://token.actions.githubusercontent.com` |
| Signed blob | `checksums.txt` |
| Bundle | `checksums.txt.sigstore.json` |

The exact identity comes from `checksum-signing-workflow-ref`; a branch name, tag name, different commit, or different workflow path does not satisfy the documented identity.

The publisher at `meigma/release/.github/workflows/publish-github-release.yml@FULL_SHA` creates GitHub build-provenance attestations in the consumer repository. Its job token creates attestations but has only `contents: read`. A short-lived Release App installation token with `contents: write` performs draft discovery, asset upload, and the final draft-state change.

## Publication states

| State | Entry condition | Workflow behavior | Exit condition |
| --- | --- | --- | --- |
| Version prepared | Release Please runs on `main` or by manual dispatch. | Release Please updates its release pull request according to the manifest configuration. | The release change reaches `main`. |
| Draft created | Release Please cuts the version through the Release App. | Release Please creates the `v*` tag and matching draft release. | The App-created tag starts the release caller. |
| Artifact built | The producer runs on the tag. | GoReleaser builds once in that run, creates SBOMs and checksums, signs the checksum manifest, and uploads `release-assets`. | The artifact ID and digest pass to the publisher in the same workflow run. |
| Draft populated | The publisher validates the artifact, signature, tag, and draft. | It attests the checksummed payloads, uploads the closed asset set, and verifies every GitHub-reported asset digest. | Asset names, states, and digests match the signed bundle. |
| Rehearsal complete | `publish-release` is `false`. | The publisher verifies that the release remains a draft. | The populated draft is available for inspection or a later recovery run. |
| Published | `publish-release` is `true`, asset verification succeeds, and any required digest-pinned OCI image reference is valid. | The publisher sets `draft: false` and verifies the resulting state. | The same release URL identifies a non-draft GitHub Release. |

The publisher does not create a release, generate release notes, change a tag, or upload an asset before validating the signed bundle. It does not set `draft: false` until the uploaded asset name and digest sets match the bundle. When `require-oci-image` is `true`, it also requires the successful OCI publisher's exact `ghcr.io/<owner>/<repository>@sha256:<digest>` output before any release mutation.

## Retry and recovery behavior

`verify handoff` uses the [`release-cli` metadata request retry policy](release-cli-contract.md#metadata-request-retries). That policy preserves the `retries: 3` behavior of the replaced artifact metadata block.

The remaining draft lookup, upload, and final verification steps request three retries from `actions/github-script` for retryable API failures. Draft discovery additionally polls up to 24 times at five-second intervals. Final asset inspection polls up to 12 times at one-second intervals for every expected asset to report an `uploaded` state and a digest.

A failed run does not roll back uploaded assets or delete the draft. Recovery is convergent while the release remains a draft:

- The publisher accepts only an unexpired artifact whose workflow run ID equals `github.run_id`; an artifact from another run cannot be supplied. A new tag-triggered run builds and signs its own artifact.
- Existing assets whose names are in the newly verified manifest may be replaced because upload uses `gh release upload --clobber`.
- Existing assets whose names are outside the newly verified manifest block upload. The workflow does not delete them automatically.
- After upload, the workflow verifies the complete name set and every GitHub-computed SHA-256 digest before it can publish.
- The tag must still resolve to `github.sha`, and the release for that tag must still be a draft.

A complete draft rehearsal sets both `publish-image: false` and `publish-release: false`. To resume, the caller changes both inputs to `true`, commits that change, and uses authorized movement of the same unpublished tag name to trigger a new run against the existing populated draft; it does not delete and recreate the draft. The workflow replaces expected assets only after the new artifact, checksums, and Cosign bundle pass validation. Any source, workflow configuration, or tool-pin correction follows the same commit and tag-movement requirement. If the unpublished tag cannot be moved safely, the incomplete candidate must be abandoned and a new candidate cut. A plain Actions rerun is reserved for failures that require no repository-content change, such as artifact expiry or a transient service failure.

If the final API call has already changed the release to non-draft before a later failure, the workflow provides no rollback. A subsequent run rejects that published release because the draft invariant no longer holds.

## Non-goals

This contract does not provide or imply:

- OCI construction or publication behavior beyond the dependency ordering defined here; see the separate [OCI image contract](oci-image-contract.md).
- Homebrew, MacPorts, Nix, Scoop, mise registry, or other package-manager publication.
- DEB, RPM, APK, package-repository, or installer publication.
- Release support for languages other than the documented Go producer profile.
- Consumer CI policy or tests in the OIDC-enabled release job.
- Release-note generation in GoReleaser.
- Automatic creation of a missing draft, deletion of unexpected assets, or rollback after publication.
- Repository ruleset, immutable-release, branch-protection, or credential provisioning automation.
- Automatic adoption by existing repositories.
