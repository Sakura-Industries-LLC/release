# Set up the shared package repository

Use this guide to publish verified DEB, RPM, and APK packages from producer GitHub Releases to a static Cloudflare R2 repository. The [package repository contract](../reference/package-repository-contract.md) defines the accepted release, configuration, object layout, trust checks, and recovery behavior.

## Prerequisites

You need:

- a central GitHub repository that owns the package policy and public keys;
- a Cloudflare R2 bucket with a public custom domain such as `https://pkgs.meigma.dev`;
- R2 S3 credentials limited to listing, reading, and writing objects in that bucket;
- one passphrase-protected OpenPGP private key for aggregate APT and RPM metadata;
- one RSA private key for aggregate APK indexes;
- the RPM and APK public signing keys for every producer;
- a GitHub App installation that can send `repository_dispatch` events to the central repository.

Do not give a producer workflow R2 credentials or aggregate signing keys. Store those credentials only in the central repository's `packages-production` environment.

## Add the policy and public keys

Create `.config/package-repository.yaml` in the central repository:

```yaml
channel: stable
origin: https://pkgs.meigma.dev
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
  - repository: meigma/release
    packages:
      - release-cli
    checksum_workflow: .github/workflows/go-pre-publish.yml
    attestation_workflow: .github/workflows/publish-github-release.yml
    rpm_key:
      source: keys/release-rpm.asc
      published: release-rpm-001.asc
    apk_key:
      source: keys/release-apk.rsa.pub
      published: release-apk-001.rsa.pub
```

Add the referenced public keys beneath `.config/keys/`. `source` paths are relative to `.config/`. `published` values are flat filenames under the public repository's `keys/` path.

The aggregate APT and RPM entries may use the same OpenPGP public-key source, as shown above. Each published filename must remain unique. Do not commit a private key or passphrase.

Commit the policy and public keys through the central repository's normal review path.

## Configure R2

Create one R2 bucket for the repository. Attach the public custom domain at the bucket root; the publication command rejects origins with a path prefix, query, fragment, credentials, or non-HTTPS scheme.

Keep the default R2 endpoint private. Clients use only the custom domain. The publisher uses the S3-compatible endpoint derived from the Cloudflare account ID.

Create credentials scoped to the package bucket with these object operations:

- list;
- read;
- write.

Delete permission is not required. Publication never deletes objects.

Add these repository variables to the central GitHub repository:

| Variable | Value |
|---|---|
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account that owns the bucket |
| `PACKAGE_REPOSITORY_R2_BUCKET` | R2 bucket name |

## Configure the production environment

Create a GitHub environment named `packages-production`. Add required reviewers and restrict who can change its configuration.

Add these environment secrets:

| Secret | Content |
|---|---|
| `R2_ACCESS_KEY_ID` | R2 S3 access key ID |
| `R2_SECRET_ACCESS_KEY` | R2 S3 secret access key |
| `PACKAGE_REPOSITORY_GPG_PRIVATE_KEY` | Base64-encoded armored OpenPGP private key |
| `PACKAGE_REPOSITORY_GPG_PASSPHRASE` | OpenPGP key passphrase |
| `PACKAGE_REPOSITORY_APK_PRIVATE_KEY` | Base64-encoded APK RSA private key |

Encode each key as a single-line value:

```bash
base64 < repository-private-key.asc | tr -d '\n'
base64 < repository-apk.rsa | tr -d '\n'
```

The setup action writes the decoded values to owner-only files on the ephemeral runner. It imports exactly one OpenPGP primary secret key and removes the decoded import file before publication.

## Add the central dispatcher workflow

Create `.github/workflows/publish-package-release.yml` in the central repository. Replace `<release-workflow-sha>` with a reviewed full commit SHA from `meigma/release`.

```yaml
name: Publish package release

on:
  repository_dispatch:
    types:
      - package-release

permissions:
  attestations: read
  contents: read

jobs:
  publish:
    uses: meigma/release/.github/workflows/publish-package-repository.yml@<release-workflow-sha>
    with:
      repository: ${{ github.event.client_payload.repository }}
      tag: ${{ github.event.client_payload.tag }}
      cloudflare-account-id: ${{ vars.CLOUDFLARE_ACCOUNT_ID }}
      r2-bucket: ${{ vars.PACKAGE_REPOSITORY_R2_BUCKET }}
```

The reusable workflow selects the central repository's `packages-production` environment. Environment secrets are not passed by the producer.

Keep this workflow on the central repository's default branch. GitHub runs `repository_dispatch` workflows only when their workflow file exists on the default branch.

## Dispatch from a producer

After the producer publishes its GitHub Release, mint a short-lived GitHub App token whose installation includes the central repository. Send only the producer repository and exact stable tag:

```yaml
- name: Create package repository token
  id: package-app
  uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3.2.0
  with:
    client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
    private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
    owner: meigma
    repositories: pkgs

- name: Request package repository publication
  env:
    GH_TOKEN: ${{ steps.package-app.outputs.token }}
    PRODUCER_REPOSITORY: ${{ github.repository }}
    PRODUCER_TAG: ${{ github.ref_name }}
  run: |
    gh api --method POST repos/meigma/pkgs/dispatches \
      -f event_type=package-release \
      -f "client_payload[repository]=${PRODUCER_REPOSITORY}" \
      -f "client_payload[tag]=${PRODUCER_TAG}"
```

The GitHub App needs permission to send the dispatch to `meigma/pkgs`. It does not need R2 or aggregate signing credentials.

## Verify the first publication

Dispatch one already-published producer tag. The central workflow must finish successfully before you configure clients.

Check the public metadata and keys:

```bash
curl --fail --silent --show-error https://pkgs.meigma.dev/apt/dists/stable/InRelease >/dev/null
curl --fail --silent --show-error https://pkgs.meigma.dev/rpm/stable/x86_64/repodata/repomd.xml >/dev/null
curl --fail --silent --show-error https://pkgs.meigma.dev/apk/stable/main/x86_64/APKINDEX.tar.gz >/dev/null
curl --fail --silent --show-error https://pkgs.meigma.dev/keys/apt-repository-001.asc >/dev/null
```

The workflow also installs the requested package version through APT, DNF, and
APK before and after upload. The DNF and APK checks load both the aggregate
metadata or index key and the producer package-signing key. A successful
workflow is the installation acceptance check.

## Replay a failed publication

Dispatch the same `{repository, tag}` pair again. Publication is convergent:

- matching immutable objects are skipped;
- missing objects are uploaded;
- replaceable metadata is regenerated and uploaded;
- a different value at an immutable path fails instead of overwriting that object.

Do not delete or rename package objects to recover a run. Fix the failed prerequisite, then replay the same request. A successful replay reports `state: unchanged` when every generated object already matches R2.
