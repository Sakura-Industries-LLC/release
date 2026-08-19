package reg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// bytesPerKiB is the number of bytes in a kibibyte.
	bytesPerKiB = 1024
	// kibibytesPerMiB is the number of kibibytes in a mebibyte.
	kibibytesPerMiB = 1024
	// manifestLimitBytes is the maximum encoded manifest Version will read.
	manifestLimitBytes int64 = 4 * bytesPerKiB * kibibytesPerMiB
)

// annotationFile is the subset of an OCI manifest Version needs.
type annotationFile struct {
	// Annotations holds OCI manifest annotations.
	Annotations map[string]string `json:"annotations"`
}

// Resolve implements [puboci.StateReader].
func (c *Client) Resolve(ctx context.Context, ref puboci.Reference) (rel.Digest, error) {
	if err := c.requireReady(ctx); err != nil {
		return "", err
	}

	return c.resolve(ctx, ref)
}

// Version implements [puboci.StateReader].
func (c *Client) Version(ctx context.Context, ref puboci.Reference) (rel.Version, error) {
	if err := c.requireReady(ctx); err != nil {
		return rel.Version{}, err
	}

	return c.version(ctx, ref)
}

// resolve looks up the digest for ref after exported guards.
func (c *Client) resolve(ctx context.Context, ref puboci.Reference) (rel.Digest, error) {
	repo, err := c.repository(ref.Image)
	if err != nil {
		return "", err
	}

	desc, err := repo.Resolve(ctx, ref.Tag.String())
	if err != nil {
		return "", classify(err)
	}

	digest, err := rel.ParseDigest(desc.Digest.String())
	if err != nil {
		return "", fmt.Errorf("registry digest: %w", err)
	}

	return digest, nil
}

// version reads the version annotation for ref after exported guards.
func (c *Client) version(ctx context.Context, ref puboci.Reference) (rel.Version, error) {
	repo, err := c.repository(ref.Image)
	if err != nil {
		return rel.Version{}, err
	}

	_, body, err := repo.FetchReference(ctx, ref.Tag.String())
	if err != nil {
		return rel.Version{}, classify(err)
	}
	defer body.Close()

	return decodeVersion(body)
}

// classify maps an oras failure onto a puboci sentinel or a diagnostic.
//
// The returned error never includes credentials, Authorization headers, or
// request URLs.
func classify(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: request canceled", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: request deadline exceeded", context.DeadlineExceeded)
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return puboci.ErrTagAbsent
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		cause := error(urlErr)
		if urlErr.Err != nil {
			cause = urlErr.Err
		}

		return fmt.Errorf("%w: %s", puboci.ErrRetryable, transportReason(cause))
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %s", puboci.ErrRetryable, transportReason(netErr))
	}

	var respErr *errcode.ErrorResponse
	if !errors.As(err, &respErr) {
		return errors.New("registry request failed")
	}

	switch code := respErr.StatusCode; {
	case code == http.StatusNotFound:
		return puboci.ErrTagAbsent
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("registry authentication failed: status %d", code)
	case code == http.StatusTooManyRequests || code >= http.StatusInternalServerError:
		return fmt.Errorf("%w: status %d", puboci.ErrRetryable, code)
	default:
		return fmt.Errorf("registry request failed: status %d", code)
	}
}

// transportReason returns a short, URL-free description of a transport failure.
func transportReason(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "i/o timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused"
	}

	return "transport error"
}

// decodeVersion reads an OCI manifest's version annotation from body.
func decodeVersion(body io.Reader) (rel.Version, error) {
	var payload annotationFile
	if err := json.NewDecoder(io.LimitReader(body, manifestLimitBytes)).Decode(&payload); err != nil {
		return rel.Version{}, fmt.Errorf("%w: manifest is not JSON", puboci.ErrCorruptState)
	}

	value := payload.Annotations[ocispec.AnnotationVersion]
	if value == "" {
		return rel.Version{}, fmt.Errorf("%w: missing %s annotation", puboci.ErrCorruptState, ocispec.AnnotationVersion)
	}

	version, err := rel.ParseVersion(value)
	if err != nil {
		return rel.Version{}, fmt.Errorf("%w: %w", puboci.ErrCorruptState, err)
	}

	return version, nil
}
