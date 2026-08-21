package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/profile/goprof"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/image"
	"github.com/meigma/release/internal/stage/pubbrew"
	"github.com/meigma/release/internal/stage/pubgh"
	"github.com/meigma/release/internal/stage/puboci"
	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// commandName is the process name shown in help and version text.
	commandName = "release-cli"
	// envProfile is the environment variable for --profile.
	envProfile = "RELEASE_PROFILE"
	// envDist is the environment variable for --dist.
	envDist = "RELEASE_DIST"
	// envGoreleaserPath is the GoReleaser binary path override.
	envGoreleaserPath = "RELEASE_GORELEASER_PATH"
	// envJSON is the environment variable for --json.
	envJSON = "RELEASE_JSON"
	// envImage is the environment variable for --image.
	envImage = "RELEASE_IMAGE"
	// envVersion is the environment variable for --version.
	envVersion = "RELEASE_VERSION"
	// envLayout is the environment variable for --layout.
	envLayout = "RELEASE_LAYOUT"
	// envDryRun is the environment variable for --dry-run.
	envDryRun = "RELEASE_DRY_RUN"
	// envIdentity is the environment variable for --identity.
	envIdentity = "RELEASE_IDENTITY"
	// envIssuer is the environment variable for --issuer.
	envIssuer = "RELEASE_ISSUER"
	// envInput is the environment variable for --input.
	envInput = "RELEASE_INPUT"
	// envWork is the environment variable for --work.
	envWork = "RELEASE_WORK"
	// envOutput is the environment variable for --output.
	envOutput = "RELEASE_OUTPUT"
	// envMelangeConfig is the environment variable for --melange-config.
	envMelangeConfig = "RELEASE_MELANGE_CONFIG"
	// envApkoConfig is the environment variable for --apko-config.
	envApkoConfig = "RELEASE_APKO_CONFIG"
	// envBuildDate is the environment variable for --build-date.
	envBuildDate = "RELEASE_BUILD_DATE"
	// envBinary is the environment variable for --binary.
	envBinary = "RELEASE_BINARY"
	// envRepositoryOwner is the Actions repository owner used as the APK namespace.
	envRepositoryOwner = "GITHUB_REPOSITORY_OWNER"
)

// LookupEnv looks up an environment variable.
//
// A nil LookupEnv uses [os.LookupEnv]. Tests inject a function to avoid
// process-global coupling when a future config reader is added.
type LookupEnv func(key string) (string, bool)

// Settings is the resolved flag and environment configuration for one invocation.
//
// Flags win over environment variables. There is no config file. A zero
// Settings means nothing was resolved yet.
type Settings struct {
	// Profile is the selected --profile / RELEASE_PROFILE value.
	Profile string
	// Dist is the selected --dist / RELEASE_DIST path.
	Dist string
	// Identity is the selected --identity / RELEASE_IDENTITY URL.
	Identity string
	// Issuer is the selected --issuer / RELEASE_ISSUER URL.
	Issuer string
	// ArtifactID is the selected --artifact-id / RELEASE_ARTIFACT_ID value.
	ArtifactID string
	// Image is the selected --image / RELEASE_IMAGE value.
	Image string
	// Version is the selected --version / RELEASE_VERSION value.
	Version string
	// Digest is the selected --digest / RELEASE_DIGEST value.
	Digest string
	// Layout is the selected --layout / RELEASE_LAYOUT path.
	Layout string
	// Input is the selected --input / RELEASE_INPUT path.
	Input string
	// Work is the selected --work / RELEASE_WORK path.
	Work string
	// Output is the selected --output / RELEASE_OUTPUT path.
	Output string
	// MelangeConfig is the selected --melange-config / RELEASE_MELANGE_CONFIG path.
	MelangeConfig string
	// ApkoConfig is the selected --apko-config / RELEASE_APKO_CONFIG path.
	ApkoConfig string
	// BuildDate is the selected --build-date / RELEASE_BUILD_DATE value.
	BuildDate string
	// Binary is the selected --binary / RELEASE_BINARY value.
	Binary string
	// DryRun reports whether --dry-run / RELEASE_DRY_RUN requested a dry run.
	DryRun bool
	// PlainHTTP reports whether --plain-http requested HTTP.
	PlainHTTP bool
	// NoUndraft reports whether --no-undraft requested a draft-only publish.
	NoUndraft bool

	// JSON reports whether --json / RELEASE_JSON requested structured output.
	JSON bool
	// err is a flag or environment parse failure discovered while resolving settings.
	err error
}

// BuildInfo describes linker-injected build metadata.
type BuildInfo struct {
	// Version is the release version.
	Version string
	// Commit is the source commit used to build the binary.
	Commit string
	// Protocol is the workflow/binary contract integer.
	Protocol int
}

// RegistryConfig is the resolved registry client configuration.
type RegistryConfig struct {
	// Credentials authenticates registry reads and writes. An empty password is anonymous.
	Credentials RegistryCredentials
	// PlainHTTP forces HTTP instead of HTTPS. Tests use this against a local registry.
	PlainHTTP bool
}

// Options customizes root command construction.
type Options struct {
	// In receives command input.
	In io.Reader
	// Out receives machine-readable command output.
	Out io.Writer
	// Err receives diagnostics and human-readable status.
	Err io.Writer
	// LookupEnv resolves RELEASE_* and Actions environment variables. Nil selects [os.LookupEnv].
	LookupEnv LookupEnv
	// Build controls version output.
	Build BuildInfo
	// ArtifactMeta, when set, is the handoff metadata port. Tests inject it.
	ArtifactMeta pubgh.ArtifactMeta
	// NewArtifactMeta constructs the metadata port from a token and API endpoint.
	NewArtifactMeta func(token string, endpoint GitHubEndpoint) (pubgh.ArtifactMeta, error)
	// StateReader, when set, is the registry read port. Tests inject it.
	StateReader puboci.StateReader
	// NewStateReader constructs the registry read port from resolved registry config.
	NewStateReader func(config RegistryConfig) (puboci.StateReader, error)
	// ContentPusher, when set, is the registry write port. Tests inject it.
	ContentPusher puboci.ContentPusher
	// NewContentPusher constructs the registry write port from resolved registry config.
	NewContentPusher func(config RegistryConfig) (puboci.ContentPusher, error)
	// Signer, when set, is the Cosign signing port. Tests inject it.
	Signer puboci.Signer
	// NewSigner constructs the Cosign signing port from a binary path.
	//
	// An empty path resolves cosign from PATH.
	NewSigner func(path string) (puboci.Signer, error)
	// TagCommitter, when set, is the registry tag-write port. Tests inject it.
	TagCommitter puboci.TagCommitter
	// NewTagCommitter constructs the registry tag-write port from resolved registry config.
	NewTagCommitter func(config RegistryConfig) (puboci.TagCommitter, error)
	// BlobVerifier, when set, is the detached-bundle verification port. Tests inject it.
	BlobVerifier pubgh.BlobVerifier
	// NewBlobVerifier constructs the detached-bundle verification port from a
	// Cosign binary path and a distribution directory.
	//
	// An empty path resolves cosign from PATH.
	NewBlobVerifier func(path, dir string) (pubgh.BlobVerifier, error)
	// ReleaseReader, when set, is the GitHub release read port. Tests inject it.
	ReleaseReader pubgh.ReleaseReader
	// NewReleaseReader constructs the GitHub release read port from a token
	// and API endpoint.
	NewReleaseReader func(token rel.Secret, endpoint GitHubEndpoint) (pubgh.ReleaseReader, error)
	// AssetReplacer, when set, is the GitHub release upload port. Tests inject it.
	AssetReplacer pubgh.AssetReplacer
	// NewAssetReplacer constructs the GitHub release upload port from a token,
	// a gh binary path, and a working directory.
	//
	// An empty path resolves gh from PATH.
	NewAssetReplacer func(token rel.Secret, path, dir string) (pubgh.AssetReplacer, error)
	// Publisher, when set, is the GitHub release undraft port. Tests inject it.
	Publisher pubgh.Publisher
	// NewPublisher constructs the GitHub release undraft port from a token
	// and API endpoint.
	NewPublisher func(token rel.Secret, endpoint GitHubEndpoint) (pubgh.Publisher, error)
	// RefResolver, when set, is the local tag-to-SHA port. Tests inject it.
	RefResolver pubgh.RefResolver
	// NewRefResolver constructs the local tag-to-SHA port from a git binary
	// path and a working directory.
	//
	// An empty path resolves git from PATH. An empty directory inherits
	// the process working directory.
	NewRefResolver func(path, dir string) (pubgh.RefResolver, error)
	// TapReader, when set, is the Homebrew tap read port. Tests inject it.
	TapReader pubbrew.RepositoryReader
	// NewTapReader constructs the tap read port from a token and API endpoint.
	NewTapReader func(token rel.Secret, endpoint GitHubEndpoint) (pubbrew.RepositoryReader, error)
	// TapWriter, when set, is the Homebrew tap mutation port. Tests inject it.
	TapWriter pubbrew.RepositoryWriter
	// NewTapWriter constructs the tap mutation port from a token and API endpoint.
	NewTapWriter func(token rel.Secret, endpoint GitHubEndpoint) (pubbrew.RepositoryWriter, error)
	// BucketReader, when set, is the Scoop bucket read port. Tests inject it.
	BucketReader pubscoop.RepositoryReader
	// NewBucketReader constructs the bucket read port from a token and API endpoint.
	NewBucketReader func(token rel.Secret, endpoint GitHubEndpoint) (pubscoop.RepositoryReader, error)
	// BucketWriter, when set, is the Scoop bucket mutation port. Tests inject it.
	BucketWriter pubscoop.RepositoryWriter
	// NewBucketWriter constructs the bucket mutation port from a token and API endpoint.
	NewBucketWriter func(token rel.Secret, endpoint GitHubEndpoint) (pubscoop.RepositoryWriter, error)
	// APKBuilder, when set, is the Melange APK-build port. Tests inject it.
	APKBuilder image.APKBuilder
	// NewAPKBuilder constructs the Melange APK-build port from a binary path.
	//
	// An empty path resolves melange from PATH.
	NewAPKBuilder func(path string) (image.APKBuilder, error)
	// Composer, when set, is the apko compose port. Tests inject it.
	Composer image.Composer
	// NewComposer constructs the apko compose port from a binary path.
	//
	// An empty path resolves apko from PATH.
	NewComposer func(path string) (image.Composer, error)
	// RunGoReleaser builds the release bundle. Nil selects [goprof.RunGoReleaser].
	RunGoReleaser func(ctx context.Context, options goprof.GoReleaserOptions) error
	// settings is filled after flags are parsed.
	settings *Settings
}

// NewRootCommand creates the release-cli Cobra command tree.
//
// Streams, environment lookup, and build metadata are injected so tests never
// touch process globals. Nil streams become empty/discard streams. A nil
// LookupEnv uses [os.LookupEnv]. Blank version and commit default to "dev"
// and "none". A zero protocol defaults to [Protocol].
//
// Flags override RELEASE_* environment variables. There is no config file.
func NewRootCommand(options Options) *cobra.Command {
	options = options.withDefaults()

	root := &cobra.Command{
		Use:           commandName,
		Short:         "Stage and publish Meigma release artifacts",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("a subcommand is required"))
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			*options.settings = resolveSettings(cmd, options.LookupEnv)
			return nil
		},
	}
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.SetFlagErrorFunc(flagParseError)
	root.PersistentFlags().Bool("json", false, "emit one JSON result document on stdout")
	root.AddCommand(newStageCommand(options))
	root.AddCommand(newPlanCommand(options))
	root.AddCommand(newVerifyCommand(options))
	root.AddCommand(newInitCommand(options))
	publish := newPublishCommand(options)
	publish.AddCommand(newGitHubCommand(options))
	root.AddCommand(publish)
	root.AddCommand(newImageCommand(options))
	root.AddCommand(newVersionCommand(options))

	return root
}

// flagParseError prints usage to stderr and classifies the parse failure as
// [ErrUsage]. Flag-parse failures never emit a JSON envelope.
func flagParseError(cmd *cobra.Command, err error) error {
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), cmd.UsageString())
	return UsageError(err)
}

// withDefaults fills nil streams, a nil LookupEnv, a nil RunGoReleaser,
// and blank build metadata.
func (options Options) withDefaults() Options {
	if options.In == nil {
		options.In = strings.NewReader("")
	}
	if options.Out == nil {
		options.Out = io.Discard
	}
	if options.Err == nil {
		options.Err = io.Discard
	}
	if options.LookupEnv == nil {
		options.LookupEnv = os.LookupEnv
	}
	if options.settings == nil {
		options.settings = &Settings{}
	}
	if strings.TrimSpace(options.Build.Version) == "" {
		options.Build.Version = "dev"
	}
	if strings.TrimSpace(options.Build.Commit) == "" {
		options.Build.Commit = "none"
	}
	if options.Build.Protocol == 0 {
		options.Build.Protocol = Protocol
	}
	if options.RunGoReleaser == nil {
		options.RunGoReleaser = goprof.RunGoReleaser
	}

	return options
}

// resolveSettings applies flag-over-env precedence for the executing command.
func resolveSettings(cmd *cobra.Command, lookup LookupEnv) Settings {
	settings := Settings{
		Profile:       resolveString(cmd, flagProfile, envProfile, lookup),
		Dist:          resolveString(cmd, flagDist, envDist, lookup),
		Identity:      resolveString(cmd, flagIdentity, envIdentity, lookup),
		Issuer:        resolveString(cmd, flagIssuer, envIssuer, lookup),
		ArtifactID:    resolveString(cmd, flagArtifactID, envArtifactID, lookup),
		Image:         resolveString(cmd, flagImage, envImage, lookup),
		Version:       resolveString(cmd, flagVersion, envVersion, lookup),
		Digest:        resolveString(cmd, flagDigest, envDigest, lookup),
		Layout:        resolveString(cmd, flagLayout, envLayout, lookup),
		Input:         resolveString(cmd, flagInput, envInput, lookup),
		Work:          resolveString(cmd, flagWork, envWork, lookup),
		Output:        resolveString(cmd, flagOutput, envOutput, lookup),
		MelangeConfig: resolveString(cmd, flagMelangeConfig, envMelangeConfig, lookup),
		ApkoConfig:    resolveString(cmd, flagApkoConfig, envApkoConfig, lookup),
		BuildDate:     resolveString(cmd, flagBuildDate, envBuildDate, lookup),
		Binary:        resolveString(cmd, flagBinary, envBinary, lookup),
		PlainHTTP:     resolveFlagBool(cmd, flagPlainHTTP),
		NoUndraft:     resolveFlagBool(cmd, flagNoUndraft),
	}

	dryRun, err := resolveBool(cmd, flagDryRun, envDryRun, lookup)
	if err != nil {
		settings.err = fmt.Errorf("%s: %w", envDryRun, err)
	} else {
		settings.DryRun = dryRun
	}
	jsonOut, err := resolveBool(cmd, "json", envJSON, lookup)
	if err != nil {
		if settings.err == nil {
			settings.err = fmt.Errorf("%s: %w", envJSON, err)
		}
	} else {
		settings.JSON = jsonOut
	}

	return settings
}

// resolveString returns the flag value when the flag was set, otherwise the
// named environment variable, otherwise the flag default.
func resolveString(cmd *cobra.Command, flagName, envName string, lookup LookupEnv) string {
	if flag := cmd.Flags().Lookup(flagName); flag != nil && flag.Changed {
		return flag.Value.String()
	}
	if value, ok := lookup(envName); ok {
		return value
	}
	if flag := cmd.Flags().Lookup(flagName); flag != nil {
		return flag.Value.String()
	}

	return ""
}

// resolveBool returns the flag value when the flag was set, otherwise the
// named environment variable parsed as a bool, otherwise false.
//
// An unparsable environment value is an error. [strconv.ParseBool] does not
// accept yes, on, y, or enabled.
func resolveBool(cmd *cobra.Command, flagName, envName string, lookup LookupEnv) (bool, error) {
	if flag := cmd.Flags().Lookup(flagName); flag != nil && flag.Changed {
		value, err := strconv.ParseBool(flag.Value.String())
		if err != nil {
			return false, err
		}

		return value, nil
	}
	if raw, ok := lookup(envName); ok {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return false, err
		}

		return value, nil
	}

	return false, nil
}

// resolveFlagBool returns the named flag when it was set, otherwise false.
func resolveFlagBool(cmd *cobra.Command, flagName string) bool {
	flag := cmd.Flags().Lookup(flagName)
	if flag == nil || !flag.Changed {
		return false
	}
	value, err := strconv.ParseBool(flag.Value.String())

	return err == nil && value
}

// usageNoArgs rejects positional arguments as a usage error.
func usageNoArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return UsageError(err)
	}

	return nil
}

// requireSubcommand rejects a missing or unknown verb as [ErrUsage].
func requireSubcommand(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return UsageError(errors.New("a subcommand is required"))
	}

	return UsageError(fmt.Errorf("unknown command %q", args[0]))
}
