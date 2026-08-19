package pubgh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// digestPrefix is the canonical digest algorithm prefix.
	digestPrefix = "sha256:"
	// maxSafeID is JavaScript Number.MAX_SAFE_INTEGER.
	//
	// The replaced github-script blocks rejected identifiers outside
	// Number.isSafeInteger. Artifact and run IDs must stay inside that range.
	maxSafeID int64 = 1<<53 - 1
	// retryAttempts is one initial Get plus three octokit retries.
	retryAttempts = 4
	// retryWait is the first backoff; later waits double (1s, 2s, 4s).
	retryWait = time.Second
)

// Sentinel errors classified for the handoff check.
var (
	// ErrRetryable reports a transient GitHub API failure that VerifyHandoff retries.
	ErrRetryable = errors.New("retryable github error")
	// ErrHandoffMismatch reports that observed artifact metadata failed the tuple check.
	ErrHandoffMismatch = errors.New("handoff mismatch")
)

// ArtifactID is a positive GitHub Actions artifact identifier.
//
// The only constructors are [ParseArtifactID] and [ArtifactIDFromInt].
// The zero value is invalid.
type ArtifactID int64

// RunID is a positive GitHub Actions workflow run identifier.
//
// The only constructors are [ParseRunID] and [RunIDFromInt]. The zero
// value is invalid.
type RunID int64

// ArtifactDigest is a lowercase sha256:<64 hex> digest.
//
// The only constructor is [ParseArtifactDigest], which accepts an optional
// sha256: prefix and normalizes hex to lowercase. The zero value is invalid.
type ArtifactDigest string

// Repository is a GitHub owner/name pair.
//
// The only constructor is [ParseRepository]. The zero value is invalid.
type Repository struct {
	// Owner is the account or organization that owns the repository.
	Owner string
	// Name is the repository name.
	Name string
}

// Handoff is the expected Actions artifact metadata tuple.
//
// The only constructor is [NewHandoff]. The zero value is invalid.
type Handoff struct {
	// Repository is the repository that owns the artifact.
	Repository Repository
	// Run is the workflow run that must own the artifact.
	Run RunID
	// Artifact is the expected artifact identifier.
	Artifact ArtifactID
	// Digest is the expected GitHub-reported artifact digest.
	Digest ArtifactDigest
}

// ArtifactMetadata is the observed Actions artifact tuple.
//
// Values are produced by [ArtifactMeta.Get]. A zero ArtifactMetadata is
// invalid and must not be treated as a successful lookup.
type ArtifactMetadata struct {
	// ID is the observed artifact identifier.
	ID ArtifactID
	// Name is the observed artifact name.
	Name string
	// Digest is the GitHub-reported digest. The zero value means GitHub
	// reported no digest.
	Digest ArtifactDigest
	// SizeBytes is the reported archive size.
	SizeBytes int64
	// HasRun reports whether workflow-run metadata was present.
	HasRun bool
	// Run is the workflow run that produced the artifact. It is only
	// valid when HasRun is true.
	Run RunID
	// ExpiresAt is the reported expiry instant. The zero value means
	// GitHub reported no expiry.
	ExpiresAt time.Time
	// Expired reports GitHub's expired flag.
	Expired bool
}

// ArtifactMeta fetches Actions artifact metadata.
type ArtifactMeta interface {
	// Get returns metadata for id in repository.
	//
	// Callers must not assume Get downloads the archive or recomputes
	// its digest.
	Get(ctx context.Context, repository Repository, id ArtifactID) (ArtifactMetadata, error)
}

// SleepFunc waits for d or until ctx is cancelled.
type SleepFunc func(ctx context.Context, d time.Duration) error

// ParseArtifactID constructs an [ArtifactID] from a decimal string.
func ParseArtifactID(raw string) (ArtifactID, error) {
	value, err := parseSafeID("artifact id", raw)
	if err != nil {
		return 0, err
	}

	return ArtifactID(value), nil
}

// ArtifactIDFromInt constructs an [ArtifactID] from a positive safe integer.
func ArtifactIDFromInt(value int64) (ArtifactID, error) {
	if err := checkSafeID("artifact id", value); err != nil {
		return 0, err
	}

	return ArtifactID(value), nil
}

// Int64 returns the identifier as int64.
func (id ArtifactID) Int64() int64 {
	return int64(id)
}

// String returns the decimal identifier.
func (id ArtifactID) String() string {
	return strconv.FormatInt(int64(id), 10)
}

// ParseRunID constructs a [RunID] from a decimal string.
func ParseRunID(raw string) (RunID, error) {
	value, err := parseSafeID("run id", raw)
	if err != nil {
		return 0, err
	}

	return RunID(value), nil
}

// RunIDFromInt constructs a [RunID] from a positive safe integer.
func RunIDFromInt(value int64) (RunID, error) {
	if err := checkSafeID("run id", value); err != nil {
		return 0, err
	}

	return RunID(value), nil
}

// Int64 returns the identifier as int64.
func (id RunID) Int64() int64 {
	return int64(id)
}

// String returns the decimal identifier.
func (id RunID) String() string {
	return strconv.FormatInt(int64(id), 10)
}

// ParseArtifactDigest constructs an [ArtifactDigest] from a SHA-256 hex string.
//
// An optional sha256: prefix is accepted. Uppercase hex is normalized to
// lowercase. The returned value always uses the sha256: prefix.
func ParseArtifactDigest(raw string) (ArtifactDigest, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("artifact digest is empty")
	}

	hexPart, found := strings.CutPrefix(strings.ToLower(trimmed), digestPrefix)
	if !found {
		hexPart = strings.ToLower(trimmed)
	}
	if len(hexPart) != hex.EncodedLen(sha256.Size) {
		return "", fmt.Errorf(
			"artifact digest %q has length %d, want %d",
			hexPart,
			len(hexPart),
			hex.EncodedLen(sha256.Size),
		)
	}
	for _, r := range hexPart {
		if !isHex(r) {
			return "", fmt.Errorf("artifact digest %q is not hexadecimal", hexPart)
		}
	}

	return ArtifactDigest(digestPrefix + hexPart), nil
}

// String returns the canonical sha256:<hex> digest.
func (d ArtifactDigest) String() string {
	return string(d)
}

// ParseRepository constructs a [Repository] from an owner/name pair.
func ParseRepository(raw string) (Repository, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Repository{}, errors.New("repository is empty")
	}

	owner, name, ok := strings.Cut(trimmed, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repository{}, fmt.Errorf("repository %q is not an owner/name pair", trimmed)
	}

	return Repository{Owner: owner, Name: name}, nil
}

// String returns the owner/name pair.
func (r Repository) String() string {
	return r.Owner + "/" + r.Name
}

// NewHandoff constructs a [Handoff] from already-validated domain values.
func NewHandoff(repository Repository, run RunID, artifact ArtifactID, digest ArtifactDigest) (Handoff, error) {
	handoff := Handoff{
		Repository: repository,
		Run:        run,
		Artifact:   artifact,
		Digest:     digest,
	}
	if err := handoff.validate(); err != nil {
		return Handoff{}, err
	}

	return handoff, nil
}

// VerifyHandoff confirms that the artifact metadata matches expected.
//
// The artifact must exist, belong to expected.Run, not be expired, include
// workflow-run metadata, and report expected.Digest after normalization.
// A cancelled [context.Context] fails before the port is called. A nil
// context or port is rejected. Failures name what mismatched and never
// include credentials.
//
// Get is called at most four times. Failures wrapping [ErrRetryable] wait
// 1s, then 2s, then 4s between attempts. Context cancellation returns
// immediately. Absent, authentication, and malformed responses are never
// retried. A nil sleep uses a context-aware timer.
func VerifyHandoff(
	ctx context.Context,
	meta ArtifactMeta,
	expected Handoff,
	sleep SleepFunc,
) (ArtifactMetadata, error) {
	if ctx == nil {
		return ArtifactMetadata{}, errors.New("context is nil")
	}
	if meta == nil {
		return ArtifactMetadata{}, errors.New("artifact metadata port is nil")
	}
	if err := expected.validate(); err != nil {
		return ArtifactMetadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArtifactMetadata{}, fmt.Errorf("verify handoff: %w", err)
	}
	if sleep == nil {
		sleep = sleepContext
	}

	return verifyHandoff(ctx, meta, expected, sleep)
}

// verifyHandoff checks the observed tuple after exported guards.
func verifyHandoff(
	ctx context.Context,
	meta ArtifactMeta,
	expected Handoff,
	sleep SleepFunc,
) (ArtifactMetadata, error) {
	var lastErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ArtifactMetadata{}, fmt.Errorf("verify handoff: %w", err)
		}
		observed, err := meta.Get(ctx, expected.Repository, expected.Artifact)
		if err != nil {
			lastErr = err
			if !errors.Is(err, ErrRetryable) || attempt == retryAttempts {
				if errors.Is(err, ErrRetryable) {
					return ArtifactMetadata{}, fmt.Errorf(
						"get artifact %s after %d attempts: %w",
						expected.Artifact,
						attempt,
						err,
					)
				}

				return ArtifactMetadata{}, fmt.Errorf("get artifact %s: %w", expected.Artifact, err)
			}
			if err := sleep(ctx, retryWait<<(attempt-1)); err != nil {
				return ArtifactMetadata{}, fmt.Errorf("verify handoff: %w", err)
			}

			continue
		}

		return matchHandoff(expected, observed)
	}
	if lastErr == nil {
		return ArtifactMetadata{}, errors.New("get artifact: retry budget exhausted")
	}

	return ArtifactMetadata{}, fmt.Errorf(
		"get artifact %s after %d attempts: %w",
		expected.Artifact,
		retryAttempts,
		lastErr,
	)
}

// matchHandoff compares observed metadata against the expected tuple.
func matchHandoff(expected Handoff, observed ArtifactMetadata) (ArtifactMetadata, error) {
	if !observed.HasRun {
		return ArtifactMetadata{}, fmt.Errorf(
			"%w: artifact has no workflow-run metadata: artifact %s",
			ErrHandoffMismatch,
			expected.Artifact,
		)
	}
	if observed.Expired {
		return ArtifactMetadata{}, fmt.Errorf("%w: artifact %s has expired", ErrHandoffMismatch, expected.Artifact)
	}
	if observed.Run != expected.Run {
		return ArtifactMetadata{}, fmt.Errorf(
			"%w: artifact %s belongs to workflow run %s, expected %s",
			ErrHandoffMismatch,
			expected.Artifact,
			observed.Run,
			expected.Run,
		)
	}
	if observed.Digest != expected.Digest {
		got := observed.Digest.String()
		if got == "" {
			got = "<none>"
		}

		return ArtifactMetadata{}, fmt.Errorf(
			"%w: expected %s, got %s",
			ErrHandoffMismatch,
			expected.Digest,
			got,
		)
	}

	return observed, nil
}

// sleepContext waits for d or returns ctx.Err() if the context ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// validate rejects a zero or incomplete handoff tuple.
func (h Handoff) validate() error {
	if h.Repository.Owner == "" || h.Repository.Name == "" {
		return errors.New("handoff repository is empty")
	}
	if h.Run == 0 {
		return errors.New("handoff run id is empty")
	}
	if h.Artifact == 0 {
		return errors.New("handoff artifact id is empty")
	}
	if h.Digest == "" {
		return errors.New("handoff digest is empty")
	}

	return nil
}

// parseSafeID parses a decimal identifier and checks the safe-integer bound.
func parseSafeID(label, raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("%s is empty", label)
	}

	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", label, trimmed)
	}
	if err := checkSafeID(label, value); err != nil {
		return 0, err
	}

	return value, nil
}

// checkSafeID rejects non-positive identifiers and values above maxSafeID.
func checkSafeID(label string, value int64) error {
	if value <= 0 {
		return fmt.Errorf("%s %d is not positive", label, value)
	}
	if value > maxSafeID {
		return fmt.Errorf("%s %d exceeds the safe integer range", label, value)
	}

	return nil
}

// isHex reports whether r is an ASCII hexadecimal digit.
func isHex(r rune) bool {
	return unicode.Is(unicode.ASCII_Hex_Digit, r)
}
