# Install `release-cli` with mise

Use mise's built-in GitHub backend to install a tagged `release-cli` release. The
current release does not require a mise plugin or a custom installer.

This guide is for direct command-line use. Repositories that call the reusable
Meigma release workflows do not install or version `release-cli` separately.
The pinned workflow revision selects and verifies the matching CLI release.

## Install a project version

From the project directory, install the current release:

```bash
mise use github:meigma/release@0.1.2
```

Mise writes this tool entry to the project's `mise.toml`:

```toml
[tools]
"github:meigma/release" = "0.1.2"
```

Verify the installed command:

```bash
mise exec -- release-cli version --json
```

The current release reports:

```json
{
  "schema": "release.dev/result/v1",
  "command": "version",
  "ok": true,
  "result": {
    "version": "0.1.2",
    "commit": "611195c21fdd44ff2cf95c6a8833f84d095270b0",
    "protocol": 1
  }
}
```

Mise selects the release archive for the host operating system and architecture.
The `v0.1.2` release contains archives for Darwin, Linux, and Windows on `amd64`
and `arm64`. During a verified installation, mise reports the selected archive,
its checksum check, and GitHub artifact attestation verification.

## Run without changing project configuration

Use `mise x` for a temporary invocation:

```bash
mise x github:meigma/release@0.1.2 -- release-cli version --json
```

## Install for your user account

Add `-g` to write the tool entry to the global mise configuration:

```bash
mise use -g github:meigma/release@0.1.2
mise exec -- release-cli version --json
```

Pin an explicit version in project and automation configuration. Update the
version deliberately after verifying the target GitHub Release.

## Use a local shorthand

`release-cli` is not registered in the mise registry. The full backend name is
therefore required by default; `mise use release-cli@0.1.2` does not resolve.

To define a local shorthand, add an alias and tool entry:

```toml
[tool_alias]
release-cli = "github:meigma/release"

[tools]
release-cli = "0.1.2"
```

The alias affects only the mise configuration that defines it. It does not
publish `release-cli` to the shared mise registry.
