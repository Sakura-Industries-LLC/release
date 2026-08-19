package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// commandName is the process name shown in help and version text.
	commandName = "release-cli"
	// envProfile is the environment variable for --profile.
	envProfile = "RELEASE_PROFILE"
	// envDist is the environment variable for --dist.
	envDist = "RELEASE_DIST"
	// envJSON is the environment variable for --json.
	envJSON = "RELEASE_JSON"
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
	// ArtifactID is the selected --artifact-id / RELEASE_ARTIFACT_ID value.
	ArtifactID string
	// Digest is the selected --digest / RELEASE_DIGEST value.
	Digest string
	// JSON reports whether --json / RELEASE_JSON requested structured output.
	JSON bool
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
	root.AddCommand(newVerifyCommand(options))
	root.AddCommand(newVersionCommand(options))

	return root
}

// flagParseError prints usage to stderr and classifies the parse failure as
// [ErrUsage]. Flag-parse failures never emit a JSON envelope.
func flagParseError(cmd *cobra.Command, err error) error {
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), cmd.UsageString())
	return UsageError(err)
}

// withDefaults fills nil streams, a nil LookupEnv, and blank build metadata.
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

	return options
}

// resolveSettings applies flag-over-env precedence for the executing command.
func resolveSettings(cmd *cobra.Command, lookup LookupEnv) Settings {
	return Settings{
		Profile:    resolveString(cmd, flagProfile, envProfile, lookup),
		Dist:       resolveString(cmd, flagDist, envDist, lookup),
		ArtifactID: resolveString(cmd, flagArtifactID, envArtifactID, lookup),
		Digest:     resolveString(cmd, flagDigest, envDigest, lookup),
		JSON:       resolveBool(cmd, "json", envJSON, lookup),
	}
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
func resolveBool(cmd *cobra.Command, flagName, envName string, lookup LookupEnv) bool {
	if flag := cmd.Flags().Lookup(flagName); flag != nil && flag.Changed {
		value, err := strconv.ParseBool(flag.Value.String())
		return err == nil && value
	}
	if raw, ok := lookup(envName); ok {
		value, err := strconv.ParseBool(raw)
		return err == nil && value
	}

	return false
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
