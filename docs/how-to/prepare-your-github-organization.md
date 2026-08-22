# Prepare your GitHub organization

Use this guide to create the organization-owned GitHub identity and policy that
the release workflows require. Complete it before copying a release caller into
a producer repository.

The App described here belongs to the adopting organization. No Meigma owner,
Meigma-owned App installation, or Meigma private key is required.

## Create the release App

Register a GitHub App under the adopting organization. Use an organization URL
for the required homepage field and disable webhooks unless the organization
uses them for another purpose.

Grant these repository permissions:

| Permission | Access | Used for |
| --- | --- | --- |
| Contents | Read and write | Release Please branches, tags, draft releases, release assets, destination branches, and `repository_dispatch`. |
| Issues | Read and write | Release Please issue and label operations. |
| Pull requests | Read and write | Release Please, Homebrew tap PRs, and Scoop bucket PRs. |

Metadata read access is implicit. Do not add administration, Actions, packages,
or organization permissions for this release path.

Record the App client ID. Generate one private key and store the downloaded PEM
in the organization's approved secret-management system. GitHub does not expose
the private key again.

## Install the App on selected repositories

Install the App on the adopting organization and select only repositories that
need release operations. Add repositories according to their role:

- every producer repository;
- each adopter-owned Homebrew tap or Scoop bucket used by a producer; and
- the adopter-owned central package repository when producers dispatch native
  package publication.

Do not select **All repositories** merely to simplify onboarding. Add another
repository when its release configuration is reviewed.

The publisher workflows request narrower installation tokens from this App:

- GitHub Release publication requests `contents: write` for the producer;
- Homebrew and Scoop publication request `contents: write` and
  `pull-requests: write` for one destination repository; and
- native package dispatch requests `contents: write` for the central receiver.

Release Please uses the App token in the producer repository for its release PR,
tag, and initial draft.

## Store the Actions variable and secret

In the adopting organization's **Settings** > **Secrets and variables** >
**Actions**, create:

| Kind | Name used by the maintained example | Value |
| --- | --- | --- |
| Variable | `MEIGMA_RELEASE_APP_CLIENT_ID` | The adopter-owned App client ID. |
| Secret | `MEIGMA_RELEASE_APP_PRIVATE_KEY` | The adopter-owned App private key PEM. |

Limit both entries to **Selected repositories** and add the producer
repositories that mint tokens. A tap, bucket, or central receiver does not need
direct access to the private key; the producer's workflow mints a token scoped
to that destination.

The names above are caller-local identifiers retained by the maintained
example. They do not identify the App owner. You may rename them if you update
every corresponding `vars.*` and `secrets.*` expression in the copied caller.

GitHub never returns a stored Actions secret value. Verify its name, selected
repository list, and a workflow that successfully creates an installation
token. Never print the private key or commit it to a repository.

## Allow the pinned workflows and actions

In the organization and each producer, central receiver, tap, and bucket
repository, open **Settings** > **Actions** > **General**. Enable Actions and
choose an allowlist policy that permits the reviewed release unit.

At minimum, allow:

- the reusable workflows in `meigma/release` at the selected full commit SHA;
- `actions/cache`, `actions/checkout`, `actions/upload-artifact`,
  `actions/download-artifact`, `actions/github-script`,
  `actions/create-github-app-token`, and `actions/attest`;
- `jdx/mise-action`;
- `docker/setup-qemu-action`;
- `Homebrew/actions/setup-homebrew` when a producer generates a cask or a tap
  validates one;
- `potatoqualitee/psmodulecache` when a Scoop bucket validates a manifest; and
- `googleapis/release-please-action` from the copied versioning workflow.

Review the exact action pins in the selected `meigma/release` revision before
adding them. The workflows pin third-party actions to full commit SHAs. Do not
replace them with moving tags to satisfy an allowlist.

If the organization allows only actions owned by the organization, add explicit
exceptions for this reviewed set. Apply the same policy to adopter-owned taps
and buckets so their generated validation workflows can call
`meigma/release`.

## Set the workflow-token ceilings

Organization, enterprise, and repository policy must permit the job-level
permissions declared by the release caller. The maintained caller starts with
`permissions: {}` and grants each called job only its required ceiling.

Confirm that policy permits:

- `attestations: read` for jobs that verify the installed `release-cli`;
- `id-token: write` for checksum signing, OCI signing, and attestations;
- `artifact-metadata: write` and `attestations: write` for publishers;
- `packages: write` for the GHCR publisher; and
- the Release Please job's `contents`, `issues`, and `pull-requests` writes.

A reusable workflow cannot elevate beyond its calling job. Do not move these
permissions to a broad top-level grant. The App token, rather than
`GITHUB_TOKEN`, owns release, tap, bucket, and dispatch mutations.

The **Allow GitHub Actions to create and approve pull requests** setting is not
required for Homebrew or Scoop: those pull requests are created with the App
token, and the publisher never approves or merges them.

## Configure tag protection

If a repository or organization ruleset restricts `v*` tags, add the
adopter-owned App as a bypass actor for tag creation. Keep the rule active for
other actors.

A draft rehearsal can require an authorized operator to move one unpublished
tag to a reviewed recovery commit. Do not weaken production tag rules for this.
Use a disposable repository for the first tutorial or define a separate,
audited break-glass path for an unpublished candidate. Never grant routine tag
movement after publication.

## Configure GHCR policy

Enable GitHub Packages for the organization and permit the caller's
`packages: write` ceiling. The OCI publisher always writes
`ghcr.io/<lowercase-owner>/<lowercase-repository>`; it does not accept a custom
registry name.

Initial GHCR visibility follows the organization's package-creation policy, not
the source repository's visibility. After the first complete publication,
inspect it:

```bash
export REPOSITORY=acme/widget
gh api "orgs/${REPOSITORY%%/*}/packages/container/${REPOSITORY#*/}" \
  --jq .visibility
```

The supported public-delivery state is `public`. If the result is `private`, an
organization owner must inspect the digest, signatures, and attestations and
then change visibility in the package settings UI. The current GitHub Packages
REST API does not expose this visibility change.

The organization is ready when the App installation, selected-repository
variable and secret, Actions allowlist, tag rule, permission ceilings, and
Packages policy all include the intended producer.
