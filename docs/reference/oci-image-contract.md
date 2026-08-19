# OCI image contract

This page defines the cross-repository contract for the reusable Go OCI builder and GHCR publisher at revision `fb8c8098ff27968fb3070e928c00e925f38c698e`.

For adoption steps, see [Configure OCI image publication](../how-to/configure-oci-images.md). The [GitHub Release contract](github-release-contract.md) defines the upstream GoReleaser producer and GitHub Release publisher. A complete consumer is available in the [Go release example](../../examples/go-release/).

## Pipeline boundary

```text
GoReleaser producer
  -> canonical linux/amd64 and linux/arm64 binaries
  -> verified oci-input artifact
  -> Melange signed APK repository
  -> locked apko multi-architecture OCI layout
  -> verified oci-image artifact
  -> release-cli digest-addressed GHCR preparation and recursive Cosign signatures
  -> GitHub and registry provenance/SBOM attestations
  -> release-cli verified tag finalization
  -> public GitHub Release
```

The image builder consumes prebuilt GoReleaser binaries. Melange packages them without compiling, stripping, or otherwise replacing them. The publisher consumes the builder's OCI layout and does not check out or execute consumer repository code.

## Reusable workflows

Consumers call both workflows at the same immutable revision:

```yaml
uses: meigma/release/.github/workflows/go-oci-build.yml@fb8c8098ff27968fb3070e928c00e925f38c698e
```

```yaml
uses: meigma/release/.github/workflows/publish-oci-image.yml@fb8c8098ff27968fb3070e928c00e925f38c698e
```

Moving branches and tags are not supported workflow references.

## Builder interface

`go-oci-build.yml` accepts these inputs:

| Input | Required | Default | Meaning |
| --- | --- | --- | --- |
| `artifact-id` | yes | none | Numeric ID of the canonical Linux binary artifact from `go-pre-publish.yml`. |
| `artifact-digest` | yes | none | GitHub artifact SHA-256 digest for that artifact. |
| `cli-path` | no | empty | Unsupported path to a caller-supplied `release-cli` binary. The caller owns the workflow-to-binary pairing. Normal consumers omit this input. |
| `melange-config` | no | `melange.yaml` | Consumer-relative Melange configuration path. |
| `apko-config` | no | `apko.yaml` | Consumer-relative apko configuration path. |

It returns:

| Output | Meaning |
| --- | --- |
| `artifact-id` | Numeric ID of the authoritative `oci-image` artifact. |
| `artifact-url` | GitHub URL for the authoritative artifact. |
| `artifact-digest` | GitHub artifact SHA-256 digest. This covers the uploaded ZIP transport, not the OCI index. |
| `image-digest` | `sha256:` digest of the exact bytes in `layout/index.json`. |

The caller grants `actions: read` and `contents: read`. The builder has no registry credentials, package write permission, attestation permission, or release credential.

After the tag gate, checkout, mise, QEMU, and tool proof, the builder's relevant sequence is:

1. If `cli-path` is nonempty, place the same-run dogfood binary at that path.
2. Run `setup-release-cli`.
3. Run `release-cli verify handoff --artifact-id <n> --digest <sha256:...>`.
4. Download the canonical Linux binaries with the SHA-pinned `actions/download-artifact` step and `digest-mismatch: error`.

## Publisher interface

`publish-oci-image.yml` accepts these inputs:

| Input | Required | Default | Meaning |
| --- | --- | --- | --- |
| `artifact-id` | yes | none | Numeric ID of the `oci-image` artifact from `go-oci-build.yml`. |
| `artifact-digest` | yes | none | Expected GitHub artifact SHA-256 digest. |
| `cli-path` | no | empty | Unsupported path to a caller-supplied `release-cli` binary. The caller owns the workflow-to-binary pairing. Normal consumers omit this input. |
| `image-digest` | yes | none | Expected OCI index digest. |
| `publish-image` | no | `false` | When `true`, push, sign, and attest the verified image. When `false`, stop after verification. |

It returns:

| Output | Meaning |
| --- | --- |
| `image-name` | Canonical `ghcr.io/owner/repository` name. |
| `image-reference` | Digest-pinned published reference, or empty when publication is disabled. |
| `image-digest` | Verified OCI index digest, regardless of publication mode. |
| `provenance-attestation-url` | GitHub provenance attestation URL, or empty when publication is disabled. |
| `amd64-sbom-attestation-url` | GitHub amd64 SBOM attestation URL, or empty when publication is disabled. |
| `arm64-sbom-attestation-url` | GitHub arm64 SBOM attestation URL, or empty when publication is disabled. |

The caller grants:

```yaml
permissions:
  actions: read
  artifact-metadata: write
  attestations: write
  contents: read
  id-token: write
  packages: write
```

`packages: write` permits the in-memory `release-cli` registry client, Cosign, and registry-backed GitHub attestations to write to GHCR. `id-token: write` supplies short-lived Sigstore identity. The publisher receives no GitHub App key and cannot mutate repository contents or releases.

After the stable-tag gate and tool setup, the publisher's relevant sequence is:

1. If `cli-path` is nonempty, place the same-run dogfood binary at that path.
2. Run `setup-release-cli`.
3. Run `release-cli verify handoff --artifact-id <n> --digest <sha256:...>`.
4. Download the authoritative OCI image with the SHA-pinned `actions/download-artifact` step and `digest-mismatch: error`.
5. Verify the OCI layout contents and expose the image, version, index, and platform values used by later steps.
6. When `publish-image` is `true`, log in to GHCR for Cosign and registry-backed attestations.
7. Run `release-cli publish oci prepare`. Publication runs push and sign the digest-addressed image; verification-only runs add `--dry-run` and make no registry writes. The workflow captures the command's JSON envelope for finalization.
8. When `publish-image` is `true`, run the three SHA-pinned `actions/attest` steps for index provenance and the two platform SBOMs.
9. When `publish-image` is `true`, pipe the captured prepare envelope to `release-cli publish oci finalize --result -`.
10. After a publication attempt, remove the GHCR entry from the Docker configuration even when an earlier publication step failed.

The workflow sets `image-reference` only for a publication run. It remains empty when `publish-image` is `false`.

## Consumer configuration

### GoReleaser

The upstream producer must emit exactly one canonical Linux binary for each pair:

| GOOS | GOARCH | GOAMD64 |
| --- | --- | --- |
| `linux` | `amd64` | `v1` |
| `linux` | `arm64` | unset |

The binaries must be static and executable. The OCI builder rejects a missing target, duplicate target, wrong architecture, symlink, non-executable file, or checksum mismatch.

### Melange

`melange.yaml` must:

- declare `x86_64` and `aarch64` target architectures;
- use `${{vars.version}}` as the package version;
- install the staged file named `application` as the intended command;
- preserve mode `0755` and ownership `0:0`; and
- name the package consumed by `apko.yaml`.

The workflow injects the stable tag version and writes an ephemeral APK signing key. It retains the public key, signed APKs, signed repository indexes, package SBOMs, and Melange provenance in the workflow artifact. The private signing key is never uploaded.

### apko

`apko.yaml` must:

- consume the Melange package;
- define `amd64` and `arm64` only;
- set exactly one executable entrypoint;
- run as numeric user and group `65532` through the `nonroot` account;
- include source, title, description, and SPDX license annotations; and
- include the runtime files the command requires.

The current example includes Alpine's CA certificate bundle. A command that does not make TLS connections may deliberately omit it after testing; a command that needs other runtime data must declare the corresponding package explicitly.

## Authoritative artifact

The builder uploads `oci-image` with seven-day retention and no additional ZIP compression. Its contract includes:

```text
apko.lock.json
apk-signing.rsa.pub
configuration/apko.yaml
configuration/melange.yaml
image-digest.txt
layout/index.json
layout/oci-layout
layout/blobs/sha256/*
packages/aarch64/*
packages/x86_64/*
sboms/sbom-aarch64.spdx.json
sboms/sbom-x86_64.spdx.json
```

The package directories also contain signed APK repository indexes, Melange provenance, embedded package SBOMs, and the signed APKs. Files not listed above may be diagnostic outputs from the pinned tools; consumers must not infer a stable API from undocumented filenames.

Artifact handoff integrity has three independent owners:

1. `release-cli verify handoff` verifies the GitHub API metadata tuple before download: the artifact exists, belongs to the current workflow run, has not expired, and has a GitHub-reported digest that matches the caller-supplied digest after normalization.
2. The SHA-pinned `actions/download-artifact` step, configured with `digest-mismatch: error`, verifies the transport digest of the artifact ZIP.
3. The SHA-pinned `actions/github-script` staging step verifies the extracted OCI content. It requires the recorded, recomputed, and caller-supplied OCI index digests to be identical, one Linux manifest for `amd64`, one for `arm64`, all referenced blobs, and parseable SPDX JSON for each architecture.

`release-cli verify handoff` does not download the artifact and never reproduces the Actions ZIP digest.

Both OCI workflows use the [`release-cli` metadata request retry policy](release-cli-contract.md#metadata-request-retries) for `verify handoff`. That policy preserves the `retries: 3` behavior of each replaced artifact metadata block.

## Published image

### Name

The image name is derived from the caller repository:

```text
ghcr.io/<lowercase-owner>/<lowercase-repository>
```

Custom registry hosts, namespaces, and image names are outside the current contract.

### Tags

A stable release tag `vMAJOR.MINOR.PATCH` publishes:

| Image tag | Behavior |
| --- | --- |
| `MAJOR.MINOR.PATCH` | Immutable exact release version. Publication fails before any registry upload when this tag already resolves to a different digest. |
| `MAJOR.MINOR` | Advances only when the candidate is a greater stable version in the same minor line. |
| `MAJOR` | Advances only when the candidate is a greater stable version in the same major line. |
| `latest` | Advances only when the candidate is greater than its current stable version. |

The exact tag must resolve to the builder's expected OCI index digest after publication. Each eligible channel tag must resolve to that digest; an out-of-order or backport release leaves newer channel tags unchanged. `release-cli publish oci prepare` resolves and validates every existing tag before uploading the image. `release-cli publish oci finalize` re-reads the registry before it applies any tag. A repository-wide publisher concurrency group prevents different release tags from planning and updating channels concurrently. Prerelease, build-metadata, malformed, branch, and untagged refs are rejected.

`release-cli plan tags` evaluates the same exact-tag and channel policy used during publication. It can run independently to inspect the decisions for a candidate release. The publisher does not call this standalone inspection command.

A direct `plan tags` invocation has no repository-wide concurrency lock. Two concurrent planners outside the publisher workflow can observe the same registry state and plan conflicting channel moves. Direct use therefore requires a single writer by convention.

The publisher runs `release-cli publish oci prepare`, the three GitHub attestation actions, and then `release-cli publish oci finalize`. Prepare publishes and signs the digest-addressed image without creating or moving a tag. Finalize compares fresh registry state with the prepare observations, recomputes the tag plan, applies tags serially, and verifies their resolutions.

Trust metadata strictly precedes every public tag. No exact or channel tag is created or moved until recursive signing and all three attestations complete. See [Why OCI publication has two phases](../explanation/two-phase-oci-publication.md) for the security and failure model.

Digest-pinned references are the durable consumer interface:

```text
ghcr.io/owner/repository@sha256:<index-digest>
```

### Runtime invariants

For both platforms, the builder verifies:

- Linux operating system and expected architecture;
- one apko index containing exactly two platform manifests;
- package and image executable bytes equal the canonical GoReleaser binary;
- executable ownership `0:0` inside the image layer;
- executable mode `0755` inside the image layer;
- configured entrypoint;
- runtime user `65532`;
- source, version, revision, title, description, and license annotations; and
- SPDX SBOM inclusion of the application package version.

## Signatures and attestations

The publisher signs the index and both platform manifests with Cosign keyless signing. Verification must require:

| Field | Value |
| --- | --- |
| Certificate identity | `https://github.com/meigma/release/.github/workflows/publish-oci-image.yml@fb8c8098ff27968fb3070e928c00e925f38c698e` |
| Certificate OIDC issuer | `https://token.actions.githubusercontent.com` |
| Subject | Digest-pinned image or platform manifest. |

The publisher also creates:

- one SLSA provenance attestation for the multi-architecture index;
- one SPDX SBOM attestation for the amd64 platform manifest; and
- one SPDX SBOM attestation for the arm64 platform manifest.

Each attestation is written to the consumer repository's GitHub attestation store and pushed to GHCR as an OCI referrer. Verification should constrain the reusable signer workflow, signer revision, consumer source tag, and GitHub-hosted runner as shown in the configuration guide.

Cosign signatures and GitHub attestations are distinct. A passing check for one does not prove the other exists.

## Publication states

| State | Registry effect | Expected next action |
| --- | --- | --- |
| `publish-image: false` | None. The workflow runs `publish oci prepare --dry-run` for layout, digest, state, and tag-plan verification. It applies no tags, and `image-reference` remains empty. | Inspect the rehearsal and enable both publication controls in one reviewed commit. |
| Prepare incomplete | Untagged, digest-addressed blobs or manifests may exist. No release tag has been created or changed. GitHub Release remains draft. | Rerun only failed jobs so the publisher reuses the authoritative artifact from the same workflow run. |
| Signing incomplete | The digest-addressed image may exist, but no release tag has been created or changed. GitHub Release remains draft. | Rerun only failed jobs; do not advertise or make the package public. |
| Attestation incomplete | The digest is signed but does not satisfy the complete contract. No release tag has been created or changed. GitHub Release remains draft. | Rerun only failed jobs and verify every attestation. |
| Finalize incomplete | The digest has complete trust metadata, but a registry failure may have applied only a prefix of the planned tag set. GitHub Release remains draft. | Rerun only failed jobs. Finalize reads fresh registry state, accepts candidate tags already applied, and applies the remaining eligible tags. Investigate any reported drift instead of replaying a saved prepare result. |
| Complete, package private | Authenticated pulls work and planned tags resolve correctly. | Inspect, then perform the one-time public visibility change. |
| Complete, package public | Anonymous digest and tag pulls work. | Monitor and publish only additive corrective releases. |

The publisher plans tags before its first upload, publishes and signs the OCI layout by digest through `publish oci prepare`, creates all three attestations, and applies public tags last through `publish oci finalize`. A failed-job rerun reuses the same authoritative artifact and may add duplicate valid signatures or attestations after a partial success. Finalize reads current registry state rather than replaying the earlier plan. The GitHub Release publisher requires the successful digest-pinned OCI output and cannot make the release public until the image publisher job succeeds.

## GHCR visibility

GHCR visibility is independent of source repository visibility. The organization's package-creation setting determines the initial visibility. The workflow links the package to its source repository through `GITHUB_TOKEN` publication and the `org.opencontainers.image.source` annotation; inherited repository access permissions do not make the package public.

The completed delivery state is public. Inspect `visibility` through the Packages REST API after the first complete publication. If it is private, an organization owner must inspect the signed and attested image, then make the package public through its settings page. GitHub does not expose a supported Packages REST operation for this visibility change.

## Security boundary

The current boundary is deliberately split:

- `go-pre-publish.yml` compiles and signs release inputs without release or package write access;
- `go-oci-build.yml` packages and composes the image without registry credentials;
- `publish-oci-image.yml` does not check out consumer source and writes only to the caller's GHCR package and attestation store; and
- `publish-github-release.yml` waits for image publication but uses a separate short-lived Release App token for release mutation.

The privileged publisher uses `release-cli` for pre-download artifact metadata verification, digest-addressed publication, signing, fresh-state tag planning, serial tag application, and postcondition verification. The SHA-pinned `actions/github-script` staging step verifies the OCI layout, and the three SHA-pinned `actions/attest` steps create trust metadata between prepare and finalize.

The workflow artifact is temporary transport, not a public distribution channel. The OCI digest, registry content, Cosign identity, and attestation identities form the public verification boundary.

## Unsupported cases

The current contract does not support:

- CGO-dependent or dynamically linked commands;
- architectures other than Linux amd64 and arm64;
- prerelease tags;
- multiple commands or entrypoints in one image;
- custom registries or image names;
- private package visibility automation;
- long-lived registry credentials;
- mutable exact-version tags; or
- publication from branch or manual-dispatch refs.
