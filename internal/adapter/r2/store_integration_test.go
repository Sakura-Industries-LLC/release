//go:build integration

package r2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// minioImage pins the S3-compatible service used by the integration test.
	minioImage = "minio/minio:RELEASE.2025-09-07T16-13-09Z"
	// minioAccessKey is the local integration-test access key.
	minioAccessKey = "release-test-access"
	// minioSecretKey is the local integration-test secret key.
	minioSecretKey = "release-test-secret-key"
	// minioBucket is the local integration-test bucket.
	minioBucket = "package-repository"
)

func TestStoreStreamsAndRecoversRepositoryObjects(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.Run(
		ctx,
		minioImage,
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithEnv(map[string]string{
			"MINIO_ROOT_USER":     minioAccessKey,
			"MINIO_ROOT_PASSWORD": minioSecretKey,
		}),
		testcontainers.WithCmd("server", "/data", "--console-address", ":9001"),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/minio/health/ready").
				WithPort("9000/tcp").
				WithStartupTimeout(90*time.Second),
		),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "9000/tcp")
	require.NoError(t, err)
	store, err := New(ctx, Options{
		Endpoint: fmt.Sprintf("http://%s:%s", host, port.Port()),
		Bucket:   minioBucket,
		Credentials: Credentials{
			AccessKeyID:     rel.NewSecret(minioAccessKey),
			SecretAccessKey: rel.NewSecret(minioSecretKey),
		},
	})
	require.NoError(t, err)
	_, err = store.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(minioBucket)})
	require.NoError(t, err)

	immutableBody := []byte("native package bytes")
	immutableDigest := testDigest(t, immutableBody)
	err = store.Upload(ctx, pkgrepo.UploadRequest{
		Path:   "rpm/stable/x86_64/release-cli-1.2.3.x86_64.rpm",
		Body:   bytes.NewReader(immutableBody),
		Digest: immutableDigest,
		Size:   int64(len(immutableBody)),
		Cache:  pkgrepo.CacheImmutable,
	})
	require.NoError(t, err)
	mutableBody := []byte("repository metadata")
	mutableDigest := testDigest(t, mutableBody)
	err = store.Upload(ctx, pkgrepo.UploadRequest{
		Path:   "rpm/stable/x86_64/repodata/repomd.xml",
		Body:   bytes.NewReader(mutableBody),
		Digest: mutableDigest,
		Size:   int64(len(mutableBody)),
		Cache:  pkgrepo.CacheNoStore,
	})
	require.NoError(t, err)

	objects, err := store.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []pkgrepo.StoredObject{
		{Path: "rpm/stable/x86_64/release-cli-1.2.3.x86_64.rpm", Size: int64(len(immutableBody))},
		{Path: "rpm/stable/x86_64/repodata/repomd.xml", Size: int64(len(mutableBody))},
	}, objects)
	content, exists, err := store.Stat(ctx, objects[0].Path)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, pkgrepo.StoredContent{Digest: immutableDigest, Size: int64(len(immutableBody))}, content)
	_, exists, err = store.Stat(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, exists)

	var downloaded bytes.Buffer
	content, err = store.Download(ctx, objects[0].Path, &downloaded)
	require.NoError(t, err)
	assert.Equal(t, immutableBody, downloaded.Bytes())
	assert.Equal(t, immutableDigest, content.Digest)

	immutableHead, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(minioBucket),
		Key:    aws.String(objects[0].Path),
	})
	require.NoError(t, err)
	assert.Equal(t, immutableCacheControl, aws.ToString(immutableHead.CacheControl))
	assert.Equal(t, immutableDigest.String(), immutableHead.Metadata[digestMetadataKey])
	mutableHead, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(minioBucket),
		Key:    aws.String(objects[1].Path),
	})
	require.NoError(t, err)
	assert.Equal(t, mutableCacheControl, aws.ToString(mutableHead.CacheControl))
	assert.Equal(t, mutableDigest.String(), mutableHead.Metadata[digestMetadataKey])
}

// testDigest constructs the canonical digest for one integration fixture body.
func testDigest(t *testing.T, body []byte) rel.Digest {
	t.Helper()

	sum := sha256.Sum256(body)
	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
	require.NoError(t, err)

	return digest
}
