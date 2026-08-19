package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// commandBundle is the envelope command path for verify bundle.
	commandBundle = "verify bundle"
	// flagIdentity is the bundle certificate-identity flag name.
	flagIdentity = "identity"
	// flagIssuer is the bundle OIDC-issuer flag name.
	flagIssuer = "issuer"
)

// newBundleCommand constructs the verify bundle verb.
func newBundleCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Verify a closed release bundle and its detached Sigstore signature",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBundle(cmd, options)
		},
	}
	cmd.Flags().String(flagDist, "", "path to the distribution directory")
	cmd.Flags().String(flagIdentity, "", "exact certificate identity URL")
	cmd.Flags().String(flagIssuer, "", "OIDC issuer URL")

	return cmd
}

// runBundle validates configuration and verifies a closed release bundle.
//
// Missing or malformed configuration is [ErrUsage] and is raised before any
// port is constructed. Opening the distribution directory with [os.OpenRoot]
// and verification failures are command failures. Success without --json
// writes nothing.
func runBundle(cmd *cobra.Command, options Options) error {
	expected, err := resolveBundle(options)
	if err != nil {
		return writeCommandResult(options, commandBundle, nil, UsageError(err))
	}

	root, err := os.OpenRoot(expected.Dist)
	if err != nil {
		return writeCommandResult(options, commandBundle, nil, fmt.Errorf("open dist %s: %w", expected.Dist, err))
	}
	defer root.Close()

	verifier, err := blobVerifier(options, expected)
	if err != nil {
		return writeCommandResult(options, commandBundle, nil, err)
	}

	bundle, err := pubgh.VerifyBundle(cmd.Context(), root.FS(), verifier, expected.Trust)
	if err != nil {
		return writeCommandResult(options, commandBundle, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandBundle, bundleResult(expected, bundle), nil)
}

// BundleFileResult is one named digest in the verify-bundle payload.
type BundleFileResult struct {
	// Name is the flat file name inside the distribution directory.
	Name string `json:"name"`
	// Digest is the lowercase SHA-256 hex digest with no prefix.
	Digest string `json:"digest"`
}

// BundleResult is the --json payload for verify bundle.
type BundleResult struct {
	// Dist is the distribution directory selected by --dist or RELEASE_DIST.
	Dist string `json:"dist"`
	// Identity is the exact certificate identity URL used for verification.
	Identity string `json:"identity"`
	// Issuer is the OIDC issuer used for verification.
	Issuer string `json:"issuer"`
	// Payloads are the checksummed release payloads, in checksums.txt order.
	Payloads []BundleFileResult `json:"payloads"`
	// Controls are checksums.txt then checksums.txt.sigstore.json.
	Controls []BundleFileResult `json:"controls"`
}

// bundleConfig is the resolved verify-bundle configuration.
type bundleConfig struct {
	// Dist is the distribution directory to open.
	Dist string
	// Trust is the already-normalized Sigstore identity policy.
	Trust pubgh.TrustPolicy
	// CosignPath is RELEASE_COSIGN_PATH. Empty resolves cosign from PATH.
	CosignPath string
}

// resolveBundle parses flags and environment into a bundle configuration.
//
// It performs no I/O.
func resolveBundle(options Options) (bundleConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return bundleConfig{}, err
	}
	if settings.Dist == "" {
		return bundleConfig{}, fmt.Errorf("--%s is required", flagDist)
	}
	if settings.Identity == "" {
		return bundleConfig{}, fmt.Errorf("--%s is required", flagIdentity)
	}

	policy, err := pubgh.TrustPolicy{
		Identity: settings.Identity,
		Issuer:   settings.Issuer,
	}.Normalize()
	if err != nil {
		return bundleConfig{}, err
	}

	return bundleConfig{
		Dist:       settings.Dist,
		Trust:      policy,
		CosignPath: resolveCosignPath(options.LookupEnv),
	}, nil
}

// blobVerifier returns the injected verification port or constructs one.
func blobVerifier(options Options, expected bundleConfig) (pubgh.BlobVerifier, error) {
	if options.BlobVerifier != nil {
		return options.BlobVerifier, nil
	}
	if options.NewBlobVerifier == nil {
		return nil, errors.New("blob verifier factory is not configured")
	}

	verifier, err := options.NewBlobVerifier(expected.CosignPath, expected.Dist)
	if err != nil {
		return nil, UsageError(fmt.Errorf("cosign: %w", err))
	}
	if verifier == nil {
		return nil, errors.New("blob verifier factory returned nil")
	}

	return verifier, nil
}

// bundleResult builds the success envelope payload.
func bundleResult(expected bundleConfig, bundle pubgh.Bundle) BundleResult {
	return BundleResult{
		Dist:     expected.Dist,
		Identity: expected.Trust.Identity,
		Issuer:   expected.Trust.Issuer,
		Payloads: bundleFileResults(bundle.Payloads),
		Controls: bundleFileResults(bundle.Controls),
	}
}

// bundleFileResults copies entries into the JSON result shape.
func bundleFileResults(entries []pubgh.BundleEntry) []BundleFileResult {
	result := make([]BundleFileResult, 0, len(entries))
	for _, entry := range entries {
		result = append(result, BundleFileResult{
			Name:   entry.Name,
			Digest: bundleEntryDigest(entry),
		})
	}

	return result
}

// bundleEntryDigest returns the JSON digest string for one bundle entry.
func bundleEntryDigest(entry pubgh.BundleEntry) string {
	return entry.Digest.String()
}
