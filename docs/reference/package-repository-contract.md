# Package repository contract

This reference defines the static package repository accepted and published by `release-cli publish package-repository` and `.github/workflows/publish-package-repository.yml`.

For deployment steps, see [Set up the shared package repository](../how-to/set-up-package-repository.md).

## Supported repository

The current contract supports one channel, two architectures, and three package formats.

| Domain | Accepted values |
|---|---|
| Channel | `stable` |
| Architectures | `amd64`, `arm64` |
| Package formats | DEB, RPM, APK |
| Public origin | Absolute HTTPS origin without a path prefix, query, fragment, or credentials |
| Object storage | One existing Cloudflare R2 bucket through its S3-compatible API |

Each producer owns an allowlisted set of package names. A package name can belong to only one producer. Every requested release must contain exactly one package for each configured package name, format, and architecture.

The publisher does not create the R2 bucket, custom domain, signing keys, GitHub environment, producer release, or central repository.

## Producer release contract

A publication request contains two public values:

- `repository`: lowercase GitHub `owner/name`;
- `tag`: exact stable `vMAJOR.MINOR.PATCH` tag.

The producer must have a published GitHub Release for the tag. The tag must resolve to one full lowercase commit SHA. The release download is a closed set: every downloaded file must be either listed in `checksums.txt` or be one of these controls:

- `checksums.txt`;
- `checksums.txt.sigstore.json`.

`checksums.txt` cannot list either control file. Every listed file must match its SHA-256 value. No extra file, directory, symlink, or irregular entry is accepted.

For each DEB, RPM, and APK entry, the publisher also requires:

1. a matching GitHub Release asset whose GitHub digest equals the checksum digest;
2. a GitHub build-provenance attestation for the exact producer repository, `refs/tags/<tag>`, resolved source commit, and configured attestation workflow;
3. package metadata whose name, version, and architecture match the reviewed producer policy and requested release;
4. a valid producer-native signature for RPM and APK packages.

The detached Sigstore bundle for `checksums.txt` must match this certificate identity:

```text
https://github.com/<producer>/<checksum_workflow>@refs/tags/<tag>
```

Its OIDC issuer must be `https://token.actions.githubusercontent.com`.

## Policy file

`--config` points to one strict YAML document. The parser rejects unknown fields, aliases, multiple documents, duplicate producers, duplicate package ownership, duplicate published key names, unsupported channels, malformed paths, and files larger than 64 KiB.

```yaml
channel: stable
origin: https://pkgs.example.com
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
  - repository: owner/project
    packages:
      - project
    checksum_workflow: .github/workflows/go-pre-publish.yml
    attestation_workflow: .github/workflows/publish-github-release.yml
    rpm_key:
      source: keys/project-rpm.asc
      published: project-rpm-001.asc
    apk_key:
      source: keys/project-apk.rsa.pub
      published: project-apk-001.rsa.pub
```

### Top-level fields

| Field | Contract |
|---|---|
| `channel` | Must be `stable`. |
| `origin` | Public HTTPS repository root used for post-upload installation. |
| `keys.apt` | Aggregate OpenPGP public key used by APT clients. |
| `keys.rpm` | Aggregate OpenPGP public key used to verify RPM metadata. It may share a `source` file with `keys.apt`. |
| `keys.apk` | Aggregate RSA public key used to verify APK indexes. |
| `producers` | Non-empty allowlist of producer repositories and package ownership. |

Each key has these fields:

| Field | Contract |
|---|---|
| `source` | Slash-separated path beneath `--keys`; no absolute path or traversal. |
| `published` | Flat public filename written beneath `keys/`; unique across all aggregate and producer keys. |

### Producer fields

| Field | Contract |
|---|---|
| `repository` | Lowercase GitHub `owner/name`; unique. |
| `packages` | Non-empty package-name allowlist; ownership is unique across producers. |
| `checksum_workflow` | Repository-relative `.github/workflows/*.yml` or `.yaml` path. |
| `attestation_workflow` | Repository-relative `.github/workflows/*.yml` or `.yaml` path. |
| `rpm_key` | Producer RPM package-signing public key. |
| `apk_key` | Producer APK package-signing public key. |

## Public object layout

The publisher regenerates the complete repository from the verified incoming release plus every existing immutable package object in R2.

```text
keys/
  <aggregate and producer public keys>
apt/
  pool/main/<prefix>/<package>/<package>_<version>_<arch>.deb
  dists/stable/InRelease
  dists/stable/main/binary-<arch>/Packages
  dists/stable/main/binary-<arch>/Packages.gz
  dists/stable/main/binary-<arch>/by-hash/SHA256/<digest>
rpm/
  stable/<rpm-arch>/Packages/<package>-<version>-1.<rpm-arch>.rpm
  stable/<rpm-arch>/repodata/repomd.xml
  stable/<rpm-arch>/repodata/repomd.xml.asc
  stable/<rpm-arch>/repodata/*
apk/
  stable/main/<apk-arch>/<package>-<version>.apk
  stable/main/<apk-arch>/APKINDEX.tar.gz
```

Architecture names are format-specific:

| Normalized | APT | RPM | APK |
|---|---|---|---|
| `amd64` | `amd64` | `x86_64` | `x86_64` |
| `arm64` | `arm64` | `aarch64` | `aarch64` |

APT `InRelease` is clear-signed with the aggregate OpenPGP key. RPM `repomd.xml.asc` is an armored detached signature of `repomd.xml`. Each `APKINDEX.tar.gz` carries the aggregate APK RSA signature.

Native clients use both trust layers. APT trusts the aggregate metadata key.
DNF trusts the aggregate RPM metadata key and every configured producer RPM
package key. APK trusts the aggregate index key and every configured producer
APK package key. Omitting a producer key must fail the installation acceptance
check even when the repository index signature is valid.

APT metadata uses the producer GitHub Release publication time as its deterministic creation time. `Valid-Until` is 365 days after that timestamp.

## Publication transaction

Publication has this order:

1. copy reviewed public keys into confined scratch storage;
2. download and close-set verify the producer release;
3. verify package attestations and native signatures;
4. list and stream every existing immutable package object from R2;
5. regenerate APT, RPM, and APK metadata locally;
6. install the exact package version from the local tree with APT, DNF, and APK;
7. upload all missing or changed non-root objects;
8. upload mutable commit roots last;
9. install the exact package version from the public origin with APT, DNF, and APK.

The commit roots are:

- `apt/dists/stable/InRelease`;
- one `rpm/stable/<arch>/repodata/repomd.xml` per architecture;
- one `apk/stable/main/<arch>/APKINDEX.tar.gz` per architecture.

All other generated objects are uploaded before any commit root. A process crash can leave unreferenced inner objects, but it does not activate an incomplete repository view before the relevant root is uploaded.

The publisher has no distributed lock. The reusable workflow uses one `package-repository-production` concurrency group with `cancel-in-progress: false`. All production writes must pass through that workflow.

## Object state and caching

Every uploaded object stores its canonical `sha256:<hex>` digest in R2 user metadata. The publisher compares the digest and size before writing.

| Object class | Mutation | `Cache-Control` |
|---|---|---|
| Packages, public keys, APT by-hash objects | Immutable | `public, max-age=31536000, immutable` |
| Indexes, signatures, other replaceable metadata | Replaceable | `no-store` |

A matching object is skipped. A digest or size mismatch at an immutable path is a hard failure. Replaceable metadata may be overwritten. Publication does not delete or prune objects.

## Installation acceptance check

The publisher runs three pinned Linux containers:

- Debian for APT;
- Fedora for DNF;
- Alpine for APK.

The local pass mounts the generated tree and public keys read-only with networking disabled. The public pass uses the configured HTTPS origin. Each client installs every package owned by the producer and verifies the exact requested version.

A publication is successful only when both passes succeed.

## Command interface

```text
release-cli publish package-repository [flags]
```

Flags override their corresponding environment variables.

| Flag | Environment | Required value |
|---|---|---|
| `--repository` | `RELEASE_REPOSITORY` | Producer `owner/name` |
| `--tag` | `RELEASE_TAG` | Stable release tag |
| `--config` | `RELEASE_PACKAGE_REPOSITORY_CONFIG` | Policy YAML path |
| `--keys` | `RELEASE_PACKAGE_KEYS` | Public-key source directory |
| `--cloudflare-account-id` | `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account ID |
| `--r2-bucket` | `RELEASE_R2_BUCKET` | Existing R2 bucket |
| `--gpg-home` | `RELEASE_GPG_HOME` | GnuPG home containing the aggregate secret key |
| `--gpg-key-id` | `RELEASE_GPG_KEY_ID` | Full aggregate OpenPGP key fingerprint |
| `--gpg-passphrase-file` | `RELEASE_GPG_PASSPHRASE_FILE` | Owner-only passphrase file |
| `--apk-signing-key` | `RELEASE_APK_SIGNING_KEY` | Aggregate APK RSA private key file |

These environment variables have no flag:

| Environment | Contract |
|---|---|
| `R2_ACCESS_KEY_ID` | R2 S3 access key ID |
| `R2_SECRET_ACCESS_KEY` | R2 S3 secret access key |
| `GITHUB_TOKEN` or `GH_TOKEN` | GitHub release and attestation read token; `GITHUB_TOKEN` wins |
| `GITHUB_API_URL` | Optional GitHub API base |
| `GITHUB_SERVER_URL` | Optional GitHub server base |
| `RELEASE_GH_PATH` | Optional `gh` executable override |
| `RELEASE_DOCKER_PATH` | Optional Docker executable override |
| `RELEASE_COSIGN_PATH` | Optional Cosign executable override |
| `RELEASE_GPG_PATH` | Optional GnuPG executable override |

The command accepts no positional arguments.

With `--json`, success writes one `release.dev/result/v1` envelope whose `result` has this shape:

```json
{
  "state": "published",
  "repository": "owner/project",
  "tag": "v1.2.3",
  "artifacts": 26,
  "uploaded": 26
}
```

`state` is `published` when at least one object was uploaded and `unchanged` when every generated object already matched. `artifacts` depends on the configured package set and generated metadata; `26` is illustrative.

Usage and configuration failures exit with code `2`. Verification, generation, installation, storage, and publication failures exit with code `1`.

## Reusable workflow interface

`.github/workflows/publish-package-repository.yml` accepts these `workflow_call` inputs:

| Input | Type | Default | Required |
|---|---|---|---|
| `repository` | string | — | yes |
| `tag` | string | — | yes |
| `config-path` | string | `.config/package-repository.yaml` | no |
| `keys-path` | string | `.config` | no |
| `cloudflare-account-id` | string | — | yes |
| `r2-bucket` | string | — | yes |

The job runs on `ubuntu-24.04`, has a 45-minute timeout, selects the `packages-production` environment, and requests only `contents: read` and `attestations: read` permissions.

The selected environment must define:

- `R2_ACCESS_KEY_ID`;
- `R2_SECRET_ACCESS_KEY`;
- `PACKAGE_REPOSITORY_GPG_PRIVATE_KEY`;
- `PACKAGE_REPOSITORY_GPG_PASSPHRASE`;
- `PACKAGE_REPOSITORY_APK_PRIVATE_KEY`.

The workflow checks out the caller repository for its policy and public keys. It builds `release-cli` from the exact reusable-workflow source revision, verifies the producer assets, materializes the aggregate keys on the ephemeral runner, and invokes one CLI publication command.
