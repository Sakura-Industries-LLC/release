# `release-cli` reference

`release-cli` is the policy executable used by the reusable workflows. This
page describes its command-line interface. Workflow interfaces, artifacts, and
publication states are defined in the [release system reference](release-system.md).

## Global behavior

### Command summary

| Command | Effect |
| --- | --- |
| `stage --profile go` | Run GoReleaser, validate the release bundle, and project canonical OCI inputs. |
| `image build` | Build signed APK repositories and a locked OCI layout from staged binaries. |
| `image verify` | Verify the OCI layout, runtime files, annotations, SBOMs, and index digest. |
| `plan tags` | Read registry state and report exact and channel tag decisions. |
| `publish oci prepare` | Validate, push, verify, and recursively sign an image by digest without applying tags. |
| `publish oci finalize` | Re-read registry state and apply eligible tags after attestation. |
| `publish github` | Reconcile a closed bundle with one matching GitHub Release and optionally undraft it. |
| `publish homebrew` | Reconcile one generated cask through a tap pull request. |
| `publish scoop` | Reconcile one generated root manifest through a bucket pull request. |
| `publish package-repository` | Verify a producer release and converge an APT/DNF/APK repository in R2. |
| `init homebrew-tap` | Generate a cask-only tap scaffold. |
| `init scoop-bucket` | Generate a root-layout Scoop bucket scaffold. |
| `verify bundle` | Verify a closed local release bundle and its Cosign signature. |
| `verify handoff` | Verify an Actions artifact's API metadata before download. |
| `version` | Report version, source commit, and protocol. |

### Configuration precedence

An explicitly set flag overrides its corresponding `RELEASE_*` environment
variable. The environment value overrides a derived default. A value with no
flag is environment-only.

Boolean environment variables use Go's `strconv.ParseBool` values:
`1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, or
`False`. Another value is invalid configuration.

### Output

With `--json` or `RELEASE_JSON=true`, stdout contains exactly one JSON document
after argument parsing succeeds:

```json
{
  "schema": "release.dev/result/v1",
  "command": "<command path>",
  "ok": true,
  "result": {}
}
```

A command or configuration failure after dispatch sets `ok` to `false`, puts an
`error` string in `result`, and preserves the nonzero exit code. Unknown flags
and invalid flag values write usage to stderr without a JSON envelope. Unknown
commands and wrong argument counts also have no envelope, but automatic usage
output is suppressed.

Without JSON, `version` writes its requested data to stdout. Other successful
commands are silent on stdout. Diagnostics, warnings, and tool output go to
stderr.

### Exit codes

| Code | Meaning |
| ---: | --- |
| `0` | Success. |
| `1` | Tool, network, verification, state, or publication failure. |
| `2` | Usage or configuration failure. |

No other exit code is defined. Exit code `1` does not by itself mean that a
mutation is safe to retry.

## `stage --profile go`

```text
release-cli stage --profile go --dist DIR [--json]
```

| Value | Flag | Environment | Default |
| --- | --- | --- | --- |
| Profile | `--profile` | `RELEASE_PROFILE` | Required; only `go`. |
| Distribution basename | `--dist` | `RELEASE_DIST` | Required. |
| GoReleaser executable | None | `RELEASE_GORELEASER_PATH` | Resolve `goreleaser` from `PATH`. |
| JSON | `--json` | `RELEASE_JSON` | `false` |

`DIR` is a basename other than `.` or `..` and must match GoReleaser's output
directory. The command invokes exactly:

```text
goreleaser release --clean --skip=publish
```

The command then verifies every `checksums.txt` entry, requires a regular
`checksums.txt.sigstore.json`, reads `artifacts.json`, and selects every
`linux/{amd64,arm64}` Binary record. Duplicate `(arch, name)` pairs are
rejected. The name set must be identical and nonempty on both architectures; a
name present on only one architecture is an error that names it. Selected
paths are confined, and the command writes `oci-build-inputs.json` as
`release.dev/oci-build-inputs/v2`.

Native package signing is controlled by environment only:

| Environment | Contract |
| --- | --- |
| `RELEASE_NATIVE_PACKAGE_SIGNING` | Enable signing when true. |
| `RELEASE_RPM_SIGNING_KEY_FILE` | Owner-only regular OpenPGP private-key file. |
| `RELEASE_APK_SIGNING_KEY_FILE` | Owner-only regular RSA private-key file. |
| `NFPM_RELEASE_RPM_PASSPHRASE` | Passphrase selected by nFPM ID `release`. |
| `NFPM_RELEASE_APK_PASSPHRASE` | Passphrase selected by nFPM ID `release`. |

When signing is disabled, inherited native-signing values are replaced with
empty values so GoReleaser templates cannot use ambient credentials. When
enabled, a missing value, malformed boolean, inaccessible file, or
group/other-readable key is configuration error `2` before GoReleaser starts.

JSON result:

| Field | Contract |
| --- | --- |
| `assets` | Number of checksum-verified payloads. |
| `binaries` | Selected Linux binaries in platform-major order (`amd64` then `arm64`), then name ascending. Each entry has `arch`, `name`, original dist-prefixed `path`, and observed permission `mode` in octal. |

`--clean` deletes and rebuilds the distribution directory. This command is not
read-only.

## `image build`

```text
release-cli image build --input DIR --work DIR --output DIR \
  [--melange-config PATH] [--apko-config PATH] \
  --build-date RFC3339 [--version VERSION] [--json]
```

| Value | Flag | Environment | Default |
| --- | --- | --- | --- |
| Input root | `--input` | `RELEASE_INPUT` | Required. |
| Scratch root | `--work` | `RELEASE_WORK` | Required. |
| Output root | `--output` | `RELEASE_OUTPUT` | Required. |
| Melange config | `--melange-config` | `RELEASE_MELANGE_CONFIG` | `melange.yaml` |
| apko config | `--apko-config` | `RELEASE_APKO_CONFIG` | `apko.yaml` |
| Build date | `--build-date` | `RELEASE_BUILD_DATE` | Required RFC 3339. |
| Version | `--version` | `RELEASE_VERSION` | `GITHUB_REF_NAME` without one leading `v`. |
| Melange executable | None | `RELEASE_MELANGE_PATH` | Resolve from `PATH`. |
| apko executable | None | `RELEASE_APKO_PATH` | Resolve from `PATH`. |

Required Actions context is `GITHUB_REPOSITORY`, `GITHUB_REPOSITORY_OWNER`,
`GITHUB_SERVER_URL`, and `GITHUB_SHA`. Work and output roots must be disjoint,
absent or empty, and neither may contain the other.

The command verifies the projected binary digests and ELF contract, stages
each file at `work/sources/<apkarch>/<binary-name>`, creates an ephemeral
Melange signing key, builds `x86_64` and `aarch64` APK repositories, writes
the public key, locks apko, and composes the OCI layout and architecture
SBOMs. The private Melange key remains under the scratch root.

`oci-build-inputs.json` is limited to 4 MiB.

The `release.dev/image-build/v2` JSON result contains `version`, `binaries`
(sorted name-ascending), `work`, `output`, `build_date`, and two `packages`
entries. Each package entry contains `platform`, APK `arch`, output-relative
`package`, and `binary_digests`. Each `binary_digests` entry is
`{name, digest}` sorted by name.

## `image verify`

```text
release-cli image verify --output DIR --work DIR \
  [--version VERSION] [--json]
```

| Value | Flag | Environment | Default |
| --- | --- | --- | --- |
| Output root | `--output` | `RELEASE_OUTPUT` | Required. |
| Scratch root | `--work` | `RELEASE_WORK` | Required. |
| Version | `--version` | `RELEASE_VERSION` | Tag name without one leading `v`. |

The command also requires `GITHUB_SHA`, `GITHUB_SERVER_URL`, and
`GITHUB_REPOSITORY`. Expected binary names and canonical digests come from
the staged `work/sources/<apkarch>/<binary-name>` trees and the v2
projection. It verifies:

- one OCI index with Linux `amd64` and `arm64` manifests;
- required source, version, revision, title, description, and license
  annotations on index, manifests, and config labels;
- one layer, runtime user `65532`, and an Entrypoint of exactly
  `/usr/bin/<name>` for one expected staged name on every platform;
- every expected name present exactly once in that layer as a regular `0755`
  uid/gid `0` file within 64 MiB, with bytes equal to its canonical digest;
  and
- one SPDX `APPLICATION` package at `<version>-r0` per architecture.

Index, manifest, config, and SPDX JSON documents are limited to 4 MiB.

The index digest is SHA-256 of the exact `layout/index.json` bytes. On success,
the command writes it to `image-digest.txt` and returns a
`release.dev/image-verify/v2` result containing `version`, `binaries` (sorted
name-ascending), `index_digest`, and two platform records with manifest,
config, layer, and `binary_digests` (`{name, digest}` sorted by name).

## `plan tags`

```text
release-cli plan tags [--image IMAGE] [--version VERSION] \
  --digest sha256:HEX [--plain-http] [--json]
```

| Value | Flag | Environment | Default |
| --- | --- | --- | --- |
| Image | `--image` | `RELEASE_IMAGE` | Lowercase caller GHCR path. |
| Version | `--version` | `RELEASE_VERSION` | Tag name without one leading `v`. |
| Digest | `--digest` | `RELEASE_DIGEST` | Required. |
| Plain HTTP | `--plain-http` | None | `false` |

The image is an untagged `host/path` value. `--plain-http` is accepted only for
`localhost`, `127.0.0.1`, or `::1`, optionally with a port.

Registry token precedence is nonempty `GITHUB_TOKEN`, then nonempty `GH_TOKEN`.
Username precedence is `GITHUB_ACTOR`, then `x-access-token`. Without a token,
reads are anonymous.

The command reads only. It reports decisions for exact, minor, major, and
`latest` tags. JSON fields are `image`, `version`, normalized `digest`, `tags`
to create, and `decisions` with `tag`, `scope`, and `action` (`create`, `accept`,
or `retain`). A direct planner has no cross-process writer lock.

## `publish oci prepare`

```text
release-cli publish oci prepare --layout DIR [--image IMAGE] \
  [--version VERSION] --digest sha256:HEX [--dry-run] \
  [--plain-http] [--json]
```

`--layout`/`RELEASE_LAYOUT` and `--digest`/`RELEASE_DIGEST` are required. Image,
version, credentials, and loopback-only plain HTTP resolve as for `plan tags`.
`--dry-run` or `RELEASE_DRY_RUN=true` disables every registry write and Cosign
invocation.

Enabled preparation:

1. validates the layout and exact index digest;
2. reads current exact and channel tags and rejects an immutable conflict;
3. pushes unique blobs, platform manifests, and index by digest;
4. resolves and verifies each pushed digest; and
5. invokes `cosign sign --yes --recursive <image>@<digest>`.

The inspected OCI index and manifest JSON documents are limited to 4 MiB.

`RELEASE_COSIGN_PATH` overrides the Cosign executable. The command never
creates or moves a tag.

The `release.dev/oci-prepare/v1` result contains `authoritative`, `image`,
`version`, `index_digest`, platform digests, and the observed exact/channel tag
state. Dry-run results set `authoritative` to `false` and cannot be finalized.

## `publish oci finalize`

```text
release-cli publish oci finalize --result - [--plain-http] [--json]
```

`--result` is required and accepts only `-`, meaning stdin. The input is limited
to 4 MiB and must contain exactly one successful `release.dev/result/v1`
envelope from `publish oci prepare --json`, with a valid authoritative
`release.dev/oci-prepare/v1` result. Files, trailing JSON, dry-run results, and
other commands are rejected. An oversized or malformed envelope is usage error
`2`.

Finalization re-reads every planned tag, compares fresh state with preparation,
accepts unchanged state or tags already on the candidate, rejects other drift,
recomputes the plan, writes tags serially, and independently verifies each
resolution.

The `release.dev/oci-finalize/v1` result contains `image`, `version`,
`index_digest`, and tag arrays `applied`, `accepted`, and `retained`. A saved
prepare envelope is not a durable receipt and must not be replayed later.

## `publish github`

```text
release-cli publish github --dist DIR [--no-undraft] [--json]
```

| Value | Flag | Environment | Default |
| --- | --- | --- | --- |
| Distribution root | `--dist` | `RELEASE_DIST` | Required. |
| Keep draft | `--no-undraft` | None | `false` |
| GitHub CLI | None | `RELEASE_GH_PATH` | Resolve `gh`. |
| Git | None | `RELEASE_GIT_PATH` | Resolve `git`. |
| App token | None | `RELEASE_APP_TOKEN` | Required. |

Required Actions context is `GITHUB_REPOSITORY`, `GITHUB_REF_NAME`, and one full
lowercase `GITHUB_SHA`. Optional `GITHUB_API_URL` and `GITHUB_SERVER_URL`
select GitHub Enterprise endpoints.

The command rebuilds the closed asset set from `checksums.txt` and its two
controls. It does not repeat Cosign verification; the reusable workflow runs
`verify bundle` before attestation and publication.

Publication order:

1. require the tag to resolve to `GITHUB_SHA`;
2. poll for exactly one release with that tag;
3. require a draft for mutation;
4. reject an existing unexpected asset name;
5. upload every expected name with clobber semantics;
6. wait for exact names, uploaded states, and GitHub SHA-256 digests;
7. undraft last unless `--no-undraft`; and
8. read and require the requested final state.

An already-public release is never mutated. Without `--no-undraft`, an exact
public match is a successful completed-publication retry; another state is
indeterminate. With `--no-undraft`, every public state is indeterminate. The
command never creates, re-drafts, or deletes a release. It refuses unexpected
asset names, but clobber convergence can replace an expected same-name asset.

JSON result fields are `release_id`, `tag`, `url`, final `draft`, and sorted
`assets`.

## `init homebrew-tap`

```text
release-cli init homebrew-tap --tap OWNER/HOMEBREW-NAME \
  --output DIR [--json]
```

The output path must be absent or an empty directory. The CLI source-commit
stamp must be one full lowercase SHA; the version stamp is not validated. The
command writes exactly:

```text
.github/workflows/casks.yml
.github/dependabot.yml
Casks/.gitkeep
README.md
```

The validation workflow pins `meigma/release` to the CLI source commit. The
command performs no Git or GitHub request. JSON fields are `tap`, `output`, and
lexically sorted `files`.

## `init scoop-bucket`

```text
release-cli init scoop-bucket --bucket OWNER/REPOSITORY \
  --output DIR [--json]
```

The same output and source-commit-stamp conditions apply. The command writes
exactly:

```text
.gitattributes
.github/workflows/manifests.yml
.github/dependabot.yml
README.md
```

It creates no sample manifest and performs no remote operation. JSON fields are
`bucket`, `output`, and sorted `files`.

## `publish homebrew`

```text
release-cli publish homebrew --dist DIR --tap OWNER/REPOSITORY \
  --cask TOKEN [--json]
```

`--dist` or `RELEASE_DIST`, `--tap`, `--cask`, and `RELEASE_APP_TOKEN` are
required. The cask token uses lowercase letters, digits, and interior hyphens.
Required Actions context is `GITHUB_REPOSITORY`, stable `GITHUB_REF_NAME`, and
full lowercase `GITHUB_SHA`.

The command reads `homebrew/Casks/<cask>.rb`, which must be a confined,
nonempty, regular file no larger than 1 MiB with one matching literal version.
It reads the destination default branch and matching deterministic pull request,
accepts exact published or open state, rejects conflicting or newer content,
and otherwise creates one child commit that changes only the cask and opens a
non-draft pull request with auto-merge disabled.

JSON fields are `tap`, `cask`, deterministic `branch`, `pull_request_url`, and
`state` (`created`, `open`, or `published`).

## `publish scoop`

```text
release-cli publish scoop --dist DIR --bucket OWNER/REPOSITORY \
  --manifest NAME [--json]
```

Configuration and Actions context match Homebrew publication. The command reads
`scoop/<manifest>.json`, a confined nonempty regular file no larger than 1 MiB.
It requires one string `version` equal to the stable source tag without `v` and
writes the exact bytes to root `<manifest>.json` through the same deterministic
branch and pull-request state machine.

JSON fields are `bucket`, `manifest`, `branch`, `pull_request_url`, and the same
three `state` values.

Neither destination publisher writes the default branch, force-updates, deletes
a ref, merges, approves, or enables auto-merge.

## `publish package-repository`

```text
release-cli publish package-repository [flags]
```

| Flag | Environment | Required value |
| --- | --- | --- |
| `--repository` | `RELEASE_REPOSITORY` | Lowercase producer `owner/name`. |
| `--tag` | `RELEASE_TAG` | Stable `vMAJOR.MINOR.PATCH`. |
| `--config` | `RELEASE_PACKAGE_REPOSITORY_CONFIG` | Strict policy YAML. |
| `--keys` | `RELEASE_PACKAGE_KEYS` | Confined reviewed public-key root. |
| `--cloudflare-account-id` | `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account ID. |
| `--r2-bucket` | `RELEASE_R2_BUCKET` | Existing bucket. |
| `--gpg-home` | `RELEASE_GPG_HOME` | Absolute owner-only GnuPG home containing the aggregate secret key. |
| `--gpg-key-id` | `RELEASE_GPG_KEY_ID` | Nonempty aggregate OpenPGP key selector; the maintained workflow passes the full fingerprint. |
| `--gpg-passphrase-file` | `RELEASE_GPG_PASSPHRASE_FILE` | Absolute owner-only regular passphrase file. |
| `--apk-signing-key` | `RELEASE_APK_SIGNING_KEY` | Absolute owner-only regular aggregate APK RSA private-key file. |

Environment-only values:

- `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY`;
- `GITHUB_TOKEN`, falling back to `GH_TOKEN`;
- optional `GITHUB_API_URL` and `GITHUB_SERVER_URL`; and
- executable overrides `RELEASE_GH_PATH`, `RELEASE_DOCKER_PATH`,
  `RELEASE_COSIGN_PATH`, and `RELEASE_GPG_PATH`.

The command accepts no operands. It verifies the public producer release closed
set, exact checksum identity from policy, GitHub asset digests and attestations,
package metadata, and producer RPM/APK signatures. It mirrors existing immutable
packages, regenerates all metadata, signs aggregate roots, installs locally with
APT/DNF/APK, uploads non-root objects before roots, and repeats installation
from the public origin.

JSON result fields are `state` (`published` or `unchanged`), `repository`,
`tag`, generated `artifacts`, and `uploaded`. The command does not create the
bucket, keys, policy, public domain, GitHub environment, or producer release and
never deletes an R2 object.

## `verify bundle`

```text
release-cli verify bundle --dist DIR --identity HTTPS-URL \
  [--issuer HTTPS-URL] [--json]
```

| Value | Flag | Environment | Default |
| --- | --- | --- | --- |
| Distribution root | `--dist` | `RELEASE_DIST` | Required. |
| Certificate identity | `--identity` | `RELEASE_IDENTITY` | Required. |
| OIDC issuer | `--issuer` | `RELEASE_ISSUER` | `https://token.actions.githubusercontent.com` |
| Cosign executable | None | `RELEASE_COSIGN_PATH` | Resolve `cosign`. |

Identity and issuer must be absolute HTTPS URLs with hosts. Before invoking
Cosign, the command verifies regular controls, every payload digest, control
exclusion, and the exact closed directory set. A local failure prevents the
Cosign invocation.

JSON fields are `dist`, `identity`, `issuer`, ordered `payloads`, and ordered
`controls`. Each file entry contains `name` and lowercase SHA-256 `digest`
without a prefix.

## `verify handoff`

```text
release-cli verify handoff --artifact-id N --digest sha256:HEX [--json]
```

`RELEASE_ARTIFACT_ID` and `RELEASE_DIGEST` are the environment alternatives.
The ID is a positive decimal safe integer. Digest hex is case-insensitive and
normalizes to lowercase with a `sha256:` prefix.

Actions context and token:

| Environment | Contract |
| --- | --- |
| `GITHUB_REPOSITORY` | Caller `owner/name`. |
| `GITHUB_RUN_ID` | Positive current run ID. |
| `GITHUB_TOKEN`, then `GH_TOKEN` | API token. |
| `GITHUB_API_URL`, `GITHUB_SERVER_URL` | Optional enterprise endpoints. |

The command reads metadata only. It requires the artifact to exist, belong to
the current run, be unexpired, and have the expected GitHub-reported digest. It
does not download the artifact or reproduce the artifact ZIP digest.

JSON contains `artifact.id`, `name`, normalized `digest`, `size_bytes`,
`run_id`, and `expires_at`.

## `version`

```text
release-cli version [--json]
```

Human output is:

```text
release-cli <version> (<commit>, protocol <n>)
```

JSON result fields are `version`, `commit`, and integer `protocol`. The current
protocol value is `1`. Reusable setup requires the reported version and protocol
to match the action's release stamps for supported acquisition modes.

## Retry behavior

Actions metadata, GitHub publication, Homebrew, Scoop, and selected retryable
OCI operations use at most four attempts with waits of 1, 2, and 4 seconds.
Artifact metadata retries only rate limits and HTTP `5xx`.

Missing-draft polling makes at most 24 observations and waits five seconds
after each miss, including the final exhausted miss. Asset convergence makes at
most 12 observations and waits one second after each incomplete observation,
including the final exhausted observation. Transient GitHub API failures within
an observation use the four-attempt policy.

Homebrew and Scoop read fresh state after a failed write. OCI preparation
retries transient blob pushes and digest verification; finalization retries tag
commits and postcondition reads. Tag planning and preparation's initial tag
collection do not use that four-attempt helper. The package-repository policy
layer does not add a retry loop; a new invocation converges from current R2
object digests and sizes.

Authentication and configuration failures, absent Actions handoff artifacts,
missing required local inputs, tag/commit or digest mismatches, unexpected
assets, immutable conflicts, and conflicting destination state are not
retryable classes. A missing draft and an incomplete expected release-asset set
use the polling contracts above; absent R2 objects are uploaded during
convergence. An undraft request can leave an indeterminate public state; inspect
it before another invocation.
