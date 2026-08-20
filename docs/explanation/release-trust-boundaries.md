# Why release trust is split across workflows and the CLI

A release begins as repository source and ends as public GitHub Release assets
and named OCI images. Those states do not require the same authority. The
release system therefore separates artifact production, artifact verification,
and publication instead of giving one job every credential and every policy
decision.

The reusable workflows, the `setup-release-cli` composite action, and
`release-cli` each enforce a different part of that separation. Their boundaries
are also failure boundaries: an artifact can be built and inspected without
having the authority to publish it, and a publisher must verify what it received
before it uses its write permissions.

## Producers stop at verified artifacts

The Go producer and OCI builder are less privileged than the publishers. The Go
producer can read repository contents and obtain an OIDC identity for the
keyless signature on `checksums.txt`, but it has no package-write permission and
never receives the GitHub App release credential. The OCI builder can read the
source and Actions artifacts, but it has no package-write, attestation-write, or
release permission.

These jobs turn source and configuration into bounded artifacts. The Go
producer calls `release-cli stage --profile go`; the CLI invokes GoReleaser,
validates the closed release bundle, and projects the canonical Linux binaries
for the image build. The OCI builder consumes those binaries, builds the signed
APK repositories and OCI layout, and verifies the completed layout before
uploading it. Neither job can use a successful build as authority to publish.

The handoff carries an Actions artifact ID and digest rather than an unchecked
directory. Each downstream job uses `release-cli verify handoff` to compare the
artifact's GitHub API metadata with that expected identity before download, and
the download action also rejects a digest mismatch. Content-specific checks run
after download. This repetition is deliberate: transport integrity does not
establish that a release bundle or OCI layout satisfies its own contract.

A single job with both build tools and publication credentials would be
shorter, but any build-time command would then run inside the publication trust
boundary. The split keeps compilers and consumer build configuration out of the
jobs that can change a public release or package tag.

## The caller sets the permission ceiling

Every reusable workflow declares the permissions its job needs, but a called
workflow cannot elevate itself beyond the permissions granted by the calling
job. The copyable caller starts with `permissions: {}` and grants permissions to
each job explicitly. This makes the caller a ceiling rather than a passive
forwarder of the callee's request.

The distinction matters for both safety and operation. A producer cannot gain
`packages: write` merely because a future edit requests it inside the reusable
workflow. Consumer repositories also must grant `attestations: read` so the
setup action can verify the released CLI archive. The release repository builds
the CLI from its matching tag instead, but the reusable contract keeps the
permission needed by consumers. Missing permissions fail at that boundary.

The publisher ceilings differ because their effects differ. The OCI publisher
has package, attestation, and OIDC permissions but receives no GitHub App key.
The GitHub Release publisher can create a short-lived App token for release
mutation but has no package-write permission. The top-level dependency from the
GitHub publisher to the OCI publisher also keeps the GitHub Release in draft
state when image publication fails.

## The CLI is verified before it enforces release policy

The CLI is itself part of the release supply chain. Verifying only the consumer
artifact would leave the verifier and publisher executable unauthenticated.
`setup-release-cli` therefore establishes the CLI's identity before any workflow
invokes it.

When the release repository runs its own matching version tag, the composite
action builds `release-cli` from the source beside the pinned action. It requires
the runner-provided reusable workflow SHA, installs the Go patch version pinned
by the release unit, and stamps that SHA into the executable. Exact-key
`GOCACHE` and `GOMODCACHE` entries accelerate later jobs in the sequential
release graph. A cache miss remains a complete build, and the executable itself
is never restored from a cache.

This source path removes the same-run CLI artifact handoff, but it deliberately
puts a Go compiler and source execution in each publishing job. The workflow SHA
and version/protocol check bind each executable to the release unit. The cache
is only an optimization and is not part of that identity.

Consumer repositories use the installed path. The action derives the
distribution repository from `github.action_repository`, downloads the stamped
release archive and `checksums.txt`, requires one checksum entry for the
archive, and verifies its SHA-256 digest. It then runs `gh attestation verify`
against the release repository and its `publish-github-release.yml` signer,
with self-hosted runners denied, before extracting and executing the binary.

Both supported paths require `release-cli version --json` to report the version
and protocol stamped into the action. Source identity or release provenance
answers where the executable came from; the version and protocol guard answers
whether it is the member of the release unit that the workflow expects.

The action's optional `cli-path` input is an explicit escape from these models.
A caller-supplied binary is not built, downloaded, or attested, and a stamp
mismatch warns instead of failing. Direct action callers that use this input own
the workflow-to-binary pairing.

## Policy lives in the CLI; platform capabilities stay in YAML

Release and registry operations contain stateful policy that is easier to test
and keep consistent in Go than in workflow shell. `release-cli` owns the
GoReleaser invocation and release-bundle projection, closed-set bundle
verification, draft discovery and asset convergence, registry inspection,
digest uploads and signatures, immutable exact tags, channel movement, and
postcondition checks. The same command behavior applies wherever the CLI runs;
it is not reimplemented in four workflow files.

Some capabilities cannot move into that process without weakening a different
boundary. `actions/attest` is a GitHub Action whose provenance and SBOM
operations use the Actions runtime and the job's attestation and OIDC
permissions. It remains a SHA-pinned YAML step rather than a second attestation
implementation inside the CLI.

The GitHub App private key also remains an input to
`actions/create-github-app-token`. The workflow passes only the resulting
short-lived installation token to `release-cli publish github`. The CLI can use
the token to apply its release policy, but it neither receives the private key
nor mints an installation token. Keeping token creation visible in YAML also
keeps its required job permission and secret binding visible at the point where
GitHub evaluates them.

The OCI path has a similar division. The CLI authenticates its registry client
in memory from the workflow token and owns registry decisions. The workflow
performs the GHCR login needed by Cosign and registry-backed `actions/attest`
steps. Moving all of this into shell would duplicate registry and release policy
outside the CLI. Moving all of it into Go would require replacing GitHub's
attestation action and recreating its Actions runtime integration.

## OCI publication crosses the boundary in two phases

OCI publication needs CLI-controlled registry work both before and after the
workflow-controlled attestation steps. The split prevents a consumer-visible
tag from naming the candidate before all required trust metadata exists, while
still letting GitHub's action create that metadata with its native job
permissions.

[Why OCI publication has two phases](two-phase-oci-publication.md) describes the
prepare, attestation, and finalize transaction, including why tags are last and
why finalization reads fresh registry state. That transaction is one concrete
result of the broader trust split described here.

## One release unit has one consumer pin

The four reusable workflows, their sibling `setup-release-cli` action, and the
CLI ship as one release unit. A consumer pins each reusable workflow and the
checksum signer identity to one full commit SHA. Inside the pinned workflow,
`uses: $/.github/actions/setup-release-cli` selects the action from that same
commit. The action's stamped version then selects the CLI release archive.
Release Please updates that stamp as an extra versioned file when it versions
the release repository.

This coupling removes an otherwise large compatibility matrix. Consumers do
not choose one workflow revision, another setup action revision, and an
independent CLI version that may implement a different protocol. The setup
action's version and protocol check fails closed when the supported acquisition
path does not match the unit.

The tradeoff is that the CLI cannot be upgraded independently to obtain one
fix. The whole unit must be reviewed, versioned, and pinned together. That cost
is intentional: a CLI change can alter how producer artifacts are interpreted
or how publisher credentials mutate remote state, so independent selection
would make the workflow pin an incomplete description of the release system.
A single full-SHA pin makes the reviewed workflows, acquisition logic, command
behavior, and signing identity one auditable choice.
