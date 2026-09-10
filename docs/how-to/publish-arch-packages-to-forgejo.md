# Publish Arch packages to Forgejo

Use this guide to serve a release's `.pkg.tar.zst` files from a Forgejo Arch
package registry, so users install and update with `pacman` instead of
downloading an archive. Rerunning the publisher accepts packages that already
exist with identical bytes and refuses to replace packages whose bytes differ.

Complete [Adopt the release workflows](adopt-the-release-workflows.md) first.
The Arch publisher consumes the same `release-assets` artifact as the GitHub
Release publisher, so the packages must be members of the signed
`checksums.txt` the producer builds.

## Understand who signs what

Forgejo signs both the package and the pacman database with the registry's own
key, which it generates per owner and serves at
`<origin>/api/packages/<owner>/arch/repository.key`. A pacman client checks
those signatures, so there is no producer key to create, review, or rotate for
this format, and `sign-native-packages` stays as it is.

That also means an upload is not one atomic act. The instance creates the
package first, then adds its signature, then rebuilds and signs the database
for that architecture. An interrupted upload can therefore leave a package
that a retry can only meet as `409`. The publisher reads the whole published
state back after every upload and fails on an incomplete one, rather than
reporting a half-published release as done.

## Build the packages into the release bundle

The producer must place exactly two Arch packages in the asset set it
checksums and signs: one `x86_64` and one `aarch64`. Both must declare the
same `pkgname` and the same `pkgver` in their `.PKGINFO`, in the plain
`VERSION-RELEASE` form with a stable `MAJOR.MINOR.PATCH` version and a numeric
`pkgrel`. Each package must also carry the `.MTREE` entry the registry
requires.

A GoReleaser producer gets them by adding one format to the existing nFPM
entry, which reuses the canonical Linux binaries:

```yaml
nfpms:
  - id: release
    formats:
      - archlinux
```

A producer that builds its packages itself must add both files to the exact
asset set it checksums, next to its other packages.

File naming is free. The publisher reads the metadata out of the archive with
`bsdtar`, never unpacks it, and addresses the registry by what the package
declares: Forgejo stores and serves it as
`<pkgname>-<pkgver>-<arch>.pkg.tar.zst` whatever the uploaded file was called.

When the job publishes, the version must also be the version the tag names:
`v1.2.3` and the monorepo form `resolver/v1.2.3` both name `1.2.3`. An epoch,
a prerelease version, one architecture, a third package, a package that
declares a different name, and a package without an `.MTREE` all fail the run
before any request is sent. A pacman repository serves stable releases only.

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
  publish-arch:
    name: Publish Arch packages
    needs: [release-assets, github-release]
    permissions:
      actions: read
      attestations: read
      contents: read
    uses: Sakura-Industries-LLC/release/.github/workflows/publish-forgejo-arch.yml@<full-sha>
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: ${{ github.repository }}/.github/workflows/release.yml@${{ github.ref }}
      package-name: widget
      origin: https://forge.example.net
      owner: downloads
      group: stable
      publisher-username: publisher
      publish-packages: false
    secrets:
      publisher-token: ${{ secrets.FORGEJO_PUBLISHER_TOKEN }}
```

## Rehearse before you publish

Keep `publish-packages: false` until the registry, account, and token exist.
The job then verifies the artifact handoff, the closed bundle, and the package
set without contacting Forgejo. That rehearsal runs from any ref, so a
`workflow_dispatch` on a pull request branch exercises the real path without
creating a tag. Publication is the only part that requires a tag ref, and that
tag must resolve to the commit the workflow is running.

You can also run the transport by hand against a staging forge, which is the
same code the workflow runs:

```bash
cd /path/to/release
FORGEJO_ORIGIN=https://forge.example.net \
FORGEJO_OWNER=downloads \
FORGEJO_GROUP=stable \
FORGEJO_PACKAGE=widget \
FORGEJO_USERNAME=publisher \
FORGEJO_PUBLISH=false \
  bash .github/actions/publish-forgejo-arch/publish-arch.sh /path/to/dist
```

To upload, set `FORGEJO_PUBLISH=true`, pass the token in `FORGEJO_TOKEN`, and
set `FORGEJO_EXPECTED_VERSION` to the version the release publishes; the
workflow derives that value from the tag, and by hand you state it. The script
needs `bsdtar`, `gpg`, `gpgv`, `openssl`, and `curl`. It reads the token from
the environment into a private `netrc`, never onto a command line or into a
URL, refuses a plaintext origin, and follows no redirect, so the credential
never leaves the origin you named.

## Read the result

The job prints one line for each package: `published` when the registry
accepted the upload, or `unchanged` when the registry already held that exact
package. Both are success, and both are reported only after the publisher has
confirmed, for that architecture:

- the registry serves the package under the name its metadata derives, with
  the SHA-256 this run built;
- the package's detached signature verifies against the registry key;
- `<group>.db` and `<group>.db.sig` exist and the signature verifies; and
- the database entry for this exact `pkgname-pkgver` declares the same
  `FILENAME`, `NAME`, `VERSION`, `ARCH`, `SHA256SUM`, and the `PGPSIG` the
  registry serves beside the package.

## Recover

A failed run leaves any package it already published in place; rerun the job
and that package reports `unchanged`.

A digest mismatch means the registry serves different bytes under a name this
release already published. The publisher never deletes and never overwrites,
and neither should you: a version that users may already have installed is not
a version to swap out underneath them. Work out why the bytes differ, then
publish the correct build as a new version.

A missing signature, a missing or unsigned database, or a database that does
not index this version means an earlier upload stopped part-way. Repair the
registry so the state is complete, then rerun. Do not delete the package to
force a fresh `201`: deleting is how a version users hold becomes
unverifiable.

Forgejo indexes one entry per package name, for the version it created most
recently. A database that names a different version of this package therefore
fails the run: either a later release now owns the package, in which case
publishing this older tag again is not what you want, or an interrupted
publication left the database stale and an operator has to rebuild it.

## Consume the registry

Users add the registry once, in `/etc/pacman.conf`:

```ini
[downloads.stable]
SigLevel = Required
Server = https://forge.example.net/api/packages/downloads/arch/stable/$arch
```

Then import the registry key through a trusted channel and sign it locally:

```bash
curl -fsSL -o forge.gpg https://forge.example.net/api/packages/downloads/arch/repository.key
sudo pacman-key --add forge.gpg
sudo pacman-key --lsign-key 'downloads@noreply.forge.example.net'
sudo pacman -Sy widget
```

A private registry has no netrc support in pacman, so the read-only credential
has to sit in the `Server` URL. Keep that line in a file owned by root with
mode `0600` that `pacman.conf` includes, and issue the reader a token with
package read scope and nothing else.

Never lower `SigLevel`. The release's Cosign bundle separately covers the
package bytes for anyone verifying a downloaded file.
