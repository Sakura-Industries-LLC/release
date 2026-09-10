# Publish RPM packages to Forgejo

Use this guide to serve a release's `.rpm` files from a Forgejo RPM package
registry, so users install and update with `dnf` instead of downloading an
archive. Rerunning the publisher accepts packages that already exist with
identical bytes and refuses to replace packages whose bytes differ.

Complete [Adopt the release workflows](adopt-the-release-workflows.md) first.
The RPM publisher consumes the same `release-assets` artifact as the GitHub
Release publisher, so the packages must be members of the signed
`checksums.txt` the producer builds.

## Sign the packages with a producer key

Forgejo signs the repository metadata it generates with its own key. It does
not sign your packages. A DNF client running `gpgcheck=1` therefore checks the
signature baked into the package header, which only the producer can put there.

Sign RPM packages during staging by selecting the format in the pre-publish
caller:

```yaml
    with:
      sign-native-packages: rpm
```

Formats are selected on their own, so `rpm` signs RPM packages and leaves APK
packages unsigned, `apk` does the reverse, and `rpm,apk` signs both. Each
selected format requires its own key and passphrase secrets. See
[Adopt the release workflows](adopt-the-release-workflows.md) for the secret
names.

Commit the matching public key to the producer repository under review, for
example `.config/keys/producer-rpm.asc`, and record its 40-digit fingerprint:

```bash
gpg --show-keys --with-colons .config/keys/producer-rpm.asc \
  | awk -F: '$1 == "fpr" { print $10; exit }'
```

The publisher accepts a package only when that reviewed key validates its
signature, so replacing the key is a reviewed change to the repository, not a
change to a secret.

## Build the packages into the release bundle

The producer must place exactly two RPM packages in the asset set it checksums
and signs: one `x86_64` and one `aarch64`. Both must declare the same package
name and the same stable `MAJOR.MINOR.PATCH` version, release, and epoch in
their headers. The publisher reads `NAME`, `VERSION`, `RELEASE`, `EPOCHNUM`,
and `ARCH` with `rpm`, so file naming is free.

When the job publishes, that version must also be the version the tag names:
`v1.2.3` and the monorepo form `resolver/v1.2.3` both name `1.2.3`. A
prerelease version, one architecture, a third package, a package that declares
a different name, an unsigned package, or a package signed by a key other than
the reviewed one fails the run before any request is sent. A DNF repository
serves stable releases only.

## Prepare the registry

Create the owner (a user or organization) that will hold the packages, and a
publisher account with package write permission on it. Issue that account a
token with the `write:package` scope and nothing else.

Record three non-secret values: the forge origin (for example
`https://forge.example.net`), the owner, and the group (for example `stable`).

## Store the publisher token

Add one repository or organization secret to the producer, for example
`FORGEJO_PUBLISHER_TOKEN`. Restrict it to the repositories that publish into
the registry. The token is read only by the step that uploads; a verify-only
run never needs it.

## Call the publisher

Add one job to the producer's release workflow after the GitHub Release job:

```yaml
  publish-rpm:
    name: Publish RPM packages
    needs: [release-assets, github-release]
    permissions:
      actions: read
      attestations: read
      contents: read
    uses: Sakura-Industries-LLC/release/.github/workflows/publish-forgejo-rpm.yml@<full-sha>
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: ${{ github.repository }}/.github/workflows/release.yml@${{ github.ref }}
      package-name: widget
      origin: https://forge.example.net
      owner: downloads
      group: stable
      producer-key-file: .config/keys/producer-rpm.asc
      producer-key-fingerprint: 0123456789ABCDEF0123456789ABCDEF01234567
      publisher-username: publisher
      publish-packages: false
    secrets:
      publisher-token: ${{ secrets.FORGEJO_PUBLISHER_TOKEN }}
```

`producer-key-file` is a path inside the producer's own checkout. An absolute
path, a `..` segment, or a symlink that leaves the repository is rejected, so a
run can only trust a key the repository reviewed.

## Rehearse before you publish

Keep `publish-packages: false` until the registry, account, and token exist.
The job then verifies the artifact handoff, the closed bundle, the package
signatures, and the package set without contacting Forgejo. That rehearsal runs
from any ref, so a `workflow_dispatch` on a pull request branch exercises the
real path without creating a tag. Publication is the only part that requires a
tag ref, and that tag must resolve to the commit the workflow is running.

You can also run the transport by hand against a staging forge, which is the
same code the workflow runs:

```bash
cd /path/to/release
FORGEJO_ORIGIN=https://forge.example.net \
FORGEJO_OWNER=downloads \
FORGEJO_GROUP=stable \
FORGEJO_PACKAGE=widget \
FORGEJO_PRODUCER_KEY_FILE=/path/to/producer-rpm.asc \
FORGEJO_PRODUCER_KEY_FINGERPRINT=0123456789ABCDEF0123456789ABCDEF01234567 \
FORGEJO_USERNAME=publisher \
FORGEJO_PUBLISH=false \
  bash .github/actions/publish-forgejo-rpm/publish-rpm.sh /path/to/dist
```

To upload, set `FORGEJO_PUBLISH=true`, pass the token in `FORGEJO_TOKEN`, and
set `FORGEJO_EXPECTED_VERSION` to the version the release publishes; the
workflow derives that value from the tag, and by hand you state it. The script
needs `rpm`, `gpg`, and `curl`. It imports the reviewed key into a scratch RPM
database of its own, so it accepts a signature only because that key validates
it. It reads the token from the environment into a private `netrc`, never onto
a command line or into a URL, refuses a plaintext origin, and follows no
redirect, so the credential never leaves the origin you named.

## Read the result

The job prints one line for each package: `published` when the registry
accepted the upload, or `unchanged` when the registry already held that exact
file. Both are success. In both cases the publisher reads the package back from
the registry and compares it byte for byte with the file this run built.

## Recover

A failed run leaves any package it already uploaded in place; rerun the job and
the uploaded package reports `unchanged`.

A digest mismatch means the registry serves different bytes under a name this
release already published. The publisher never deletes and never overwrites,
and neither should you: a version that users may already have installed is not
a version to swap out underneath them. Work out why the bytes differ, then
publish the correct build as a new version.

A mismatch immediately after a `201` means the registry did not store what it
accepted. The most common cause is an instance that re-signs uploaded RPMs
(`DEFAULT_RPM_SIGN_ENABLED`), which replaces the producer signature users
verify. Turn that setting off rather than relaxing the comparison.

## Consume the registry

Users add the registry once. For a private registry, keep the read-only
credential in the repository file, owned by root with mode `0600`:

```ini
[forge-downloads]
name=Forge downloads
baseurl=https://forge.example.net/api/packages/downloads/rpm/stable
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-producer file:///etc/pki/rpm-gpg/RPM-GPG-KEY-forge-downloads
username=YOUR_DOWNLOAD_USERNAME
password=YOUR_READ_ONLY_TOKEN
```

Fetch both keys through a trusted channel before installing: the producer key
that signs the packages, and the registry key at
`https://forge.example.net/api/packages/downloads/rpm/repository.key` that
signs the metadata. Install them under `/etc/pki/rpm-gpg/` and point `gpgkey`
at those local files, so DNF never learns a key from the same request that
serves the packages.

Never disable `gpgcheck` or `repo_gpgcheck`. The release's Cosign bundle
separately covers the package bytes for anyone verifying a downloaded file.
