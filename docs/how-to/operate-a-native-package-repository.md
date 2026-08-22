# Operate a native package repository

Use this guide to operate one adopter-owned APT, DNF, and APK repository backed
by Cloudflare R2. Producers send only their repository and stable release tag to
a central receiver. They never receive R2 credentials or aggregate repository
signing keys.

The public repository is not `pkgs.meigma.dev`. Choose and operate your own
Cloudflare account, bucket, custom domain, GitHub repository, and keys.

## Prepare the central repository

Create one central GitHub repository, such as `acme/packages`. Keep these files
under review:

```text
.config/package-repository.yaml
.config/keys/<reviewed public keys>
.github/workflows/publish-package-release.yml
```

Install the adopter-owned release App on the central repository. It needs
`contents: write` so producer workflows can create `repository_dispatch`
events. Keep the App client ID and private key available to producer
repositories, not to the central publication environment.

All package-repository writes must pass through this central workflow. The
reusable receiver serializes writers with one non-cancelling concurrency group;
the CLI and R2 do not provide a distributed repository lock.

## Configure Cloudflare R2

In the adopter's Cloudflare account:

1. Create one R2 bucket for the package repository.
2. Attach a public custom domain to the bucket root.
3. Require HTTPS at that domain.
4. Create an R2 S3 token scoped to this bucket with list, read, and write object
   access.
5. Record the Cloudflare account ID, bucket name, S3 access key ID, and S3
   secret access key.

The policy `origin` must be an absolute HTTPS root such as
`https://packages.example.com`. It cannot contain a path prefix, query,
fragment, credentials, or a trailing alternate origin. Clients use the custom
domain. The publisher uses the account-specific S3-compatible endpoint derived
from the Cloudflare account ID.

Delete permission is not required. Publication never deletes or prunes an
object.

Do not add a cache rule that overrides the publisher's `Cache-Control` values.
The publisher assigns:

- `public, max-age=31536000, immutable` to packages, public keys, and APT
  by-hash objects; and
- `no-store` to indexes, signatures, and other replaceable metadata.

Confirm that the custom domain and any Cloudflare cache rules preserve these
headers. Caching replaceable roots can leave clients on a repository view that
the operator has already replaced.

Add these repository variables to `acme/packages`:

| Variable | Value |
| --- | --- |
| `CLOUDFLARE_ACCOUNT_ID` | Account that owns the bucket. |
| `PACKAGE_REPOSITORY_R2_BUCKET` | Existing R2 bucket name. |

## Create the signing domains

The central repository needs:

- one passphrase-protected OpenPGP private key for aggregate APT and RPM
  metadata;
- the corresponding OpenPGP public key;
- one RSA private key for aggregate APK indexes; and
- the corresponding RSA public key.

Each producer also needs its own:

- OpenPGP key pair for RPM package signatures; and
- RSA key pair for APK package signatures.

Keep producer private keys in that producer's Actions secrets. Keep aggregate
private keys only in the central protected environment. Commit only public keys
to `.config/keys/`.

The aggregate APT and RPM entries may refer to the same OpenPGP public-key file.
Every published key filename must be unique and stable. A key replacement uses
a new reviewed object name; immutable public-key objects are not overwritten.

## Write the producer policy

Create `.config/package-repository.yaml`. The following is a template; replace
the domain and `REPLACE_WITH_RELEASE_COMMIT_SHA` before use:

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

The signer fields are explicit shared-workflow identities:

- `checksum_identity` must be exactly
  `https://github.com/<owner>/<repository>/.github/workflows/<file>@<40-lowercase-sha>`.
  Branches, tags, abbreviated SHAs, non-GitHub hosts, queries, and fragments are
  rejected.
- `attestation_signer` must be exactly
  `<owner>/<repository>/.github/workflows/<file>`, without a URL or ref.

In both fields, the workflow filename must end in `.yml` or `.yaml`.

For a producer that calls `meigma/release`, both fields name the reusable
workflow repository, not `acme/widget`. The checksum identity pins the release
unit. GitHub attestation verification independently binds the package to the
producer repository, `refs/tags/<tag>`, the resolved producer commit, and the
configured shared signer workflow.

Replace the revision placeholder with the same full commit used in that
producer's release caller. Update the producer caller and this reviewed policy
atomically before the next release. Do not derive trust from the producer tag
or assume that signer workflows belong to the producer repository.

A package name belongs to one producer. Each requested release must contain one
DEB, RPM, and APK for every allowlisted package on both `amd64` and `arm64`.
Add the referenced public-key files beneath `.config/keys/`, then merge the
policy and keys through the central repository's review process.

## Protect the production environment

Create a GitHub environment named `packages-production` in `acme/packages`.
Add required reviewers and restrict who can change its configuration.

Add these environment secrets:

| Secret | Content |
| --- | --- |
| `R2_ACCESS_KEY_ID` | Bucket-scoped R2 S3 access key ID. |
| `R2_SECRET_ACCESS_KEY` | Bucket-scoped R2 S3 secret access key. |
| `PACKAGE_REPOSITORY_GPG_PRIVATE_KEY` | Base64-encoded armored aggregate OpenPGP private key. |
| `PACKAGE_REPOSITORY_GPG_PASSPHRASE` | Aggregate OpenPGP key passphrase. |
| `PACKAGE_REPOSITORY_APK_PRIVATE_KEY` | Base64-encoded aggregate APK RSA private key. |

Encode each private key as one line:

```bash
base64 < repository-private-key.asc | tr -d '\n'
base64 < repository-apk.rsa | tr -d '\n'
```

The setup action decodes these values into owner-only files on the ephemeral
runner, imports exactly one OpenPGP primary secret key, and removes the decoded
OpenPGP import file before publication.

## Add the receiver workflow

Create `.github/workflows/publish-package-release.yml` in the central
repository. Replace the revision placeholder with the reviewed full SHA used by
its producer policies:

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
    uses: meigma/release/.github/workflows/publish-package-repository.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      repository: ${{ github.event.client_payload.repository }}
      tag: ${{ github.event.client_payload.tag }}
      cloudflare-account-id: ${{ vars.CLOUDFLARE_ACCOUNT_ID }}
      r2-bucket: ${{ vars.PACKAGE_REPOSITORY_R2_BUCKET }}
```

Keep this file on the central repository's default branch. GitHub processes a
`repository_dispatch` only when a matching workflow exists there.

The called workflow selects `packages-production`, checks out the caller's
reviewed policy and public keys, builds `release-cli` from the exact reusable
workflow source, and invokes one publication command. It requests only
`contents: read` and `attestations: read`; R2 and aggregate signing authority
come from the protected environment.

## Enable native signing in each producer

The producer's `.goreleaser.yaml` must keep nFPM ID `release` and these signature
key expressions:

```yaml
nfpms:
  - id: release
    rpm:
      signature:
        key_file: "{{ .Env.RELEASE_RPM_SIGNING_KEY_FILE }}"
    apk:
      signature:
        key_file: "{{ .Env.RELEASE_APK_SIGNING_KEY_FILE }}"
        key_name: widget-001
```

Add the producer private keys and passphrases as Actions secrets, then enable
and map them in the `release-assets` call:

```yaml
  release-assets:
    permissions:
      attestations: read
      contents: read
      id-token: write
    uses: meigma/release/.github/workflows/go-pre-publish.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      sign-native-packages: true
    secrets:
      rpm-signing-key: ${{ secrets.RPM_SIGNING_KEY }}
      rpm-signing-passphrase: ${{ secrets.RPM_SIGNING_PASSPHRASE }}
      apk-signing-key: ${{ secrets.APK_SIGNING_KEY }}
      apk-signing-passphrase: ${{ secrets.APK_SIGNING_PASSPHRASE }}
```

The workflow validates all four secrets, materializes owner-only temporary key
files, and removes them after staging. GoReleaser signs RPM and APK bytes before
`checksums.txt` is generated. A package repository rejects unsigned or
wrong-key RPM and APK packages even when their release checksums are valid.

## Onboard the producer dispatch

Install the adopter-owned App on both `acme/widget` and `acme/packages`. Make the
App client-ID variable and private-key secret available to the producer.

Customize the maintained request job and leave it disabled:

```yaml
  package-repository:
    name: Request package repository publication
    needs: github-release
    permissions: {}
    uses: meigma/release/.github/workflows/request-package-repository.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      package-repository-owner: acme
      package-repository-name: packages
      release-app-client-id: ${{ vars.MEIGMA_RELEASE_APP_CLIENT_ID }}
      publish-package-repository: false
    secrets:
      release-app-private-key: ${{ secrets.MEIGMA_RELEASE_APP_PRIVATE_KEY }}
```

The request sends only `acme/widget` and its exact tag. It does not send
artifacts, R2 credentials, or aggregate keys. The central publisher downloads
the public GitHub Release and applies the reviewed policy.

Enable `publish-package-repository: true` only after all of these conditions
hold:

- `publish-release: true` is enabled for the same tag-triggered run;
- the producer release workflow signs RPM and APK packages;
- the policy contains the producer's exact checksum and attestation signers;
- both producer public keys are committed;
- the central receiver workflow is on the default branch;
- the protected environment and R2 variables are configured; and
- the App is installed on the central repository.

The job must remain after `github-release`; the receiver rejects a missing,
draft, prerelease, or mismatched release.

## Accept the first publication

Publish one stable producer release. Approve the central protected-environment
deployment and watch the central workflow:

```bash
gh run list --repo acme/packages --workflow publish-package-release.yml --limit 5
gh run view <RUN_ID> --repo acme/packages --log
```

The CLI performs two installation passes with pinned Debian, Fedora, and Alpine
containers:

1. before upload, it mounts the generated tree and reviewed keys read-only,
   disables networking, and installs the exact requested package version with
   APT, DNF, and APK; and
2. after upload, it installs the same version from the public HTTPS origin.

DNF checks both the aggregate RPM metadata signature and the producer RPM
signature. APK checks both the aggregate index signature and the producer APK
signature. A successful receiver workflow proves that all six local and public
client checks passed.

Check the public roots and one reviewed key at the configured origin:

```bash
export PACKAGE_ORIGIN=https://packages.example.com
curl --fail --silent --show-error \
  "$PACKAGE_ORIGIN/apt/dists/stable/InRelease" >/dev/null
curl --fail --silent --show-error \
  "$PACKAGE_ORIGIN/rpm/stable/x86_64/repodata/repomd.xml" >/dev/null
curl --fail --silent --show-error \
  "$PACKAGE_ORIGIN/apk/stable/main/x86_64/APKINDEX.tar.gz" >/dev/null
curl --fail --silent --show-error \
  "$PACKAGE_ORIGIN/keys/apt-repository-001.asc" >/dev/null
```

Do not configure clients until the receiver is green and these public objects
are available over HTTPS.

## Replay and recover

Authenticate `gh` as an authorized operator whose token has `Contents: write`
access to the central repository. Then replay an exact published producer
release by sending the same dispatch again:

```bash
export PRODUCER_REPOSITORY=acme/widget
export PRODUCER_TAG=v1.2.3
gh api --method POST repos/acme/packages/dispatches --input - <<EOF
{
  "event_type": "package-release",
  "client_payload": {
    "repository": "$PRODUCER_REPOSITORY",
    "tag": "$PRODUCER_TAG"
  }
}
EOF
```

Publication converges from current R2 state:

- matching immutable objects are skipped;
- missing objects are uploaded;
- replaceable metadata is regenerated and uploaded;
- non-root objects are uploaded before APT, RPM, and APK commit roots; and
- a different digest or size at an immutable path fails instead of overwriting
  the object.

A complete replay reports `state: unchanged`. A crash can leave unreferenced
inner objects, but it does not activate an incomplete view before the relevant
commit root is uploaded.

Do not delete, rename, or overwrite package objects to recover a run. Correct
the failed external prerequisite, policy, public key, signature, or release and
replay the same request. If an immutable path conflicts, stop and investigate
the existing object; the normal publication path cannot replace it.
