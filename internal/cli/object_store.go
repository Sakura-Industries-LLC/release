package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sakura-Industries-LLC/release/internal/adapter/r2"
	"github.com/Sakura-Industries-LLC/release/internal/rel"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pubobj"
)

const (
	// commandObjectStore is the envelope command path for publish object-store.
	commandObjectStore = "publish object-store"
	// flagObjectStoreProject is the first object-key segment flag.
	flagObjectStoreProject = "project"
	// flagObjectStoreEndpoint is the S3-compatible origin flag.
	flagObjectStoreEndpoint = "endpoint"
	// flagObjectStoreBucket is the destination bucket flag.
	flagObjectStoreBucket = "bucket"
	// flagObjectStoreRegion is the S3 signing region flag.
	flagObjectStoreRegion = "region"
	// envObjectStoreProject is the first object-key segment variable.
	envObjectStoreProject = "RELEASE_OBJECT_STORE_PROJECT"
	// envObjectStoreEndpoint is the S3-compatible origin variable.
	envObjectStoreEndpoint = "RELEASE_OBJECT_STORE_ENDPOINT"
	// envObjectStoreBucket is the destination bucket variable.
	envObjectStoreBucket = "RELEASE_OBJECT_STORE_BUCKET"
	// envObjectStoreRegion is the S3 signing region variable.
	envObjectStoreRegion = "RELEASE_OBJECT_STORE_REGION"
	// envObjectStoreAccessKeyID is the object-store access-key variable.
	//
	// The value is a variable name, not a credential.
	envObjectStoreAccessKeyID = "OBJECT_STORE_ACCESS_KEY_ID"
	// envObjectStoreSecretAccessKey is the object-store secret-key variable.
	//
	// The value is a variable name, not a credential.
	envObjectStoreSecretAccessKey = "OBJECT_STORE_SECRET_ACCESS_KEY"
)

// newObjectStoreCommand constructs the publish object-store verb.
func newObjectStoreCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "object-store",
		Short: "Publish a verified closed release bundle to a private object store",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runObjectStore(cmd, options)
		},
	}
	cmd.Flags().String(flagDist, "", "path to the distribution directory")
	cmd.Flags().String(flagObjectStoreProject, "", "object-store project name")
	cmd.Flags().String(flagObjectStoreEndpoint, "", "S3-compatible HTTP(S) origin")
	cmd.Flags().String(flagObjectStoreBucket, "", "private S3-compatible bucket")
	cmd.Flags().String(flagObjectStoreRegion, "", "S3 signing region")

	return cmd
}

// runObjectStore validates configuration and publishes a closed bundle.
//
// Missing or malformed configuration is [ErrUsage] and is raised before any
// port is constructed. Opening the distribution directory, rebuilding the
// closed set, and publication failures are command failures. Success without
// --json writes nothing. The --json envelope result is the
// [pubobj.PublishResult] itself.
func runObjectStore(cmd *cobra.Command, options Options) error {
	expected, err := resolveObjectStore(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandObjectStore, nil, UsageError(err))
	}

	root, err := os.OpenRoot(expected.Dist)
	if err != nil {
		return writeCommandResult(options, commandObjectStore, nil, fmt.Errorf("open dist %s: %w", expected.Dist, err))
	}
	defer root.Close()

	bundle, err := buildExpectedBundle(root)
	if err != nil {
		return writeCommandResult(options, commandObjectStore, nil, err)
	}
	assets, err := expectedAssetPaths(expected.Dist, bundle)
	if err != nil {
		return writeCommandResult(options, commandObjectStore, nil, err)
	}

	store, err := objectStore(cmd.Context(), options, expected)
	if err != nil {
		return writeCommandResult(options, commandObjectStore, nil, err)
	}
	result, err := pubobj.Publish(cmd.Context(), pubobj.PublishInput{
		Project:  expected.Project,
		Tag:      expected.Tag,
		Expected: bundle,
		Assets:   assets,
	}, store)
	if err != nil {
		return writeCommandResult(options, commandObjectStore, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandObjectStore, result, nil)
}

// objectStoreConfig is the resolved publish-object-store configuration.
type objectStoreConfig struct {
	// Dist is the distribution directory to open.
	Dist string
	// Project is the first object-key segment.
	Project pubobj.Project
	// Tag is the git tag from GITHUB_REF_NAME.
	Tag rel.GitTag
	// Endpoint is the S3-compatible HTTP(S) origin.
	Endpoint string
	// Bucket is the destination bucket name.
	Bucket string
	// Region is the S3 signing region.
	Region string
	// AccessKeyID authenticates object storage. It is never logged.
	AccessKeyID rel.Secret
	// SecretAccessKey authenticates object storage. It is never logged.
	SecretAccessKey rel.Secret
}

// resolveObjectStore parses flags and environment into a publish config.
//
// It performs no I/O.
func resolveObjectStore(cmd *cobra.Command, options Options) (objectStoreConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return objectStoreConfig{}, err
	}
	if settings.Dist == "" {
		return objectStoreConfig{}, fmt.Errorf("--%s is required", flagDist)
	}

	projectRaw, err := requiredPackageValue(cmd, flagObjectStoreProject, envObjectStoreProject, options.LookupEnv)
	if err != nil {
		return objectStoreConfig{}, err
	}
	project, err := pubobj.ParseProject(projectRaw)
	if err != nil {
		return objectStoreConfig{}, err
	}

	refRaw, err := requiredEnv(options.LookupEnv, envRefName)
	if err != nil {
		return objectStoreConfig{}, err
	}
	tag, err := rel.ParseGitTag(refRaw)
	if err != nil {
		return objectStoreConfig{}, err
	}
	if _, err = tag.Version(); err != nil {
		return objectStoreConfig{}, fmt.Errorf("%s: %w", envRefName, err)
	}

	endpoint, err := requiredPackageValue(cmd, flagObjectStoreEndpoint, envObjectStoreEndpoint, options.LookupEnv)
	if err != nil {
		return objectStoreConfig{}, err
	}
	if err = validateObjectStoreEndpoint(endpoint); err != nil {
		return objectStoreConfig{}, err
	}
	bucket, err := requiredPackageValue(cmd, flagObjectStoreBucket, envObjectStoreBucket, options.LookupEnv)
	if err != nil {
		return objectStoreConfig{}, err
	}
	region, err := requiredPackageValue(cmd, flagObjectStoreRegion, envObjectStoreRegion, options.LookupEnv)
	if err != nil {
		return objectStoreConfig{}, err
	}
	accessKeyID, err := requiredEnv(options.LookupEnv, envObjectStoreAccessKeyID)
	if err != nil {
		return objectStoreConfig{}, err
	}
	secretAccessKey, err := requiredEnv(options.LookupEnv, envObjectStoreSecretAccessKey)
	if err != nil {
		return objectStoreConfig{}, err
	}

	return objectStoreConfig{
		Dist:            settings.Dist,
		Project:         project,
		Tag:             tag,
		Endpoint:        endpoint,
		Bucket:          bucket,
		Region:          region,
		AccessKeyID:     rel.NewSecret(accessKeyID),
		SecretAccessKey: rel.NewSecret(secretAccessKey),
	}, nil
}

// objectStore returns the injected port or constructs one.
func objectStore(ctx context.Context, options Options, expected objectStoreConfig) (pubobj.ObjectStore, error) {
	if options.ObjectStore != nil {
		return options.ObjectStore, nil
	}
	store, err := r2.New(ctx, r2.Options{
		Endpoint: expected.Endpoint,
		Bucket:   expected.Bucket,
		Region:   expected.Region,
		Credentials: r2.Credentials{
			AccessKeyID:     expected.AccessKeyID,
			SecretAccessKey: expected.SecretAccessKey,
		},
	})
	if err != nil {
		return nil, err
	}

	return store, nil
}

// validateObjectStoreEndpoint requires an HTTP(S) origin without a path or credentials.
func validateObjectStoreEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse object store endpoint: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("object store endpoint %q must be an HTTP(S) origin without credentials", endpoint)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("object store endpoint %q must not include a path", endpoint)
	}

	return nil
}
