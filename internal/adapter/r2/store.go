package r2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// cloudflareEndpointSuffix is the account-scoped R2 S3 endpoint suffix.
	cloudflareEndpointSuffix = ".r2.cloudflarestorage.com"
	// cloudflareRegion is the signing region required by R2.
	cloudflareRegion = "auto"
	// digestMetadataKey stores the canonical sha256:<hex> digest on each object.
	digestMetadataKey = "sha256"
	// immutableCacheControl is applied to content-addressed and package objects.
	immutableCacheControl = "public, max-age=31536000, immutable"
	// mutableCacheControl disables storage of replaceable repository metadata.
	mutableCacheControl = "no-store"
)

// Credentials contains the static S3 authentication values used by R2.
type Credentials struct {
	// AccessKeyID is the R2 API token access key.
	AccessKeyID rel.Secret
	// SecretAccessKey is the R2 API token secret.
	SecretAccessKey rel.Secret
	// SessionToken is optional for S3-compatible integration tests.
	SessionToken rel.Secret
}

// Options configures a [Store].
type Options struct {
	// AccountID is the Cloudflare account that owns the bucket.
	// It is required when Endpoint is empty.
	AccountID string
	// Endpoint overrides the account-derived S3 endpoint.
	// It exists for S3-compatible integration tests.
	Endpoint string
	// Bucket is the existing R2 bucket name.
	Bucket string
	// Credentials are the least-privilege S3 API credentials.
	Credentials Credentials
}

// Store streams package repository objects through the AWS S3 client.
type Store struct {
	// client is the account-scoped S3 API client.
	client *s3.Client
	// bucket is the fixed destination bucket.
	bucket string
}

// New constructs an R2 store using static S3 credentials and path-style requests.
func New(ctx context.Context, options Options) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	endpoint, err := resolveEndpoint(options)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.Bucket) == "" {
		return nil, errors.New("R2 bucket is empty")
	}
	accessKeyID := options.Credentials.AccessKeyID.Reveal()
	secretAccessKey := options.Credentials.SecretAccessKey.Reveal()
	if accessKeyID == "" {
		return nil, errors.New("R2 access key ID is empty")
	}
	if secretAccessKey == "" {
		return nil, errors.New("R2 secret access key is empty")
	}

	awsConfig, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(cloudflareRegion),
		config.WithBaseEndpoint(endpoint),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKeyID,
			secretAccessKey,
			options.Credentials.SessionToken.Reveal(),
		)),
		config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("configure R2 client: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(clientOptions *s3.Options) {
		clientOptions.UsePathStyle = true
	})

	return &Store{client: client, bucket: options.Bucket}, nil
}

// List implements [pkgrepo.Store].
func (s *Store) List(ctx context.Context) ([]pkgrepo.StoredObject, error) {
	if err := s.validateStore(ctx); err != nil {
		return nil, err
	}
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	})
	objects := make([]pkgrepo.StoredObject, 0)
	seen := make(map[string]struct{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list R2 objects: %w", err)
		}
		for _, object := range page.Contents {
			objectPath := aws.ToString(object.Key)
			if !fs.ValidPath(objectPath) || objectPath == "." {
				return nil, fmt.Errorf("R2 returned unconfined object path %q", objectPath)
			}
			if object.Size == nil || *object.Size < 0 {
				return nil, fmt.Errorf("R2 object %s has no valid content length", objectPath)
			}
			if _, duplicate := seen[objectPath]; duplicate {
				return nil, fmt.Errorf("R2 returned duplicate object path %q", objectPath)
			}
			seen[objectPath] = struct{}{}
			objects = append(objects, pkgrepo.StoredObject{Path: objectPath, Size: *object.Size})
		}
	}
	sort.Slice(objects, func(left, right int) bool { return objects[left].Path < objects[right].Path })

	return objects, nil
}

// Download implements [pkgrepo.Store].
func (s *Store) Download(
	ctx context.Context,
	objectPath string,
	destination io.Writer,
) (pkgrepo.StoredContent, error) {
	if err := s.validateRequest(ctx, objectPath); err != nil {
		return pkgrepo.StoredContent{}, err
	}
	if destination == nil {
		return pkgrepo.StoredContent{}, errors.New("R2 download destination is nil")
	}
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectPath),
	})
	if err != nil {
		return pkgrepo.StoredContent{}, fmt.Errorf("download R2 object %s: %w", objectPath, err)
	}
	if output.Body == nil {
		return pkgrepo.StoredContent{}, fmt.Errorf("R2 object %s has no body", objectPath)
	}
	if output.ContentLength == nil || *output.ContentLength < 0 {
		closeErr := output.Body.Close()
		return pkgrepo.StoredContent{}, errors.Join(
			fmt.Errorf("R2 object %s has no valid content length", objectPath),
			closeErr,
		)
	}
	digest, parseErr := rel.ParseDigest(output.Metadata[digestMetadataKey])
	if parseErr != nil {
		closeErr := output.Body.Close()
		return pkgrepo.StoredContent{}, errors.Join(
			fmt.Errorf("R2 object %s digest metadata: %w", objectPath, parseErr),
			closeErr,
		)
	}
	written, copyErr := io.Copy(destination, output.Body)
	closeErr := output.Body.Close()
	if copyErr != nil {
		return pkgrepo.StoredContent{}, fmt.Errorf("stream R2 object %s: %w", objectPath, copyErr)
	}
	if closeErr != nil {
		return pkgrepo.StoredContent{}, fmt.Errorf("close R2 object %s: %w", objectPath, closeErr)
	}
	if written != *output.ContentLength {
		return pkgrepo.StoredContent{}, fmt.Errorf(
			"R2 object %s streamed %d bytes, want %d",
			objectPath,
			written,
			*output.ContentLength,
		)
	}

	return pkgrepo.StoredContent{Digest: digest, Size: *output.ContentLength}, nil
}

// Stat implements [pkgrepo.Store].
func (s *Store) Stat(ctx context.Context, objectPath string) (pkgrepo.StoredContent, bool, error) {
	if err := s.validateRequest(ctx, objectPath); err != nil {
		return pkgrepo.StoredContent{}, false, err
	}
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectPath),
	})
	if err != nil {
		if objectNotFound(err) {
			return pkgrepo.StoredContent{}, false, nil
		}
		return pkgrepo.StoredContent{}, false, fmt.Errorf("stat R2 object %s: %w", objectPath, err)
	}
	if output.ContentLength == nil || *output.ContentLength < 0 {
		return pkgrepo.StoredContent{}, false, fmt.Errorf("R2 object %s has no valid content length", objectPath)
	}
	digest, err := rel.ParseDigest(output.Metadata[digestMetadataKey])
	if err != nil {
		return pkgrepo.StoredContent{}, false, fmt.Errorf("R2 object %s digest metadata: %w", objectPath, err)
	}

	return pkgrepo.StoredContent{Digest: digest, Size: *output.ContentLength}, true, nil
}

// Upload implements [pkgrepo.Store].
func (s *Store) Upload(ctx context.Context, request pkgrepo.UploadRequest) error {
	if err := s.validateRequest(ctx, request.Path); err != nil {
		return err
	}
	if request.Body == nil {
		return errors.New("R2 upload body is nil")
	}
	if request.Size < 0 {
		return fmt.Errorf("R2 upload %s has negative size %d", request.Path, request.Size)
	}
	if _, err := rel.ParseDigest(request.Digest.String()); err != nil {
		return fmt.Errorf("R2 upload %s digest: %w", request.Path, err)
	}
	cacheControl, err := formatCacheControl(request.Cache)
	if err != nil {
		return err
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(request.Path),
		Body:          request.Body,
		ContentLength: aws.Int64(request.Size),
		CacheControl:  aws.String(cacheControl),
		Metadata: map[string]string{
			digestMetadataKey: request.Digest.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("upload R2 object %s: %w", request.Path, err)
	}

	return nil
}

// resolveEndpoint validates an explicit endpoint or constructs the Cloudflare endpoint.
func resolveEndpoint(options Options) (string, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		accountID := strings.TrimSpace(options.AccountID)
		if accountID == "" {
			return "", errors.New("cloudflare account ID is empty")
		}
		if strings.ContainsAny(accountID, "/?#") {
			return "", errors.New("cloudflare account ID contains URL delimiters")
		}
		endpoint = "https://" + accountID + cloudflareEndpointSuffix
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse R2 endpoint: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("R2 endpoint %q must be an HTTP(S) origin without credentials", endpoint)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("R2 endpoint %q must not include a path", endpoint)
	}

	return strings.TrimSuffix(endpoint, "/"), nil
}

// validateRequest confirms one store method has a usable receiver, context, and key.
func (s *Store) validateRequest(ctx context.Context, objectPath string) error {
	if err := s.validateStore(ctx); err != nil {
		return err
	}
	if !fs.ValidPath(objectPath) || objectPath == "." {
		return fmt.Errorf("R2 object path %q is not confined", objectPath)
	}

	return nil
}

// validateStore confirms one store method has a usable receiver and context.
func (s *Store) validateStore(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if s == nil || s.client == nil || s.bucket == "" {
		return errors.New("R2 store is nil")
	}

	return nil
}

// objectNotFound reports whether one S3 error is an absent object.
func objectNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "NoSuchObject":
			return true
		}
	}
	var responseErr *smithyhttp.ResponseError

	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == 404
}

// formatCacheControl maps the domain cache policy onto one exact HTTP directive.
func formatCacheControl(policy pkgrepo.CachePolicy) (string, error) {
	switch policy {
	case pkgrepo.CacheImmutable:
		return immutableCacheControl, nil
	case pkgrepo.CacheNoStore:
		return mutableCacheControl, nil
	default:
		return "", fmt.Errorf("R2 cache policy %q is unsupported", policy)
	}
}
