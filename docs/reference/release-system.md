# Release system reference

This reference defines the supported reusable workflow, repository, artifact,
signer, and publication contracts. For setup procedures, see
[Adopt the release workflows](../how-to/adopt-the-release-workflows.md). For
failure handling, see [Operate and recover releases](../how-to/operate-and-recover-releases.md).

## Support matrix

| Domain | Supported contract |
| --- | --- |
| Application layout | One Go repository, one unscoped tag stream, and one GHCR image. Linux `amd64` and `arm64` must publish the same nonempty set of static binary names. |
| Source tags | Stable, unscoped `vMAJOR.MINOR.PATCH`. |
| Binary operating systems | Darwin, Linux, and Windows. |
| Binary architectures | `amd64` and `arm64`. |
| Linkage | Static binaries; the canonical Linux binaries must be static ELF executables. |
| GitHub Release | Archives, DEB/RPM/APK packages, archive/package SBOMs, checksum manifest, and Cosign bundle. |
| OCI image | Linux `amd64` and `arm64` at `ghcr.io/<lowercase-owner>/<lowercase-repository>`. |
| Homebrew | One cask in an adopter-owned `homebrew-<name>` tap. |
| Scoop | One manifest at the root of an adopter-owned bucket. |
| Native repository | One `stable` channel, `amd64` and `arm64`, DEB/RPM/APK, one Cloudflare R2 bucket. |

## Release unit and revision invariant

The reusable workflows, `.github/actions/setup-release-cli`, and `release-cli`
form one release unit. An external caller pins each `uses:` reference to one
reviewed, full 40-character commit SHA in `meigma/release`. The same SHA appears
in every `checksum-signing-workflow-ref` and in each native package policy
`checksum_identity` for that producer.

A consumer does not select a separate composite-action revision or CLI version.
The pinned workflow loads its sibling setup action, whose release stamp selects
and verifies the matching CLI. Moving branches, tags, abbreviated SHAs, and
mixed release-unit revisions are unsupported.

`REPLACE_WITH_RELEASE_COMMIT_SHA` in the maintained example is a template value.
The caller is not ready to run until every occurrence is replaced by one full
lowercase SHA.

## Version and tag grammar

The source ref must match:

```text
^refs/tags/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$
```

The version has three unsigned decimal components without leading zeroes,
prerelease suffix, build metadata, or component prefix. Release Please uses an
unscoped `v` tag and creates the initial draft. The publishers do not calculate
a version, create a tag, create a release, or generate notes.

For `v1.2.3`, OCI publication evaluates:

| Tag | Contract |
| --- | --- |
| `1.2.3` | Immutable exact version. An existing different digest fails before upload. |
| `1.2` | Advances only to a newer stable version in the `1.2` line. |
| `1` | Advances only to a newer stable version in the `1` line. |
| `latest` | Advances only to a newer stable version. |

An out-of-order release always publishes an available exact tag and leaves any
newer channel unchanged. Equal versions on different digests are corrupt state.

## Workflow graph

```text
release-please.yml
  -> vMAJOR.MINOR.PATCH + draft
  -> go-pre-publish.yml
       -> release-assets
       -> oci-build-inputs
  -> go-oci-build.yml
       -> oci-image
  -> publish-oci-image.yml
       -> digest-pinned image
  -> publish-github-release.yml
       -> public release
  -> publish-homebrew.yml       (optional, independent PR)
  -> publish-scoop.yml          (optional, independent PR)
  -> request-package-repository.yml
       -> adopter-owned central repository_dispatch
       -> publish-package-repository.yml
```

`publish-github-release.yml` depends on OCI publication in the maintained
caller. Homebrew, Scoop, and native dispatch depend on the GitHub Release job.
The supported caller enables those destinations only when `publish-release` is
also enabled. The native receiver independently rejects a nonpublic release;
the Homebrew and Scoop publishers do not check public release state.

Except for `publish-oci-image.yml`, each reusable workflow declares
`permissions: {}` at workflow scope. The OCI publisher declares
`artifact-metadata: read` there for attestation subject discovery. The calling
job always supplies the maximum token permissions; a callee cannot elevate
above that ceiling.

## Caller permission ceilings

| Called workflow | Required caller permissions |
| --- | --- |
| `go-pre-publish.yml` | `attestations: read`, `contents: read`, `id-token: write` |
| `go-oci-build.yml` | `actions: read`, `attestations: read`, `contents: read` |
| `publish-oci-image.yml` | `actions: read`, `artifact-metadata: write`, `attestations: write`, `contents: read`, `id-token: write`, `packages: write` |
| `publish-github-release.yml` | `actions: read`, `artifact-metadata: write`, `attestations: write`, `contents: read`, `id-token: write` |
| `publish-homebrew.yml` | `actions: read`, `attestations: read`, `contents: read` |
| `publish-scoop.yml` | `actions: read`, `attestations: read`, `contents: read` |
| `request-package-repository.yml` | `{}` |
| `publish-package-repository.yml` | `attestations: read`, `contents: read` |

The Release Please job requires `contents: write`, `issues: write`, and
`pull-requests: write`. It performs mutations with an adopter-owned App token.

## Reusable workflow interfaces

### `go-pre-publish.yml`

The producer runs on `ubuntu-24.04` with a 30-minute timeout.

Inputs:

| Input | Type | Required | Default | Contract |
| --- | --- | --- | --- | --- |
| `sign-and-notarize-macos` | boolean | No | `false` | Enable the producer's guarded GoReleaser macOS signing and notarization block. |
| `sign-native-packages` | boolean | No | `false` | Sign RPM and APK packages before checksum generation. |

Optional secrets become required when their input is enabled:

| Input | Required secrets |
| --- | --- |
| macOS signing | `macos-sign-p12`, `macos-sign-password`, `macos-notary-key`, `macos-notary-key-id`, `macos-notary-issuer-id` |
| Native signing | `rpm-signing-key`, `rpm-signing-passphrase`, `apk-signing-key`, `apk-signing-passphrase` |

The private key values are base64 encoded. Native keys are materialized as
owner-only files under `RUNNER_TEMP` immediately before staging and removed
afterward, including on a failed stage.

Outputs:

| Output | Contract |
| --- | --- |
| `artifact-id` | Numeric ID of `release-assets`. |
| `artifact-url` | GitHub Actions artifact URL for `release-assets`. |
| `artifact-digest` | GitHub SHA-256 transport digest for `release-assets`. |
| `oci-input-artifact-id` | Numeric ID of `oci-build-inputs`. |
| `oci-input-artifact-url` | GitHub Actions artifact URL for `oci-build-inputs`. |
| `oci-input-artifact-digest` | GitHub SHA-256 transport digest for `oci-build-inputs`. |

The workflow installs the producer's locked Go, GoReleaser, Syft, and Cosign,
sets up the release-unit CLI, and runs `release-cli stage --profile go --dist
dist`. It retains both artifacts for seven days without additional ZIP
compression.

### `go-oci-build.yml`

The OCI builder runs on `ubuntu-24.04` with a 20-minute timeout.

| Input | Type | Required | Default |
| --- | --- | --- | --- |
| `artifact-id` | string | Yes | None |
| `artifact-digest` | string | Yes | None |
| `melange-config` | string | No | `melange.yaml` |
| `apko-config` | string | No | `apko.yaml` |

| Output | Contract |
| --- | --- |
| `artifact-id` | Numeric ID of `oci-image`. |
| `artifact-url` | GitHub Actions artifact URL for `oci-image`. |
| `artifact-digest` | GitHub SHA-256 transport digest for `oci-image`. |
| `image-digest` | SHA-256 digest of the exact `layout/index.json` bytes. |

The builder downloads only the projected canonical Linux binaries, verifies
that handoff, uses Melange and apko from the consumer's lock, verifies the
resulting layout and SBOMs, and uploads `oci-image` for seven days. It has no
registry or release credential.

### `publish-oci-image.yml`

The OCI publisher runs on `ubuntu-24.04` with a 15-minute timeout and a
repository-wide concurrency group.

| Input | Type | Required | Default |
| --- | --- | --- | --- |
| `artifact-id` | string | Yes | None |
| `artifact-digest` | string | Yes | None |
| `image-digest` | string | Yes | None |
| `publish-image` | boolean | No | `false` |

| Output | Contract |
| --- | --- |
| `image-name` | `ghcr.io/<lowercase-owner>/<lowercase-repository>` |
| `image-reference` | Digest-pinned image reference; empty when disabled. |
| `image-digest` | Verified index digest in either mode. |
| `provenance-attestation-url` | GitHub provenance URL; empty when disabled. |
| `amd64-sbom-attestation-url` | GitHub amd64 SBOM URL; empty when disabled. |
| `arm64-sbom-attestation-url` | GitHub arm64 SBOM URL; empty when disabled. |

Disabled publication validates the artifact and runs OCI preparation in dry-run
mode without logging in, pushing, signing, attesting, or applying tags. Enabled
publication prepares and recursively signs the digest, creates one index
provenance and two platform SBOM attestations through `actions/attest`, and then
finalizes tags from fresh registry state.

### `publish-github-release.yml`

The GitHub publisher runs on `ubuntu-24.04` with a 10-minute timeout.

| Input | Type | Required | Default | Contract |
| --- | --- | --- | --- | --- |
| `artifact-id` | string | Yes | None | ID from `go-pre-publish.yml`. |
| `artifact-digest` | string | Yes | None | Expected Actions artifact digest. |
| `checksum-signing-workflow-ref` | string | Yes | None | `owner/repository/.github/workflows/file@revision`; the workflow adds `https://github.com/`. |
| `release-app-client-id` | string | Yes | None | Adopter-owned App client ID. |
| `publish-release` | boolean | No | `true` | Make the verified draft public. |
| `require-oci-image` | boolean | No | `false` | Require a successful digest-pinned caller image before publication. |
| `oci-image-reference` | string | No | Empty | Required `ghcr.io/<caller>@sha256:<digest>` when the preceding condition applies. |

Secret:

| Secret | Required | Contract |
| --- | --- | --- |
| `release-app-private-key` | Yes | Adopter-owned App private key used to mint a short-lived `contents: write` token. |

Outputs:

| Output | Contract |
| --- | --- |
| `attestation-url` | GitHub build-provenance attestation URL. |
| `release-url` | URL of the populated draft or public release. |

The publisher verifies the artifact metadata and download digest, removes the
two package-manager controls, verifies the closed bundle and exact Cosign
identity, attests subjects from `checksums.txt`, and then reconciles the matching
draft. With `publish-release: false`, it keeps and verifies draft state. With
publication enabled, undrafting is its last mutation.

### `publish-homebrew.yml`

| Input | Type | Required | Default |
| --- | --- | --- | --- |
| `artifact-id` | string | Yes | None |
| `artifact-digest` | string | Yes | None |
| `checksum-signing-workflow-ref` | string | Yes | None |
| `tap` | string | No | Empty |
| `cask` | string | No | Empty |
| `release-app-client-id` | string | No | Empty |
| `publish-homebrew` | boolean | No | `false` |

`release-app-private-key` is optional at interface level and required only when
publication is enabled. A disabled call skips before validation, token creation,
or a tap request.

Outputs are `branch`, `pull-request-url`, and `state`. State is `created`,
`open`, or `published`. The deterministic branch is
`release/<cask>/v<version>`. The workflow accepts exactly one
`homebrew/Casks/<cask>.rb` control, verifies the underlying signed release
bundle, mints a tap-scoped App token, and opens or reconciles a pull request. It
never writes the default branch, force-updates, merges, or enables auto-merge.

### `publish-scoop.yml`

| Input | Type | Required | Default |
| --- | --- | --- | --- |
| `artifact-id` | string | Yes | None |
| `artifact-digest` | string | Yes | None |
| `checksum-signing-workflow-ref` | string | Yes | None |
| `bucket` | string | No | Empty |
| `manifest` | string | No | Empty |
| `release-app-client-id` | string | No | Empty |
| `publish-scoop` | boolean | No | `false` |

`release-app-private-key` is conditional in the same way as the Homebrew
secret. Outputs are `branch`, `pull-request-url`, and `state`, with the same
three states. The deterministic branch is
`release/<manifest>/v<version>`. The workflow accepts exactly one
`scoop/<manifest>.json` control and writes `<manifest>.json` at the bucket root
through a reviewed pull request.

### `request-package-repository.yml`

| Input | Type | Required | Default |
| --- | --- | --- | --- |
| `package-repository-owner` | string | Yes | None |
| `package-repository-name` | string | Yes | None |
| `release-app-client-id` | string | No | Empty |
| `publish-package-repository` | boolean | No | `false` |

`release-app-private-key` is required only when enabled. The workflow mints a
token scoped to the named central repository and sends a `package-release`
`repository_dispatch` containing only the producer `owner/name` and exact tag.
It has no outputs and receives no R2 or aggregate signing credential.

### `publish-package-repository.yml`

The central receiver runs on `ubuntu-24.04` with a 45-minute timeout, selects
environment `packages-production`, and serializes every production write.

This reusable workflow is valid only when the central repository belongs to
the same organization as this repository. GitHub does not deliver a caller's
environment secrets to a reusable workflow owned by another organization, so a
cross-organization call sees every environment secret below as empty.
Cross-organization central repositories run the equivalent local receiver
documented in
[Operate a native package repository](../how-to/operate-a-native-package-repository.md).

| Input | Type | Required | Default |
| --- | --- | --- | --- |
| `repository` | string | Yes | None |
| `tag` | string | Yes | None |
| `config-path` | string | No | `.config/package-repository.yaml` |
| `keys-path` | string | No | `.config` |
| `cloudflare-account-id` | string | Yes | None |
| `r2-bucket` | string | Yes | None |

The selected environment defines:

- `R2_ACCESS_KEY_ID`;
- `R2_SECRET_ACCESS_KEY`;
- `PACKAGE_REPOSITORY_GPG_PRIVATE_KEY`;
- `PACKAGE_REPOSITORY_GPG_PASSPHRASE`; and
- `PACKAGE_REPOSITORY_APK_PRIVATE_KEY`.

The receiver checks out the caller's policy and public keys, builds the CLI from
the reusable workflow source, materializes aggregate signing keys, and performs
one convergent repository publication.

## Producer repository contract

The producer supplies:

- one Go module and one or more commands that share that module;
- `.goreleaser.yaml` schema version 2;
- `mise.toml` and `mise.lock` with Go, GoReleaser, Syft, Cosign, GitHub CLI,
  Melange, and apko;
- `melange.yaml` and `apko.yaml`;
- Release Please config and manifest; and
- the two caller workflows.

The Go profile invokes:

```text
goreleaser release --clean --skip=publish
```

A compatible GoReleaser configuration:

- builds static Darwin, Linux, and Windows binaries on `amd64` and `arm64`;
- archives Darwin and Linux as `tar.gz` and Windows as ZIP;
- reuses each canonical Linux binary for DEB, RPM, and APK;
- emits archive and package SBOMs;
- writes `checksums.txt` and `checksums.txt.sigstore.json`;
- disables GoReleaser changelog and release publication;
- uses `skip_upload: true` for Homebrew and Scoop controls; and
- keeps nFPM ID `release` when optional native signing is enabled.

Staging selects every `linux/{amd64,arm64}` GoReleaser Binary record. It
rejects a duplicate `(arch, name)` pair and requires the name set to be
identical and nonempty on both architectures. A name present on only one
architecture is an error that names it. The `release.dev/oci-build-inputs/v2`
projection lists those binaries in platform-major order (`linux/amd64` before
`linux/arm64`), then name ascending within a platform.

Melange packages the projected files for `x86_64` and `aarch64` without
compiling them. Each staged file is named for its GoReleaser binary, not
`application`. apko composes one index for `amd64` and `arm64`, runs as numeric
user and group `65532`, and requires each platform config Entrypoint to be
exactly `/usr/bin/<name>` for one staged binary name. The same name is required
on every platform. Source, version, revision, title, description, and license
annotations remain required.

## Actions artifacts and public assets

`release-assets` includes:

```text
dist/*.tar.gz
dist/*.zip
dist/*.deb
dist/*.rpm
dist/*.apk
dist/*.sbom.json
dist/checksums.txt
dist/checksums.txt.sigstore.json
dist/homebrew/Casks/*.rb
dist/scoop/*.json
```

For one cask and one Scoop manifest, the maintained example has 28 Actions
artifact files. The two package-manager controls are not entries in
`checksums.txt`, attestation subjects, or GitHub Release assets.

The public GitHub Release contains 26 files:

- six platform archives;
- six native packages;
- twelve SBOMs;
- `checksums.txt`; and
- `checksums.txt.sigstore.json`.

Every checksum entry is a flat, unique filename matching
`[A-Za-z0-9][A-Za-z0-9._+-]*`. The manifest cannot name either control file.
The extracted release directory must contain exactly the checksummed payloads
and the two controls. Symlinks, directories, irregular files, missing payloads,
unexpected entries, and digest mismatches fail closed.

GitHub attestation subjects are the 24 payloads listed in `checksums.txt`. The
manifest and Cosign bundle are distribution controls, not subjects.

`oci-build-inputs` contains `artifacts.json`, `oci-build-inputs.json`, and the
two canonical binary trees. `oci-image` contains the locked apko configuration,
signed ephemeral APK repositories and public key, OCI layout,
`image-digest.txt`, and one SPDX SBOM per architecture. The ephemeral Melange
private key is not uploaded.

## Signer identities

Checksum verification requires:

| Field | Required value |
| --- | --- |
| Certificate identity | `https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@<full-release-unit-sha>` for external consumers. |
| OIDC issuer | `https://token.actions.githubusercontent.com` |
| Signed blob | `checksums.txt` |
| Bundle | `checksums.txt.sigstore.json` |

The maintained public GitHub Release and OCI attestation verification commands
constrain the reusable signer workflow, its full signer digest, the producer
repository, `refs/tags/<stable-tag>`, and GitHub-hosted runners. They do not pass
a source-digest constraint.

Native package attestation verification instead constrains the reusable signer
workflow, producer repository, tag ref, resolved producer commit, and
GitHub-hosted runners. The native policy has no signer-digest field; its
`checksum_identity` separately pins the checksum signer to the full release-unit
SHA.

The setup action verifies a released `release-cli` archive against the
`meigma/release` repository, GitHub publisher workflow path, and GitHub-hosted
runner. It also verifies the selected release checksum and the binary's version
and protocol stamps, but does not pass signer-digest, source-ref, or
source-digest constraints.

OCI signatures constrain the exact reusable OCI publisher identity and OIDC
issuer. OCI GitHub attestations exist in the producer's attestation store and as
registry referrers. A Cosign signature and a GitHub attestation are separate
requirements.

## Publication contracts and states

### GitHub Release

The publisher requires the tag to resolve to the workflow commit and exactly one
release to carry that tag. The normal mutation path begins from a draft. It
refuses an existing asset outside the expected closed set, uploads expected
names with clobber semantics, waits for GitHub's uploaded state and SHA-256
digest, and makes the draft public last.

| State | Result |
| --- | --- |
| Draft missing or duplicated | Failure; no release is created. |
| Draft-only success | Expected assets converge and the release remains draft. |
| Publish success | Expected assets converge, then the same release becomes public. |
| Public exact match during publish-enabled retry | Success without mutation. |
| Any public state during draft-only operation | Indeterminate. |
| Public mismatched asset state | Indeterminate; no re-draft or deletion. |

### OCI image

Preparation validates layout bytes and tag state, pushes by digest, verifies the
pushed manifests, and recursively signs the index without creating a tag.
GitHub creates one provenance and two SBOM attestations. Finalization re-reads
registry state, rejects drift, recomputes the plan, applies tags serially, and
verifies each resolution.

`publish-image: false` performs validation and planning only. `image-reference`
remains empty. Failed prepare or attestation can leave untagged content or
partial trust metadata. Failed finalization can leave a prefix of planned tags,
but every applied candidate tag already names signed and attested content.

GHCR package visibility is independent of source repository visibility. The
supported delivery state is public; an organization owner performs the current
one-time visibility change through the package settings UI when necessary.

### Homebrew and Scoop

Both publishers reconcile one generated control against a deterministic branch.
They accept an existing branch only when it has the observed default head as its
sole parent, changes only the expected path, and contains the exact generated
bytes.

| State | Meaning |
| --- | --- |
| `created` | A new non-draft pull request was opened. |
| `open` | The one matching pull request remains open. |
| `published` | Exact generated bytes are already on the default branch. |

A different control at the same or a newer version, multiple matching pull
requests, a closed unmerged pull request, or unrelated branch changes are
conflicts. The publishers do not merge, approve, auto-merge, force-update, or
delete refs.

### Native repository

A request contains lowercase producer `owner/name` and one stable tag. The
release must be public and closed. Each accepted native package has:

1. a checksum entry and matching GitHub asset digest;
2. a GitHub attestation matching the explicit shared signer, producer, source
   tag, and source commit;
3. policy-matching package name, version, architecture, and format; and
4. a valid producer signature for RPM and APK.

The publisher regenerates the complete repository from the incoming release and
all existing immutable package objects. It installs the exact version locally,
uploads non-root objects before commit roots, then installs from the public
origin.

The commit roots are:

- `apt/dists/stable/InRelease`;
- `rpm/stable/<arch>/repodata/repomd.xml`; and
- `apk/stable/main/<arch>/APKINDEX.tar.gz`.

Packages, public keys, and APT by-hash objects are immutable with one-year cache
headers. Indexes, signatures, and other replaceable metadata use `no-store`.
Publication never deletes an object. Its result state is `published` when it
writes at least one object and `unchanged` when every generated object matches.

## Native package policy schema

The parser accepts one YAML document no larger than 64 KiB. It rejects unknown
fields, YAML aliases, multiple documents, duplicate producers, duplicate
package ownership, duplicate published key names, unsupported channels, and
malformed paths.

Template; replace the origin and revision before use:

```yaml
channel: stable
origin: https://packages.example.com
keys:
  apt:
    source: keys/repository.asc
    published: apt-repository-001.asc
  rpm:
    source: keys/repository.asc
    published: rpm-repository-001.asc
  apk:
    source: keys/repository-apk.rsa.pub
    published: apk-index-001.rsa.pub
producers:
  - repository: acme/widget
    packages:
      - widget
    checksum_identity: https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    attestation_signer: meigma/release/.github/workflows/publish-github-release.yml
    rpm_key:
      source: keys/widget-rpm.asc
      published: widget-rpm-001.asc
    apk_key:
      source: keys/widget-apk.rsa.pub
      published: widget-apk-001.rsa.pub
```

Top-level fields:

| Field | Contract |
| --- | --- |
| `channel` | Exactly `stable`. |
| `origin` | Public absolute HTTPS root without path prefix, query, fragment, or credentials. |
| `keys.apt` | Aggregate APT metadata public key. |
| `keys.rpm` | Aggregate RPM metadata public key; may share the APT source. |
| `keys.apk` | Aggregate APK index public key. |
| `producers` | Nonempty producer and package allowlist. |

Producer fields:

| Field | Contract |
| --- | --- |
| `repository` | Unique lowercase GitHub `owner/name`. |
| `packages` | Nonempty unique package ownership list. |
| `checksum_identity` | Exact immutable GitHub workflow certificate URL whose filename ends in `.yml` or `.yaml` and whose ref is one full lowercase SHA. |
| `attestation_signer` | Exact `owner/repository/.github/workflows/<file>.yml` or `.yaml`, without URL or ref. |
| `rpm_key` | Producer RPM package-signing public key. |
| `apk_key` | Producer APK package-signing public key. |

Each key has a confined slash-separated `source` beneath the keys root and a
unique flat `published` filename beneath public `keys/`.

Public architecture names are:

| Normalized | APT | RPM | APK |
| --- | --- | --- | --- |
| `amd64` | `amd64` | `x86_64` | `x86_64` |
| `arm64` | `arm64` | `aarch64` | `aarch64` |

## Retry classes and manual boundaries

| Operation | Retry or replay contract |
| --- | --- |
| Actions artifact metadata | Four attempts total; waits of 1, 2, and 4 seconds for rate limits and HTTP `5xx`. |
| GitHub API publication operations | Up to four attempts for retryable failures, with 1, 2, and 4 second waits. |
| Missing draft | Up to 24 observations; a 5-second wait follows every miss, including the final exhausted miss. |
| Incomplete GitHub asset set | Up to 12 observations; a 1-second wait follows every incomplete result, including the final exhausted result. |
| Homebrew and Scoop repository operations | Up to four attempts; each write failure is followed by a fresh-state read. |
| OCI registry operations | Preparation retries blob pushes and digest verification. Finalization retries tag commits and postcondition reads. Planning and preparation's initial tag collection do not use that helper. |
| Native repository replay | The release-policy layer has no retry loop. A new invocation converges from current R2 objects: matching immutable objects skip and replaceable metadata regenerates. |

Authentication failures, invalid configuration, absent Actions handoff
artifacts, missing required local inputs, tag/commit mismatches, unexpected
release assets, digest conflicts, immutable OCI tag conflicts, corrupt channel
state, conflicting destination branches, invalid native signatures, and
conflicting immutable R2 objects are not transient retry classes. A missing
draft and incomplete expected asset set use the polling contracts above; an
absent R2 object is uploaded during convergence.

Manual inspection is required after an uncertain undraft, unexpected GitHub
asset, OCI registry drift, conflicting Homebrew/Scoop destination state, or
immutable R2 conflict. A public release has no automated rollback.

## Unsupported cases

The current release system does not support:

- languages other than the Go producer profile;
- more than one application, GHCR image, or image entrypoint per repository;
- monorepo component tags or scoped versions;
- prereleases or build metadata;
- CGO-dependent or dynamically linked commands;
- binary architectures outside Darwin/Linux/Windows `amd64` and `arm64`;
- OCI registries, namespaces, or names other than the caller's GHCR path;
- mutable exact OCI version tags;
- automatic GHCR visibility changes;
- Homebrew formula publication;
- Scoop manifests below a `bucket/` directory;
- direct publisher merges or auto-merge;
- producer access to R2 or aggregate package-repository signing keys;
- package-repository channels other than `stable`;
- object deletion, pruning, or in-place replacement of immutable package paths;
- automatic creation of a missing draft or deletion of an unexpected asset;
- moving a public tag, re-drafting a public release, or rollback after
  publication; or
- automatic repository ruleset, environment, App, key, or credential setup.
