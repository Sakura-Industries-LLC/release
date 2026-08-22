# Architecture and trust

The release system separates versioning, construction, verification, and
publication because those phases do not need the same authority. The split is a
security boundary and a recovery boundary: an artifact can be built and
inspected without a credential that can make it public, and a publisher must
verify its input before using a write credential.

## Versioning creates the candidate, not the payload

Release Please owns the release pull request, stable tag, notes, and initial
draft. It uses an adopter-owned GitHub App because an App-created tag can trigger
the downstream tag workflow and can be granted a narrow protected-tag bypass.
The release pipeline does not recreate these objects if they are missing.

GoReleaser then builds payloads for the immutable candidate commit, but both its
release pipe and its changelog are disabled. That avoids two components
competing to create notes or mutate a release. The release publisher begins
from the draft that Release Please created and treats its tag and target commit
as inputs to verify.

This division also makes a draft rehearsal faithful. The build, checksum,
image, and release convergence paths run against a real stable candidate, while
the final public mutations remain separately disabled.

## Builders stop at verified artifacts

The Go producer can read source and obtain an OIDC identity for checksum
signing. It cannot write GHCR or mutate a GitHub Release. The OCI builder can
read the consumer repository and Actions artifacts, but it has no registry,
attestation-write, or release credential.

Those jobs upload bounded artifacts with an Actions artifact ID and transport
digest. A downstream job verifies the API metadata before download, the
download action verifies the artifact ZIP digest, and a content-specific command
verifies the extracted files. These checks answer different questions:
transport integrity does not establish that an archive set or OCI layout
satisfies its release contract.

A single job with compilers and every publisher credential would be shorter. It
would also run source-controlled build tools inside the same boundary that can
change public release assets and image tags. The extra handoffs keep that
authority out of the build environment.

## The caller is the permission ceiling

Except for the OCI publisher, each reusable workflow starts with
`permissions: {}`. The OCI publisher declares `artifact-metadata: read` at
workflow scope so attestation subjects remain discoverable. In every case, the
calling job supplies the maximum token permissions; a called workflow cannot
elevate above that ceiling.

The boundaries differ by effect:

- the producer has content read and checksum-signing OIDC;
- the OCI builder has artifact and content read;
- the OCI publisher has package, attestation, and OIDC authority but no App
  private key;
- the GitHub Release publisher can mint a short-lived, contents-scoped App token
  but has no package-write permission;
- the Homebrew and Scoop publishers can mint a token scoped to one destination
  with contents and pull-request writes; and
- the native package producer can dispatch a public release identity but cannot
  read R2 or aggregate signing credentials.

The GitHub Release job depends on OCI publication, so an enabled release remains
a draft when registry publication fails. Homebrew, Scoop, and native dispatch
depend on the GitHub Release job. The supported caller enables them only when
`publish-release` is also enabled; the native receiver independently rejects a
draft. The Homebrew and Scoop publishers do not check public release state, so
enabling them during a draft-only run could create controls that point at a
draft.

## The CLI carries policy; YAML exposes platform capabilities

Stateful release policy is implemented in `release-cli`: closed-set bundle
validation, tag-to-commit binding, draft discovery, asset convergence, package
policy, registry tag decisions, immutable object checks, and postconditions.
Keeping this logic in Go makes one tested implementation responsible for the
same rule across producer and publisher workflows.

GitHub-specific capabilities remain visible in workflow YAML. `actions/attest`
uses the Actions runtime and job-level OIDC and attestation permissions. The
App private key is an input to `actions/create-github-app-token`; only its
short-lived result reaches the CLI. The CLI neither receives the App private
key nor mints installation tokens.

This is not a claim that all policy belongs in the CLI. YAML remains the place
where GitHub evaluates permission ceilings, protected environments, action
pins, secret bindings, and job dependencies. The CLI owns rules that must be
consistent and testable across those jobs.

## The CLI is part of the release unit

A verifier is itself executable supply-chain input. Verifying only consumer
artifacts would leave the process that interprets and publishes them
unconstrained.

The reusable workflows, sibling setup action, and CLI therefore ship as one
release unit. An external consumer pins each reusable workflow and signer
identity to one full commit SHA. The pinned workflow loads the setup action from
that commit; the action's stamped version selects the matching CLI release and
requires its version and protocol.

This removes a compatibility matrix in which a caller might combine one
workflow revision, another setup action, and a CLI with different release
semantics. The cost is that a CLI fix cannot be adopted independently. That
cost is intentional: a CLI change can alter how unprivileged artifacts are
interpreted or how a privileged publisher mutates remote state.

The checksum identity uses the full shared workflow URL and release-unit SHA.
Native package policy records that immutable value directly. The GitHub
attestation signer field names the shared workflow without a ref because the
attestation verifier separately binds the source tag and producer commit. This
lets `acme/widget` use a signer implemented in `meigma/release` without
pretending the workflow belongs to `acme/widget`.

## OCI publication has a trust-metadata gap

OCI registry work must occur on both sides of GitHub's attestation steps:

```text
prepare digest-addressed image and signatures
  -> GitHub provenance attestation
  -> GitHub amd64 SBOM attestation
  -> GitHub arm64 SBOM attestation
  -> finalize exact and channel tags
```

Preparation validates the layout and expected index digest, reads current tag
state, pushes content by digest, verifies it, and recursively signs the index.
It does not create or move a tag. GitHub then creates attestations through its
native action. Finalization runs only after all three succeed.

Tags are last because they are consumer-facing names. Applying `1.2.3`, `1.2`,
`1`, or `latest` before signatures and attestations exist would expose a window
where normal consumers can discover incomplete trust metadata. Untagged
digest-addressed content can remain after an interruption without changing a
name that tag-based consumers follow.

The preparation result records registry observations, not a durable plan.
Attestation takes time, and registry state can change. Finalization re-reads
exact and channel tags, accepts state that is unchanged or already on the
candidate, rejects other drift, and recomputes the plan. This fresh-state rule
also makes a retry converge after a partial tag commit: completed candidate tags
are accepted, remaining tags are applied, and newer channels remain retained.

## Public releases are the commit point for package-manager updates

Homebrew and Scoop controls are generated beside the release bundle but excluded
from its signed public payload. The Actions artifact digest binds them during
handoff. Each package-manager publisher isolates its own control, verifies the
underlying signed bundle, restores only that control, and opens a destination
pull request.

Direct writes or automatic merges would bypass the destination's own policy and
platform validation. A reviewed pull request lets the tap or bucket require its
validation workflow, inspect generated URLs, and retain an independent audit
record. The publisher's deterministic branch and exact-byte checks make reruns
convergent without granting it authority to approve or merge its output.

The cost is a manual merge and a delay between the GitHub Release and package
manager availability. That delay is preferable to turning the producer token
into an unchecked default-branch writer.

## Native package credentials stay central

A producer publishes signed and attested DEB, RPM, and APK assets in its GitHub
Release, then dispatches only `{repository, tag}`. It does not receive the
Cloudflare token or aggregate APT, RPM, and APK signing keys.

The adopter-owned central repository records package ownership, explicit shared
workflow identities, and producer public keys under review. Its protected
environment supplies bucket-scoped R2 credentials and aggregate private keys.
The central CLI independently verifies the release closed set, checksum signer,
GitHub attestations, package metadata, and producer-native signatures before it
uses those credentials.

The publisher reconstructs repository metadata from existing immutable package
objects plus the incoming release. It uploads referenced inner objects before
APT, RPM, and APK roots. A crash can leave unreachable objects but cannot
activate a root that points at missing content. A single workflow concurrency
group serializes writes because R2 does not provide a multi-object repository
transaction.

This creates a central signing and availability bottleneck. In return, producer
onboarding is a reviewed policy and public-key change rather than a grant of
shared production credentials. A replay can converge from current object state,
and an immutable conflict fails instead of silently replacing history.

## One application per repository is a deliberate limit

The repository name determines the GHCR image name, the caller has one stable
unscoped tag stream, the OCI layout has one entrypoint, and Release Please owns
one root manifest version. Supporting multiple applications would require
component-aware tags, separate asset namespaces, multiple image names, and more
complex ownership and recovery rules across every publisher.

Keeping one application per repository avoids that cross-product. The tradeoff
is more repositories and repeated organization setup. The repeated unit is
operationally visible: each application has one App installation entry, one
release-unit SHA, one draft, one image, and one set of optional destinations.

## Unsupported cases preserve these boundaries

The system excludes prereleases, component tags, dynamic Linux binaries, custom
registries, mutable exact image tags, direct package-manager merges, producer
access to central credentials, and rollback after publication. These are not
missing conveniences around an otherwise compatible path. Each would change a
trust or naming assumption used by versioning, artifact verification,
publication ordering, or recovery.

A public correction is therefore additive. The system does not move a public
tag, re-draft a release, replace an exact image tag, or overwrite an immutable
native package object. It publishes a new stable version whose complete release
unit can be reviewed and verified again.
