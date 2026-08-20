# Install `release-cli` with Nix

Use the repository flake to build and run a tagged `release-cli` revision. The
flake supports Darwin and Linux on `arm64` and `amd64`.

This guide is for direct command-line and development-shell use. Repositories
that call the reusable Meigma release workflows do not install or version
`release-cli` separately. Their pinned workflow revision selects the matching
CLI release.

## Run without installing

Run the current release directly from GitHub:

```bash
nix run github:meigma/release/v0.1.3#release-cli -- version --json
```

The current release reports:

```json
{
  "schema": "release.dev/result/v1",
  "command": "version",
  "ok": true,
  "result": {
    "version": "0.1.3",
    "commit": "0fc99489d31d400bc3f69d6636d60e7d3f3d0251",
    "protocol": 1
  }
}
```

The first invocation builds `release-cli` when the result is not already in a
configured Nix cache.

## Install into your Nix profile

Install the tagged package for your user account:

```bash
nix profile add github:meigma/release/v0.1.3#release-cli
release-cli version --json
```

Pin an explicit tag in interactive installation commands. For project and CI
use, add the flake as an input so `flake.lock` records its exact commit and
content hash.

## Add `release-cli` to a project flake

Add the release flake as an input and make it follow the project's `nixpkgs`
input:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    release = {
      url = "github:meigma/release/v0.1.3";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { nixpkgs, release, ... }:
    let
      system = "aarch64-darwin";
      pkgs = import nixpkgs { inherit system; };
    in
    {
      devShells.${system}.default = pkgs.mkShellNoCC {
        packages = [ release.packages.${system}.release-cli ];
      };
    };
}
```

Replace `aarch64-darwin` with the project's host system. Supported values are:

- `aarch64-darwin`
- `aarch64-linux`
- `x86_64-darwin`
- `x86_64-linux`

Lock and verify the input:

```bash
nix flake lock
nix develop --command release-cli version --json
```

The [Nix consumer example](../../examples/nix-release-cli/) exposes the package,
app, and development shell on all four systems.

## Update the pinned release

Change `inputs.release.url` to the new tag, then update only that input:

```bash
nix flake update release
nix develop --command release-cli version --json
```

Commit the changed `flake.lock` with the version update.

## Understand the build and trust boundary

The flake builds `release-cli` from the exact Git revision in the locked input.
It uses the locked Nixpkgs revision, a fixed Go 1.26.6 source hash,
and a fixed Go module dependency hash. It embeds the manifest version and source
revision in the resulting binary.

This path does not install the prebuilt GitHub Release archive and does not run
GitHub artifact-attestation verification. Nix instead verifies every locked
flake input and fixed-output dependency. Use the [mise installation
path](install-release-cli-with-mise.md) when you need the released archive,
checksum, and GitHub artifact-attestation checks.
