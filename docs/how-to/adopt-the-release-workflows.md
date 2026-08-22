# Adopt the release workflows

Use this guide to add the reusable release unit to an existing Go application
repository. Complete [Prepare your GitHub organization](prepare-your-github-organization.md)
first.

The supported unit releases one application and one binary from one repository.
Use a separate repository and caller for each additional application.

## Select one immutable release unit

Choose a published `meigma/release` tag, review that release's workflow and
contract changes, and resolve the tag to a full commit SHA:

```bash
export RELEASE_TAG="$(gh api repos/meigma/release/releases/latest --jq .tag_name)"
export RELEASE_REVISION="$(gh api "repos/meigma/release/commits/$RELEASE_TAG" --jq .sha)"
[[ "$RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]
test "$(gh api "repos/meigma/release/commits/$RELEASE_REVISION" --jq .sha)" = \
  "$RELEASE_REVISION"
printf '%s %s\n' "$RELEASE_TAG" "$RELEASE_REVISION"
```

Do not use a branch, moving tag, abbreviated SHA, or an independently selected
`release-cli` version. The full commit selects the reusable workflows, their
sibling setup action, the CLI release stamp, and the accepted signer identities.

## Copy the maintained configuration

From a checkout of this repository, copy the maintained example into the
producer. Do not overwrite existing project configuration. Merge applicable
settings by hand when a destination exists.

```bash
export RELEASE_EXAMPLE=/absolute/path/to/release/examples/go-release
export PRODUCER=/absolute/path/to/widget
mkdir -p "$PRODUCER/.github/workflows"
cp "$RELEASE_EXAMPLE/.github/workflows/release-please.yml" \
  "$PRODUCER/.github/workflows/"
cp "$RELEASE_EXAMPLE/.github/workflows/release.yml" \
  "$PRODUCER/.github/workflows/"
cp "$RELEASE_EXAMPLE/.goreleaser.yaml" "$PRODUCER/"
cp "$RELEASE_EXAMPLE/apko.yaml" "$PRODUCER/"
cp "$RELEASE_EXAMPLE/melange.yaml" "$PRODUCER/"
cp "$RELEASE_EXAMPLE/.release-please-manifest.json" "$PRODUCER/"
cp "$RELEASE_EXAMPLE/release-please-config.json" "$PRODUCER/"
cp "$RELEASE_EXAMPLE/mise.toml" "$PRODUCER/"
cp "$RELEASE_EXAMPLE/mise.lock" "$PRODUCER/"
```

Keep the producer's existing `go.mod`, source, CI, review policy, and branch
protection. The example is release configuration, not a complete repository
policy.

In `.github/workflows/release.yml`, replace every
`REPLACE_WITH_RELEASE_COMMIT_SHA` occurrence with `$RELEASE_REVISION`. Confirm
that no placeholder or second full-SHA workflow ref remains:

```bash
cd "$PRODUCER"
! grep -R 'REPLACE_WITH_RELEASE_COMMIT_SHA' .github/workflows
refs="$(grep -Eo '@[0-9a-f]{40}' .github/workflows/release.yml | sort -u)"
test "$refs" = "@$RELEASE_REVISION"
```

Update all reusable workflow references and every
`checksum-signing-workflow-ref` together whenever the release unit changes. A
mixed revision is not a supported migration state.

## Adapt the GoReleaser configuration

Edit `.goreleaser.yaml` for the producer's command:

- set `project_name`, build ID, archive ID, binary name, and `main` package;
- keep `CGO_ENABLED=0` only if the command is genuinely static on all supported
  targets;
- keep Darwin, Linux, and Windows on `amd64` and `arm64`;
- update linker variables if the command does not define `main.version` and
  `main.commit`;
- update vendor, homepage, maintainer, description, license, and installed
  binary path;
- keep the nFPM ID as `release` if native package signing may be enabled;
- keep DEB, RPM, and APK generated from the canonical Linux builds;
- keep one SBOM for each archive and native package;
- keep `checksums.txt` and its keyless Cosign bundle; and
- keep both `changelog.disable: true` and `release.disable: true`.

`release-cli stage --profile go --dist dist` invokes exactly:

```text
goreleaser release --clean --skip=publish
```

The GoReleaser release pipe must remain disabled. Release Please owns the
notes, tag, and initial draft; the reusable publishers own remote mutation.

The maintained `homebrew_casks` and `scoops` entries use `skip_upload: true`.
Set their source repository and generated control names now, even if those
publishers remain disabled. Replace `TAP` and `BUCKET` only after the adopter-owned
destination repositories exist. GoReleaser generates controls; it never writes
those repositories directly.

## Configure Release Please

Edit `release-please-config.json`:

- set `package-name` to the application name;
- choose the first stable version in `initial-version`;
- keep `include-v-in-tag: true`;
- keep `include-component-in-tag: false`;
- keep `force-tag-creation: true`; and
- keep `draft: true`.

Set `.` in `.release-please-manifest.json` to the latest released version
without `v`. Use `0.0.0` only for a repository with no prior release.

If the default branch is not `main`, change the branch filter in
`.github/workflows/release-please.yml`. The version workflow uses the
adopter-owned App credentials configured in the organization guide.

## Lock the release tools

Merge the example's tool declarations into the producer's `mise.toml`. The
release path requires locked versions of:

- Go;
- GoReleaser;
- Syft;
- Cosign;
- GitHub CLI;
- Melange; and
- apko.

Regenerate the supported-platform lock after changing a declaration:

```bash
mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64
mise install --locked
mise exec -- goreleaser check
mise exec -- go list ./cmd/...
```

Commit `mise.toml` and `mise.lock` together. The called workflows do not install
an undeclared replacement when the lock is incomplete.

## Adapt Melange and apko

In `melange.yaml`:

- use the application binary as the package name;
- keep `version: ${{vars.version}}`;
- keep `x86_64` and `aarch64`;
- replace the organization metadata and SPDX license expression; and
- install the staged `application` file at `/usr/bin/<binary>` with mode `0755`
  and ownership `0:0`.

In `apko.yaml`:

- consume the same Melange package;
- set the entrypoint to `/usr/bin/<binary>`;
- keep `amd64` and `arm64`;
- keep numeric runtime user and group `65532`; and
- set title, description, source, and SPDX license annotations.

The current example includes CA certificates. Keep them for a command that
makes TLS connections. Add other runtime files through apko packages rather
than copying the build environment into the image.

The OCI builder packages the canonical staged Linux binaries. Do not compile a
second binary in Melange or apko.

## Configure optional signing

Both signing inputs default to `false` in the maintained caller.

### Sign and notarize macOS archives

The example GoReleaser configuration contains a guarded `notarize.macos` block.
Before setting `sign-and-notarize-macos: true`, add these repository or
organization secrets to the producer:

- `MACOS_SIGN_P12`: base64-encoded Developer ID Application certificate;
- `MACOS_SIGN_PASSWORD`;
- `MACOS_NOTARY_KEY`: base64-encoded App Store Connect API private key;
- `MACOS_NOTARY_KEY_ID`; and
- `MACOS_NOTARY_ISSUER_ID`.

Map all five secrets in the `release-assets` call. The workflow fails before
staging if any enabled credential is absent. Apple rejection or timeout also
fails before a publisher runs.

### Sign RPM and APK packages

Before setting `sign-native-packages: true`, add:

- `RPM_SIGNING_KEY`: base64-encoded OpenPGP private key;
- `RPM_SIGNING_PASSPHRASE`;
- `APK_SIGNING_KEY`: base64-encoded RSA private key; and
- `APK_SIGNING_PASSPHRASE`.

Map all four secrets in the `release-assets` call. Keep the `.goreleaser.yaml`
RPM and APK `key_file` expressions supplied by the example. The workflow writes
owner-only temporary key files, GoReleaser signs packages before checksums are
generated, and the workflow removes the files after staging.

Give the corresponding public producer keys to the central package-repository
operator. Do not give that operator the producer private keys.

## Rehearse before enabling publishers

The copied caller leaves all remote publishers disabled:

```yaml
publish-image: false
publish-release: false
publish-homebrew: false
publish-scoop: false
publish-package-repository: false
```

Submit the complete release configuration through review. After it reaches the
default branch, confirm that GitHub recognizes both workflows:

```bash
export REPOSITORY="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
gh workflow view release-please.yml --repo "$REPOSITORY"
gh workflow view release.yml --repo "$REPOSITORY"
```

Follow [Operate and recover releases](operate-and-recover-releases.md) to create
a stable candidate, inspect the populated draft and OCI artifact, and recover
any failure.

After the rehearsal passes, set `publish-image` and `publish-release` to `true`
in the same reviewed commit. Keep Homebrew, Scoop, and package-repository
publication disabled until their external repositories, App installations,
keys, environments, and required checks exist.

A successful stable release publishes:

- the verified closed asset set in one GitHub Release;
- `ghcr.io/<owner>/<repository>` for Linux `amd64` and `arm64`; and
- the exact image tag plus eligible `MAJOR.MINOR`, `MAJOR`, and `latest`
  channels.

After the first image publication, confirm GHCR visibility as described in the
organization guide. Consumers that require repeatability must use the
`ghcr.io/<owner>/<repository>@sha256:<digest>` output, not a moving channel tag.

To release another application, repeat this guide in another repository. Do
not add a second command, component-prefixed tag, or second image name to the
same caller.
