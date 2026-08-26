# Release workflows

This repository publishes `release-cli` and reusable GitHub Actions workflows
for releasing static Go binaries from one repository. A release can produce
GitHub Release assets, a multi-architecture image in GHCR, Homebrew and Scoop
update pull requests, and signed DEB, RPM, and APK repositories in Cloudflare
R2.

## Supported release

The supported application contract is intentionally narrow:

- one Go repository, one unscoped tag stream, and one GHCR image;
- Linux `amd64` and `arm64` must publish the same nonempty set of static
  binary names;
- stable, unscoped `vMAJOR.MINOR.PATCH` tags;
- static Darwin, Linux, and Windows binaries for `amd64` and `arm64`;
- Linux `amd64` and `arm64` images at `ghcr.io/<owner>/<repository>`;
- DEB, RPM, and APK packages;
- cask-only Homebrew taps; and
- root-layout Scoop buckets.

Each consumer pins every reusable workflow and signer identity to one reviewed,
full `meigma/release` commit SHA. The workflows, setup action, and
`release-cli` at that commit form one release unit.

## Release flow

```text
Release Please pull request
  -> stable tag and draft GitHub Release
  -> build, sign, and verify release assets
  -> build and verify the OCI image
  -> publish and attest the image
  -> attest assets and publish the GitHub Release
  -> open reviewed Homebrew and Scoop pull requests
  -> optionally request central native-package publication
```

Publication controls are disabled in the
[copyable Go example](examples/go-release/). Rehearse the complete build and
inspect its draft and workflow artifacts before enabling destinations.

## Documentation

- [Release your first Go application](docs/tutorials/release-your-first-go-application.md)
- [Prepare your GitHub organization](docs/how-to/prepare-your-github-organization.md)
- [Adopt the release workflows](docs/how-to/adopt-the-release-workflows.md)
- [Add Homebrew and Scoop](docs/how-to/add-homebrew-and-scoop.md)
- [Operate a native package repository](docs/how-to/operate-a-native-package-repository.md)
- [Operate and recover releases](docs/how-to/operate-and-recover-releases.md)
- [Install `release-cli`](docs/how-to/install-release-cli.md)
- [Release system reference](docs/reference/release-system.md)
- [`release-cli` reference](docs/reference/release-cli.md)
- [Architecture and trust](docs/explanation/architecture-and-trust.md)

## License

Licensed under either the [Apache License 2.0](LICENSE-APACHE) or the
[MIT License](LICENSE-MIT), at your option.
