package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/adapter/cosign"
	"github.com/meigma/release/internal/adapter/ghattest"
	"github.com/meigma/release/internal/adapter/ghrel"
	"github.com/meigma/release/internal/adapter/gpg"
	"github.com/meigma/release/internal/adapter/pkginstall"
	"github.com/meigma/release/internal/adapter/pkgmeta"
	"github.com/meigma/release/internal/adapter/pkgverify"
	"github.com/meigma/release/internal/adapter/r2"
	"github.com/meigma/release/internal/adapter/repogen"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// commandPackageRepository is the result envelope command path.
	commandPackageRepository = "publish package-repository"
	// flagPackageRepository is the producer GitHub owner/name flag.
	flagPackageRepository = "repository"
	// flagPackageTag is the exact producer release tag flag.
	flagPackageTag = "tag"
	// flagPackageConfig is the reviewed package publication YAML flag.
	flagPackageConfig = "config"
	// flagPackageKeys is the reviewed public-key source root flag.
	flagPackageKeys = "keys"
	// flagCloudflareAccount is the Cloudflare account ID flag.
	flagCloudflareAccount = "cloudflare-account-id"
	// flagR2Bucket is the existing package repository bucket flag.
	flagR2Bucket = "r2-bucket"
	// flagGPGHome is the aggregate metadata key home flag.
	flagGPGHome = "gpg-home"
	// flagGPGKeyID is the aggregate metadata key ID flag.
	flagGPGKeyID = "gpg-key-id"
	// flagGPGPassphraseFile is the owner-only aggregate key passphrase file flag.
	flagGPGPassphraseFile = "gpg-passphrase-file" //nolint:gosec // Flag name, not a credential.
	// flagAPKSigningKey is the aggregate APK index private key flag.
	flagAPKSigningKey = "apk-signing-key"

	// envPackageRepository is the producer GitHub owner/name variable.
	envPackageRepository = "RELEASE_REPOSITORY"
	// envPackageTag is the producer release tag variable.
	envPackageTag = "RELEASE_TAG"
	// envPackageConfig is the reviewed package publication YAML variable.
	envPackageConfig = "RELEASE_PACKAGE_REPOSITORY_CONFIG"
	// envPackageKeys is the reviewed public-key source root variable.
	envPackageKeys = "RELEASE_PACKAGE_KEYS"
	// envCloudflareAccount is the Cloudflare account ID variable.
	envCloudflareAccount = "CLOUDFLARE_ACCOUNT_ID"
	// envR2Bucket is the existing R2 bucket variable.
	envR2Bucket = "RELEASE_R2_BUCKET"
	// envR2AccessKeyID is the R2 S3 access-key variable.
	//
	// The value is a variable name, not a credential.
	envR2AccessKeyID = "R2_ACCESS_KEY_ID"
	// envR2SecretAccessKey is the R2 S3 secret-key variable.
	//
	// The value is a variable name, not a credential.
	envR2SecretAccessKey = "R2_SECRET_ACCESS_KEY"
	// envGPGHome is the aggregate metadata key home variable.
	envGPGHome = "RELEASE_GPG_HOME"
	// envGPGKeyID is the aggregate metadata key ID variable.
	envGPGKeyID = "RELEASE_GPG_KEY_ID"
	// envGPGPassphraseFile is the aggregate key passphrase file variable.
	envGPGPassphraseFile = "RELEASE_GPG_PASSPHRASE_FILE"
	// envAPKSigningKey is the aggregate APK private key variable.
	envAPKSigningKey = "RELEASE_APK_SIGNING_KEY"
	// envDockerPath is the optional Docker executable variable.
	envDockerPath = "RELEASE_DOCKER_PATH"
	// envGPGPath is the optional GnuPG executable variable.
	envGPGPath = "RELEASE_GPG_PATH"
)

// PackageRepositoryPublisher is the complete package repository publication seam.
type PackageRepositoryPublisher interface {
	// Publish verifies one producer release and converges the configured repository.
	Publish(ctx context.Context, input pkgrepo.PublishInput) (pkgrepo.PublishResult, error)
}

// packageRepositoryConfig is the resolved CLI and environment configuration.
type packageRepositoryConfig struct {
	// repository is the requested producer repository.
	repository pkgrepo.Repository
	// tag is the exact producer release tag.
	tag string
	// configPath is the reviewed publication YAML path.
	configPath string
	// keysPath is the reviewed public-key source root.
	keysPath string
	// cloudflareAccount is the account containing the existing R2 bucket.
	cloudflareAccount string
	// r2Bucket is the existing object namespace.
	r2Bucket string
	// r2AccessKeyID authenticates object storage.
	r2AccessKeyID rel.Secret
	// r2SecretAccessKey authenticates object storage.
	r2SecretAccessKey rel.Secret
	// githubToken authenticates release and attestation reads.
	githubToken rel.Secret
	// githubAPIURL is the optional GitHub Enterprise API base.
	githubAPIURL string
	// githubServerURL is the optional GitHub Enterprise server base.
	githubServerURL string
	// ghPath is the optional GitHub CLI executable.
	ghPath string
	// dockerPath is the optional Docker executable.
	dockerPath string
	// cosignPath is the optional Cosign executable.
	cosignPath string
	// gpgPath is the optional GnuPG executable.
	gpgPath string
	// gpgHome contains the aggregate metadata private key.
	gpgHome string
	// gpgKeyID selects the aggregate metadata private key.
	gpgKeyID string
	// gpgPassphraseFile contains the aggregate key passphrase.
	gpgPassphraseFile string
	// apkSigningKey is the aggregate APK index private key.
	apkSigningKey string
}

// scratchRoots owns the confined roots for one publication invocation.
type scratchRoots struct {
	// parent is removed after the command completes.
	parent string
	// source receives verified release assets and mirrored packages.
	source *os.Root
	// work receives intermediate verification and generation files.
	work *os.Root
	// output receives the complete generated repository tree.
	output *os.Root
}

// newPackageRepositoryCommand constructs the package repository publication verb.
func newPackageRepositoryCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package-repository",
		Short: "Publish verified native packages to the static repository",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPackageRepository(cmd, options)
		},
	}
	cmd.Flags().String(flagPackageRepository, "", "producer repository as owner/name")
	cmd.Flags().String(flagPackageTag, "", "exact producer release tag")
	cmd.Flags().String(flagPackageConfig, "", "reviewed package repository YAML")
	cmd.Flags().String(flagPackageKeys, "", "reviewed public-key source directory")
	cmd.Flags().String(flagCloudflareAccount, "", "Cloudflare account ID")
	cmd.Flags().String(flagR2Bucket, "", "existing R2 package repository bucket")
	cmd.Flags().String(flagGPGHome, "", "GnuPG home containing the aggregate metadata key")
	cmd.Flags().String(flagGPGKeyID, "", "aggregate metadata signing key ID")
	cmd.Flags().String(flagGPGPassphraseFile, "", "owner-only aggregate key passphrase file")
	cmd.Flags().String(flagAPKSigningKey, "", "aggregate APK index private key")

	return cmd
}

// runPackageRepository validates configuration and publishes one producer release.
func runPackageRepository(cmd *cobra.Command, options Options) error {
	resolved, err := resolvePackageRepository(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandPackageRepository, nil, UsageError(err))
	}
	publication, err := readPublicationConfig(resolved.configPath)
	if err != nil {
		return writeCommandResult(options, commandPackageRepository, nil, UsageError(err))
	}
	keys, err := os.OpenRoot(resolved.keysPath)
	if err != nil {
		return writeCommandResult(options, commandPackageRepository, nil, fmt.Errorf("open package keys: %w", err))
	}
	defer keys.Close()
	scratch, err := newScratchRoots()
	if err != nil {
		return writeCommandResult(options, commandPackageRepository, nil, err)
	}
	defer scratch.Close()

	publisher, err := packageRepositoryPublisher(cmd.Context(), cmd, options, resolved, scratch)
	if err != nil {
		return writeCommandResult(options, commandPackageRepository, nil, err)
	}
	result, err := publisher.Publish(cmd.Context(), pkgrepo.PublishInput{
		Config: publication,
		Request: pkgrepo.Request{
			Repository: resolved.repository,
			Tag:        resolved.tag,
		},
		Keys:   keys,
		Source: scratch.source,
		Work:   scratch.work,
		Output: scratch.output,
	})
	if err != nil {
		return writeCommandResult(options, commandPackageRepository, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandPackageRepository, result, nil)
}

// resolvePackageRepository parses flags and environment without performing I/O.
func resolvePackageRepository(cmd *cobra.Command, options Options) (packageRepositoryConfig, error) {
	repositoryRaw, err := requiredPackageValue(cmd, flagPackageRepository, envPackageRepository, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	repository, err := pkgrepo.ParseRepository(repositoryRaw)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	tag, err := requiredPackageTag(cmd, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	configPath, err := requiredPackageValue(cmd, flagPackageConfig, envPackageConfig, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	keysPath, err := requiredPackageValue(cmd, flagPackageKeys, envPackageKeys, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}

	resolved := packageRepositoryConfig{
		repository:      repository,
		tag:             tag,
		configPath:      configPath,
		keysPath:        keysPath,
		githubAPIURL:    lookupValue(options.LookupEnv, envAPIURL),
		githubServerURL: lookupValue(options.LookupEnv, envServerURL),
		ghPath:          lookupValue(options.LookupEnv, envGHPath),
		dockerPath:      lookupValue(options.LookupEnv, envDockerPath),
		cosignPath:      lookupValue(options.LookupEnv, envCosignPath),
		gpgPath:         lookupValue(options.LookupEnv, envGPGPath),
	}
	if options.PackageRepositoryPublisher != nil {
		return resolved, nil
	}
	resolved.cloudflareAccount, err = requiredPackageValue(
		cmd,
		flagCloudflareAccount,
		envCloudflareAccount,
		options.LookupEnv,
	)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	resolved.r2Bucket, err = requiredPackageValue(cmd, flagR2Bucket, envR2Bucket, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	resolved.gpgHome, err = requiredPackageValue(cmd, flagGPGHome, envGPGHome, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	resolved.gpgKeyID, err = requiredPackageValue(cmd, flagGPGKeyID, envGPGKeyID, options.LookupEnv)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	resolved.gpgPassphraseFile, err = requiredPackageValue(
		cmd,
		flagGPGPassphraseFile,
		envGPGPassphraseFile,
		options.LookupEnv,
	)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	resolved.apkSigningKey, err = requiredPackageValue(
		cmd,
		flagAPKSigningKey,
		envAPKSigningKey,
		options.LookupEnv,
	)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	r2AccessKeyID, err := requiredEnv(options.LookupEnv, envR2AccessKeyID)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	r2SecretAccessKey, err := requiredEnv(options.LookupEnv, envR2SecretAccessKey)
	if err != nil {
		return packageRepositoryConfig{}, err
	}
	githubToken := resolveToken(options.LookupEnv)
	if githubToken == "" {
		return packageRepositoryConfig{}, fmt.Errorf("%s or %s is required", envGitHubToken, envGHToken)
	}
	resolved.r2AccessKeyID = rel.NewSecret(r2AccessKeyID)
	resolved.r2SecretAccessKey = rel.NewSecret(r2SecretAccessKey)
	resolved.githubToken = rel.NewSecret(githubToken)

	return resolved, nil
}

// requiredPackageTag resolves and validates the exact producer release tag.
func requiredPackageTag(cmd *cobra.Command, lookup LookupEnv) (string, error) {
	tag, err := requiredPackageValue(cmd, flagPackageTag, envPackageTag, lookup)
	if err != nil {
		return "", err
	}
	if _, err = pkgrepo.ParseTag(tag); err != nil {
		return "", err
	}

	return tag, nil
}

// requiredPackageValue applies flag-over-environment precedence and rejects blanks.
func requiredPackageValue(
	cmd *cobra.Command,
	flagName string,
	environmentName string,
	lookup LookupEnv,
) (string, error) {
	value := strings.TrimSpace(resolveString(cmd, flagName, environmentName, lookup))
	if value == "" {
		return "", fmt.Errorf("--%s or %s is required", flagName, environmentName)
	}

	return value, nil
}

// readPublicationConfig loads and validates one bounded reviewed YAML document.
func readPublicationConfig(name string) (pkgrepo.PublicationConfig, error) {
	file, err := os.Open(name)
	if err != nil {
		return pkgrepo.PublicationConfig{}, fmt.Errorf("open package repository config: %w", err)
	}
	publication, parseErr := pkgrepo.ParsePublicationConfig(file)
	closeErr := file.Close()
	if parseErr != nil {
		return pkgrepo.PublicationConfig{}, errors.Join(
			fmt.Errorf("parse package repository config: %w", parseErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return pkgrepo.PublicationConfig{}, fmt.Errorf("close package repository config: %w", closeErr)
	}

	return publication, nil
}

// newScratchRoots creates the three isolated roots required by the publication engine.
func newScratchRoots() (scratchRoots, error) {
	parent, err := os.MkdirTemp("", "release-package-repository-")
	if err != nil {
		return scratchRoots{}, fmt.Errorf("create package repository scratch: %w", err)
	}
	roots := scratchRoots{parent: parent}
	for _, name := range []string{"source", "work", "output"} {
		if mkdirErr := os.Mkdir(filepath.Join(parent, name), 0o750); mkdirErr != nil {
			_ = roots.Close()
			return scratchRoots{}, fmt.Errorf("create package repository %s root: %w", name, mkdirErr)
		}
	}
	roots.source, err = os.OpenRoot(filepath.Join(parent, "source"))
	if err != nil {
		_ = roots.Close()
		return scratchRoots{}, fmt.Errorf("open package source root: %w", err)
	}
	roots.work, err = os.OpenRoot(filepath.Join(parent, "work"))
	if err != nil {
		_ = roots.Close()
		return scratchRoots{}, fmt.Errorf("open package work root: %w", err)
	}
	roots.output, err = os.OpenRoot(filepath.Join(parent, "output"))
	if err != nil {
		_ = roots.Close()
		return scratchRoots{}, fmt.Errorf("open package output root: %w", err)
	}

	return roots, nil
}

// Close closes every opened root and removes the scratch parent.
func (roots *scratchRoots) Close() error {
	if roots == nil {
		return nil
	}
	var errs []error
	for _, root := range []*os.Root{roots.source, roots.work, roots.output} {
		if root != nil {
			errs = append(errs, root.Close())
		}
	}
	if roots.parent != "" {
		errs = append(errs, os.RemoveAll(roots.parent))
	}

	return errors.Join(errs...)
}

// packageRepositoryPublisher returns the injected seam or constructs every production adapter.
func packageRepositoryPublisher(
	ctx context.Context,
	cmd *cobra.Command,
	options Options,
	resolved packageRepositoryConfig,
	scratch scratchRoots,
) (PackageRepositoryPublisher, error) {
	if options.PackageRepositoryPublisher != nil {
		return options.PackageRepositoryPublisher, nil
	}
	releases, err := ghrel.NewAuthenticated(
		resolved.githubToken,
		resolved.githubAPIURL,
		resolved.githubServerURL,
	)
	if err != nil {
		return nil, err
	}
	store, err := r2.New(ctx, r2.Options{
		AccountID: resolved.cloudflareAccount,
		Bucket:    resolved.r2Bucket,
		Credentials: r2.Credentials{
			AccessKeyID:     resolved.r2AccessKeyID,
			SecretAccessKey: resolved.r2SecretAccessKey,
		},
	})
	if err != nil {
		return nil, err
	}
	stderr := cmd.ErrOrStderr()
	dockerOptions := pkgmeta.Options{DockerPath: resolved.dockerPath, Stderr: stderr}

	return pkgrepo.NewPublisher(pkgrepo.PublisherOptions{
		Releases: releases,
		Bundles: cosign.NewVerifier(cosign.VerifierOptions{
			Path:   resolved.cosignPath,
			Dir:    filepath.Join(scratch.source.Name(), "release"),
			Stderr: stderr,
		}),
		Attestations: ghattest.New(ghattest.Options{
			Path:   resolved.ghPath,
			Token:  resolved.githubToken,
			Stderr: stderr,
		}),
		Store:     store,
		Inspector: pkgmeta.New(dockerOptions),
		Verifier: pkgverify.New(pkgverify.Options{
			DockerPath: resolved.dockerPath,
			Stderr:     stderr,
		}),
		Generator: repogen.New(repogen.Options{
			DockerPath:    resolved.dockerPath,
			APKSigningKey: resolved.apkSigningKey,
			Stderr:        stderr,
		}),
		Signer: gpg.New(gpg.Options{
			Path:           resolved.gpgPath,
			Home:           resolved.gpgHome,
			KeyID:          resolved.gpgKeyID,
			PassphraseFile: resolved.gpgPassphraseFile,
			Stderr:         stderr,
		}),
		Installer: pkginstall.New(pkginstall.Options{
			DockerPath: resolved.dockerPath,
			Stderr:     stderr,
		}),
	}), nil
}
