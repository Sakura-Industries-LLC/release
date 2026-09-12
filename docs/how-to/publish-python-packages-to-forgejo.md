# Publish Python packages to Forgejo

Use this guide to serve a release's Python distributions from a Forgejo PyPI
package registry, so users install and update with `pip` or `uv` instead of
downloading an archive. Rerunning the publisher accepts distributions that
already exist with identical bytes and refuses to replace distributions whose
bytes differ.

Complete [Adopt the release workflows](adopt-the-release-workflows.md) first.
The PyPI publisher consumes a producer-built artifact the same way the Debian
publisher does, so the distributions must be members of a signed
`checksums.txt` the producer builds and signs in the job that built them.

## Build the distributions into the release bundle

The producer must upload one artifact whose root holds exactly four files:

```text
<stem>-<version>.tar.gz
<stem>-<version>-py3-none-any.whl
checksums.txt
checksums.txt.sigstore.json
```

`<stem>` is the PEP 625 file stem: the project name with every run of `-`,
`_`, or `.` folded to one `_`, so `dntls-testnet-sdk` builds
`dntls_testnet_sdk-1.2.3.tar.gz`. `checksums.txt` is `sha256sum` over the two
distributions with relative names, and `checksums.txt.sigstore.json` is the
keyless Cosign bundle over it. The set is closed: a third distribution, a
stray `.gitignore` that `uv build --out-dir` leaves behind, a missing control
file, or a prerelease version fails the run before any request is sent. A
shared index serves stable `MAJOR.MINOR.PATCH` releases only.

When the job publishes, that version must also be the version the tag names:
`v1.2.3` and the component form `sdk/python/v1.2.3` both name `1.2.3`.

## Prepare the registry

Create the owner (a user or organization) that will hold the packages, and a
publisher account with package write permission on it. Issue that account a
token with the `write:package` scope and nothing else; that scope also permits
the package reads this publisher performs.

Record three non-secret values: the forge origin (for example
`https://forge.example.net`), the owner, and the publisher account name.

## Store the publisher token

Add one repository or organization secret to the producer, for example
`FORGEJO_PUBLISHER_TOKEN`. Restrict it to the repositories that publish into
the registry. The token is read only by the step that talks to the registry.

## Call the publisher

Add one job to the producer's release workflow after the job that builds and
signs the distributions:

```yaml
  publish-pypi:
    name: Publish Python distributions
    needs: [python-distributions]
    permissions:
      actions: read
      attestations: read
      contents: read
    uses: Sakura-Industries-LLC/release/.github/workflows/publish-forgejo-pypi.yml@<full-sha>
    with:
      artifact-id: ${{ needs.python-distributions.outputs.artifact-id }}
      artifact-digest: ${{ needs.python-distributions.outputs.artifact-digest }}
      checksum-signing-workflow-ref: ${{ github.repository }}/.github/workflows/publish-python.yml@${{ github.ref }}
      package-name: widget
      origin: https://forge.example.net
      owner: downloads
      publisher-username: publisher
      publish-packages: false
    secrets:
      publisher-token: ${{ secrets.FORGEJO_PUBLISHER_TOKEN }}
```

`package-name` is the PEP 503 normalized project name: lowercase, hyphen
separated, never the underscore file stem. `checksum-signing-workflow-ref`
names the workflow whose identity signed `checksums.txt`, which for this
format is the producer's own build workflow.

The publisher installs `cosign` and `uv` through the caller's `mise.toml`, so
that file must pin both.

## Rehearse before you publish

Keep `publish-packages: false` until the registry, account, and token exist.
The job then verifies the artifact handoff, the closed bundle, and the
distribution set, reads the registry's simple index, and reports what an
upload would send without sending it. That rehearsal runs from any ref, so a
`workflow_dispatch` on a pull request branch exercises the real path without
creating a tag. Publication is the only part that requires a tag ref, and that
tag must resolve to the commit the workflow is running.

You can also run the transport by hand against a staging forge, which is the
same code the workflow runs:

```bash
cd /path/to/release
FORGEJO_ORIGIN=https://forge.example.net \
FORGEJO_OWNER=downloads \
FORGEJO_PACKAGE=widget \
FORGEJO_USERNAME=publisher \
FORGEJO_PUBLISH=false \
  bash .github/actions/publish-forgejo-pypi/publish-pypi.sh /path/to/dist
```

Without `FORGEJO_TOKEN` the run stops after the set checks, because a private
index refuses an unauthenticated read. Pass the token to have a rehearsal
report the registry state too.

To upload, set `FORGEJO_PUBLISH=true`, pass the token in `FORGEJO_TOKEN`, and
set `FORGEJO_EXPECTED_VERSION` to the version the release publishes; the
workflow derives that value from the tag, and by hand you state it. The script
needs `curl` and a `uv` it can find on `PATH`, through `mise`, or at
`RELEASE_UV_PATH`; `uv` runs the pinned twine that performs the upload. It
reads the token from the environment into a private `netrc` for the index read
and into twine's environment for the upload, never onto a command line or into
a URL, refuses a plaintext origin, ignores `~/.curlrc` and `~/.pypirc`, and
follows no redirect, so the credential never leaves the origin you named.

## Read the result

The job prints one line for each distribution: `published` when the registry
accepted the upload, or `unchanged` when the registry already listed that file
with the bytes this run built. Both are success. When both are already
published the job prints `<name> <version> is already published, identical`
and uploads nothing.

## Recover

A PyPI registry refuses to replace a file, so the publisher reads the simple
index before it uploads and sends only what the registry does not already
hold. A failed run that uploaded one of the two distributions is therefore
resumable: rerun the job, and it reports the uploaded file as `unchanged` and
uploads the other.

A digest mismatch means the registry holds a different file under a name this
release already published. The publisher never deletes and never overwrites,
and neither should you: a version that users may already have installed is not
a version to swap out underneath them. Work out why the bytes differ, then
publish the correct build as a new version.

## Consume the registry

Users add the registry once. For a private registry, put the read-only
credential in `~/.netrc`, owned by the user with mode `0600`:

```text
machine forge.example.net
login YOUR_DOWNLOAD_USERNAME
password YOUR_READ_ONLY_TOKEN
```

Do not put credentials in the index URL, a shell command, or a checked-in
`pip.conf`. Both `pip` and `uv` read `~/.netrc`:

```bash
pip install --index-url https://forge.example.net/api/packages/downloads/pypi/simple widget
uv pip install --index-url https://forge.example.net/api/packages/downloads/pypi/simple widget
```

The index serves the SHA-256 of every file it lists, so pip and uv check the
bytes they download against the registry's record. The release's Cosign bundle
separately covers those bytes for anyone verifying a downloaded distribution
against `checksums.txt`.
