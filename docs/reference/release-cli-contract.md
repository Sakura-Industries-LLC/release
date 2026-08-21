# `release-cli` contract reference

`release-cli` builds and validates Go release data, reports machine-readable results, builds and verifies OCI layouts from staged binaries, initializes cask-only Homebrew taps, opens protected tap pull requests, publishes verified GitHub Releases, and performs two-phase digest-addressed OCI publication. The [GitHub Release contract](github-release-contract.md) defines the workflow inputs, artifacts, and publication behavior that surround the CLI.

## Commands

| Command | Purpose |
| --- | --- |
| `release-cli stage --profile go --dist PATH [--json]` | Build and validate a Go release bundle under `PATH`, then write its OCI input projection. |
| `release-cli image build [--input DIR] [--work DIR] [--output DIR] [--melange-config PATH] [--apko-config PATH] [--build-date RFC3339] [--version VERSION] [--json]` | Build a locked multi-architecture OCI layout from staged Linux binaries. |
| `release-cli image verify [--output DIR] [--work DIR] [--binary NAME] [--version VERSION] [--json]` | Verify the built OCI layout, runtime contract, and architecture SBOMs. |
| `release-cli plan tags [--image IMAGE] [--version VERSION] --digest DIGEST [--plain-http] [--json]` | Inspect the immutable exact tag and moving channel tags for an OCI release. |
| `release-cli publish oci prepare --layout PATH [--image IMAGE] [--version VERSION] --digest DIGEST [--dry-run] [--plain-http] [--json]` | Validate and prepare a digest-addressed OCI image publication and recursive signature. |
| `release-cli publish oci finalize --result - [--plain-http] [--json]` | Re-read registry state and apply verified OCI image tags after attestation. |
| `release-cli publish github --dist PATH [--no-undraft] [--json]` | Reconcile a verified bundle with its matching GitHub Release and optionally publish the draft. |
| `release-cli init homebrew-tap --tap OWNER/HOMEBREW-NAME --output DIR [--json]` | Write a cask-only tap scaffold into a new or empty local directory. |
| `release-cli publish homebrew --dist PATH --tap OWNER/REPOSITORY --cask TOKEN [--json]` | Reconcile a generated cask through a protected Homebrew tap pull request. |
| `release-cli verify bundle --dist PATH --identity URL [--issuer URL] [--json]` | Verify a closed release bundle and its detached Sigstore signature. |
| `release-cli verify handoff --artifact-id <n> --digest <sha256:...> [--json]` | Verify an Actions artifact's GitHub API metadata before download. |
| `release-cli version [--json]` | Report the CLI version, source commit, and protocol integer. |

`stage`, `verify bundle`, `publish github`, and `publish homebrew` require a distribution path. `init homebrew-tap` requires `--tap` and `--output`; the repository name must use `homebrew-<name>`. The initializer also requires a released CLI whose build metadata contains a full source commit. The only accepted profile is `go`. `verify bundle` also requires an exact certificate identity. `verify handoff` requires artifact ID and digest values. Supply handoff values with `--artifact-id` and `--digest`, or with `RELEASE_ARTIFACT_ID` and `RELEASE_DIGEST`. An explicitly set flag takes precedence over its environment variable.

Boolean `RELEASE_*` environment variables must contain a value accepted by Go's `strconv.ParseBool`: `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, or `False`. Any other value is invalid configuration and exits with code `2`.

The artifact ID must be a positive decimal safe integer. The digest must be a 64-digit hexadecimal SHA-256 value with or without the `sha256:` prefix. Digest hex is case-insensitive and is normalized to lowercase with the prefix.

## JSON output

When option and argument parsing succeeds and `--json` is requested, stdout contains exactly one JSON document and no other output. The envelope has this structure:

```text
{"schema":"release.dev/result/v1","command":"<verb path>","ok":<boolean>,"result":{...}}
```

| Field | Value |
| --- | --- |
| `schema` | Always `release.dev/result/v1`. |
| `command` | The command path, such as `image build`, `image verify`, `init homebrew-tap`, `plan tags`, `publish github`, `publish homebrew`, `publish oci prepare`, `publish oci finalize`, `stage`, `verify bundle`, `verify handoff`, or `version`. |
| `ok` | `true` when the command succeeds; otherwise `false`. |
| `result` | The command-specific result object. |

The `stage --json` result contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `assets` | number | Number of payloads whose checksums matched. |
| `binaries` | object | Entries named `amd64` and `arm64` for the verified Linux binaries. |
| `binaries.<arch>.path` | string | Original `<dist-basename>/`-prefixed path from `artifacts.json`. |
| `binaries.<arch>.mode` | string | Observed permission bits in octal notation. |

For `init homebrew-tap --json`, `command` is exactly `init homebrew-tap`. The `result` object contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `tap` | string | Validated target repository in `owner/homebrew-name` form. |
| `output` | string | Clean local output path. |
| `files` | array of strings | Generated slash-separated paths in lexical order. |

For `image build --json`, `command` is exactly `image build`. The `result` object contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `schema` | string | Always `release.dev/image-build/v1`. |
| `version` | string | Stable release version used for the APKs and image annotations. |
| `binary` | string | Shared filename of the two canonical Linux binaries. |
| `work` | string | Scratch workspace selected by `--work` or `RELEASE_WORK`. |
| `output` | string | Authoritative artifact output root selected by `--output` or `RELEASE_OUTPUT`. |
| `build_date` | string | Reproducible build time in RFC 3339 format. |
| `packages` | array of objects | The two APKs, ordered as `linux/amd64` and then `linux/arm64`. |
| `packages[].platform` | string | Canonical Linux platform: `linux/amd64` or `linux/arm64`. |
| `packages[].arch` | string | Corresponding APK architecture: `x86_64` or `aarch64`. |
| `packages[].package` | string | Output-root-relative path to the architecture's only APK. |
| `packages[].binary_digest` | string | Verified canonical binary digest with the `sha256:` prefix. |

For example, a successful build writes this envelope:

```json
{
  "schema": "release.dev/result/v1",
  "command": "image build",
  "ok": true,
  "result": {
    "schema": "release.dev/image-build/v1",
    "version": "1.2.3",
    "binary": "release-cli",
    "work": "/tmp/oci-build",
    "output": "/tmp/oci-output",
    "build_date": "2026-08-19T15:04:05Z",
    "packages": [
      {
        "platform": "linux/amd64",
        "arch": "x86_64",
        "package": "packages/x86_64/release-cli-1.2.3-r0.apk",
        "binary_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      },
      {
        "platform": "linux/arm64",
        "arch": "aarch64",
        "package": "packages/aarch64/release-cli-1.2.3-r0.apk",
        "binary_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
      }
    ]
  }
}
```

For `image verify --json`, `command` is exactly `image verify`. The `result` object contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `schema` | string | Always `release.dev/image-verify/v1`. |
| `version` | string | Stable release version expected in the image and architecture SBOMs. |
| `binary` | string | Name of the canonical binary installed in the image. |
| `index_digest` | string | SHA-256 digest of the exact `index.json` bytes, with the `sha256:` prefix. |
| `platforms` | array of objects | Verified platforms in canonical order: `linux/amd64`, then `linux/arm64`. |
| `platforms[].platform` | string | Canonical Linux platform: `linux/amd64` or `linux/arm64`. |
| `platforms[].arch` | string | Corresponding APK architecture: `x86_64` or `aarch64`. |
| `platforms[].manifest` | string | Platform manifest digest with the `sha256:` prefix. |
| `platforms[].config` | string | Platform config digest with the `sha256:` prefix. |
| `platforms[].layer` | string | Platform layer digest with the `sha256:` prefix. |
| `platforms[].binary_digest` | string | SHA-256 digest of the installed binary, with the `sha256:` prefix. |

For example, successful verification writes this envelope:

```json
{
  "schema": "release.dev/result/v1",
  "command": "image verify",
  "ok": true,
  "result": {
    "schema": "release.dev/image-verify/v1",
    "version": "1.2.3",
    "binary": "release-cli",
    "index_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "platforms": [
      {
        "platform": "linux/amd64",
        "arch": "x86_64",
        "manifest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "config": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        "layer": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
        "binary_digest": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
      },
      {
        "platform": "linux/arm64",
        "arch": "aarch64",
        "manifest": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
        "config": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
        "layer": "sha256:2222222222222222222222222222222222222222222222222222222222222222",
        "binary_digest": "sha256:3333333333333333333333333333333333333333333333333333333333333333"
      }
    ]
  }
}
```

The `verify bundle --json` result contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `dist` | string | Distribution directory selected by `--dist` or `RELEASE_DIST`. |
| `identity` | string | Exact certificate identity URL used for Sigstore verification. |
| `issuer` | string | OIDC issuer used for Sigstore verification. |
| `payloads` | array of objects | Checksummed release payloads in `checksums.txt` order. |
| `payloads[].name` | string | Flat payload name inside the distribution directory. |
| `payloads[].digest` | string | Payload SHA-256 digest as 64 lowercase hexadecimal characters without a `sha256:` prefix. |
| `controls` | array of objects | The two control files, ordered as `checksums.txt` and `checksums.txt.sigstore.json`. |
| `controls[].name` | string | Control file name inside the distribution directory. |
| `controls[].digest` | string | Control file SHA-256 digest as 64 lowercase hexadecimal characters without a `sha256:` prefix. |

For example, a verified bundle produces this result object:

```json
{
  "dist": "dist",
  "identity": "https://github.com/owner/repo/.github/workflows/go-pre-publish.yml@refs/heads/main",
  "issuer": "https://token.actions.githubusercontent.com",
  "payloads": [
    {
      "name": "release-cli_1.2.3_linux_amd64.tar.gz",
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    }
  ],
  "controls": [
    {
      "name": "checksums.txt",
      "digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    },
    {
      "name": "checksums.txt.sigstore.json",
      "digest": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
    }
  ]
}
```

For `publish github --json`, `command` is exactly `publish github`. The `result` object contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `release_id` | number | Positive GitHub Release ID. |
| `tag` | string | Exact tag carried by the release. |
| `url` | string | GitHub HTML URL for the release. |
| `draft` | boolean | Final observed draft state. `false` means the release is public. |
| `assets` | array of strings | Converged asset names, sorted lexicographically. |

For example, a successful publication writes this envelope:

```json
{
  "schema": "release.dev/result/v1",
  "command": "publish github",
  "ok": true,
  "result": {
    "release_id": 123456,
    "tag": "v1.2.3",
    "url": "https://github.com/owner/repo/releases/tag/v1.2.3",
    "draft": false,
    "assets": [
      "checksums.txt",
      "checksums.txt.sigstore.json",
      "example_1.2.3_linux_amd64.tar.gz"
    ]
  }
}
```

For `publish homebrew --json`, `command` is exactly `publish homebrew`. The `result` object contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `tap` | string | Target tap in `owner/repository` form. |
| `cask` | string | Published cask token. |
| `branch` | string | Deterministic publication branch in `release/<cask>/v<version>` form. |
| `pull_request_url` | string | Matching pull request URL. This can be empty when matching cask content reached the default branch without a discoverable pull request. |
| `state` | string | `created` when the command opened the pull request, `open` when it accepted an existing pull request, or `published` when matching content is on the default branch. |

For example, a new tap publication writes this envelope:

```json
{
  "schema": "release.dev/result/v1",
  "command": "publish homebrew",
  "ok": true,
  "result": {
    "tap": "owner/homebrew-tap",
    "cask": "example",
    "branch": "release/example/v1.2.3",
    "pull_request_url": "https://github.com/owner/homebrew-tap/pull/42",
    "state": "created"
  }
}
```

For `plan tags --json`, `command` is exactly `plan tags`. The `result` object contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `image` | string | OCI image name whose tags were inspected. |
| `version` | string | Candidate stable release version. |
| `digest` | string | Candidate OCI index digest, normalized to lowercase with the `sha256:` prefix. |
| `tags` | array of strings | Tags with a `create` decision, in decision order. Tags with an `accept` or `retain` decision are omitted. |
| `decisions` | array of objects | Decision for the exact tag and each channel tag, in policy order. |
| `decisions[].tag` | string | Exact or channel tag that was evaluated. |
| `decisions[].scope` | string | Tag scope: `exact`, `minor`, `major`, or `latest`. |
| `decisions[].action` | string | Result: `create`, `accept`, or `retain`. |

For example, this result plans to apply the exact and minor tags, retain the major tag, and accept the existing `latest` tag:

```json
{
  "image": "ghcr.io/owner/repo",
  "version": "1.2.3",
  "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "tags": ["1.2.3", "1.2"],
  "decisions": [
    {"tag": "1.2.3", "scope": "exact", "action": "create"},
    {"tag": "1.2", "scope": "minor", "action": "create"},
    {"tag": "1", "scope": "major", "action": "retain"},
    {"tag": "latest", "scope": "latest", "action": "accept"}
  ]
}
```

For `publish oci prepare --json`, `command` is exactly `publish oci prepare`. The `result` object has schema `release.dev/oci-prepare/v1` and contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `schema` | string | Always `release.dev/oci-prepare/v1`. |
| `authoritative` | boolean | `true` after a non-dry-run preparation completes; `false` for `--dry-run`. A non-authoritative result is not usable for publication. |
| `image` | string | OCI image name prepared by the command. |
| `version` | string | Candidate stable release version. |
| `index_digest` | string | OCI index digest, normalized to lowercase with the `sha256:` prefix. |
| `platforms` | array of objects | Platform manifests in the order recorded by `index.json`. |
| `platforms[].platform` | string | Platform in `OS/architecture` form, such as `linux/amd64`. |
| `platforms[].digest` | string | Digest of the platform manifest. |
| `observed` | array of objects | Registry observations ordered by scope: exact, minor, major, then latest. |
| `observed[].tag` | string | Exact or channel tag that was observed. |
| `observed[].scope` | string | Tag scope: `exact`, `minor`, `major`, or `latest`. |
| `observed[].present` | boolean | Whether the tag was present in the registry. |
| `observed[].digest` | string | Digest resolved from a present tag. This field is omitted for an absent tag. |
| `observed[].version` | string | Stable version read from the current manifest annotation. This field is omitted when no annotation was read. |

For example, a successful non-dry-run preparation writes this standard envelope:

```json
{
  "schema": "release.dev/result/v1",
  "command": "publish oci prepare",
  "ok": true,
  "result": {
    "schema": "release.dev/oci-prepare/v1",
    "authoritative": true,
    "image": "ghcr.io/owner/repo",
    "version": "1.2.3",
    "index_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "platforms": [
      {
        "platform": "linux/amd64",
        "digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
      },
      {
        "platform": "linux/arm64",
        "digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
      }
    ],
    "observed": [
      {"tag": "1.2.3", "scope": "exact", "present": false},
      {"tag": "1.2", "scope": "minor", "present": false},
      {
        "tag": "1",
        "scope": "major",
        "present": true,
        "digest": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
        "version": "1.1.9"
      },
      {
        "tag": "latest",
        "scope": "latest",
        "present": true,
        "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      }
    ]
  }
}
```

For `publish oci finalize --json`, `command` is exactly `publish oci finalize`. The `result` object has schema `release.dev/oci-finalize/v1` and contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `schema` | string | Always `release.dev/oci-finalize/v1`. |
| `image` | string | OCI image name from the prepare result. |
| `version` | string | Candidate stable release version from the prepare result. |
| `index_digest` | string | Candidate OCI index digest from the prepare result. |
| `applied` | array of strings | Tags written by this run, in tag-plan order. |
| `accepted` | array of strings | Tags that already resolved to the candidate index digest. |
| `retained` | array of strings | Channel tags left on a newer release. |

For example, this result applies the exact and minor tags while retaining newer major and `latest` channels:

```json
{
  "schema": "release.dev/result/v1",
  "command": "publish oci finalize",
  "ok": true,
  "result": {
    "schema": "release.dev/oci-finalize/v1",
    "image": "ghcr.io/owner/repo",
    "version": "1.2.3",
    "index_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "applied": ["1.2.3", "1.2"],
    "accepted": [],
    "retained": ["1", "latest"]
  }
}
```

The `version --json` result contains exactly these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `version` | string | Release version stamped into the binary. |
| `commit` | string | Source commit stamped into the binary. |
| `protocol` | number | Protocol integer compiled into the CLI. The current value is `1`. |

The `verify handoff --json` result contains this object:

```json
{
  "artifact": {
    "id": 11,
    "name": "release-assets",
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "size_bytes": 42,
    "run_id": 100,
    "expires_at": "2026-08-19T15:04:05Z"
  }
}
```

| Field | JSON type | Value |
| --- | --- | --- |
| `artifact.id` | number | Positive Actions artifact ID returned by GitHub. |
| `artifact.name` | string | Artifact name reported by GitHub. |
| `artifact.digest` | string | Normalized, lowercase GitHub-reported digest with the `sha256:` prefix. |
| `artifact.size_bytes` | number | Artifact size in bytes reported by GitHub. |
| `artifact.run_id` | number | Workflow run ID associated with the artifact. |
| `artifact.expires_at` | string | Artifact expiration time in RFC 3339 format, or an empty string if GitHub omitted it. |

After command-line parsing and dispatch succeed, a command or configuration
failure under `--json` sets `ok` to `false` and gives `result` one string field
named `error`. The command also returns its nonzero exit code. Configuration
failures return code `2` and still emit exactly one envelope. Only command-line
parse or dispatch failures skip the envelope. These include an unknown command
or flag, an invalid flag value, or the wrong number of arguments. The usage
error goes to stderr and the process exits with code `2`.

Without `--json`, a successful `image build`, `image verify`, `plan tags`, `publish github`, `publish homebrew`, `publish oci prepare`, `publish oci finalize`, `stage`, `verify bundle`, or `verify handoff` command writes nothing to stdout. A successful `version` command writes `release-cli <version> (<commit>, protocol <n>)` to stdout because the version data is the requested output and can be piped. This human format is a convenience, not a stable interface. Human diagnostics and warnings go to stderr. With `--json`, the envelope is the stable machine-readable stdout contract for all commands.

## Exit codes

| Code | Meaning |
| ---: | --- |
| `0` | The command completed successfully. |
| `1` | A tool invocation, release contract, or verification check failed. The command fails closed. |
| `2` | Command usage or configuration is invalid. This includes an unsupported `--profile` value. |

No other exit code is defined; in particular, code `3` has no meaning. An exit code does not make a general promise that a command is safe to run again.

## OCI image build

`release-cli image build` turns the canonical Linux binaries recorded by `stage --profile go` into signed APK repositories and a locked multi-architecture OCI layout.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Input artifact root | `--input` | `RELEASE_INPUT` | None. A path is required. |
| Scratch workspace | `--work` | `RELEASE_WORK` | None. A path is required. |
| Authoritative output root | `--output` | `RELEASE_OUTPUT` | None. A path is required. |
| Melange configuration | `--melange-config` | `RELEASE_MELANGE_CONFIG` | `melange.yaml`. |
| apko configuration | `--apko-config` | `RELEASE_APKO_CONFIG` | `apko.yaml`. |
| Build date | `--build-date` | `RELEASE_BUILD_DATE` | None. An RFC 3339 value is required. |
| Version | `--version` | `RELEASE_VERSION` | `GITHUB_REF_NAME` with one optional leading `v` stripped. |
| Melange binary | None. | `RELEASE_MELANGE_PATH` | Resolve `melange` from `PATH` when invoked. |
| apko binary | None. | `RELEASE_APKO_PATH` | Resolve `apko` from `PATH` when invoked. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

An explicitly set flag takes precedence over its environment variable. The version default applies only when `--version` and `RELEASE_VERSION` are absent. The command reads `oci-build-inputs.json` from the input artifact root.

The command reads this GitHub Actions context:

| Variable | Value |
| --- | --- |
| `GITHUB_REPOSITORY` | Repository in `owner/name` form. The name after the slash forms the local image reference `local/<name>:<version>`. |
| `GITHUB_REPOSITORY_OWNER` | APK package namespace. |
| `GITHUB_SERVER_URL` | Absolute GitHub server URL. Combined with `GITHUB_REPOSITORY` for the provenance source URL. |
| `GITHUB_SHA` | Provenance commit SHA and OCI revision annotation. |
| `GITHUB_REF_NAME` | Version source when neither the flag nor `RELEASE_VERSION` is set. |

`--work` and `--output` must be disjoint. Equality is invalid, as is either path containing the other. The command exits with code `2` for any of these relationships. This separation keeps the ephemeral Melange signing key in the work directory and out of the output directory that becomes the authoritative uploaded artifact.

The command performs these operations in order:

1. Resolve and validate all flags, environment variables, and Actions context, including the work and output path relationship. Every configuration failure exits with code `2` before the command creates a directory or invokes a tool.
2. Open the input artifact root, then decode and validate `oci-build-inputs.json`. A missing or malformed projection exits with code `1` without creating the work or output directory.
3. Open the selected Melange and apko configuration files. An unopenable configuration file exits with code `1` without creating the work or output directory or invoking a tool.
4. Construct the Melange and apko ports. Construction does not invoke either tool.
5. Create the work and output roots when absent, then open them. Each root must be empty. The command refuses any pre-existing entry in either directory, so unrelated files cannot enter the build or the authoritative artifact.
6. Create new source directories under the work root and new `configuration`, `packages`, `layout`, and `sboms` directories under the output root.
7. Process `linux/amd64` and then `linux/arm64`. For each platform, stream the projected binary into `sources/<apk-arch>/application` while computing its SHA-256 digest, set mode `0755`, compare the computed digest with the projection, and inspect the copied executable.
8. Write `vars.json` with the stable version and a trailing newline.
9. Copy the selected Melange and apko configurations to `configuration/melange.yaml` and `configuration/apko.yaml` with mode `0644`.
10. Write `canonical-binaries.sha256` in GNU coreutils form, with `x86_64` first and `aarch64` second.
11. Use Melange to compile-check `x86_64`, generate an ephemeral signing key, and build the `x86_64` and `aarch64` APK repositories in that order.
12. Require the builder's repository and public-key paths to match the requested paths, then inspect both APK repositories.
13. Copy the generated public key to `apk-signing.rsa.pub` with mode `0644`. The private key remains in the scratch workspace.
14. Use apko to write `apko.lock.json`, then compose the two-architecture layout and SBOMs with version and revision annotations.
15. Require the lockfile, OCI layout marker and index, and both architecture SBOMs to be nonempty regular files.
16. Return the build result, including both verified binary digests and APK paths.

The build checks these boundaries:

| Check | Requirement |
| --- | --- |
| Canonical binary digest | The SHA-256 digest computed from each downloaded binary must equal its digest in `oci-build-inputs.json`. |
| Executable format | Each binary must be a statically linked, 64-bit, little-endian ELF executable with no interpreter or needed dynamic libraries. The `x86_64` source must have the x86-64 machine type; the `aarch64` source must have the AArch64 machine type. |
| APK repository | Each architecture directory must contain a nonempty `APKINDEX.tar.gz` and exactly one nonempty `.apk` file. |
| Composer outputs | `apko.lock.json`, `layout/index.json`, `layout/oci-layout`, `sboms/sbom-x86_64.spdx.json`, and `sboms/sbom-aarch64.spdx.json` must be nonempty regular files. |

`image build` checks that the layout files and SBOMs exist but does not deeply verify their contents. `image verify` independently checks the layout structure, runtime invariants, architecture SBOMs, and digest of the exact `layout/index.json` bytes.

| Exit code | Meaning |
| ---: | --- |
| `0` | The image build completed successfully. |
| `1` | The input projection is missing or malformed; a Melange or apko configuration file cannot be opened; or a staged-content contract, executable, tool invocation, APK repository, or composer-output check failed. |
| `2` | Command usage or configuration is invalid. |

## OCI image verification

`release-cli image verify` verifies the layout and SBOMs produced by `image build` against the canonical binaries in the scratch workspace.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Authoritative output root | `--output` | `RELEASE_OUTPUT` | None. A path is required. |
| Scratch workspace | `--work` | `RELEASE_WORK` | None. A path is required. |
| Binary name | `--binary` | `RELEASE_BINARY` | None. A name is required. |
| Version | `--version` | `RELEASE_VERSION` | `GITHUB_REF_NAME` with one optional leading `v` stripped. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

An explicitly set flag takes precedence over its environment variable. The version default applies only when `--version` and `RELEASE_VERSION` are absent. The command validates all configuration before opening either root. Unlike `image build`, `image verify` does not require the work and output roots to be disjoint because it does not write into the work root.

The command reads this GitHub Actions context:

| Variable | Value |
| --- | --- |
| `GITHUB_SHA` | Expected OCI revision annotation. |
| `GITHUB_SERVER_URL` | Absolute GitHub server URL. |
| `GITHUB_REPOSITORY` | Repository in `owner/name` form. The expected source annotation is `<GITHUB_SERVER_URL>/<GITHUB_REPOSITORY>`. |
| `GITHUB_REF_NAME` | Version source when neither the flag nor `RELEASE_VERSION` is set. |

The command enforces these checks:

- `oci-layout` is a regular file. The OCI index is valid JSON, has schema version `2`, uses media type `application/vnd.oci.image.index.v1+json`, and contains exactly two manifests. Both manifests are for Linux, and their architecture set is exactly `amd64` and `arm64`.
- The index has `org.opencontainers.image.description`, `org.opencontainers.image.licenses`, `org.opencontainers.image.revision`, `org.opencontainers.image.source`, `org.opencontainers.image.title`, and `org.opencontainers.image.version` annotations. Description, licenses, and title are nonempty. Revision, source, and version equal the expected values.
- Each referenced platform manifest is a regular file of its declared size. It is valid JSON, has schema version `2`, uses media type `application/vnd.oci.image.manifest.v1+json`, contains exactly one layer, and repeats the six index annotation values.
- Each platform config has the descriptor's architecture and Linux as its operating system. Its labels repeat the six index annotation values. Its entrypoint is exactly `["/usr/bin/<binary>"]`, and its user is exactly `65532`.
- The layer media type is `application/vnd.oci.image.layer.v1.tar+gzip` or `application/vnd.oci.image.layer.v1.tar`. In its tar stream, `usr/bin/<binary>` appears exactly once as a regular file with mode exactly `0755`, with no setuid, setgid, or sticky bit set, and ownership exactly `0:0`. Its content is byte-identical to the corresponding `sources/x86_64/application` or `sources/aarch64/application` file in the scratch workspace.
- Each architecture SBOM contains an SPDX package whose `primaryPackagePurpose` is `APPLICATION` and whose `versionInfo` is `<version>-r0`.

The index digest is SHA-256 over the exact `layout/index.json` bytes. The command never computes this digest from re-marshaled JSON. After every check succeeds, it writes the digest followed by a newline to `image-digest.txt` in the output directory.

| Exit code | Meaning |
| ---: | --- |
| `0` | The layout, runtime contract, and architecture SBOMs passed verification. |
| `1` | A layout, blob, runtime, canonical-binary, or SBOM verification check failed. |
| `2` | Command usage or configuration is invalid. |

## OCI tag planning

`release-cli plan tags` inspects the current registry state and returns the tag decisions for one candidate OCI index.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Image | `--image` | `RELEASE_IMAGE` | `ghcr.io/<owner>/<repo>`, lowercased from `GITHUB_REPOSITORY`. |
| Version | `--version` | `RELEASE_VERSION` | `GITHUB_REF_NAME` with one optional leading `v` stripped. |
| Digest | `--digest` | `RELEASE_DIGEST` | None. A digest is required. |
| Plain HTTP | `--plain-http` | None. The option is flag-only. | Disabled. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

An explicitly set flag takes precedence over its environment variable. The derived default applies only when the corresponding flag and release environment variable are absent. The image must have the lowercase form `host/path[/path...]` without a tag or digest. The digest must have the `sha256:` prefix followed by 64 hexadecimal digits.

`--plain-http` permits an HTTP registry connection for local-registry testing only. The command refuses this flag unless the image host is `127.0.0.1`, `::1`, or `localhost`, optionally with a port. Any other host is invalid configuration and exits with code `2`.

The command resolves registry credentials in this order:

| Credential | Resolution |
| --- | --- |
| Token | Nonempty `GITHUB_TOKEN`, then nonempty `GH_TOKEN`. |
| Username | Nonempty `GITHUB_ACTOR`, then `x-access-token`. |

If neither token is present, the command reads the registry anonymously. Anonymous reads work only for public packages.

Missing or invalid configuration exits with code `2`. Under `--json`, this failure still writes exactly one envelope with `ok` set to `false`. A planning or registry failure exits with code `1`.

`plan tags` performs registry reads only. It never writes a tag, blob, or manifest. The publisher workflow uses `publish oci prepare` and `publish oci finalize` for publication; it does not invoke the standalone `plan tags` inspection command.

### Tag policy

The candidate version must match this canonical stable-version grammar:

```text
^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$
```

The version has exactly three components. It has no `v` prefix, leading zeros, prerelease, or build metadata. Each component must fit in a 64-bit unsigned integer.

The exact tag is `MAJOR.MINOR.PATCH`:

| Current exact-tag state | Decision |
| --- | --- |
| The tag is absent. | `create`: apply the tag to the candidate digest. |
| The tag resolves to the candidate digest. | `accept`: leave the tag unchanged. |
| The tag resolves to another digest. | Fail with an immutable-tag conflict. |

The command then evaluates channels in this order:

| Channel tag | Scope | Required release line |
| --- | --- | --- |
| `MAJOR.MINOR` | `minor` | The current annotation must have the candidate's major and minor components. |
| `MAJOR` | `major` | The current annotation must have the candidate's major component. |
| `latest` | `latest` | No release-line check. |

An absent channel gets a `create` decision. A channel that already resolves to the candidate digest gets an `accept` decision. Otherwise, the command reads the current manifest's `org.opencontainers.image.version` annotation. A missing or invalid stable-version annotation fails planning. A minor or major channel outside its required release line also fails planning.


For a valid channel annotation on a different digest, the command compares the candidate version with the annotated version:

| Comparison | Decision |
| --- | --- |
| The candidate is newer. | `create`: move the channel to the candidate digest. |
| The candidate is older. | `retain`: keep the channel on the newer release. |
| The versions are equal. | Fail because equal versions on different digests are corrupt state. |

### Concurrency

The publisher workflow serializes tag planning and application with a repository-wide concurrency group. A direct `plan tags` invocation outside that workflow has no cross-run lock. Two concurrent planners can observe the same registry state and plan conflicting channel moves. Direct use therefore requires a single writer by convention.

## OCI digest preparation

`release-cli publish oci prepare` validates and prepares one digest-addressed OCI layout for publication.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Layout directory | `--layout` | `RELEASE_LAYOUT` | None. A path is required. |
| Image | `--image` | `RELEASE_IMAGE` | `ghcr.io/<owner>/<repo>`, lowercased from `GITHUB_REPOSITORY`. |
| Version | `--version` | `RELEASE_VERSION` | `GITHUB_REF_NAME` with one optional leading `v` stripped. |
| Expected index digest | `--digest` | `RELEASE_DIGEST` | None. A digest is required. |
| Dry run | `--dry-run` | `RELEASE_DRY_RUN` | Disabled. |
| Plain HTTP | `--plain-http` | None. The option is flag-only. | Disabled. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

`--layout` identifies the extracted `oci-image/layout` directory. An explicitly set flag takes precedence over its environment variable. The derived image or version default applies only when the corresponding flag and release environment variable are absent. Image, version, and digest validation is the same as for `plan tags`.

`--plain-http` permits an HTTP registry connection for local-registry testing only. The command refuses this flag unless the image host is `127.0.0.1`, `::1`, or `localhost`, optionally with a port. Any other host is invalid configuration and exits with code `2`. Never use plain HTTP for a real publication.

Registry credentials use the same token and username resolution as `plan tags`. The command keeps these credentials in memory and does not write a Docker configuration file.

The command performs these operations in order:

1. Read and validate the OCI layout.
2. Compute the digest of the exact `index.json` bytes and require it to equal the expected `--digest`.
3. Collect fresh registry state and plan the exact and channel tags. An immutable exact-tag conflict stops the command before any registry write.
4. Push every unique config and layer blob, each platform manifest, and the index by digest.
5. Verify that the index and each platform manifest resolve by their expected digest.
6. Sign `image@<index digest>` recursively with Cosign.

The command never creates or moves a tag.

With `--dry-run`, the command performs layout validation, digest verification, fresh registry-state collection, and tag planning only. It makes zero registry writes and does not invoke Cosign. The result has `"authoritative": false`; a non-authoritative result is not usable for publication.

The command invokes a `cosign` binary resolved from `PATH`. Set `RELEASE_COSIGN_PATH` to override the binary path. Its signing invocation is:

```text
cosign sign --yes --recursive <image>@<digest>
```

Keyless signing requires the ambient OIDC credentials supplied by the workflow.

The reusable publisher workflow invokes this command before its three GitHub attestation steps. If all three attestations succeed, the workflow passes the command's JSON envelope to `publish oci finalize`. See [Why OCI publication has two phases](../explanation/two-phase-oci-publication.md) for the ordering rationale.

## OCI tag finalization

`release-cli publish oci finalize` accepts a successful prepare result, re-reads registry state, and applies the exact and eligible channel tags.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Prepare result | `--result` | None. | None. The flag is required and its only accepted value is `-`, which selects stdin. |
| Plain HTTP | `--plain-http` | None. The option is flag-only. | Disabled. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

Image, version, and index digest do not have finalize flags. The command reads all three values from the piped prepare result. Registry credentials and the loopback-only `--plain-http` restriction resolve exactly as they do for `publish oci prepare`.

Stdin must contain exactly the JSON envelope emitted by a successful `publish oci prepare --json` command:

```text
{"schema":"release.dev/result/v1","command":"publish oci prepare","ok":true,"result":{...}}
```

The nested `result` must be a valid `release.dev/oci-prepare/v1` document. An empty stdin stream, trailing content, a wrong envelope schema, a command other than `publish oci prepare`, or `ok` set to `false` is invalid configuration. A malformed nested prepare result is also invalid configuration. Any `--result` value other than `-` is a usage error; there is no receipt-file mode. All of these failures exit with code `2` before any registry request.

The command refuses a dry-run prepare result because its `authoritative` field is `false`; this is a publication failure with exit code `1`. For an authoritative result, finalization performs these operations:

1. Collect fresh state for the exact tag and the minor, major, and `latest` channels.
2. Compare fresh state with the observations in the prepare result. A tag that is unchanged or now resolves to the candidate index digest is accepted. Any other change is registry drift.
3. Recompute the tag plan from fresh state.
4. Commit the plan's apply tags serially, verifying each tag after it is written. The command skips the commit when the plan has no apply tags.
5. Independently resolve the exact tag and every applied tag and require each one to match the candidate index digest.

Drift, an immutable exact-tag conflict, corrupt channel state, a registry failure, or a failed postcondition exits with code `1`. The commit and postcondition registry reads retry only retryable failures, with the same four-attempt and 1-second, 2-second, and 4-second wait pattern as preparation. A rerun can accept tags already applied to the candidate digest, but the command makes no general promise that arbitrary failures are safe to retry.

## GitHub Release publication

`release-cli publish github` reconciles the local closed bundle with the GitHub Release for the workflow tag. The default behavior publishes the draft after every asset check succeeds.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Distribution directory | `--dist` | `RELEASE_DIST` | None. A path is required. |
| Keep the release as a draft | `--no-undraft` | None. The option is flag-only. | Disabled. |
| GitHub CLI binary | None. | `RELEASE_GH_PATH` | Resolve `gh` from `PATH`. |
| Git binary | None. | `RELEASE_GIT_PATH` | Resolve `git` from `PATH`. |
| Release App installation token | None. | `RELEASE_APP_TOKEN` | None. A token is required. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

`--no-undraft` has no environment-variable form because it controls whether the release becomes public. With the flag, the command converges and verifies the assets, then verifies that the release remains a draft. Without the flag, the command publishes the draft only after asset convergence.

The command requires this GitHub Actions context:

| Variable | Value |
| --- | --- |
| `GITHUB_REPOSITORY` | Repository in `owner/name` form. |
| `GITHUB_REF_NAME` | Exact release tag. |
| `GITHUB_SHA` | Expected 40-character lowercase commit SHA for the workflow run. |
| `GITHUB_API_URL` | Optional absolute GitHub API base URL. The public GitHub API is the default. |
| `GITHUB_SERVER_URL` | Optional absolute GitHub server and upload base URL used with a custom API URL. |

The workflow mints the short-lived Release App installation token and passes it through `RELEASE_APP_TOKEN`. The CLI holds the token as a redacted secret. It does not receive the App private key, mint an App token, or put the token in an argument or diagnostic.

The command rebuilds the expected bundle from `--dist`: the payloads listed by `checksums.txt` plus `checksums.txt` and `checksums.txt.sigstore.json`, with their local SHA-256 digests. It repeats the closed-set and digest checks but does not repeat Sigstore verification. The publisher workflow runs `verify bundle` before attestation and invokes `publish github` only after attestation succeeds.

Publication enforces these guarantees in order:

1. Resolve the exact tag with Git and require it to equal `GITHUB_SHA`.
2. Poll GitHub for the tag and require it to identify exactly one release. Draft discovery makes 24 attempts, 5 seconds apart. Absence after this budget fails; the command never creates a release.
3. Require a draft before taking the mutation path. If `--no-undraft` was requested and the one matching release is already public, the state is indeterminate because the requested draft-only outcome was not preserved. Without `--no-undraft`, an already-public release is read without mutation: an exact match of count, names, uploaded states, and digests is a successful completed-publication rerun; any other state is indeterminate. The command never re-drafts a release.
4. Read the draft's assets and refuse any name outside the expected closed set. The command never deletes an unexpected asset.
5. Upload every expected local path with `gh release upload --clobber`. Clobber applies only to expected names that passed the closed-set check.
6. Poll asset state up to 12 times, 1 second apart. Success requires the expected count, unique expected names, `uploaded` state, and the exact GitHub-reported `sha256:<hex>` digest for every asset.
7. If `--no-undraft` is absent, change the release from draft to public. This is the last mutation. A failure from the undraft request is indeterminate because the update may have applied. If `--no-undraft` is present, do not change the draft state.
8. Read the release again and require its final draft state to match the requested outcome before returning the release URL and sorted asset names. A failed final read or an unexpected state after an undraft request is indeterminate. A public final state under `--no-undraft` is also indeterminate.

A retryable operation uses at most four attempts, waiting 1 second, 2 seconds, and 4 seconds between attempts. Tag and commit mismatches, unexpected assets, and digest mismatches are not retryable.

A missing distribution path (`--dist` or `RELEASE_DIST`), a missing `RELEASE_APP_TOKEN`, missing or malformed `GITHUB_REPOSITORY`, `GITHUB_REF_NAME`, or `GITHUB_SHA`, and malformed GitHub endpoint configuration are configuration errors. They exit with code `2` before any publication request. An unresolvable `RELEASE_GIT_PATH` or `RELEASE_GH_PATH` is reported when the selected binary is first invoked and exits with code `1`. The Git path is first used for tag resolution; the GitHub CLI path is first used for upload, after tag resolution, draft discovery, and the pre-upload asset read. Every other post-configuration tag-resolution, GitHub API, upload, convergence, or release-contract failure also exits with code `1`. Success exits with code `0`. No other exit code is defined.

## Homebrew tap initialization

`release-cli init homebrew-tap` writes a minimal cask-only tap into a local output directory. It performs no Git or GitHub operation. The output path must not exist or must be an empty directory. A file, symlink, or nonempty directory is rejected without changing its contents.

The command writes exactly these files:

| Path | Purpose |
| --- | --- |
| `.github/workflows/casks.yml` | Calls the reusable `homebrew-tap-ci.yml` workflow for pull requests that change `Casks/**`. |
| `.github/dependabot.yml` | Checks weekly for GitHub Actions updates. |
| `Casks/.gitkeep` | Keeps the empty cask directory in Git. |
| `README.md` | Records the tap and install syntax. |

The reusable workflow reference is pinned to the full source commit stamped into the running `release-cli` binary. Development builds stamped with `none`, malformed commits, and abbreviated commits exit with code `2` before creating the output directory. The generated workflow grants `contents: read` to its only caller job and sets top-level permissions to an empty map.

The command does not generate `Formula/`, a publisher workflow, repository settings, branch protection, secrets, or a GitHub App. Follow [Set up a Homebrew tap](../how-to/set-up-homebrew-tap.md) to create the repository and connect a producer.

## Homebrew cask publication

`release-cli publish homebrew` reads the cask generated by GoReleaser and reconciles it through a tap pull request. The command never writes the tap's default branch, force-updates a branch, deletes a path, enables auto-merge, or merges the pull request.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Distribution directory | `--dist` | `RELEASE_DIST` | None. A path is required. |
| Target tap | `--tap` | None. | None. Use `owner/repository` form. |
| Cask token | `--cask` | None. | None. Use lowercase letters, digits, and interior hyphens. |
| Release App installation token | None. | `RELEASE_APP_TOKEN` | None. A token is required. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

The command requires this GitHub Actions context:

| Variable | Value |
| --- | --- |
| `GITHUB_REPOSITORY` | Source repository in `owner/name` form. |
| `GITHUB_REF_NAME` | Stable release tag. |
| `GITHUB_SHA` | Expected 40-character lowercase commit SHA for the workflow run. |
| `GITHUB_API_URL` | Optional absolute GitHub API base URL. The public GitHub API is the default. |
| `GITHUB_SERVER_URL` | Optional absolute GitHub server and upload base URL used with a custom API URL. |

The command opens `homebrew/Casks/<cask>.rb` beneath the distribution root. The path must resolve to a nonempty regular file no larger than 1 MiB. Root-confined file access rejects a symbolic link that escapes the distribution directory. The cask must contain one literal `version "<version>"` declaration, and that version must equal `GITHUB_REF_NAME` after removal of its leading `v`.

Publication enforces these guarantees in order:

1. Read the tap's default branch, head commit, and current cask.
2. Find the unique pull request whose base is the default branch and whose head is `release/<cask>/v<version>`. Multiple matching pull requests are a conflict.
3. Return `published` without mutation when the default branch already contains the exact generated bytes.
4. Refuse a different cask at the same or a newer version. A malformed current version also fails before mutation.
5. Create the deterministic publication branch from the observed default-branch commit when the branch is absent.
6. Accept an existing publication commit only when it has the observed default-branch commit as its sole parent, changes only `Casks/<cask>.rb`, classifies that path as added or modified, and contains the exact generated bytes. The command refuses every other branch state.
7. Commit the generated cask to an unchanged new branch. The update uses the observed blob SHA when the cask already exists.
8. Return `open` when a matching pull request already exists. Otherwise, open a non-draft pull request with maintainer edits and auto-merge disabled, then return `created`.

After a pull request is merged, a later invocation returns `published` only when the default branch contains the exact generated cask. A merged pull request without those bytes, or a closed unmerged pull request, is a conflict.

Repository reads and retryable writes use at most four attempts, waiting 1 second, 2 seconds, and 4 seconds between attempts. After a failed branch, file, or pull-request write, the command reads fresh state before retrying. This accepts a write that GitHub applied before losing the response without creating a duplicate commit or pull request.

A missing or malformed flag, Actions variable, token, endpoint, or source commit is a configuration error and exits with code `2` before a tap request. A missing, malformed, empty, non-regular, or oversized generated cask exits with code `1` before a tap request. Repository failures, conflicts, and failed postconditions also exit with code `1`. Success exits with code `0`.

### Reusable Homebrew publisher

`.github/workflows/publish-homebrew.yml` publishes one generated cask only after the public GitHub Release job succeeds. The caller passes the authoritative `release-assets` artifact ID and digest, the exact checksum-signing workflow ref, the tap and cask names, and the Release App client ID. Set `publish-homebrew` to `true` to enable publication. The default is `false`.

The reusable workflow declares `release-app-private-key` as an optional secret because a disabled call must not require or mint a tap credential. An enabled call requires the client ID and private key before any tap request. It verifies the artifact handoff and signed release bundle before minting a repository-scoped App token with only `contents: write` and `pull-requests: write` for the selected tap. The generated `homebrew/Casks/<cask>.rb` control file is protected by the Actions artifact digest but is deliberately excluded from `checksums.txt` and the GitHub Release assets. The GitHub Release publisher removes the Homebrew control after artifact-digest verification; the Homebrew publisher isolates it while verifying the signed bundle, then restores it for tap publication.

The publisher returns the deterministic branch, pull request URL, and reconciled state. A successful first run returns `created`; a rerun while the same pull request remains open returns `open`; and a rerun after the exact cask reaches the tap's default branch returns `published`. The workflow never enables auto-merge or merges the pull request.

The producer's `.goreleaser.yaml` must declare a `homebrew_casks` entry with `skip_upload: true`. The Go pre-publish workflow carries `dist/homebrew/Casks/*.rb` in the authoritative Actions artifact and formats generated casks with Homebrew before upload. It does not add the control file to the signed release payload set.

### Optional macOS signing and notarization

`.github/workflows/go-pre-publish.yml` accepts `sign-and-notarize-macos`, which defaults to `false`. Enabling it requires all five optional workflow secrets:

- `macos-sign-p12`;
- `macos-sign-password`;
- `macos-notary-key`;
- `macos-notary-key-id`;
- `macos-notary-issuer-id`.

The workflow fails before staging when any enabled credential is absent. GoReleaser uses Quill to sign and notarize every Darwin build, waits up to 20 minutes for Apple to accept each submission, and archives only accepted binaries. Apple rejection or timeout fails pre-publish, so neither the GitHub Release nor Homebrew publisher runs.

When signing is disabled, the workflow does not require Apple credentials. Existing external callers therefore preserve their credential-free release path. Producers that enable signing must add a guarded `notarize.macos` block to `.goreleaser.yaml`; a workflow input alone cannot add signing policy to a producer's GoReleaser configuration.

## Signed release bundle verification

`release-cli verify bundle` verifies the local release bundle before the GitHub Release workflow attests or uploads it.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Distribution directory | `--dist` | `RELEASE_DIST` | None. A path is required. |
| Certificate identity | `--identity` | `RELEASE_IDENTITY` | None. An exact, absolute HTTPS URL with a host is required. |
| Certificate OIDC issuer | `--issuer` | `RELEASE_ISSUER` | `https://token.actions.githubusercontent.com`. The effective value must be an absolute HTTPS URL with a host. |
| Cosign binary | None. | `RELEASE_COSIGN_PATH` | Resolve `cosign` from `PATH`. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

An explicitly set flag takes precedence over its environment variable. The identity and issuer must each be an absolute HTTPS URL with a host. `RELEASE_COSIGN_PATH` has no corresponding flag; set it to the Cosign executable when `cosign` is not on `PATH`.

All local checks precede the Cosign invocation. The local checks enforce these requirements:

- `checksums.txt` and `checksums.txt.sigstore.json` are regular files.
- Every `checksums.txt` entry is a regular file whose SHA-256 digest matches the manifest.
- Neither control file is listed as a payload.
- The distribution directory contains only the listed payloads and the two control files. An extra file, directory, symbolic link, or other entry fails this closed-set check.

Only after the local checks succeed does the command verify the detached `checksums.txt.sigstore.json` bundle for `checksums.txt` against the exact certificate identity and OIDC issuer. Any local failure means Cosign is never invoked.

Missing `--dist`/`RELEASE_DIST` or `--identity`/`RELEASE_IDENTITY`, and a malformed identity or issuer URL, are configuration errors and exit with code `2`. A local verification failure, a Sigstore verification failure, or an unresolvable Cosign binary exits with code `1`.

## Actions artifact handoff

`verify handoff` reads the artifact metadata from the GitHub Actions API before any artifact download. It validates all of these conditions:

- the artifact exists;
- the artifact belongs to the current workflow run;
- the artifact has not expired; and
- the artifact's GitHub-reported digest matches `--digest` after digest normalization.

The command obtains its Actions context from these environment variables:

| Variable | Value |
| --- | --- |
| `GITHUB_REPOSITORY` | Repository in `owner/name` form. |
| `GITHUB_RUN_ID` | Positive workflow run ID. |
| `GITHUB_TOKEN` or `GH_TOKEN` | Token used to authenticate the GitHub API client. A nonempty `GITHUB_TOKEN` takes precedence; `GH_TOKEN` is the fallback. |
| `GITHUB_API_URL` | Optional absolute GitHub API base URL. If it is unset or identifies `api.github.com`, the command uses the public GitHub client. A custom URL selects that API endpoint. |
| `GITHUB_SERVER_URL` | Optional absolute GitHub server and upload base URL used with a custom `GITHUB_API_URL`. If omitted for a custom API, the API URL is also used as the upload base. |

A missing or malformed artifact input or Actions environment is a configuration failure. The command exits with code `2` before making a network request. An absent artifact, missing workflow-run metadata, wrong workflow run, expired artifact, or digest mismatch is a verification failure and exits with code `1`.

### Metadata request retries

`verify handoff` makes at most four metadata requests: one initial request and up to three retries. After successive retryable failures, it waits 1 second, 2 seconds, and 4 seconds before the next request.

The command retries only transient failures: GitHub rate limiting and HTTP `5xx` responses. It never retries an absent artifact, an authentication failure, or a malformed response. If the context is canceled before or during a request, or during a retry wait, the command returns immediately without another request.

This policy deliberately matches the `retries: 3` behavior of the three `actions/github-script` metadata blocks that `verify handoff` replaces.

Artifact handoff integrity has three owners:

1. `release-cli verify handoff` verifies the GitHub API metadata tuple before download.
2. The SHA-pinned `actions/download-artifact` step, configured with `digest-mismatch: error`, verifies the transport digest of the artifact ZIP.
3. Later `release-cli` content commands verify the extracted content.

`release-cli` does not download the artifact and never reproduces the Actions ZIP digest.

## Go staging profile

`stage --profile go` builds the Go release bundle, validates the result, and writes the OCI input projection.

| Value | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Profile | `--profile` | `RELEASE_PROFILE` | None. The only accepted value is `go`. |
| Distribution directory | `--dist` | `RELEASE_DIST` | None. A basename is required. |
| GoReleaser binary | None. | `RELEASE_GORELEASER_PATH` | Resolve `goreleaser` from `PATH` when the build starts. |
| JSON output | `--json` | `RELEASE_JSON` | Disabled. |

An explicitly set flag takes precedence over its environment variable. `RELEASE_GORELEASER_PATH` is environment-only, as are `RELEASE_MELANGE_PATH`, `RELEASE_APKO_PATH`, `RELEASE_COSIGN_PATH`, `RELEASE_GH_PATH`, and `RELEASE_GIT_PATH`. The distribution directory must be a basename other than `.` or `..`; a value containing a path separator is invalid. It must name the same directory configured in `.goreleaser.yaml` because the GoReleaser invocation has no distribution-directory flag.

The command performs these operations in order:

1. Resolve and validate the CLI flags and environment variables. A missing profile or distribution directory, an unknown profile, a distribution value that is not a basename, or a malformed boolean environment value exits with code `2` before the build starts.
2. Resolve the configured GoReleaser executable and run `goreleaser release --clean --skip=publish`.
3. Read `checksums.txt`, require at least one payload entry, and verify every listed payload against its SHA-256 digest.
4. Require a nonempty regular `checksums.txt.sigstore.json`. This stage requires the bundle but does not verify its signature.
5. Read `artifacts.json` and require exactly two Linux `Binary` records: one for `amd64` and one for `arm64`.
6. Require each selected binary path to start with the distribution directory's basename. The remaining path must stay confined beneath that directory, and each selected binary must be a regular executable file.
7. Write `oci-build-inputs.json` into the distribution directory for the downstream OCI builder.

The GoReleaser `--clean` option deletes and rebuilds the distribution directory. `stage --profile go` is therefore a build command, not read-only validation. An unresolvable GoReleaser executable or a failed GoReleaser process exits with code `1`; executable resolution fails before the process starts. A post-build validation or projection-write failure also exits with code `1` and identifies the offending artifact or operation.

GoReleaser's stdout and stderr both go to the CLI's stderr stream. Under `--json`, the `release.dev/result/v1` envelope remains the only stdout content.

The projection has schema `release.dev/oci-build-inputs/v1` and contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `schema` | string | Always `release.dev/oci-build-inputs/v1`. |
| `profile` | string | Staging profile that produced the projection. For this profile, the value is `go`. |
| `binaries` | array of objects | Exactly one canonical binary for `linux/amd64` and one for `linux/arm64`. |
| `binaries[].platform` | string | Canonical platform: `linux/amd64` or `linux/arm64`. |
| `binaries[].name` | string | Shared, nonempty binary filename. Both Linux binaries must have the same name. |
| `binaries[].path` | string | Confined, artifact-root-relative path to the canonical binary. |
| `binaries[].digest` | string | SHA-256 digest of the canonical binary, as `sha256:` followed by 64 lowercase hexadecimal digits. |

The projection records staged facts only. The image builder recomputes each digest from the downloaded bytes before packaging.

## Profiles

`--profile` selects ecosystem-specific staging rules while keeping `stage` as the command. The current implementation dispatches `go` directly and rejects every other value with exit code `2`. New ecosystems extend the accepted profile values rather than adding ecosystem-specific top-level verbs.

## Nix flake

The repository root is a Nix flake with these outputs for each supported
system:

| Output | Meaning |
| --- | --- |
| `packages.<system>.release-cli` | Source-built `release-cli` package. |
| `packages.<system>.default` | Alias of the `release-cli` package. |
| `apps.<system>.release-cli` | Runnable `release-cli` application. |
| `apps.<system>.default` | Alias of the `release-cli` application. |
| `checks.<system>.release-cli` | Package build used by `nix flake check`. |

The supported systems are `aarch64-darwin`, `aarch64-linux`,
`x86_64-darwin`, and `x86_64-linux`. Nixpkgs 26.05 is pinned because it is the
last Nixpkgs release that supports `x86_64-darwin`.

The package builds `cmd/release-cli` from the exact flake source with CGO
disabled. It uses Go 1.26.6 from a fixed source hash and downloads Go modules
through the fixed `vendorHash`. Linker flags embed the version from
`.release-please-manifest.json` and the flake source revision.

The flake does not expose an overlay or NixOS module. It does not install the
prebuilt GitHub Release archive or verify its GitHub artifact attestation.
Consumers pin the source, Nixpkgs, Go source, and Go module dependency content
through `flake.lock` and the fixed-output hashes.

## Release unit and consumer pin

The reusable workflows, `.github/actions/setup-release-cli`, and `release-cli` form one release unit and share one version. The producer loads the sibling action with `uses: ./.github/actions/setup-release-cli`. A consumer pins the workflow at one full commit SHA, and that self-reference selects the action from the same commit and its stamped default CLI version. The current released pin is `0fc99489d31d400bc3f69d6636d60e7d3f3d0251` (`v0.1.3`). Consumers cannot select an independent CLI version.

Direct CLI users can install a tagged release through
[mise's built-in GitHub backend](../how-to/install-release-cli-with-mise.md) or
[the repository's Nix flake](../how-to/install-release-cli-with-nix.md). These
installation paths do not change the release-unit pin used by reusable workflow
consumers.

The setup action has two optional inputs:

| Input | Required | Meaning |
| --- | --- | --- |
| `cli-path` | No | Unsupported path to a caller-supplied `release-cli` binary. The caller owns the workflow-to-binary pairing. |
| `local-build` | No | Acquisition policy: `auto`, `always`, or `never`. The default is `auto`. |

With `local-build: auto`, the action builds from source only when the caller and reusable workflow belong to the same repository and the current version tag matches the action's stamped version. `always` forces a source build, and `never` forces installation of the stamped release. A nonempty `cli-path` takes precedence unless it conflicts with `local-build: always`.

For a source build, the action requires the sibling action and reusable workflow to come from the same repository, uses the source beside that action, and requires the runner-provided reusable workflow SHA. It reads the pinned Go patch version from `go.mod`, restores exact OS, architecture, Go version, and source SHA caches for `GOCACHE` and `GOMODCACHE`, and builds with the stamped version and workflow SHA. A cache miss performs a complete build. The executable itself is not cached.

For an installed release, the action requires `github.action_repository` and a runner-provided `gh` with attestation support. It downloads exactly one Linux amd64 archive and `checksums.txt`, verifies the archive's unique SHA-256 entry, and runs `gh attestation verify` against the action repository with `--signer-workflow <action_repository>/.github/workflows/publish-github-release.yml` and `--deny-self-hosted-runners`.

Both supported acquisition modes require the binary's reported version and protocol to match the action stamps before a workflow invokes a CLI command. With `cli-path`, the supplied path must exist, be a regular file, and be executable. A version or protocol mismatch warns and continues because the caller owns that unsupported pairing.

The action exposes `cli-path`, `reported-version`, and `reported-protocol` as outputs.

## Released archive names

GoReleaser names archives with this pattern:

```text
release-cli_<version>_<os>_<arch>.tar.gz
```

Windows archives use `.zip`. For example, the Linux amd64 archive is `release-cli_<version>_linux_amd64.tar.gz`. The checksum manifest is `checksums.txt`.
