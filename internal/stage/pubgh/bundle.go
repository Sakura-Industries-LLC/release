package pubgh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"strings"

	"github.com/meigma/release/internal/stage"
)

const (
	// checksumsName is the checksum claim filename.
	checksumsName = "checksums.txt"
	// bundleName is the Sigstore bundle filename.
	bundleName = "checksums.txt.sigstore.json"
	// defaultOIDCIssuer is the GitHub Actions OIDC issuer used when
	// [TrustPolicy.Issuer] is empty.
	defaultOIDCIssuer = "https://token.actions.githubusercontent.com"
)

// TrustPolicy is the exact Sigstore identity a release bundle must carry.
type TrustPolicy struct {
	// Identity is the exact certificate identity URL. It is required.
	Identity string
	// Issuer is the OIDC issuer. An empty value defaults to
	// https://token.actions.githubusercontent.com.
	Issuer string
}

// BlobVerification is one detached-bundle verification request.
type BlobVerification struct {
	// Payload is the name inside the distribution directory, for example
	// checksums.txt.
	Payload string
	// Bundle is the name inside the distribution directory, for example
	// checksums.txt.sigstore.json.
	Bundle string
	// Identity is the exact certificate identity URL.
	Identity string
	// Issuer is the OIDC issuer URL.
	Issuer string
}

// BlobVerifier verifies a detached Sigstore bundle against a payload.
type BlobVerifier interface {
	// Verify checks request.Payload against request.Bundle using the
	// exact identity and issuer. Implementations must not mutate the
	// distribution directory.
	Verify(ctx context.Context, request BlobVerification) error
}

// BundleEntry is one named digest inside a closed release bundle.
type BundleEntry struct {
	// Name is the flat file name inside the distribution directory.
	Name string
	// Digest is the lowercase SHA-256 hex digest with no prefix.
	Digest stage.Digest
}

// Bundle is a closed, checksummed release distribution.
//
// Payloads follow checksums.txt order. Controls are always checksums.txt
// then checksums.txt.sigstore.json.
type Bundle struct {
	// Payloads are the checksummed release payloads, in checksums.txt order.
	Payloads []BundleEntry
	// Controls are exactly checksums.txt then checksums.txt.sigstore.json.
	Controls []BundleEntry
}

// Normalize fills the default issuer and rejects an empty or non-URL identity
// or issuer.
//
// Identity must be an absolute https URL. An empty issuer becomes
// https://token.actions.githubusercontent.com. The issuer, after defaulting,
// must also be an absolute https URL.
func (p TrustPolicy) Normalize() (TrustPolicy, error) {
	identity, err := parseHTTPSURL("certificate identity", p.Identity)
	if err != nil {
		return TrustPolicy{}, err
	}

	issuer := strings.TrimSpace(p.Issuer)
	if issuer == "" {
		issuer = defaultOIDCIssuer
	}
	issuer, err = parseHTTPSURL("certificate issuer", issuer)
	if err != nil {
		return TrustPolicy{}, err
	}

	return TrustPolicy{Identity: identity, Issuer: issuer}, nil
}

// Names returns payload names then control names, in order.
func (b Bundle) Names() []string {
	names := make([]string, 0, len(b.Payloads)+len(b.Controls))
	for _, entry := range b.Payloads {
		names = append(names, entry.Name)
	}
	for _, entry := range b.Controls {
		names = append(names, entry.Name)
	}

	return names
}

// BuildBundle performs the closed-set reconciliation of claim against fsys.
//
// Every claimed payload must be a regular file whose digest matches. Both
// control files must exist as regular files and must not appear in the claim.
// The directory must contain nothing else: no extra file, directory, symlink,
// or irregular entry. The closed-set scan uses each [fs.DirEntry] mode so
// a symlink is refused rather than followed. Payload digests are then
// checked with [stage.VerifyBundle]. Control digests are streamed with
// [io.Copy] into [sha256.New]. A nil filesystem is rejected. The first
// offending entry is named in the error.
func BuildBundle(fsys fs.FS, claim stage.ChecksumSet) (Bundle, error) {
	if fsys == nil {
		return Bundle{}, errors.New("filesystem is nil")
	}

	return buildBundle(fsys, claim)
}

// VerifyBundle parses checksums.txt, builds the closed bundle, and verifies
// the Sigstore signature last.
//
// Local checks run first: [stage.ParseChecksums], then [BuildBundle], then
// [TrustPolicy.Normalize]. The signature check runs only after every local
// check has passed. A nil [context.Context], filesystem, or verifier is
// rejected. A cancelled context fails before the filesystem is read.
func VerifyBundle(
	ctx context.Context,
	fsys fs.FS,
	verifier BlobVerifier,
	trust TrustPolicy,
) (Bundle, error) {
	if ctx == nil {
		return Bundle{}, errors.New("context is nil")
	}
	if fsys == nil {
		return Bundle{}, errors.New("filesystem is nil")
	}
	if verifier == nil {
		return Bundle{}, errors.New("blob verifier is nil")
	}
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("verify bundle: %w", err)
	}

	return verifyBundle(ctx, fsys, verifier, trust)
}

// buildBundle reconciles the claim after the exported nil check.
func buildBundle(fsys fs.FS, claim stage.ChecksumSet) (Bundle, error) {
	claimed := make(map[string]struct{}, claim.Len())
	for _, entry := range claim.Entries() {
		name := entry.Name.String()
		if isControl(name) {
			return Bundle{}, fmt.Errorf("control file %s must not be listed in checksums.txt", name)
		}
		claimed[name] = struct{}{}
	}
	if err := checkClosedSet(fsys, claimed); err != nil {
		return Bundle{}, err
	}
	if err := stage.VerifyBundle(fsys, claim); err != nil {
		return Bundle{}, err
	}

	checksumDigest, err := hashFile(fsys, checksumsName)
	if err != nil {
		return Bundle{}, err
	}
	bundleDigest, err := hashFile(fsys, bundleName)
	if err != nil {
		return Bundle{}, err
	}

	payloads := make([]BundleEntry, 0, claim.Len())
	for _, entry := range claim.Entries() {
		payloads = append(payloads, BundleEntry{
			Name:   entry.Name.String(),
			Digest: entry.Digest,
		})
	}

	return Bundle{
		Payloads: payloads,
		Controls: []BundleEntry{
			{Name: checksumsName, Digest: checksumDigest},
			{Name: bundleName, Digest: bundleDigest},
		},
	}, nil
}

// verifyBundle verifies after the exported nil checks.
func verifyBundle(
	ctx context.Context,
	fsys fs.FS,
	verifier BlobVerifier,
	trust TrustPolicy,
) (Bundle, error) {
	file, err := fsys.Open(checksumsName)
	if err != nil {
		return Bundle{}, fmt.Errorf("open %s: %w", checksumsName, err)
	}
	claim, err := stage.ParseChecksums(file)
	closeErr := file.Close()
	if err != nil {
		return Bundle{}, err
	}
	if closeErr != nil {
		return Bundle{}, fmt.Errorf("close %s: %w", checksumsName, closeErr)
	}

	bundle, err := buildBundle(fsys, claim)
	if err != nil {
		return Bundle{}, err
	}

	policy, err := trust.Normalize()
	if err != nil {
		return Bundle{}, err
	}

	request := BlobVerification{
		Payload:  checksumsName,
		Bundle:   bundleName,
		Identity: policy.Identity,
		Issuer:   policy.Issuer,
	}
	if verifyErr := verifier.Verify(ctx, request); verifyErr != nil {
		return Bundle{}, verifyErr
	}

	return bundle, nil
}

// checkClosedSet rejects extra, missing, or non-regular directory entries.
func checkClosedSet(fsys fs.FS, claimed map[string]struct{}) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("read distribution directory: %w", err)
	}

	seenChecksums := false
	seenBundle := false
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case checksumsName:
			seenChecksums = true
		case bundleName:
			seenBundle = true
		default:
			if _, ok := claimed[name]; !ok {
				return fmt.Errorf("unlisted or invalid release bundle entry: %s", name)
			}
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("%s: %w", name, infoErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unlisted or invalid release bundle entry: %s", name)
		}
	}
	if !seenChecksums {
		return fmt.Errorf("%s does not exist", checksumsName)
	}
	if !seenBundle {
		return fmt.Errorf("%s does not exist", bundleName)
	}

	return nil
}

// hashFile streams name through SHA-256 and returns a [stage.Digest].
func hashFile(fsys fs.FS, name string) (stage.Digest, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	sum := sha256.New()
	if _, copyErr := io.Copy(sum, file); copyErr != nil {
		return "", fmt.Errorf("hash %s: %w", name, copyErr)
	}

	return stage.Digest(hex.EncodeToString(sum.Sum(nil))), nil
}

// isControl reports whether name is a reserved control file.
func isControl(name string) bool {
	return name == checksumsName || name == bundleName
}

// parseHTTPSURL requires raw to be a nonempty absolute https URL.
func parseHTTPSURL(label, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%s is empty", label)
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%s %q is not an absolute URL: %w", label, value, err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("%s %q is not an absolute URL", label, value)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("%s %q is not an https URL", label, value)
	}

	return value, nil
}
