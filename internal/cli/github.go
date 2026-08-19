package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// commandGitHub is the envelope command path for publish github.
	commandGitHub = "publish github"
	// flagNoUndraft is the flag that leaves a converged release drafted.
	flagNoUndraft = "no-undraft"
	// envAppToken is the GitHub App installation token variable.
	//
	// The value is a variable name, not a credential.
	envAppToken = "RELEASE_APP_TOKEN" //nolint:gosec // Environment variable name.
	// envGHPath is the optional gh binary path override.
	envGHPath = "RELEASE_GH_PATH"
	// envGitPath is the optional git binary path override.
	envGitPath = "RELEASE_GIT_PATH"
	// envCommitSHA is the Actions commit SHA the tag must resolve to.
	envCommitSHA = "GITHUB_SHA"
	// checksumsFile is the closed-set claim filename inside --dist.
	checksumsFile = "checksums.txt"
	// indeterminateHint tells the operator not to rerun a publication
	// whose final draft state cannot be confirmed.
	indeterminateHint = "inspect the release in GitHub and reconcile manually; do not rerun the publication blindly"
)

// newGitHubCommand constructs the publish github verb.
func newGitHubCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Publish a verified GitHub Release from a closed distribution",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGitHub(cmd, options)
		},
	}
	cmd.Flags().String(flagDist, "", "path to the distribution directory")
	cmd.Flags().Bool(flagNoUndraft, false, "converge the draft and leave it unpublished")

	return cmd
}

// runGitHub validates configuration and publishes a verified GitHub Release.
//
// Missing or malformed configuration is [ErrUsage] and is raised before any
// port is constructed. Opening the distribution directory, rebuilding the
// closed set, and publication failures are command failures. Success without
// --json writes nothing. The --json envelope result is the
// [pubgh.PublishResult] itself.
func runGitHub(cmd *cobra.Command, options Options) error {
	expected, err := resolveGitHub(options)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, UsageError(err))
	}

	root, err := os.OpenRoot(expected.Dist)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, fmt.Errorf("open dist %s: %w", expected.Dist, err))
	}
	defer root.Close()

	bundle, err := buildExpectedBundle(root)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, err)
	}
	assets, err := expectedAssetPaths(expected.Dist, bundle)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, err)
	}

	reader, err := releaseReader(options, expected)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, err)
	}
	replacer, err := assetReplacer(options, expected)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, err)
	}
	publisher, err := releasePublisher(options, expected)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, err)
	}
	resolver, err := refResolver(options, expected)
	if err != nil {
		return writeCommandResult(options, commandGitHub, nil, err)
	}

	result, err := pubgh.Publish(cmd.Context(), pubgh.PublishInput{
		Repository: expected.Repository,
		Tag:        expected.Tag,
		Commit:     expected.Commit,
		Expected:   bundle,
		Assets:     assets,
		Undraft:    expected.Undraft,
	}, reader, replacer, publisher, resolver)
	if err != nil {
		writeIndeterminateHint(options, err)
		return writeCommandResult(options, commandGitHub, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandGitHub, result, nil)
}

// githubConfig is the resolved publish-github configuration.
type githubConfig struct {
	// Dist is the distribution directory to open.
	Dist string
	// Repository is the owner/name pair from GITHUB_REPOSITORY.
	Repository pubgh.Repository
	// Tag is the release tag from GITHUB_REF_NAME.
	Tag rel.Tag
	// Commit is the expected tag SHA from GITHUB_SHA.
	Commit pubgh.CommitSHA
	// Token is the App installation token. It is never logged.
	Token rel.Secret
	// Endpoint is the GitHub API location. Empty APIURL is the public API.
	Endpoint GitHubEndpoint
	// GHPath is RELEASE_GH_PATH. Empty resolves gh from PATH.
	GHPath string
	// GitPath is RELEASE_GIT_PATH. Empty resolves git from PATH.
	GitPath string
	// Undraft is false when --no-undraft was set.
	Undraft bool
}

// resolveGitHub parses flags and Actions environment into a publish config.
//
// It performs no I/O.
func resolveGitHub(options Options) (githubConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return githubConfig{}, err
	}
	if settings.Dist == "" {
		return githubConfig{}, fmt.Errorf("--%s is required", flagDist)
	}

	repositoryRaw, err := requiredEnv(options.LookupEnv, envRepository)
	if err != nil {
		return githubConfig{}, err
	}
	repository, err := pubgh.ParseRepository(repositoryRaw)
	if err != nil {
		return githubConfig{}, err
	}

	refRaw, err := requiredEnv(options.LookupEnv, envRefName)
	if err != nil {
		return githubConfig{}, err
	}
	tag, err := rel.ParseTag(refRaw)
	if err != nil {
		return githubConfig{}, err
	}

	shaRaw, err := requiredEnv(options.LookupEnv, envCommitSHA)
	if err != nil {
		return githubConfig{}, err
	}
	commit, err := pubgh.ParseCommitSHA(shaRaw)
	if err != nil {
		return githubConfig{}, err
	}

	tokenRaw, err := requiredEnv(options.LookupEnv, envAppToken)
	if err != nil {
		return githubConfig{}, err
	}
	endpoint, err := resolveGitHubEndpoint(options.LookupEnv)
	if err != nil {
		return githubConfig{}, err
	}

	return githubConfig{
		Dist:       settings.Dist,
		Repository: repository,
		Tag:        tag,
		Commit:     commit,
		Token:      rel.NewSecret(tokenRaw),
		Endpoint:   endpoint,
		GHPath:     lookupValue(options.LookupEnv, envGHPath),
		GitPath:    lookupValue(options.LookupEnv, envGitPath),
		Undraft:    !settings.NoUndraft,
	}, nil
}

// requiredEnv returns a nonempty named environment value.
func requiredEnv(lookup LookupEnv, name string) (string, error) {
	value := lookupValue(lookup, name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}

	return value, nil
}

// lookupValue returns the named environment value, or empty when unset.
func lookupValue(lookup LookupEnv, name string) string {
	if lookup == nil {
		return ""
	}
	value, ok := lookup(name)
	if !ok {
		return ""
	}

	return value
}

// buildExpectedBundle rebuilds the closed set and digests from dist.
//
// It does not verify the Sigstore signature. [runBundle] owns that check.
func buildExpectedBundle(root *os.Root) (pubgh.Bundle, error) {
	file, err := root.Open(checksumsFile)
	if err != nil {
		return pubgh.Bundle{}, fmt.Errorf("open %s: %w", checksumsFile, err)
	}
	claim, err := stage.ParseChecksums(file)
	closeErr := file.Close()
	if err != nil {
		return pubgh.Bundle{}, err
	}
	if closeErr != nil {
		return pubgh.Bundle{}, fmt.Errorf("close %s: %w", checksumsFile, closeErr)
	}

	return pubgh.BuildBundle(root.FS(), claim)
}

// expectedAssetPaths returns absolute paths for each bundle name, in order.
func expectedAssetPaths(dist string, bundle pubgh.Bundle) ([]pubgh.AssetPath, error) {
	absDist, err := filepath.Abs(dist)
	if err != nil {
		return nil, fmt.Errorf("dist path: %w", err)
	}

	names := bundle.Names()
	paths := make([]pubgh.AssetPath, 0, len(names))
	for _, name := range names {
		paths = append(paths, pubgh.AssetPath(filepath.Join(absDist, name)))
	}

	return paths, nil
}

// releaseReader returns the injected read port or constructs one.
func releaseReader(options Options, expected githubConfig) (pubgh.ReleaseReader, error) {
	if options.ReleaseReader != nil {
		return options.ReleaseReader, nil
	}
	if options.NewReleaseReader == nil {
		return nil, errors.New("release reader factory is not configured")
	}

	reader, err := options.NewReleaseReader(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, UsageError(fmt.Errorf("github client: %w", err))
	}
	if reader == nil {
		return nil, errors.New("release reader factory returned nil")
	}

	return reader, nil
}

// assetReplacer returns the injected upload port or constructs one.
func assetReplacer(options Options, expected githubConfig) (pubgh.AssetReplacer, error) {
	if options.AssetReplacer != nil {
		return options.AssetReplacer, nil
	}
	if options.NewAssetReplacer == nil {
		return nil, errors.New("asset replacer factory is not configured")
	}

	dir, err := filepath.Abs(expected.Dist)
	if err != nil {
		return nil, fmt.Errorf("dist path: %w", err)
	}
	replacer, err := options.NewAssetReplacer(expected.Token, expected.GHPath, dir)
	if err != nil {
		return nil, UsageError(fmt.Errorf("gh: %w", err))
	}
	if replacer == nil {
		return nil, errors.New("asset replacer factory returned nil")
	}

	return replacer, nil
}

// releasePublisher returns the injected undraft port or constructs one.
func releasePublisher(options Options, expected githubConfig) (pubgh.Publisher, error) {
	if options.Publisher != nil {
		return options.Publisher, nil
	}
	if options.NewPublisher == nil {
		return nil, errors.New("publisher factory is not configured")
	}

	publisher, err := options.NewPublisher(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, UsageError(fmt.Errorf("github client: %w", err))
	}
	if publisher == nil {
		return nil, errors.New("publisher factory returned nil")
	}

	return publisher, nil
}

// refResolver returns the injected tag-to-SHA port or constructs one.
func refResolver(options Options, expected githubConfig) (pubgh.RefResolver, error) {
	if options.RefResolver != nil {
		return options.RefResolver, nil
	}
	if options.NewRefResolver == nil {
		return nil, errors.New("ref resolver factory is not configured")
	}

	resolver, err := options.NewRefResolver(expected.GitPath, "")
	if err != nil {
		return nil, UsageError(fmt.Errorf("git: %w", err))
	}
	if resolver == nil {
		return nil, errors.New("ref resolver factory returned nil")
	}

	return resolver, nil
}

// writeIndeterminateHint prints a one-line operator note when publication
// cannot be confirmed. Ordinary failures write nothing.
func writeIndeterminateHint(options Options, err error) {
	if !errors.Is(err, pubgh.ErrIndeterminate) {
		return
	}
	if options.Err == nil {
		return
	}
	_, _ = fmt.Fprintln(options.Err, indeterminateHint)
}
