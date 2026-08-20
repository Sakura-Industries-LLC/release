# Configure OCI image publication

Use this guide to add signed, multi-architecture GHCR images to a repository that already uses the Meigma Go release workflows. The [OCI image contract](../reference/oci-image-contract.md) defines the reusable workflow interfaces, image contents, tags, signatures, attestations, and recovery behavior.

The documented workflow revision is `fb8c8098ff27968fb3070e928c00e925f38c698e`.

## Prerequisites

Before changing the consumer repository, confirm that:

- [Configure GitHub Releases](configure-github-releases.md) is complete;
- the Go command builds as a static Linux binary for both `amd64` and `arm64`;
- GitHub Actions policy permits the pinned Meigma workflows and actions;
- the repository's workflow token policy permits `packages: write`;
- GitHub Packages is enabled for the organization;
- `mise`, Git, GitHub CLI, ORAS, Cosign, and Docker are available for local verification; and
- GitHub CLI is authenticated with package read access.

Record the consumer repository and immutable workflow revision:

```bash
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
export RELEASE_REVISION=fb8c8098ff27968fb3070e928c00e925f38c698e
export IMAGE="ghcr.io/${REPOSITORY,,}"
```

The publisher does not accept a custom image name. A repository named `OWNER/REPOSITORY` publishes `ghcr.io/owner/repository`.

## 1. Copy the image configuration

From a checkout of `meigma/release`, copy the example configuration into the consumer repository. Stop and merge by hand if either destination already exists.

```bash
export CONSUMER=/absolute/path/to/consumer
test ! -e "$CONSUMER/melange.yaml"
test ! -e "$CONSUMER/apko.yaml"
cp examples/go-release/melange.yaml "$CONSUMER/"
cp examples/go-release/apko.yaml "$CONSUMER/"
```

The shared builder runs `release-cli image build` and then `release-cli image verify`. The workflow passes its `melange-config` and `apko-config` inputs to `image build`; their defaults remain `melange.yaml` and `apko.yaml`. `image build` uses Melange to package the canonical GoReleaser Linux binaries as signed APKs and apko to compose one OCI index for `linux/amd64` and `linux/arm64`. It does not compile the command again. `image verify` checks the layout, runtime contract, architecture SBOMs, and exact OCI index bytes before the workflow uploads the authoritative artifact. The workflow delegates these checks to the CLI instead of implementing them in shell.

Ensure the consumer's `mise.toml` and `mise.lock` contain the tool versions required by the target workflow revision. The current workflow pins Melange `0.59.1` and apko `1.2.37`, resolves both executable paths with `mise which`, and supplies them to `release-cli image build` through `RELEASE_MELANGE_PATH` and `RELEASE_APKO_PATH`.

## 2. Set project values

Edit `melange.yaml`:

- replace package name `example` with the command's binary name;
- replace the description;
- replace `LicenseRef-Proprietary` with the repository's SPDX license expression; and
- replace `/usr/bin/example` with the intended image command path.

Keep these contract values:

- `version: ${{vars.version}}`;
- target architectures `x86_64` and `aarch64`;
- the Wolfi repository and keyring;
- installation mode `0755`; and
- installation ownership `0:0`.

Edit `apko.yaml`:

- replace package name `example` with the Melange package name;
- replace `/usr/bin/example` with the installed command path;
- replace the title and description annotations;
- replace `https://github.com/OWNER/REPOSITORY` with the consumer repository URL; and
- replace `LicenseRef-Proprietary` with the repository's SPDX license expression.

Keep these contract values:

- the `nonroot` user and group at ID `65532`;
- `run-as: nonroot`;
- architectures `amd64` and `arm64`;
- the CA certificate package and `SSL_CERT_FILE`; and
- `/usr/bin` in `PATH`.

Do not add a compiler or source build to either configuration. The authoritative executable comes from GoReleaser through the verified `oci-build-inputs` artifact.

## 3. Add the builder and publisher jobs

Use the complete caller in `examples/go-release/.github/workflows/release.yml` as the source. The image path consists of two jobs:

1. `oci-image` calls `go-oci-build.yml` with the `oci-build-inputs` artifact ID and digest exposed by the `release-assets` job.
2. `oci-publish` calls `publish-oci-image.yml` with the authoritative OCI artifact ID, artifact digest, and image index digest from `oci-image`.

The builder job must grant these permissions:

```yaml
permissions:
  actions: read
  attestations: read
  contents: read
```

`attestations: read` is required because the builder installs `release-cli` and
verifies its attestation. A called workflow can never request more than the
calling job grants, so omitting it fails the run before any job starts.

The publisher job must grant only these permissions:

```yaml
permissions:
  actions: read
  artifact-metadata: write
  attestations: write
  contents: read
  id-token: write
  packages: write
```

Pin every reusable workflow to the same full revision:

```text
fb8c8098ff27968fb3070e928c00e925f38c698e
```

Make `github-release` depend on `oci-publish`. That ordering keeps the GitHub Release in draft state when registry publication, signing, or attestation fails.

## 4. Rehearse without registry writes

Keep both publication controls disabled for the first tag rehearsal:

```yaml
publish-image: false
publish-release: false
```

The run still executes `release-cli image build` to build the APK repository and OCI index and to verify the projected canonical binary digests. It then runs `release-cli image verify` to check the layout, runtime contract, architecture SBOMs, and exact index digest before upload. The publisher validates the artifact handoff and runs `release-cli publish oci prepare --dry-run`. The dry run validates the layout, digest, registry state, and tag plan without a registry write. It does not log in to GHCR, create tags, sign an image, create OCI attestations, or publish the GitHub Release. The publisher's `image-reference` output remains empty.

Inspect the `oci-image` workflow artifact. It must contain:

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

Follow [Rehearse and recover GitHub Releases](rehearse-and-recover-github-releases.md) for the tag and draft procedure.

## 5. Publish the image

After the rehearsal passes, change both controls in the same reviewed commit:

```yaml
publish-image: true
publish-release: true
```

Create the next stable `vMAJOR.MINOR.PATCH` release through Release Please. The image publisher rejects non-stable tags.

The publisher runs `release-cli publish oci prepare` to push, verify, and recursively sign the image by digest without creating tags. It then runs the three GitHub attestation actions for index provenance and the two platform SBOMs. After all three attestations succeed, `release-cli publish oci finalize` re-reads registry state, applies eligible tags serially, and verifies their resolutions. See [Why OCI publication has two phases](../explanation/two-phase-oci-publication.md) for why tags are last.

A successful `v1.2.3` run always publishes the immutable exact tag:

```text
1.2.3
```

It also advances `1.2`, `1`, and `latest` when `1.2.3` is newer than each channel's current stable version. An out-of-order or backport release publishes its exact tag and advances only the channels for which it is newer; it never moves a channel backward.

`release-cli publish oci prepare` enforces exact-tag immutability before uploading registry content. If `1.2.3` already resolves to a different digest, preparation fails without writing the candidate image. Consumers that require repeatable deployment must use `ghcr.io/owner/repository@sha256:...`, not a moving tag.

Package visibility follows the organization's package-creation setting; it does not inherit repository visibility. After the first complete publication, inspect the package:

```bash
gh api "orgs/${REPOSITORY%%/*}/packages/container/${REPOSITORY#*/}" --jq .visibility
```

The required delivery state is `public`. If the result is `private`, an organization owner must inspect the signed and attested image, then use the package settings page to change its visibility to **Public**. GitHub does not expose a supported Packages REST operation for this visibility change. Until it is public, anonymous pulls fail.

## 6. Verify the published image

Set the release tag and authenticate ORAS. Authentication is required while the package remains private.

```bash
export TAG=v1.2.3
gh auth token | oras login ghcr.io --username "$(gh api user --jq .login)" --password-stdin
export DIGEST="$(oras resolve "$IMAGE:${TAG#v}")"
test "$DIGEST" = "$(oras resolve "$IMAGE:latest")"
printf 'Image: %s@%s\n' "$IMAGE" "$DIGEST"
```

Verify the keyless Cosign signature against the reusable publisher identity:

```bash
cosign verify \
  --certificate-identity "https://github.com/meigma/release/.github/workflows/publish-oci-image.yml@$RELEASE_REVISION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$IMAGE@$DIGEST"
```

Verify the GitHub provenance attestation from both GitHub and the registry:

```bash
gh attestation verify "oci://$IMAGE@$DIGEST" \
  --repo "$REPOSITORY" \
  --signer-workflow meigma/release/.github/workflows/publish-oci-image.yml \
  --signer-digest "$RELEASE_REVISION" \
  --source-ref "refs/tags/$TAG" \
  --deny-self-hosted-runners

gh attestation verify "oci://$IMAGE@$DIGEST" \
  --repo "$REPOSITORY" \
  --bundle-from-oci \
  --signer-workflow meigma/release/.github/workflows/publish-oci-image.yml \
  --signer-digest "$RELEASE_REVISION" \
  --source-ref "refs/tags/$TAG" \
  --deny-self-hosted-runners
```

Verify each platform signature and SBOM attestation:

```bash
for ARCH in amd64 arm64; do
  PLATFORM_DIGEST="$(
    oras manifest fetch "$IMAGE@$DIGEST" |
      jq -r --arg arch "$ARCH" \
        '.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest'
  )"
  cosign verify \
    --certificate-identity "https://github.com/meigma/release/.github/workflows/publish-oci-image.yml@$RELEASE_REVISION" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$IMAGE@$PLATFORM_DIGEST"
  gh attestation verify "oci://$IMAGE@$PLATFORM_DIGEST" \
    --repo "$REPOSITORY" \
    --bundle-from-oci \
    --predicate-type https://spdx.dev/Document/v2.3 \
    --signer-workflow meigma/release/.github/workflows/publish-oci-image.yml \
    --signer-digest "$RELEASE_REVISION" \
    --source-ref "refs/tags/$TAG" \
    --deny-self-hosted-runners
done
```

Finally, run both published platforms:

```bash
docker run --rm --platform linux/amd64 "$IMAGE@$DIGEST" --version
docker run --rm --platform linux/arm64 "$IMAGE@$DIGEST" --version
```

Both commands must report the release version and commit. Running a non-native platform requires binfmt/QEMU support.

## Recovery

A failed publisher leaves the GitHub Release draft unpublished because `github-release` depends on `oci-publish` and requires its digest-pinned image output. The registry effect depends on the phase that fails:

- A failed prepare can leave untagged, digest-addressed blobs or manifests. It creates or moves no tag, so tag-based consumers see no release change.
- A failed attestation can leave a signed digest and partial trust metadata. It creates or moves no tag.
- A failed finalize can leave only a prefix of the planned tags on the candidate digest. The digest already has its required signatures and attestations, but the workflow remains failed.

When repository content is unchanged, rerun only the failed jobs:

```bash
gh run rerun "$FAILED_RUN_ID" --repo "$REPOSITORY" --failed
```

This preserves the successful builder job and reuses its authoritative OCI artifact. The publisher runs prepare again, records current observations, completes the three attestation steps, and gives finalize a result from that run. Finalize then reads fresh registry state, accepts tags that already resolve to the candidate digest, and applies the remaining eligible tags. A retry after a partial success may add duplicate valid signatures or attestations.

Never hand-replay a saved prepare result. It records registry observations from an earlier point in time and is not a durable receipt. Rerun the publisher so prepare and finalize are paired around the attestation steps and finalize can detect drift.

If finalize reports drift, inspect the named tag and the current registry state before another run. Drift means the registry changed in a way that does not match either prepare's observation or this publication's candidate digest.

If source, workflow configuration, or tool pins must change, follow the unpublished-tag recovery procedure in [Rehearse and recover GitHub Releases](rehearse-and-recover-github-releases.md). Never move a tag after its GitHub Release is public. Never delete and recreate a public exact-version image tag to substitute different content. Publish a corrective release instead.
