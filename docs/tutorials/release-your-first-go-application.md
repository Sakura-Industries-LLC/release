# Release your first Go application

In this tutorial, you will release a disposable Go command from `acme/widget`.
You will copy the maintained example, select one immutable release-unit
revision, rehearse against a draft, and then publish the same candidate.

The tutorial publishes a GitHub Release and a digest-pinned GHCR image. It
leaves Homebrew, Scoop, and native package-repository publication disabled.

## Before you begin

Complete [Prepare your GitHub organization](../how-to/prepare-your-github-organization.md)
for a disposable repository. You need:

- an adopter-owned GitHub App installed on `acme/widget`;
- the App client ID and private key available to that repository as Actions
  variable `MEIGMA_RELEASE_APP_CLIENT_ID` and secret
  `MEIGMA_RELEASE_APP_PRIVATE_KEY`;
- any `v*` tag rules configured to let the App create tags and to let an
  authorized operator move this unpublished rehearsal tag;
- GitHub Actions and GitHub Packages enabled;
- Git, GitHub CLI, mise, ORAS, Cosign, Docker, and `jq`; and
- a local checkout of this repository and an empty checkout of `acme/widget`.

Use a disposable repository because resuming the rehearsal moves an
unpublished tag. Never use this procedure to move a published tag.

Start in the empty `acme/widget` checkout:

```bash
export REPOSITORY=acme/widget
export RELEASE_EXAMPLE=/absolute/path/to/release/examples/go-release
test "$(gh repo view --json nameWithOwner --jq .nameWithOwner)" = "$REPOSITORY"
test -d "$RELEASE_EXAMPLE"
```

## Copy the maintained example

Copy the release files and sample command without copying the example README:

```bash
mkdir -p .github cmd
cp -R "$RELEASE_EXAMPLE/.github" .
cp "$RELEASE_EXAMPLE/.goreleaser.yaml" .
cp "$RELEASE_EXAMPLE/apko.yaml" .
cp "$RELEASE_EXAMPLE/melange.yaml" .
cp "$RELEASE_EXAMPLE/.release-please-manifest.json" .
cp "$RELEASE_EXAMPLE/release-please-config.json" .
cp "$RELEASE_EXAMPLE/mise.toml" .
cp "$RELEASE_EXAMPLE/mise.lock" .
cp "$RELEASE_EXAMPLE/go.mod" .
cp -R "$RELEASE_EXAMPLE/cmd/example" cmd/widget
```

Select the latest published `meigma/release` release and resolve its tag to one
full commit SHA:

```bash
export RELEASE_TAG="$(gh api repos/meigma/release/releases/latest --jq .tag_name)"
export RELEASE_REVISION="$(gh api "repos/meigma/release/commits/$RELEASE_TAG" --jq .sha)"
[[ "$RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]
test "$(gh api "repos/meigma/release/commits/$RELEASE_REVISION" --jq .sha)" = \
  "$RELEASE_REVISION"
printf 'Release unit: %s at %s\n' "$RELEASE_TAG" "$RELEASE_REVISION"
```

Replace every `REPLACE_WITH_RELEASE_COMMIT_SHA` occurrence in
`.github/workflows/release.yml` with `$RELEASE_REVISION`. Then confirm that the
caller contains only that immutable release-unit revision:

```bash
! grep -R 'REPLACE_WITH_RELEASE_COMMIT_SHA' .github/workflows
refs="$(grep -Eo '@[0-9a-f]{40}' .github/workflows/release.yml | sort -u)"
test "$refs" = "@$RELEASE_REVISION"
```

This one SHA selects every reusable workflow, every checksum signer identity,
and the `release-cli` installed by those workflows.

## Adapt the application

Edit the copied files as follows:

1. Set the module in `go.mod` to `github.com/acme/widget`.
2. Replace the user-facing command name `example` with `widget` in
   `cmd/widget/main.go`. Keep the `version` and `commit` variables.
3. In `.goreleaser.yaml`, replace the project, build, archive, package, cask,
   and Scoop names with `widget`; set `main` to `./cmd/widget`; set the homepage
   and release URL owner and repository to `acme/widget`; and replace the
   example organization metadata.
4. In `release-please-config.json`, set `package-name` to `widget`.
5. In `melange.yaml`, set the package name to `widget`, install the staged
   `widget` file at `/usr/bin/widget`, and replace the example organization
   metadata.
6. In `apko.yaml`, set the package and entrypoint to `/usr/bin/widget` and use
   `https://github.com/acme/widget` as the source annotation.

Keep these release controls unchanged:

```yaml
changelog:
  disable: true

release:
  disable: true
```

Also keep these caller inputs disabled:

```yaml
publish-image: false
publish-release: false
publish-homebrew: false
publish-scoop: false
publish-package-repository: false
```

The adopter-owned destination placeholders can remain while their publishers
are disabled. Homebrew and Scoop values affect only controls in the temporary
Actions artifact; those controls are excluded from the GitHub Release. The
package-repository request job is skipped.

Install the locked tools and check the adapted command:

```bash
mise install --locked
mise exec -- goreleaser check
mise exec -- go run ./cmd/widget --version
```

The command prints `widget dev (none)`.

## Merge the rehearsal configuration

Commit the application and release configuration on a branch, submit it for
review, and merge it through the repository's normal pull request process:

```bash
git switch -c feat/add-widget-release
git add .
git commit -m 'feat: add widget command'
git push -u origin HEAD
gh pr create --fill
```

After the pull request is merged, dispatch Release Please:

```bash
export DEFAULT_BRANCH="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
gh workflow run release-please.yml --repo "$REPOSITORY" --ref "$DEFAULT_BRANCH"
gh run list --repo "$REPOSITORY" --workflow release-please.yml --limit 5
```

The run opens or updates one Release Please pull request. Find it, review the
version and changelog, and merge it after its required checks pass:

```bash
export RELEASE_PR="$(gh pr list \
  --repo "$REPOSITORY" \
  --label 'autorelease: pending' \
  --json number \
  --jq '.[0].number')"
test -n "$RELEASE_PR"
gh pr view "$RELEASE_PR" --repo "$REPOSITORY"
gh pr merge "$RELEASE_PR" --repo "$REPOSITORY" --squash --delete-branch
export VERSION_SHA="$(gh pr view "$RELEASE_PR" \
  --repo "$REPOSITORY" \
  --json mergeCommit \
  --jq .mergeCommit.oid)"
[[ "$VERSION_SHA" =~ ^[0-9a-f]{40}$ ]]
export VERSION_RUNS='[]'
until test "$(jq length <<<"$VERSION_RUNS")" -gt 0; do
  export VERSION_RUNS="$(gh run list \
    --repo "$REPOSITORY" \
    --workflow release-please.yml \
    --branch "$DEFAULT_BRANCH" \
    --commit "$VERSION_SHA" \
    --event push \
    --limit 100 \
    --json databaseId)"
  test "$(jq length <<<"$VERSION_RUNS")" -gt 0 || sleep 2
done
test "$(jq length <<<"$VERSION_RUNS")" -eq 1
export VERSION_RUN_ID="$(jq -r '.[0].databaseId' <<<"$VERSION_RUNS")"
gh run watch "$VERSION_RUN_ID" --repo "$REPOSITORY" --compact --exit-status
```

Release Please creates the stable tag and matching draft. For the unchanged
initial version, record the candidate:

```bash
export TAG=v0.1.0
git fetch origin "refs/tags/$TAG:refs/tags/$TAG"
export TAG_SHA="$(git rev-list -n 1 "$TAG")"
[[ "$TAG_SHA" =~ ^[0-9a-f]{40}$ ]]
export RELEASE_RUNS='[]'
until test "$(jq length <<<"$RELEASE_RUNS")" -gt 0; do
  export RELEASE_RUNS="$(gh run list \
    --repo "$REPOSITORY" \
    --workflow release.yml \
    --branch "$TAG" \
    --commit "$TAG_SHA" \
    --event push \
    --limit 100 \
    --json databaseId)"
  test "$(jq length <<<"$RELEASE_RUNS")" -gt 0 || sleep 2
done
test "$(jq length <<<"$RELEASE_RUNS")" -eq 1
export RELEASE_RUN_ID="$(jq -r '.[0].databaseId' <<<"$RELEASE_RUNS")"
gh run watch "$RELEASE_RUN_ID" --repo "$REPOSITORY" --compact --exit-status
```

## Inspect the rehearsal

Find the one draft for the candidate tag:

```bash
test "$(gh api --paginate --slurp \
  "repos/$REPOSITORY/releases?per_page=100" \
  --jq "[.[][] | select(.tag_name == \"$TAG\")] | length")" -eq 1
export RELEASE_ID="$(gh api --paginate --slurp \
  "repos/$REPOSITORY/releases?per_page=100" \
  --jq "[.[][] | select(.tag_name == \"$TAG\")][0].id")"
gh api "repos/$REPOSITORY/releases/$RELEASE_ID" \
  --jq '{tag_name, draft, prerelease, assets: [.assets[].name]}'
```

The release is a non-prerelease draft with 26 assets: six archives, six native
packages, twelve SBOMs, `checksums.txt`, and
`checksums.txt.sigstore.json`. GHCR has no release tag because
`publish-image` was false.

Download the two authoritative workflow artifacts from the exact run:

```bash
mkdir rehearsal-assets rehearsal-image
gh run download "$RELEASE_RUN_ID" \
  --repo "$REPOSITORY" \
  --name release-assets \
  --dir rehearsal-assets
gh run download "$RELEASE_RUN_ID" \
  --repo "$REPOSITORY" \
  --name oci-image \
  --dir rehearsal-image
test -s rehearsal-assets/checksums.txt
test -s rehearsal-assets/checksums.txt.sigstore.json
test -s rehearsal-image/image-digest.txt
test -s rehearsal-image/layout/index.json
jq -r '.manifests[] | "\(.platform.os)/\(.platform.architecture)"' \
  rehearsal-image/layout/index.json | sort
```

The final command prints `linux/amd64` and `linux/arm64`.

## Publish the candidate

Change only `publish-image` and `publish-release` to `true` in
`.github/workflows/release.yml`. Submit and merge that change. Leave Homebrew,
Scoop, and native package-repository publication disabled.

Fetch the enabling commit and move the still-unpublished candidate tag to it:

```bash
git fetch origin "$DEFAULT_BRANCH" --tags
export PUBLISH_SHA="$(git rev-parse "origin/$DEFAULT_BRANCH")"
git tag --force "$TAG" "$PUBLISH_SHA"
git push --force origin "refs/tags/$TAG"
```

Select and watch the new run by both tag and commit:

```bash
export PUBLISH_RUNS='[]'
until test "$(jq length <<<"$PUBLISH_RUNS")" -gt 0; do
  export PUBLISH_RUNS="$(gh run list \
    --repo "$REPOSITORY" \
    --workflow release.yml \
    --branch "$TAG" \
    --commit "$PUBLISH_SHA" \
    --event push \
    --limit 100 \
    --json databaseId)"
  test "$(jq length <<<"$PUBLISH_RUNS")" -gt 0 || sleep 2
done
test "$(jq length <<<"$PUBLISH_RUNS")" -eq 1
export PUBLISH_RUN_ID="$(jq -r '.[0].databaseId' <<<"$PUBLISH_RUNS")"
gh run watch "$PUBLISH_RUN_ID" --repo "$REPOSITORY" --compact --exit-status
```

Confirm that the same release ID is now public:

```bash
test "$(gh release view "$TAG" --repo "$REPOSITORY" --json databaseId --jq .databaseId)" = \
  "$RELEASE_ID"
gh release view "$TAG" \
  --repo "$REPOSITORY" \
  --json tagName,isDraft,isPrerelease,publishedAt,url
```

## Verify the release and image

Download and verify the public release bundle:

```bash
mkdir published-assets
gh release download "$TAG" --repo "$REPOSITORY" --dir published-assets
cd published-assets
sha256sum --check checksums.txt
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/meigma/release/.github/workflows/go-pre-publish.yml@$RELEASE_REVISION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify widget_0.1.0_linux_amd64.tar.gz \
  --repo "$REPOSITORY" \
  --signer-workflow meigma/release/.github/workflows/publish-github-release.yml \
  --signer-digest "$RELEASE_REVISION" \
  --source-ref "refs/tags/$TAG" \
  --deny-self-hosted-runners
cd ..
```

On macOS, use `shasum -a 256 --check checksums.txt` instead of `sha256sum`.

Resolve and verify the image by digest:

```bash
export IMAGE=ghcr.io/acme/widget
gh auth token | oras login ghcr.io \
  --username "$(gh api user --jq .login)" \
  --password-stdin
export DIGEST="$(oras resolve "$IMAGE:${TAG#v}")"
cosign verify \
  --certificate-identity "https://github.com/meigma/release/.github/workflows/publish-oci-image.yml@$RELEASE_REVISION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$IMAGE@$DIGEST"
gh attestation verify "oci://$IMAGE@$DIGEST" \
  --repo "$REPOSITORY" \
  --bundle-from-oci \
  --signer-workflow meigma/release/.github/workflows/publish-oci-image.yml \
  --signer-digest "$RELEASE_REVISION" \
  --source-ref "refs/tags/$TAG" \
  --deny-self-hosted-runners
docker run --rm "$IMAGE@$DIGEST" --version
```

You have now released one Go application through one immutable release unit.
Continue with [Add Homebrew and Scoop](../how-to/add-homebrew-and-scoop.md) or
[Operate a native package repository](../how-to/operate-a-native-package-repository.md)
only after their external repositories, credentials, and review controls exist.
