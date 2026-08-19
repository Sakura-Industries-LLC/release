package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// commandHandoff is the envelope command path for verify handoff.
	commandHandoff = "verify handoff"
	// flagArtifactID is the handoff artifact-id flag name.
	flagArtifactID = "artifact-id"
	// flagDigest is the handoff digest flag name.
	flagDigest = "digest"
	// envArtifactID is the environment variable for --artifact-id.
	envArtifactID = "RELEASE_ARTIFACT_ID"
	// envDigest is the environment variable for --digest.
	envDigest = "RELEASE_DIGEST"
	// envRepository is the Actions repository owner/name pair.
	envRepository = "GITHUB_REPOSITORY"
	// envRunID is the Actions workflow run identifier.
	envRunID = "GITHUB_RUN_ID"
	// envGitHubToken is the primary Actions token variable. The value is a
	// variable name, not a credential.
	envGitHubToken = "GITHUB_TOKEN" //nolint:gosec // Environment variable name.
	// envGHToken is the fallback GitHub token variable name.
	envGHToken = "GH_TOKEN"
	// envAPIURL is the GitHub API base URL.
	envAPIURL = "GITHUB_API_URL"
	// envServerURL is the GitHub HTML/upload base URL.
	envServerURL = "GITHUB_SERVER_URL"
	// publicAPIHost is the public github.com API host.
	publicAPIHost = "api.github.com"
)

// newVerifyCommand constructs the verify parent verb.
func newVerifyCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify release-unit handoffs and bundles",
		Args:  requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("a verify subcommand is required"))
		},
	}
	cmd.AddCommand(newHandoffCommand(options))

	return cmd
}

// newHandoffCommand constructs the verify handoff verb.
func newHandoffCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Verify an Actions artifact metadata tuple",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHandoff(cmd, options)
		},
	}
	cmd.Flags().String(flagArtifactID, "", "GitHub Actions artifact id")
	cmd.Flags().String(flagDigest, "", "expected GitHub-reported artifact digest")

	return cmd
}

// runHandoff validates configuration and verifies the artifact metadata tuple.
//
// Missing or malformed configuration is [ErrUsage] and is raised before the
// GitHub port is constructed or called. A tuple mismatch is a verification
// failure.
func runHandoff(cmd *cobra.Command, options Options) error {
	expected, err := resolveHandoff(options)
	if err != nil {
		return writeCommandResult(options, commandHandoff, nil, UsageError(err))
	}

	meta, err := artifactMeta(options, expected)
	if err != nil {
		return writeCommandResult(options, commandHandoff, nil, err)
	}

	observed, err := pubgh.VerifyHandoff(cmd.Context(), meta, expected.Handoff, nil)
	if err != nil {
		return writeCommandResult(options, commandHandoff, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandHandoff, handoffResult(observed), nil)
}

// GitHubEndpoint is the resolved GitHub API location for one invocation.
//
// An empty APIURL selects the public https://api.github.com client.
type GitHubEndpoint struct {
	// APIURL is a non-public GitHub API base. Empty selects api.github.com.
	APIURL string
	// ServerURL is the GitHub HTML/upload base used with a non-public APIURL.
	ServerURL string
}

// handoffConfig is the resolved verify-handoff configuration.
type handoffConfig struct {
	// Handoff is the expected metadata tuple.
	Handoff pubgh.Handoff
	// Token is the already-resolved GitHub credential. It is never logged.
	Token string
	// Endpoint is the GitHub API location. Empty APIURL is the public API.
	Endpoint GitHubEndpoint
}

// resolveHandoff parses flags and Actions environment into a handoff tuple.
//
// It performs no network I/O.
func resolveHandoff(options Options) (handoffConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if settings.ArtifactID == "" {
		return handoffConfig{}, fmt.Errorf("--%s is required", flagArtifactID)
	}
	if settings.Digest == "" {
		return handoffConfig{}, fmt.Errorf("--%s is required", flagDigest)
	}

	artifactID, err := pubgh.ParseArtifactID(settings.ArtifactID)
	if err != nil {
		return handoffConfig{}, err
	}
	digest, err := pubgh.ParseArtifactDigest(settings.Digest)
	if err != nil {
		return handoffConfig{}, err
	}

	repositoryRaw, ok := options.LookupEnv(envRepository)
	if !ok || repositoryRaw == "" {
		return handoffConfig{}, fmt.Errorf("%s is required", envRepository)
	}
	repository, err := pubgh.ParseRepository(repositoryRaw)
	if err != nil {
		return handoffConfig{}, err
	}

	runRaw, ok := options.LookupEnv(envRunID)
	if !ok || runRaw == "" {
		return handoffConfig{}, fmt.Errorf("%s is required", envRunID)
	}
	runID, err := pubgh.ParseRunID(runRaw)
	if err != nil {
		return handoffConfig{}, err
	}

	handoff, err := pubgh.NewHandoff(repository, runID, artifactID, digest)
	if err != nil {
		return handoffConfig{}, err
	}
	endpoint, err := resolveGitHubEndpoint(options.LookupEnv)
	if err != nil {
		return handoffConfig{}, err
	}

	return handoffConfig{
		Handoff:  handoff,
		Token:    resolveToken(options.LookupEnv),
		Endpoint: endpoint,
	}, nil
}

// artifactMeta returns the injected port or constructs one from the token.
func artifactMeta(options Options, expected handoffConfig) (pubgh.ArtifactMeta, error) {
	if options.ArtifactMeta != nil {
		return options.ArtifactMeta, nil
	}
	if expected.Token == "" {
		return nil, UsageError(fmt.Errorf("%s or %s is required", envGitHubToken, envGHToken))
	}
	if options.NewArtifactMeta == nil {
		return nil, errors.New("artifact metadata factory is not configured")
	}

	meta, err := options.NewArtifactMeta(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, UsageError(fmt.Errorf("github client: %w", err))
	}
	if meta == nil {
		return nil, errors.New("artifact metadata factory returned nil")
	}

	return meta, nil
}

// resolveGitHubEndpoint reads GITHUB_API_URL and GITHUB_SERVER_URL.
//
// An absent or public https://api.github.com value keeps the default client.
// Any other value must be an absolute URL and is passed to the factory as-is.
func resolveGitHubEndpoint(lookup LookupEnv) (GitHubEndpoint, error) {
	apiURL, err := optionalAbsoluteURL(lookup, envAPIURL)
	if err != nil {
		return GitHubEndpoint{}, err
	}
	if apiURL == "" || isPublicAPI(apiURL) {
		return GitHubEndpoint{}, nil
	}
	serverURL, err := optionalAbsoluteURL(lookup, envServerURL)
	if err != nil {
		return GitHubEndpoint{}, err
	}

	return GitHubEndpoint{APIURL: apiURL, ServerURL: serverURL}, nil
}

// optionalAbsoluteURL returns a named environment URL, or empty when unset.
func optionalAbsoluteURL(lookup LookupEnv, name string) (string, error) {
	if lookup == nil {
		return "", nil
	}
	raw, ok := lookup(name)
	if !ok {
		return "", nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s is empty", name)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%s %q is not an absolute URL", name, trimmed)
	}

	return trimmed, nil
}

// isPublicAPI reports whether raw is the public github.com API.
func isPublicAPI(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}

	return strings.EqualFold(parsed.Hostname(), publicAPIHost)
}

// resolveToken returns GITHUB_TOKEN, or GH_TOKEN when GITHUB_TOKEN is unset.
func resolveToken(lookup LookupEnv) string {
	if lookup == nil {
		return ""
	}
	if value, ok := lookup(envGitHubToken); ok && value != "" {
		return value
	}
	if value, ok := lookup(envGHToken); ok && value != "" {
		return value
	}

	return ""
}

// handoffResult builds the success envelope payload.
func handoffResult(observed pubgh.ArtifactMetadata) HandoffResult {
	expiresAt := ""
	if !observed.ExpiresAt.IsZero() {
		expiresAt = observed.ExpiresAt.UTC().Format(time.RFC3339)
	}

	return HandoffResult{
		Artifact: ArtifactHandoffResult{
			ID:        observed.ID.Int64(),
			Name:      observed.Name,
			Digest:    observed.Digest.String(),
			SizeBytes: observed.SizeBytes,
			RunID:     observed.Run.Int64(),
			ExpiresAt: expiresAt,
		},
	}
}
