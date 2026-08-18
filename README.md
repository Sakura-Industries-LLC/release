# Meigma release workflows

This repository defines the reusable workflows and repository contract that Meigma uses to build, sign, attest, and publish Go binaries through GitHub Releases and multi-architecture OCI images through GHCR.

## Documentation

- [Configure GitHub releases](docs/how-to/configure-github-releases.md)
- [Configure OCI image publication](docs/how-to/configure-oci-images.md)
- [Rehearse and recover GitHub releases](docs/how-to/rehearse-and-recover-github-releases.md)
- [Upgrade GitHub release workflows](docs/how-to/upgrade-github-release-workflows.md)
- [GitHub release contract reference](docs/reference/github-release-contract.md)
- [OCI image contract reference](docs/reference/oci-image-contract.md)
- [Copyable Go release example](examples/go-release/)

Consumer repositories call the reusable workflows at a full commit SHA. The documented revision is `fb8c8098ff27968fb3070e928c00e925f38c698e`.
