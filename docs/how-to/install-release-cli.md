# Install `release-cli`

Use this guide to install `release-cli` for direct command-line use. Repositories
that call the reusable workflows do not install or pin the CLI separately; the
one workflow revision selects it as part of the release unit.

Sakura distributes `release-cli` through GitHub Releases. Mise and direct
archive installs use those release assets; Nix builds from the selected source
revision.

## Select a released version

For mise, Nix, or a direct archive, select one published stable release and
resolve its tag to a full commit:

```bash
export RELEASE_TAG="$(gh api repos/Sakura-Industries-LLC/release/releases/latest --jq .tag_name)"
export RELEASE_VERSION="${RELEASE_TAG#v}"
export RELEASE_REVISION="$(gh api "repos/Sakura-Industries-LLC/release/commits/$RELEASE_TAG" --jq .sha)"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]
printf 'Installing release-cli %s from %s\n' "$RELEASE_VERSION" "$RELEASE_REVISION"
```

For automation, set `RELEASE_TAG`, `RELEASE_VERSION`, and `RELEASE_REVISION` to
a reviewed release rather than resolving the latest release on every run.

## Install with mise

Use mise's built-in GitHub backend:

```bash
mise use "github:Sakura-Industries-LLC/release@$RELEASE_VERSION"
mise exec -- release-cli version --json
```

Mise writes an explicit project version:

```toml
[tools]
"github:Sakura-Industries-LLC/release" = "<version>"
```

For a temporary invocation:

```bash
mise x "github:Sakura-Industries-LLC/release@$RELEASE_VERSION" -- release-cli version --json
```

For a user-level installation:

```bash
mise use -g "github:Sakura-Industries-LLC/release@$RELEASE_VERSION"
mise exec -- release-cli version --json
```

`release-cli` is not registered under a short name in the shared mise registry.
To use one locally, add:

```toml
[tool_alias]
release-cli = "github:Sakura-Industries-LLC/release"

[tools]
release-cli = "<version>"
```

The released archives cover Darwin, Linux, and Windows on `amd64` and `arm64`.
Mise's verified GitHub backend selects the host archive and checks its release
checksum and GitHub artifact attestation.

To update, review a newer release and run:

```bash
mise use "github:Sakura-Industries-LLC/release@$RELEASE_VERSION"
mise install --locked
mise exec -- release-cli version --json
```

Commit the changed `mise.toml` and `mise.lock` when the tool is project-scoped.

## Install with Nix

The repository flake builds `release-cli` from source for Darwin and Linux on
`aarch64` and `x86_64`.

Run the selected immutable revision without installing:

```bash
nix run "github:Sakura-Industries-LLC/release/$RELEASE_REVISION#release-cli" -- version --json
```

Install it into the current profile:

```bash
nix profile add "github:Sakura-Industries-LLC/release/$RELEASE_REVISION#release-cli"
release-cli version --json
```

For a project flake, add a tagged input and make it follow the project's
`nixpkgs` input:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    release = {
      url = "github:Sakura-Industries-LLC/release/vMAJOR.MINOR.PATCH";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { nixpkgs, release, ... }:
    let
      system = "aarch64-darwin";
      pkgs = import nixpkgs { inherit system; };
    in {
      devShells.${system}.default = pkgs.mkShellNoCC {
        packages = [ release.packages.${system}.release-cli ];
      };
    };
}
```

Replace the tag and system. Supported systems are `aarch64-darwin`,
`aarch64-linux`, `x86_64-darwin`, and `x86_64-linux`.

Lock and run the input:

```bash
nix flake lock
nix develop --command release-cli version --json
```

To update, change the input tag and update only that input:

```bash
nix flake update release
nix develop --command release-cli version --json
```

Commit `flake.lock`. This path builds from the locked source and fixed-output
dependencies. It does not install or verify the prebuilt GitHub Release archive.


## Install a verified GitHub archive

Use this path when mise or Nix is unavailable. The following Bash procedure
supports Darwin and Linux on `amd64` and `arm64`.

Derive the released archive name:

```bash
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) printf 'Unsupported operating system\n' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf 'Unsupported architecture\n' >&2; exit 1 ;;
esac
export ARCHIVE="release-cli_${RELEASE_VERSION}_${os}_${arch}.tar.gz"
```

Download and verify the selected release:

```bash
export INSTALL_DIR="$(mktemp -d)"
gh release download "$RELEASE_TAG" \
  --repo Sakura-Industries-LLC/release \
  --dir "$INSTALL_DIR" \
  --pattern "$ARCHIVE" \
  --pattern checksums.txt \
  --pattern checksums.txt.sigstore.json
cd "$INSTALL_DIR"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum --check --ignore-missing checksums.txt
else
  shasum -a 256 --check checksums.txt --ignore-missing
fi
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/Sakura-Industries-LLC/release/.github/workflows/go-pre-publish.yml@refs/tags/$RELEASE_TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify "$ARCHIVE" \
  --repo Sakura-Industries-LLC/release \
  --signer-workflow Sakura-Industries-LLC/release/.github/workflows/publish-github-release.yml \
  --signer-digest "$RELEASE_REVISION" \
  --source-ref "refs/tags/$RELEASE_TAG" \
  --deny-self-hosted-runners
tar -xzf "$ARCHIVE"
sudo install -m 0755 release-cli /usr/local/bin/release-cli
/usr/local/bin/release-cli version --json
```

Do not extract or install the binary when a checksum, Cosign identity, issuer,
or GitHub attestation check fails. Repeat the complete procedure with a reviewed
new tag to update.

On Windows, use the corresponding
`release-cli_<version>_windows_<arch>.zip`. Verify its SHA-256 entry from
`checksums.txt`, run the same `gh attestation verify` signer and source
constraints, expand the ZIP, and place `release-cli.exe` in an administrator-
controlled directory on `PATH`.

## Choose the trust path

| Method | Installed content | Verification boundary |
| --- | --- | --- |
| mise | Prebuilt release archive | Release checksum and GitHub artifact attestation through mise's GitHub backend. |
| Nix | Source build | Locked Git source, Nixpkgs input, Go source, and fixed dependency hashes. |
| Direct archive | Prebuilt release archive | Local checksum, exact Cosign workflow identity, and GitHub artifact attestation. |

Do not recover an installation by skipping checksum or attestation checks.
Correct the system clock, CA store, or selected release instead.
