# Meigma release workflows

This repository defines the reusable workflows and repository contract that Meigma uses to build, sign, attest, and publish Go binaries through GitHub Releases and multi-architecture OCI images through GHCR. It also builds and publishes `release-cli`, which the Go producer uses to validate staged release artifacts. The repository's tagged release builds the CLI from its own source and supplies that binary to the producer workflow.

## Documentation

- [Configure GitHub releases](docs/how-to/configure-github-releases.md)
- [Configure OCI image publication](docs/how-to/configure-oci-images.md)
- [Rehearse and recover GitHub releases](docs/how-to/rehearse-and-recover-github-releases.md)
- [Upgrade GitHub release workflows](docs/how-to/upgrade-github-release-workflows.md)
- [`release-cli` contract reference](docs/reference/release-cli-contract.md)
- [GitHub release contract reference](docs/reference/github-release-contract.md)
- [OCI image contract reference](docs/reference/oci-image-contract.md)
- [Copyable Go release example](examples/go-release/)

Consumer repositories call the reusable workflows at one full commit SHA. The current released pin is `5fb7584b465ab9c0ca4e1057c7b2ca694f950d59` (`v0.1.1`).
