# Why OCI publication has two phases

OCI publication is split across `release-cli` and the reusable GitHub Actions workflow because each has a different security boundary. The CLI owns registry state, digest-addressed content, signatures, and tags. The workflow owns the GitHub attestation actions and the job permissions that those actions require.

The resulting sequence is:

```text
release-cli publish oci prepare
  -> actions/attest provenance
  -> actions/attest amd64 SBOM
  -> actions/attest arm64 SBOM
  -> release-cli publish oci finalize
```

This order makes consumer-visible tags the commit point for a publication.

## Why attestation separates prepare from finalize

A GitHub Action cannot run inside a CLI process. `actions/attest` depends on the GitHub Actions runtime and on permissions granted to the workflow job, including permission to request an OIDC token, write attestations, and push registry-backed attestations. `release-cli` cannot acquire or hold those job-level permissions on its own.

Moving attestation into the CLI would require a second, less direct implementation of the GitHub Actions integration. Moving all registry work into workflow scripts would duplicate the release policy outside the CLI. The two-phase design keeps each operation at the boundary that can perform and verify it.

`prepare` validates the OCI layout and expected index digest, reads the current tags, pushes the image by digest, verifies the pushed manifests, and signs the index recursively. It never creates or moves a tag. The three `actions/attest` steps then attach provenance to the index and SBOM attestations to the two platform manifests. Only after all three steps succeed does `finalize` apply the planned exact and channel tags.

## Why tags are last

Release tags are the consumer-facing names of the image. An exact tag such as `1.2.3` makes a release discoverable, while channel tags such as `1.2`, `1`, and `latest` direct consumers to a selected release. Applying any of these tags before signing and attestation would create a period in which consumers could pull an image that does not yet have its required trust metadata.

Digest-addressed content does not create that exposure. Pushing `image@sha256:...` without a tag does not change any release name that consumers follow. A party that already knows the digest and has registry access can address the content, but ordinary tag-based consumers cannot discover it as a release. This is why an interrupted prepare or attestation phase can leave registry content behind without changing the published release.

Tags therefore act as a commit point rather than an upload mechanism. Before the commit point, the candidate can exist and accumulate trust metadata without replacing a consumer-visible reference. After the commit point, every newly applied tag names content that has already been pushed, verified, signed, and attested.

## Why finalize reads the registry again

The prepare result records what the registry contained before the digest upload. It is evidence for detecting a change, not a plan that `finalize` replays. Time passes while the workflow creates the three attestations, so the registry state seen by `prepare` may no longer be current when tag publication begins.

`finalize` re-reads the exact tag and all channel tags. It compares that fresh state with the observations from `prepare`. An unchanged observation is valid. A tag that now resolves to the candidate index digest is also valid because it can be work completed by an earlier, partially successful finalization. Any other change is drift: another digest appeared, a version annotation changed, or a tag disappeared. `finalize` refuses that state instead of applying a decision derived from stale observations.

After the drift check, `finalize` computes a new tag plan from the fresh state. It commits the required tags serially and verifies each result through an independent registry read. Serial commits preserve the policy order and avoid concurrent channel movement within one publication.

This fresh-state rule is also what makes a rerun converge after a partial tag commit. Tags already applied to the candidate become accepted decisions, while tags that were not reached remain create decisions. Channels that correctly point to a newer release remain retained. The rerun does not assume that the first attempt made no progress.

## Failure states

A failed image publication does not always mean that the registry is unchanged. The phase that failed determines what an operator can infer.

| Failure point | Registry state | Operator meaning |
| --- | --- | --- |
| Prepare validation or initial planning | No candidate tag is created or moved. The command can fail before any content write. | The candidate has not reached the trust-metadata sequence. Correct the invalid layout, configuration, immutable-tag conflict, or corrupt channel state before another workflow run. |
| Prepare upload, verification, or signing | Some digest-addressed blobs or manifests may exist, but no candidate tag is created or moved. | The candidate is not a consumer-visible release. A rerun can reuse content that the registry already has and must complete signing before attestation begins. |
| Provenance or SBOM attestation | The digest-addressed image is signed, and some attestations may exist, but no candidate tag is created or moved. | The trust metadata is incomplete. The workflow must complete all three attestation steps before finalization. |
| Finalize drift or planning refusal | The failing finalization writes no additional tags. Tags from an earlier partial attempt may already resolve to the candidate digest. | The registry changed outside the observations that preparation recorded, or current tag policy rejects the candidate. The refusal is a signal to inspect the named tag, not to force or hand-replay the saved prepare result. |
| Finalize commit | Trust metadata is complete, but only a prefix of the ordered tag set may resolve to the candidate digest. | Some consumers may see the exact release or a subset of its channels. Rerunning the publisher re-reads the registry, accepts the candidate tags already present, and applies the remaining eligible tags. |
| Finalize postcondition verification | Tags may have been written, but the CLI could not independently confirm the required resolutions. | Publication remains failed and the dependent GitHub Release remains draft. A rerun establishes fresh state and verifies the postcondition again. |

The prepare envelope is deliberately not a durable receipt. It connects two phases within one workflow execution and supplies the observations used for drift detection. Replaying a saved envelope by hand would turn stale state into an input to a privileged tag operation, which defeats the fresh-state design.
