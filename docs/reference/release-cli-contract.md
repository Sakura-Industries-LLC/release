# `release-cli` contract reference

`release-cli` validates release data, reports machine-readable results, and prepares digest-addressed OCI publication. The [GitHub Release contract](github-release-contract.md) defines the workflow inputs, artifacts, and publication behavior that surround the CLI.

## Commands

| Command | Purpose |
| --- | --- |
| `release-cli stage --profile go --dist PATH [--json]` | Validate the staged Go release files under `PATH`. |
| `release-cli plan tags [--image IMAGE] [--version VERSION] --digest DIGEST [--plain-http] [--json]` | Inspect the immutable exact tag and moving channel tags for an OCI release. |
| `release-cli publish oci prepare --layout PATH [--image IMAGE] [--version VERSION] --digest DIGEST [--dry-run] [--plain-http] [--json]` | Validate and prepare a digest-addressed OCI image publication and recursive signature. |
| `release-cli verify handoff --artifact-id <n> --digest <sha256:...> [--json]` | Verify an Actions artifact's GitHub API metadata before download. |
| `release-cli version [--json]` | Report the CLI version, source commit, and protocol integer. |

`--dist` is required for `stage`. The only accepted profile is `go`. `verify handoff` requires artifact ID and digest values. Supply them with `--artifact-id` and `--digest`, or with `RELEASE_ARTIFACT_ID` and `RELEASE_DIGEST`. An explicitly set flag takes precedence over its environment variable.

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
| `command` | The command path, such as `plan tags`, `publish oci prepare`, `stage`, `verify handoff`, or `version`. |
| `ok` | `true` when the command succeeds; otherwise `false`. |
| `result` | The command-specific result object. |

The `stage --json` result contains these fields:

| Field | JSON type | Value |
| --- | --- | --- |
| `assets` | number | Number of payloads whose checksums matched. |
| `binaries` | object | Entries named `amd64` and `arm64` for the verified Linux binaries. |
| `binaries.<arch>.path` | string | Original `<dist-basename>/`-prefixed path from `artifacts.json`. |
| `binaries.<arch>.mode` | string | Observed permission bits in octal notation. |

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

Without `--json`, a successful `plan tags`, `publish oci prepare`, `stage`, or `verify handoff` command writes nothing to stdout. A successful `version` command writes `release-cli <version> (<commit>, protocol <n>)` to stdout because the version data is the requested output and can be piped. This human format is a convenience, not a stable interface. Human diagnostics and warnings go to stderr. With `--json`, the envelope is the stable machine-readable stdout contract for all commands.

## Exit codes

| Code | Meaning |
| ---: | --- |
| `0` | The command completed successfully. |
| `1` | A release contract or verification check failed. The command fails closed. |
| `2` | Command usage or configuration is invalid. This includes an unsupported `--profile` value. |

No other exit code is defined; in particular, code `3` has no meaning. An exit code does not make a general promise that a command is safe to run again.

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

`plan tags` performs registry reads only. It never writes a tag, blob, or manifest. The reusable publisher workflow still owns tag application in this release. Its existing `actions/github-script` tag planner remains authoritative for publication. The workflow does not call `plan tags` in this release. The command supports planning and inspection only.

### Tag policy

The candidate version must match this canonical stable-version grammar:

```text
^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$
```

The version has exactly three components. It has no `v` prefix, leading zeros, prerelease, or build metadata. Each component must fit in a 64-bit unsigned integer. The publisher workflow compares components with arbitrary-precision integers. The CLI's 64-bit limit is a deliberate fail-closed narrowing of that workflow behavior.

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

Failure ordering differs from the publisher workflow. The workflow checks the exact tag before it resolves any channel. The CLI collects the exact tag and all three channels before it decides the plan. If an immutable-tag conflict and a corrupt channel exist together, the CLI may report the channel failure instead of the immutable-tag conflict. Both planners refuse the plan, and the CLI exits with code `1`. Only the failure reported first can differ.

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

The command exists for verification and the upcoming two-phase publication. In this release, the reusable publisher workflow still performs publication, signing, attestation, and tagging through its existing `actions/github-script` steps. Those workflow steps remain authoritative, and the workflow does not call `publish oci prepare`.

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
