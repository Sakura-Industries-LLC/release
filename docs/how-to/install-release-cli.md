# Install `release-cli`

Use this guide to install `release-cli` for direct command-line use. Repositories
that call the reusable workflows do not install or pin the CLI separately; the
one workflow revision selects it as part of the release unit.

The `meigma/release` and `pkgs.meigma.dev` URLs on this page install
`release-cli` itself. Adopting organizations must use their own repositories and
origins for their applications.

## Select a released version

For mise, Nix, or a direct archive, select one published stable release and
resolve its tag to a full commit:

```bash
export RELEASE_TAG="$(gh api repos/meigma/release/releases/latest --jq .tag_name)"
export RELEASE_VERSION="${RELEASE_TAG#v}"
export RELEASE_REVISION="$(gh api "repos/meigma/release/commits/$RELEASE_TAG" --jq .sha)"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]
printf 'Installing release-cli %s from %s\n' "$RELEASE_VERSION" "$RELEASE_REVISION"
```

For automation, set `RELEASE_TAG`, `RELEASE_VERSION`, and `RELEASE_REVISION` to
a reviewed release rather than resolving the latest release on every run.

## Install with mise

Use mise's built-in GitHub backend:

```bash
mise use "github:meigma/release@$RELEASE_VERSION"
mise exec -- release-cli version --json
```

Mise writes an explicit project version:

```toml
[tools]
"github:meigma/release" = "<version>"
```

For a temporary invocation:

```bash
mise x "github:meigma/release@$RELEASE_VERSION" -- release-cli version --json
```

For a user-level installation:

```bash
mise use -g "github:meigma/release@$RELEASE_VERSION"
mise exec -- release-cli version --json
```

`release-cli` is not registered under a short name in the shared mise registry.
To use one locally, add:

```toml
[tool_alias]
release-cli = "github:meigma/release"

[tools]
release-cli = "<version>"
```

The released archives cover Darwin, Linux, and Windows on `amd64` and `arm64`.
Mise's verified GitHub backend selects the host archive and checks its release
checksum and GitHub artifact attestation.

To update, review a newer release and run:

```bash
mise use "github:meigma/release@$RELEASE_VERSION"
mise install --locked
mise exec -- release-cli version --json
```

Commit the changed `mise.toml` and `mise.lock` when the tool is project-scoped.

## Install with Nix

The repository flake builds `release-cli` from source for Darwin and Linux on
`aarch64` and `x86_64`.

Run the selected immutable revision without installing:

```bash
nix run "github:meigma/release/$RELEASE_REVISION#release-cli" -- version --json
```

Install it into the current profile:

```bash
nix profile add "github:meigma/release/$RELEASE_REVISION#release-cli"
release-cli version --json
```

For a project flake, add a tagged input and make it follow the project's
`nixpkgs` input:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    release = {
      url = "github:meigma/release/vMAJOR.MINOR.PATCH";
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

## Install from the APT repository

The public Meigma repository distributes `release-cli` on `amd64` and `arm64`.
Install the HTTPS prerequisites and reviewed aggregate key:

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -d -m 0755 /etc/apt/keyrings
curl --fail --silent --show-error --location \
  https://pkgs.meigma.dev/keys/apt-repository-001.asc \
  | sudo tee /etc/apt/keyrings/meigma-packages.asc >/dev/null
sudo chmod 0644 /etc/apt/keyrings/meigma-packages.asc
printf '%s\n' \
  'deb [signed-by=/etc/apt/keyrings/meigma-packages.asc] https://pkgs.meigma.dev/apt stable main' \
  | sudo tee /etc/apt/sources.list.d/meigma-packages.list >/dev/null
sudo apt-get update
sudo apt-get install -y release-cli
release-cli version --json
```

APT verifies the aggregate signed repository metadata. Update through the same
repository:

```sh
sudo apt-get update
sudo apt-get install --only-upgrade release-cli
```

## Install from the DNF repository

Configure both the aggregate RPM metadata key and the producer package key:

```sh
sudo tee /etc/yum.repos.d/meigma-packages.repo >/dev/null <<'EOF'
[meigma-packages]
name=Meigma packages
baseurl=https://pkgs.meigma.dev/rpm/stable/$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://pkgs.meigma.dev/keys/rpm-repository-001.asc https://pkgs.meigma.dev/keys/release-rpm-001.asc
EOF
sudo dnf install -y release-cli
release-cli version --json
```

Keep both `gpgcheck=1` and `repo_gpgcheck=1`. Update with:

```sh
sudo dnf upgrade release-cli
```

## Install from the APK repository

APK identifies signing keys by filename. Preserve both reviewed basenames:

```sh
sudo wget -q \
  https://pkgs.meigma.dev/keys/apk-index-001.rsa.pub \
  -O /etc/apk/keys/apk-index-001.rsa.pub
sudo wget -q \
  https://pkgs.meigma.dev/keys/meigma-release-001.rsa.pub \
  -O /etc/apk/keys/meigma-release-001.rsa.pub
printf '%s\n' 'https://pkgs.meigma.dev/apk/stable/main' \
  | sudo tee -a /etc/apk/repositories >/dev/null
sudo apk update
sudo apk add release-cli
release-cli version --json
```

The aggregate key verifies `APKINDEX.tar.gz`; the producer key verifies the APK.
Update with:

```sh
sudo apk update
sudo apk upgrade release-cli
```

## Install a verified GitHub archive

Use this path when mise, Nix, and the native repositories are unavailable. The
following Bash procedure supports Darwin and Linux on `amd64` and `arm64`.

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
  --repo meigma/release \
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
  --certificate-identity "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@refs/tags/$RELEASE_TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify "$ARCHIVE" \
  --repo meigma/release \
  --signer-workflow meigma/release/.github/workflows/publish-github-release.yml \
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
| APT | Native DEB | HTTPS plus signed aggregate APT metadata. |
| DNF | Native RPM | HTTPS, signed aggregate RPM metadata, and producer RPM signature. |
| APK | Native APK | HTTPS, aggregate APK index signature, and producer APK signature. |
| Direct archive | Prebuilt release archive | Local checksum, exact Cosign workflow identity, and GitHub artifact attestation. |

Do not recover an installation by using HTTP, APT `trusted=yes`, DNF
`gpgcheck=0`, APK `--allow-untrusted`, or skipped checksum and attestation
checks. Correct the system clock, CA store, key files, or selected release
instead.
