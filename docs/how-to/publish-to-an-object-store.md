# Publish to an object store

Use this guide to mirror each verified release bundle into a private
S3-compatible bucket, so a self-hosted download surface can serve the files
without sending users to GitHub. The publisher is convergent: rerunning it for
the same tag uploads nothing and refuses to overwrite a differing object.

Complete [Adopt the release workflows](adopt-the-release-workflows.md) first.
The object store publisher consumes the same `release-assets` artifact as the
GitHub Release publisher and runs after it in the maintained caller.

## Prepare the bucket

Create one private bucket on any S3-compatible service that supports SigV4
path-style requests and object metadata. Create a key with read and write
permission on that bucket for the publisher, and separate read-only keys for
each download surface. The bucket must never be world-readable: consumers
receive presigned URLs from the surface that owns the read key.

Record three non-secret values: the S3 API origin (for example
`https://objects.example.net`), the bucket name, and the signing region the
service expects (`garage` for Garage, `auto` for Cloudflare R2).

## Store the publisher key

Add two repository or organization secrets to the producer:

- `OBJECT_STORE_ACCESS_KEY_ID`; and
- `OBJECT_STORE_SECRET_ACCESS_KEY`.

Restrict them to the release environment or to the producer repositories that
publish into the bucket. The publisher never receives a read key, and the
download surface never receives the write key.

## Call the publisher

Add one job to the producer's release workflow after the GitHub Release job:

```yaml
  publish-object-store:
    name: Publish to object store
    needs: [release-assets, publish-github]
    if: github.ref_type == 'tag'
    permissions:
      actions: read
      contents: read
    uses: Sakura-Industries-LLC/release/.github/workflows/publish-object-store.yml@<full-sha>
    with:
      artifact-id: ${{ needs.release-assets.outputs.artifact-id }}
      artifact-digest: ${{ needs.release-assets.outputs.artifact-digest }}
      checksum-signing-workflow-ref: Sakura-Industries-LLC/release/.github/workflows/go-pre-publish.yml@<full-sha>
      project: dntls
      endpoint: https://objects.example.net
      bucket: releases
      region: garage
      publish-object-store: false
    secrets:
      object-store-access-key-id: ${{ secrets.OBJECT_STORE_ACCESS_KEY_ID }}
      object-store-secret-access-key: ${{ secrets.OBJECT_STORE_SECRET_ACCESS_KEY }}
```

Keep `publish-object-store: false` for the first tagged run: the job verifies
the handoff and the bundle and reports `state: skipped`. Set it to `true` once
the bucket, keys, and download surface exist.

`project` is the first key segment and should be the program name users
recognize. The second segment is `v<version>` from the tag, so a monorepo tag
`cli/v0.2.0` publishes to `dntls/v0.2.0/`. Every payload named in
`checksums.txt` is stored under that prefix, followed by `checksums.txt` and
`checksums.txt.sigstore.json`; the Homebrew and Scoop controls are excluded.

## Read the result

The job prints the `release-cli publish object-store` JSON envelope and exposes
two outputs: `prefix`, the key prefix that was published, and `state`,
`published` when at least one object was uploaded or `unchanged` when every
object already matched, or `skipped` when publication is disabled. All three
are success.

## Recover

A failed run leaves any objects it already wrote in place; rerun the job and it
resumes at the first missing object. A digest mismatch means the bucket holds a
different file under a published name. That is never repaired automatically:
inspect the object, decide which bytes are authoritative, and remove the wrong
object by hand before rerunning. Nothing in the bucket is deleted by the
publisher.

Rebuilding a bucket from nothing is a matter of rerunning the release workflow's
publisher job for every release that must remain downloadable.
