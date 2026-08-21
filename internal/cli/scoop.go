package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// commandScoop is the envelope command path for publish scoop.
	commandScoop = "publish scoop"
	// flagBucket is the Scoop bucket owner/repository flag name.
	flagBucket = "bucket"
	// flagManifest is the expected Scoop manifest name flag name.
	flagManifest = "manifest"
	// generatedManifestDirectory is GoReleaser's Scoop output directory under dist.
	generatedManifestDirectory = "scoop/"
	// maxGeneratedManifestBytes bounds the generated JSON read into memory.
	maxGeneratedManifestBytes int64 = 1 << 20
	// maxGeneratedManifestReadBytes distinguishes an exact-size file from an
	// oversized file.
	maxGeneratedManifestReadBytes = maxGeneratedManifestBytes + 1
)

// newScoopCommand constructs the publish scoop verb.
func newScoopCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scoop",
		Short: "Open a protected bucket pull request for a generated manifest",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScoop(cmd, options)
		},
	}
	cmd.Flags().String(flagDist, "", "path to the authoritative release artifact directory")
	cmd.Flags().String(flagBucket, "", "target Scoop bucket as owner/repository")
	cmd.Flags().String(flagManifest, "", "expected Scoop manifest name")

	return cmd
}

// scoopConfig is the resolved publish-scoop configuration.
type scoopConfig struct {
	// Dist is the authoritative release artifact directory.
	Dist string
	// Bucket is the target Scoop bucket.
	Bucket pubscoop.Repository
	// Source is the repository that owns the release.
	Source pubscoop.Repository
	// Version is the stable source release version.
	Version rel.Version
	// Commit is the source commit that built the release.
	Commit pubscoop.CommitSHA
	// Manifest is the expected generated manifest name.
	Manifest pubscoop.ManifestName
	// Token is the GitHub App installation token.
	Token rel.Secret
	// Endpoint is the GitHub API location.
	Endpoint GitHubEndpoint
}

// runScoop validates configuration, opens the generated manifest through a
// confined distribution root, and reconciles the bucket pull request.
//
//nolint:dupl // Scoop policy intentionally remains isolated from Homebrew policy.
func runScoop(cmd *cobra.Command, options Options) error {
	expected, err := resolveScoop(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandScoop, nil, UsageError(err))
	}
	content, err := readGeneratedManifest(expected.Dist, expected.Manifest)
	if err != nil {
		return writeCommandResult(options, commandScoop, nil, err)
	}
	reader, err := bucketReader(options, expected)
	if err != nil {
		return writeCommandResult(options, commandScoop, nil, err)
	}
	writer, err := bucketWriter(options, expected)
	if err != nil {
		return writeCommandResult(options, commandScoop, nil, err)
	}

	result, err := pubscoop.Publish(cmd.Context(), pubscoop.PublishInput{
		Bucket:   expected.Bucket,
		Source:   expected.Source,
		Version:  expected.Version,
		Commit:   expected.Commit,
		Manifest: expected.Manifest,
		Content:  content,
	}, reader, writer)
	if err != nil {
		return writeCommandResult(options, commandScoop, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandScoop, result, nil)
}

// resolveScoop parses flags and Actions environment without performing I/O.
func resolveScoop(cmd *cobra.Command, options Options) (scoopConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return scoopConfig{}, err
	}
	if settings.Dist == "" {
		return scoopConfig{}, fmt.Errorf("--%s is required", flagDist)
	}

	bucketRaw, err := requiredCommandFlag(cmd, flagBucket)
	if err != nil {
		return scoopConfig{}, err
	}
	bucket, err := pubscoop.ParseRepository(bucketRaw)
	if err != nil {
		return scoopConfig{}, fmt.Errorf("--%s: %w", flagBucket, err)
	}
	manifestRaw, err := requiredCommandFlag(cmd, flagManifest)
	if err != nil {
		return scoopConfig{}, err
	}
	manifest, err := pubscoop.ParseManifestName(manifestRaw)
	if err != nil {
		return scoopConfig{}, fmt.Errorf("--%s: %w", flagManifest, err)
	}

	sourceRaw, err := requiredEnv(options.LookupEnv, envRepository)
	if err != nil {
		return scoopConfig{}, err
	}
	source, err := pubscoop.ParseRepository(sourceRaw)
	if err != nil {
		return scoopConfig{}, fmt.Errorf("%s: %w", envRepository, err)
	}
	versionRaw, err := deriveVersion(options.LookupEnv)
	if err != nil {
		return scoopConfig{}, err
	}
	version, err := rel.ParseVersion(versionRaw)
	if err != nil {
		return scoopConfig{}, fmt.Errorf("%s: %w", envRefName, err)
	}
	commitRaw, err := requiredEnv(options.LookupEnv, envCommitSHA)
	if err != nil {
		return scoopConfig{}, err
	}
	commit, err := pubgh.ParseCommitSHA(commitRaw)
	if err != nil {
		return scoopConfig{}, err
	}
	tokenRaw, err := requiredEnv(options.LookupEnv, envAppToken)
	if err != nil {
		return scoopConfig{}, err
	}
	endpoint, err := resolveGitHubEndpoint(options.LookupEnv)
	if err != nil {
		return scoopConfig{}, err
	}

	return scoopConfig{
		Dist:     settings.Dist,
		Bucket:   bucket,
		Source:   source,
		Version:  version,
		Commit:   pubscoop.CommitSHA(commit.String()),
		Manifest: manifest,
		Token:    rel.NewSecret(tokenRaw),
		Endpoint: endpoint,
	}, nil
}

// readGeneratedManifest reads exactly scoop/<name>.json through an [os.Root].
//
//nolint:dupl // Channel-specific paths and diagnostics stay explicit.
func readGeneratedManifest(dist string, name pubscoop.ManifestName) ([]byte, error) {
	root, err := os.OpenRoot(dist)
	if err != nil {
		return nil, fmt.Errorf("open dist %s: %w", dist, err)
	}
	defer root.Close()

	path := generatedManifestDirectory + name.Path().String()
	file, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open generated manifest %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat generated manifest %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("generated manifest %s is not a regular file", path)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("generated manifest %s is empty", path)
	}
	if info.Size() > maxGeneratedManifestBytes {
		return nil, fmt.Errorf("generated manifest %s exceeds %d bytes", path, maxGeneratedManifestBytes)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxGeneratedManifestReadBytes))
	if err != nil {
		return nil, fmt.Errorf("read generated manifest %s: %w", path, err)
	}
	if int64(len(content)) > maxGeneratedManifestBytes {
		return nil, fmt.Errorf("generated manifest %s exceeds %d bytes", path, maxGeneratedManifestBytes)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("generated manifest %s is empty", path)
	}

	return content, nil
}

// bucketReader returns the injected read port or constructs one.
func bucketReader(options Options, expected scoopConfig) (pubscoop.RepositoryReader, error) {
	if options.BucketReader != nil {
		return options.BucketReader, nil
	}
	if options.NewBucketReader == nil {
		return nil, errors.New("bucket repository reader is not configured")
	}
	reader, err := options.NewBucketReader(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("construct bucket repository reader: %w", err)
	}
	if reader == nil {
		return nil, errors.New("bucket repository reader is nil")
	}

	return reader, nil
}

// bucketWriter returns the injected write port or constructs one.
func bucketWriter(options Options, expected scoopConfig) (pubscoop.RepositoryWriter, error) {
	if options.BucketWriter != nil {
		return options.BucketWriter, nil
	}
	if options.NewBucketWriter == nil {
		return nil, errors.New("bucket repository writer is not configured")
	}
	writer, err := options.NewBucketWriter(expected.Token, expected.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("construct bucket repository writer: %w", err)
	}
	if writer == nil {
		return nil, errors.New("bucket repository writer is nil")
	}

	return writer, nil
}
