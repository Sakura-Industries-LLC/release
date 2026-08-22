# Meigma release workflows

This repository defines the reusable workflows and repository contracts that Meigma uses to build, sign, attest, and publish Go binaries through GitHub Releases, multi-architecture OCI images through GHCR, and native Linux packages through a static Cloudflare R2 repository. It also builds and publishes `release-cli`, which owns release verification and publication behavior. The repository's tagged release builds the CLI from its own source and supplies that binary to the reusable workflows.

## Documentation

- [Install `release-cli` with mise](docs/how-to/install-release-cli-with-mise.md)
- [Install `release-cli` with Nix](docs/how-to/install-release-cli-with-nix.md)
- [Configure GitHub releases](docs/how-to/configure-github-releases.md)
- [Configure OCI image publication](docs/how-to/configure-oci-images.md)
- [Rehearse and recover GitHub releases](docs/how-to/rehearse-and-recover-github-releases.md)
- [Upgrade GitHub release workflows](docs/how-to/upgrade-github-release-workflows.md)
- [Set up the shared package repository](docs/how-to/set-up-package-repository.md)
- [`release-cli` contract reference](docs/reference/release-cli-contract.md)
- [GitHub release contract reference](docs/reference/github-release-contract.md)
- [OCI image contract reference](docs/reference/oci-image-contract.md)
- [Package repository contract reference](docs/reference/package-repository-contract.md)
- [Copyable Go release example](examples/go-release/)
- [Copyable Nix consumer example](examples/nix-release-cli/)

Consumer repositories call the reusable workflows at one full commit SHA. The current released pin is `0fc99489d31d400bc3f69d6636d60e7d3f3d0251` (`v0.1.3`).
