# Go release example

This directory models a repository named `example` with module `example.com/meigma/release-consumer` and command `./cmd/example`. It contains the minimum source needed to build GitHub Release assets and a multi-architecture OCI image, plus a disabled package-repository dispatch job. It is not a complete CI policy: add the consumer repository's own build, test, review, and branch-protection controls.

See [Configure GitHub Releases](../../docs/how-to/configure-github-releases.md) for release credential setup and [Configure OCI image publication](../../docs/how-to/configure-oci-images.md) for image configuration, publication, and verification. [Set up the shared package repository](../../docs/how-to/set-up-package-repository.md) defines the central receiver, producer policy, public keys, and signing prerequisites; [Install release-cli from native package repositories](../../docs/how-to/install-release-cli-from-package-repositories.md) shows the resulting client flow. See [Rehearse and recover GitHub Releases](../../docs/how-to/rehearse-and-recover-github-releases.md) before the first publication. Use [Upgrade GitHub Release workflows](../../docs/how-to/upgrade-github-release-workflows.md) to change the pinned revision. The reusable interfaces are defined in the [GitHub Release contract](../../docs/reference/github-release-contract.md), [OCI image contract](../../docs/reference/oci-image-contract.md), and [package repository contract](../../docs/reference/package-repository-contract.md).

## Files to copy

Copy these release files into an existing Go repository, preserving their paths:

- `.github/workflows/release-please.yml`
- `.github/workflows/release.yml`
- `.goreleaser.yaml`
- `apko.yaml`
- `melange.yaml`
- `.release-please-manifest.json`
- `release-please-config.json`
- `mise.toml`
- `mise.lock`

To reproduce the complete minimal consumer in a new empty repository, also copy:

- `go.mod`
- `cmd/example/main.go`

Do not copy this README into the consumer repository.

## Release build

The producer workflow sets up `release-cli` and runs:

```text
release-cli stage --profile go --dist dist
```

The stage command runs `goreleaser release --clean --skip=publish` under mise's environment, then validates the release bundle and writes the OCI input projection. GoReleaser shells out to the mise-managed Go, Syft, and Cosign executables during the build.

The GoReleaser configuration packages each Linux build as DEB, RPM, and APK
and emits one SBOM for every archive and native package. These standalone
packages are GitHub Release assets. The example does not configure native
RPM or APK signing. Its package-repository request remains disabled until the
producer is reviewed, allowlisted, and configured with both signing keys.

GoReleaser has no command-line distribution-directory option in this invocation. The consumer's `.goreleaser.yaml` must write the same distribution directory that the workflow passes to `release-cli stage --dist`. This example uses GoReleaser's default `dist` directory and passes `--dist dist`.

The `--clean` option deletes and rebuilds `dist`. Keep `release.disable: true` in `.goreleaser.yaml`; `--skip=publish` is a second boundary against GoReleaser publication. Keep `changelog.disable: true` because Release Please owns release notes.

## Values to replace

Replace these project-specific example values:

- `example.com/meigma/release-consumer` in `go.mod` with the consumer's module path.
- `./cmd/example` in `.goreleaser.yaml` with the consumer command package.
- Project name, build ID, archive ID, nFPM ID, binary name, and Release Please package name `example` with the consumer's project and binary names.
- Package name, vendor, homepage, maintainer, description, license, and installed command path in `.goreleaser.yaml` and `melange.yaml`.
- Package name, entrypoint, image annotations, and source URL in `apko.yaml`.
- The literal command name and default output in `cmd/example/main.go` if you copy the sample command.
- Branch name `main` in `.github/workflows/release-please.yml` if the consumer uses another default branch.
- `initial-version` value `0.1.0` in `release-please-config.json` if the first intended release differs.
- Manifest value `0.0.0` in `.release-please-manifest.json` if the consumer already has a release. Use its latest released version without the `v` prefix.
- Linker variables `main.version` and `main.commit` in `.goreleaser.yaml` if the consumer command exposes version data through different variables. The copied sample defines both variables and prints `example <version> (<commit>)` for `--version`.
- Package repository owner and repository name in `.github/workflows/release.yml` with the reviewed central receiver.

The release-asset and OCI jobs use one full commit SHA for their reusable
workflow references and checksum signing identity. That SHA is their consumer
pin for the complete release unit. The current baseline pin is the `v0.1.3`
release revision, `0fc99489d31d400bc3f69d6636d60e7d3f3d0251`.

The disabled package-repository job uses the `v0.1.16` revision,
`583937edadfbae183e49f16df46b98e0b36807ba`, because that revision introduced
the dispatch workflow and multi-version repository fix. Upgrade the complete
release unit to a reviewed current revision before enabling the job.

Keep these contract values unchanged:

- all four reusable workflow references at `0fc99489d31d400bc3f69d6636d60e7d3f3d0251`;
- `checksum-signing-workflow-ref` value `meigma/release/.github/workflows/go-pre-publish.yml@0fc99489d31d400bc3f69d6636d60e7d3f3d0251`;
- disabled package-repository workflow reference at `583937edadfbae183e49f16df46b98e0b36807ba`;
- organization variable `MEIGMA_RELEASE_APP_CLIENT_ID`;
- organization secret `MEIGMA_RELEASE_APP_PRIVATE_KEY`; and
- the locked Go 1.26.6, GoReleaser 2.17.1, Syft 1.51.0, Cosign 3.1.3, GitHub CLI 2.97.0, Melange 0.59.1, and apko 1.2.37 versions unless the shared workflow contract is deliberately updated.

The caller sets `publish-image: false`, `publish-release: false`, and `publish-package-repository: false`. The first run therefore leaves a populated draft without writing to GHCR or dispatching to a central package repository. After inspecting the draft and `oci-image` artifact, change the image and GitHub Release inputs to `true` and follow the recovery guide to publish through the same tag and release. Enable package-repository dispatch only after the release is public, the central receiver allowlists the producer, native RPM and APK signing is configured, and the receiver's protected environment is ready.

The OCI builder retains the signed APK repository, apko lock, SPDX files, and OCI layout in the `oci-image` workflow artifact. The separate publisher verifies that artifact before pushing, signing, and attesting `ghcr.io/owner/repository`.
