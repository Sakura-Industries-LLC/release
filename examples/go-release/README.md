# Go release example

This directory is the maintained template for one static Go application in one
repository. It includes the release workflows, a minimal command, GitHub
Release and OCI configuration, Homebrew and Scoop control generation, and a
native package-repository request.

It is not a complete repository policy. Add the adopter's CI, review, rulesets,
and ownership controls.

## Copy the example

Copy these paths into an existing Go repository without overwriting its source
or module files:

- `.github/workflows/release-please.yml`
- `.github/workflows/release.yml`
- `.goreleaser.yaml`
- `apko.yaml`
- `melange.yaml`
- `.release-please-manifest.json`
- `release-please-config.json`
- `mise.toml`
- `mise.lock`

For a new disposable repository, also copy `go.mod` and `cmd/example/`. Do not
copy this README into the producer.

## Replace the template values

Before the workflows run:

1. Replace every `REPLACE_WITH_RELEASE_COMMIT_SHA` in
   `.github/workflows/release.yml` with one reviewed, full 40-character
   `meigma/release` commit SHA.
2. Replace `OWNER/REPOSITORY` in `.goreleaser.yaml` and `apko.yaml` with the
   producer owner and repository.
3. Replace `HOMEBREW-OWNER`, `HOMEBREW-TAP`, `SCOOP-OWNER`,
   `SCOOP-BUCKET`, and the `PACKAGE-REPOSITORY-*` values only after those
   adopter-owned destinations exist.
4. Replace the `example` project, package, binary, cask, manifest, command path,
   module path, and Release Please package name.
5. Replace the organization metadata, maintainer, description, homepage, and
   SPDX license expression.
6. Change the Release Please branch and manifest version when the repository
   does not use a new `main`-branch release history.
7. Update the linker variables when the command does not define
   `main.version` and `main.commit`.

The one full SHA selects every reusable workflow and checksum signer identity.
It also selects the sibling setup action and the CLI release stamp. Do not mix
release-unit revisions or add an independent CLI pin.

## Default safety controls

The caller begins with:

```yaml
sign-and-notarize-macos: false
sign-native-packages: false
publish-image: false
publish-release: false
publish-homebrew: false
publish-scoop: false
publish-package-repository: false
```

The first tag therefore builds and verifies release assets and the OCI layout,
populates a draft GitHub Release, and performs a dry-run registry plan. It does
not write GHCR, open a tap or bucket pull request, dispatch native package
publication, or make the draft public.

The supported publication configuration enables `publish-image` and
`publish-release` together. Enable Homebrew, Scoop, or package-repository
publication only in a run that also makes the GitHub Release public.

The Homebrew and Scoop entries use `skip_upload: true`. GoReleaser generates
their controls inside the authoritative Actions artifact, but the dedicated
publishers own destination pull requests. The package-repository request stays
disabled until producer-native RPM and APK signing, central policy, public
keys, R2, and the protected receiver environment exist.

## Documentation

- [Release your first Go application](../../docs/tutorials/release-your-first-go-application.md)
- [Prepare your GitHub organization](../../docs/how-to/prepare-your-github-organization.md)
- [Adopt the release workflows](../../docs/how-to/adopt-the-release-workflows.md)
- [Add Homebrew and Scoop](../../docs/how-to/add-homebrew-and-scoop.md)
- [Operate a native package repository](../../docs/how-to/operate-a-native-package-repository.md)
- [Operate and recover releases](../../docs/how-to/operate-and-recover-releases.md)
- [Release system reference](../../docs/reference/release-system.md)
