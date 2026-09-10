# Publish Debian packages to Forgejo

Use this guide to serve a release's `.deb` files from a Forgejo Debian package
registry, so users install and update with `apt` instead of downloading an
archive. Rerunning the publisher accepts packages that already exist with
identical bytes and refuses to replace packages whose bytes differ.

Complete [Adopt the release workflows](adopt-the-release-workflows.md) first.
The Debian publisher consumes the same `release-assets` artifact as the GitHub
Release publisher, so the packages must be members of the signed
`checksums.txt` the producer builds.

## Build the packages into the release bundle

The producer must place exactly two Debian packages in the asset set it
checksums and signs: one `amd64` and one `arm64`. Both must declare the same
package name and the same stable `MAJOR.MINOR.PATCH` version in their control
fields. The publisher reads `Package`, `Version`, and `Architecture` with
`dpkg-deb`, so file naming is free: GoReleaser's `name_version_linux_arch.deb`
and nFPM's `name_version_arch.deb` both work.

When the job publishes, that version must also be the version the tag names:
`v1.2.3` and the monorepo form `resolver/v1.2.3` both name `1.2.3`. A
prerelease version, one architecture, a third package, a package that declares
a different name, or a version the tag does not name fails the run before any
request is sent. An apt suite serves stable releases only.

## Prepare the registry

Create the owner (a user or organization) that will hold the packages, and a
publisher account with package write permission on it. Issue that account a
token with the `write:package` scope and nothing else.

Record four non-secret values: the forge origin (for example
`https://forge.example.net`), the owner, the distribution (for example
`stable`), and the component (for example `main`).

## Store the publisher token

Add one repository or organization secret to the producer, for example
`FORGEJO_PUBLISHER_TOKEN`. Restrict it to the repositories that publish into
the registry. The token is read only by the step that uploads; a verify-only
run never needs it.

## Call the publisher

Add one job to the producer's release workflow after the GitHub Release job:

```yaml
  publish-debian:
    name: Publish Debian packages
    needs: [release-assets, github-release]
    permissions:
      actions: read
      attestations: read
      contents: read
    uses: Sakura-Industries-LLC/release/.github/workflows/publish-forgejo-debian.yml@<full-sha>
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: ${{ github.repository }}/.github/workflows/release.yml@${{ github.ref }}
      package-name: widget
      origin: https://forge.example.net
      owner: downloads
      distribution: stable
      component: main
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
FORGEJO_DISTRIBUTION=stable \
FORGEJO_COMPONENT=main \
FORGEJO_PACKAGE=widget \
FORGEJO_USERNAME=publisher \
FORGEJO_PUBLISH=false \
  bash .github/actions/publish-forgejo-debian/publish-debian.sh /path/to/dist
```

To upload, set `FORGEJO_PUBLISH=true`, pass the token in `FORGEJO_TOKEN`, and
set `FORGEJO_EXPECTED_VERSION` to the version the release publishes; the
workflow derives that value from the tag, and by hand you state it. The script
needs `dpkg-deb` and `curl`. It reads the token from the environment into a
private `netrc`, never onto a command line or into a URL, refuses a plaintext
origin, and follows no redirect, so the credential never leaves the origin you
named.

## Read the result

The job prints one line for each package: `published` when the registry
accepted the upload, or `unchanged` when the registry already held that exact
file. Both are success.

## Recover

A failed run leaves any package it already uploaded in place; rerun the job and
the uploaded package reports `unchanged`.

A digest mismatch means the registry holds a different file under a name this
release already published. The publisher never deletes and never overwrites,
and neither should you: a version that users may already have installed is
not a version to swap out underneath them. Work out why the bytes differ, then
publish the correct build as a new version.

## Consume the registry

Users add the registry once. For a private registry, put the read-only
credential in `/etc/apt/auth.conf.d/forge-downloads.conf`, owned by root with
mode `0600`:

```text
machine forge.example.net
login YOUR_DOWNLOAD_USERNAME
password YOUR_READ_ONLY_TOKEN
```

Do not put credentials in the source URL or a shell command. Fetch the key
using the protected file, then require it for this repository:

```bash
sudo install -d -m 0755 /etc/apt/keyrings
sudo curl --fail --silent --show-error \
  --netrc-file /etc/apt/auth.conf.d/forge-downloads.conf \
  https://forge.example.net/api/packages/downloads/debian/repository.key \
  -o /etc/apt/keyrings/forge-downloads.asc
sudo chmod 0644 /etc/apt/keyrings/forge-downloads.asc
echo "deb [signed-by=/etc/apt/keyrings/forge-downloads.asc] https://forge.example.net/api/packages/downloads/debian stable main" \
  | sudo tee /etc/apt/sources.list.d/forge-downloads.list
sudo apt update
sudo apt install widget
```

Forgejo signs the repository indexes with its own key. Never use `trusted=yes`
or disable APT signature checks. The release's Cosign bundle separately covers
the package bytes for anyone verifying a downloaded file.
