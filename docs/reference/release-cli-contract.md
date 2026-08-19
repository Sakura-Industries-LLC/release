# `release-cli` contract reference

`release-cli` validates and reports release data for the reusable workflows. The [GitHub Release contract](github-release-contract.md) defines the workflow inputs, artifacts, and publication behavior that surround the CLI.

## Commands

| Command | Purpose |
| --- | --- |
| `release-cli stage --profile go --dist PATH [--json]` | Validate the staged Go release files under `PATH`. |
| `release-cli verify handoff --artifact-id <n> --digest <sha256:...> [--json]` | Verify an Actions artifact's GitHub API metadata before download. |
| `release-cli version [--json]` | Report the CLI version, source commit, and protocol integer. |

`--dist` is required for `stage`. The only accepted profile is `go`. `verify handoff` requires artifact ID and digest values. Supply them with `--artifact-id` and `--digest`, or with `RELEASE_ARTIFACT_ID` and `RELEASE_DIGEST`. An explicitly set flag takes precedence over its environment variable.

The artifact ID must be a positive decimal safe integer. The digest must be a 64-digit hexadecimal SHA-256 value with or without the `sha256:` prefix. Digest hex is case-insensitive and is normalized to lowercase with the prefix.

## JSON output

When option and argument parsing succeeds and `--json` is requested, stdout contains exactly one JSON document and no other output. The envelope has this structure:

```text
{"schema":"release.dev/result/v1","command":"<verb path>","ok":<boolean>,"result":{...}}
```

| Field | Value |
| --- | --- |
| `schema` | Always `release.dev/result/v1`. |
| `command` | The command path, such as `stage`, `verify handoff`, or `version`. |
| `ok` | `true` when the command succeeds; otherwise `false`. |
| `result` | The command-specific result object. |

The `stage --json` result contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `assets` | number | Number of payloads whose checksums matched. |
| `binaries` | object | Entries named `amd64` and `arm64` for the verified Linux binaries. |
| `binaries.<arch>.path` | string | Original `<dist-basename>/`-prefixed path from `artifacts.json`. |
| `binaries.<arch>.mode` | string | Observed permission bits in octal notation. |

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

After successful parsing, a command failure under `--json` sets `ok` to `false`
and gives `result` one string field named `error`. The command also returns its
nonzero exit code. If parsing itself fails because of an unknown command or
flag, an invalid flag value, or the wrong number of arguments, no envelope is
written; the usage error goes to stderr and the process exits with code
`2`.

Without `--json`, a successful `stage` or `verify handoff` command writes nothing to stdout. A successful `version` command writes `release-cli <version> (<commit>, protocol <n>)` to stdout because the version data is the requested output and can be piped. This human format is a convenience, not a stable interface. Human diagnostics and warnings go to stderr. With `--json`, the envelope is the stable machine-readable stdout contract for all commands.

## Exit codes

| Code | Meaning |
| ---: | --- |
| `0` | The command completed successfully. |
| `1` | A release contract or verification check failed. The command fails closed. |
| `2` | Command usage or configuration is invalid. This includes an unsupported `--profile` value. |

No other exit code is defined; in particular, code `3` has no meaning. An exit code does not make a general promise that a command is safe to run again.

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

`stage --profile go` validates the files already written under the distribution directory. It performs these checks:

- `checksums.txt` contains at least one payload entry, and every listed payload matches its SHA-256 digest.
- `checksums.txt.sigstore.json` is a non-empty regular file. This stage requires the bundle but does not verify its signature.
- `artifacts.json` contains exactly two Linux `Binary` records: one for `amd64` and one for `arm64`.
- Each selected binary path starts with the distribution directory's basename; the remaining path is relative to that directory and remains confined beneath it.
- Each selected binary is a regular executable file.

A failed check exits with code `1` and writes a diagnostic that identifies the offending artifact. The command does not persist a staging manifest or modify the files it validates.

## Profiles

`--profile` selects ecosystem-specific staging rules while keeping `stage` as the command. The current implementation dispatches `go` directly and rejects every other value with exit code `2`. New ecosystems extend the accepted profile values rather than adding ecosystem-specific top-level verbs.

## Release unit and consumer pin

The reusable workflows, `.github/actions/setup-release-cli`, and `release-cli` form one release unit and share one version. The producer loads the sibling action with `uses: $/.github/actions/setup-release-cli`. A consumer pins the workflow at one full commit SHA, and that self-reference selects the action from the same commit and its stamped default CLI version. `FULL_SHA` is the documentation placeholder for the released commit and will be replaced when this program's final pull request lands. Consumers cannot select an independent CLI version.

The setup action has one optional input:

| Input | Required | Meaning |
| --- | --- | --- |
| `cli-path` | No | Unsupported path to a caller-supplied `release-cli` binary. The caller owns the workflow-to-binary pairing. |

With no `cli-path`, the action requires `github.action_repository` and a runner-provided `gh` with attestation support. It downloads exactly one Linux amd64 archive and `checksums.txt`, verifies the archive's unique SHA-256 entry, and runs `gh attestation verify` against the action repository with `--signer-workflow <action_repository>/.github/workflows/publish-github-release.yml` and `--deny-self-hosted-runners`. It then requires the binary's reported version and protocol to match the action stamps. A mismatch fails before the workflow invokes a CLI command.

With `cli-path`, the action requires the supplied path to exist, be a regular file, and be executable, then runs `version --json`. A version or protocol mismatch produces a warning and continues. This path supports this repository's dogfood release, but it is not a compatibility promise.

The action exposes `cli-path`, `reported-version`, and `reported-protocol` as outputs.

## Released archive names

GoReleaser names archives with this pattern:

```text
release-cli_<version>_<os>_<arch>.tar.gz
```

Windows archives use `.zip`. For example, the Linux amd64 archive is `release-cli_<version>_linux_amd64.tar.gz`. The checksum manifest is `checksums.txt`.
