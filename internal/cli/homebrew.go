package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubbrew"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// commandHomebrew is the envelope command path for publish homebrew.
	commandHomebrew = "publish homebrew"
	// flagTap is the Homebrew tap owner/repository flag name.
	flagTap = "tap"
	// flagCask is the expected Homebrew cask token flag name.
	flagCask = "cask"
	// generatedCaskDirectory is GoReleaser's cask output directory under dist.
	generatedCaskDirectory = "homebrew/"
	// maxGeneratedCaskBytes bounds the generated Ruby source read into memory.
	maxGeneratedCaskBytes int64 = 1 << 20
	// maxGeneratedCaskReadBytes distinguishes an exact-size file from an
	// oversized file.
	maxGeneratedCaskReadBytes = maxGeneratedCaskBytes + 1
)

// newHomebrewCommand constructs the publish homebrew verb.
func newHomebrewCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "homebrew",
		Short: "Open a protected tap pull request for a generated cask",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHomebrew(cmd, options)
		},
	}
	cmd.Flags().String(flagDist, "", "path to the authoritative release artifact directory")
	cmd.Flags().String(flagTap, "", "target Homebrew tap as owner/repository")
	cmd.Flags().String(flagCask, "", "expected Homebrew cask token")

	return cmd
}

// homebrewConfig is the resolved publish-homebrew configuration.
type homebrewConfig struct {
	// Dist is the authoritative release artifact directory.
	Dist string
	// Tap is the target Homebrew tap.
	Tap pubbrew.Repository
	// Source is the repository that owns the release.
	Source pubbrew.Repository
	// Version is the stable source release version.
	Version rel.Version
	// Commit is the source commit that built the release.
	Commit pubbrew.CommitSHA
	// Cask is the expected generated cask token.
	Cask pubbrew.CaskToken
	// Token is the GitHub App installation token.
	Token rel.Secret
	// Endpoint is the GitHub API location.
	Endpoint GitHubEndpoint
}

// runHomebrew validates configuration, opens the generated cask through a
// confined distribution root, and reconciles the tap pull request.
//
//nolint:dupl // Homebrew policy intentionally remains isolated from Scoop policy.
func runHomebrew(cmd *cobra.Command, options Options) error {
	expected, err := resolveHomebrew(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandHomebrew, nil, UsageError(err))
	}
	content, err := readGeneratedCask(expected.Dist, expected.Cask)
	if err != nil {
		return writeCommandResult(options, commandHomebrew, nil, err)
	}
	reader, err := tapReader(options, expected)
	if err != nil {
		return writeCommandResult(options, commandHomebrew, nil, err)
	}
	writer, err := tapWriter(options, expected)
	if err != nil {
		return writeCommandResult(options, commandHomebrew, nil, err)
	}

	result, err := pubbrew.Publish(cmd.Context(), pubbrew.PublishInput{
		Tap:     expected.Tap,
		Source:  expected.Source,
		Version: expected.Version,
		Commit:  expected.Commit,
		Cask:    expected.Cask,
		Content: content,
	}, reader, writer)
	if err != nil {
		return writeCommandResult(options, commandHomebrew, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandHomebrew, result, nil)
}

// resolveHomebrew parses flags and Actions environment without performing I/O.
func resolveHomebrew(cmd *cobra.Command, options Options) (homebrewConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return homebrewConfig{}, err
	}
	if settings.Dist == "" {
		return homebrewConfig{}, fmt.Errorf("--%s is required", flagDist)
	}

	tapRaw, err := requiredCommandFlag(cmd, flagTap)
	if err != nil {
		return homebrewConfig{}, err
	}
	tap, err := pubbrew.ParseRepository(tapRaw)
	if err != nil {
		return homebrewConfig{}, fmt.Errorf("--%s: %w", flagTap, err)
	}
	caskRaw, err := requiredCommandFlag(cmd, flagCask)
	if err != nil {
		return homebrewConfig{}, err
	}
	cask, err := pubbrew.ParseCaskToken(caskRaw)
	if err != nil {
		return homebrewConfig{}, fmt.Errorf("--%s: %w", flagCask, err)
	}

	sourceRaw, err := requiredEnv(options.LookupEnv, envRepository)
	if err != nil {
		return homebrewConfig{}, err
	}
	source, err := pubbrew.ParseRepository(sourceRaw)
	if err != nil {
		return homebrewConfig{}, fmt.Errorf("%s: %w", envRepository, err)
	}
	versionRaw, err := deriveVersion(options.LookupEnv)
	if err != nil {
		return homebrewConfig{}, err
	}
	version, err := rel.ParseVersion(versionRaw)
	if err != nil {
		return homebrewConfig{}, fmt.Errorf("%s: %w", envRefName, err)
	}
	commitRaw, err := requiredEnv(options.LookupEnv, envCommitSHA)
	if err != nil {
		return homebrewConfig{}, err
	}
	commit, err := pubgh.ParseCommitSHA(commitRaw)
	if err != nil {
		return homebrewConfig{}, err
	}
	tokenRaw, err := requiredEnv(options.LookupEnv, envAppToken)
	if err != nil {
		return homebrewConfig{}, err
	}
	endpoint, err := resolveGitHubEndpoint(options.LookupEnv)
	if err != nil {
		return homebrewConfig{}, err
	}

	return homebrewConfig{
		Dist:     settings.Dist,
		Tap:      tap,
		Source:   source,
		Version:  version,
		Commit:   pubbrew.CommitSHA(commit.String()),
		Cask:     cask,
		Token:    rel.NewSecret(tokenRaw),
		Endpoint: endpoint,
	}, nil
}

// requiredCommandFlag returns one nonempty local command flag.
func requiredCommandFlag(cmd *cobra.Command, name string) (string, error) {
	value, err := cmd.Flags().GetString(name)
	if err != nil {
		return "", fmt.Errorf("read --%s: %w", name, err)
	}
	if value == "" {
		return "", fmt.Errorf("--%s is required", name)
	}

	return value, nil
}

// readGeneratedCask reads exactly homebrew/Casks/<token>.rb through an [os.Root].
//
//nolint:dupl // Channel-specific paths and diagnostics stay explicit.
func readGeneratedCask(dist string, token pubbrew.CaskToken) ([]byte, error) {
	root, err := os.OpenRoot(dist)
	if err != nil {
		return nil, fmt.Errorf("open dist %s: %w", dist, err)
	}
	defer root.Close()

	path := generatedCaskDirectory + token.Path().String()
	file, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open generated cask %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat generated cask %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("generated cask %s is not a regular file", path)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("generated cask %s is empty", path)
	}
	if info.Size() > maxGeneratedCaskBytes {
		return nil, fmt.Errorf("generated cask %s exceeds %d bytes", path, maxGeneratedCaskBytes)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxGeneratedCaskReadBytes))
	if err != nil {
		return nil, fmt.Errorf("read generated cask %s: %w", path, err)
	}
	if int64(len(content)) > maxGeneratedCaskBytes {
		return nil, fmt.Errorf("generated cask %s exceeds %d bytes", path, maxGeneratedCaskBytes)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("generated cask %s is empty", path)
	}

	return content, nil
}

// tapReader returns the injected read port or constructs one.
func tapReader(options Options, expected homebrewConfig) (pubbrew.RepositoryReader, error) {
	if options.TapReader != nil {
		return options.TapReader, nil
	}
	if options.NewTapReader == nil {
		return nil, errors.New("tap repository reader is not configured")
	}
	reader, err := options.NewTapReader(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("construct tap repository reader: %w", err)
	}
	if reader == nil {
		return nil, errors.New("tap repository reader is nil")
	}

	return reader, nil
}

// tapWriter returns the injected write port or constructs one.
func tapWriter(options Options, expected homebrewConfig) (pubbrew.RepositoryWriter, error) {
	if options.TapWriter != nil {
		return options.TapWriter, nil
	}
	if options.NewTapWriter == nil {
		return nil, errors.New("tap repository writer is not configured")
	}
	writer, err := options.NewTapWriter(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("construct tap repository writer: %w", err)
	}
	if writer == nil {
		return nil, errors.New("tap repository writer is nil")
	}

	return writer, nil
}
