package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

const (
	// commandPrepare is the envelope command path for publish oci prepare.
	commandPrepare = "publish oci prepare"
	// commandFinalize is the envelope command path for publish oci finalize.
	commandFinalize = "publish oci finalize"
	// flagLayout is the prepare layout-directory flag name.
	flagLayout = "layout"
	// flagDryRun is the prepare dry-run flag name.
	flagDryRun = "dry-run"
	// flagPlainHTTP is the registry plain-HTTP flag name.
	flagPlainHTTP = "plain-http"
	// flagResult is the finalize prepare-envelope flag name.
	flagResult = "result"
	// resultStdin is the only accepted --result value.
	resultStdin = "-"
	// envCosignPath is the Cosign binary path override.
	envCosignPath = "RELEASE_COSIGN_PATH"
	// jsonLimitMiB is the prepare-envelope size bound in mebibytes.
	//
	// It matches the unexported puboci JSON bound used by
	// [puboci.ParsePrepareResult].
	jsonLimitMiB = 4
	// jsonBytesPerKiB is the number of bytes in a kibibyte.
	jsonBytesPerKiB = 1024
	// jsonKibibytesPerMiB is the number of kibibytes in a mebibyte.
	jsonKibibytesPerMiB = 1024
	// jsonLimitBytes is the maximum encoded prepare envelope read from stdin.
	jsonLimitBytes int64 = jsonLimitMiB * jsonBytesPerKiB * jsonKibibytesPerMiB
	// jsonLimitReadBytes is one past [jsonLimitBytes] so an oversize
	// document can be distinguished from a document that fills the bound.
	jsonLimitReadBytes = jsonLimitBytes + 1
)

// newPublishCommand constructs the publish parent verb.
func newPublishCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish release artifacts",
		Args:  requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("a publish subcommand is required"))
		},
	}
	cmd.AddCommand(newOCICommand(options))
	cmd.AddCommand(newHomebrewCommand(options))

	return cmd
}

// newOCICommand constructs the publish oci verb.
func newOCICommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oci",
		Short: "Publish OCI images",
		Args:  requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("an oci subcommand is required"))
		},
	}
	cmd.AddCommand(newPrepareCommand(options))
	cmd.AddCommand(newFinalizeCommand(options))

	return cmd
}

// newPrepareCommand constructs the publish oci prepare verb.
func newPrepareCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Prepare a digest-addressed OCI image publication",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPrepare(cmd, options)
		},
	}
	cmd.Flags().String(flagLayout, "", "path to the extracted oci-image/layout directory")
	cmd.Flags().String(flagImage, "", "OCI image name without a tag or digest")
	cmd.Flags().String(flagVersion, "", "stable MAJOR.MINOR.PATCH version")
	cmd.Flags().String(flagDigest, "", "expected OCI index digest")
	cmd.Flags().Bool(flagDryRun, false, "validate and plan without writing or signing")
	cmd.Flags().Bool(flagPlainHTTP, false, "use HTTP instead of HTTPS for the registry")

	return cmd
}

// newFinalizeCommand constructs the publish oci finalize verb.
func newFinalizeCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "finalize",
		Short: "Apply trusted OCI image tags after preparation",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFinalize(cmd, options)
		},
	}
	cmd.Flags().String(flagResult, "", "prepare result envelope; only - (stdin) is accepted")
	cmd.Flags().Bool(flagPlainHTTP, false, "use HTTP instead of HTTPS for the registry")

	return cmd
}

// runPrepare validates configuration and prepares a digest-addressed publication.
//
// Missing or malformed configuration is [ErrUsage] and is raised before any
// port is constructed. Opening the layout and publication failures are
// command failures. Success without --json writes nothing. The --json
// envelope result is the [puboci.OCIPrepareResult] itself.
func runPrepare(cmd *cobra.Command, options Options) error {
	expected, err := resolvePrepare(options)
	if err != nil {
		return writeCommandResult(options, commandPrepare, nil, UsageError(err))
	}

	root, err := os.OpenRoot(expected.Layout)
	if err != nil {
		return writeCommandResult(
			options,
			commandPrepare,
			nil,
			fmt.Errorf("open layout %s: %w", expected.Layout, err),
		)
	}
	defer root.Close()

	reader, err := stateReader(options, expected.Registry)
	if err != nil {
		return writeCommandResult(options, commandPrepare, nil, err)
	}

	pusher, signer, err := prepareWriters(options, expected)
	if err != nil {
		return writeCommandResult(options, commandPrepare, nil, err)
	}

	result, err := puboci.Prepare(cmd.Context(), puboci.PrepareInput{
		Image:       expected.Image,
		Version:     expected.Version,
		IndexDigest: expected.Digest,
		Layout:      root.FS(),
		DryRun:      expected.DryRun,
	}, reader, pusher, signer)
	if err != nil {
		return writeCommandResult(options, commandPrepare, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandPrepare, result, nil)
}

// runFinalize validates the piped prepare envelope and applies trusted tags.
//
// Missing or malformed configuration, including a prepare envelope that is
// not a successful [commandPrepare] document, is [ErrUsage] and is raised
// before any port is constructed. Image, version, and digest come from the
// piped result, not from flags. Drift, planning, and registry failures are
// command failures. Success without --json writes nothing. The --json
// envelope result is the [puboci.FinalizeResult] itself.
func runFinalize(cmd *cobra.Command, options Options) error {
	expected, err := resolveFinalize(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandFinalize, nil, UsageError(err))
	}

	reader, err := stateReader(options, expected.Registry)
	if err != nil {
		return writeCommandResult(options, commandFinalize, nil, err)
	}
	committer, err := tagCommitter(options, expected.Registry)
	if err != nil {
		return writeCommandResult(options, commandFinalize, nil, err)
	}

	result, err := puboci.Finalize(cmd.Context(), puboci.FinalizeInput{
		Prepared: expected.Prepared,
	}, reader, committer)
	if err != nil {
		return writeCommandResult(options, commandFinalize, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandFinalize, result, nil)
}

// prepareConfig is the resolved publish-oci-prepare configuration.
type prepareConfig struct {
	// Image is the untagged repository to publish.
	Image puboci.Image
	// Version is the candidate stable release version.
	Version rel.Version
	// Digest is the expected image index digest.
	Digest rel.Digest
	// Layout is the extracted oci-image/layout directory.
	Layout string
	// DryRun skips registry writes and Cosign signing.
	DryRun bool
	// Registry authenticates and configures the registry client.
	Registry RegistryConfig
	// CosignPath is RELEASE_COSIGN_PATH. Empty resolves cosign from PATH.
	CosignPath string
}

// resolvePrepare parses flags and Actions environment into a prepare config.
//
// It performs no I/O.
func resolvePrepare(options Options) (prepareConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return prepareConfig{}, err
	}
	if settings.Layout == "" {
		return prepareConfig{}, fmt.Errorf("--%s is required", flagLayout)
	}

	digest, err := resolvePlanDigest(settings)
	if err != nil {
		return prepareConfig{}, err
	}
	image, err := resolvePlanImage(settings, options.LookupEnv)
	if err != nil {
		return prepareConfig{}, err
	}
	if plainErr := requireLoopbackPlainHTTP(image, settings.PlainHTTP); plainErr != nil {
		return prepareConfig{}, plainErr
	}
	version, err := resolvePlanVersion(settings, options.LookupEnv)
	if err != nil {
		return prepareConfig{}, err
	}

	return prepareConfig{
		Image:      image,
		Version:    version,
		Digest:     digest,
		Layout:     settings.Layout,
		DryRun:     settings.DryRun,
		Registry:   resolveRegistryConfig(settings, options.LookupEnv),
		CosignPath: resolveCosignPath(options.LookupEnv),
	}, nil
}

// finalizeConfig is the resolved publish-oci-finalize configuration.
type finalizeConfig struct {
	// Prepared is the validated prepare document from stdin.
	Prepared puboci.OCIPrepareResult
	// Registry authenticates and configures the registry client.
	Registry RegistryConfig
}

// resolveFinalize parses flags and the piped prepare envelope.
//
// It reads [Options.In] and performs no registry I/O.
func resolveFinalize(cmd *cobra.Command, options Options) (finalizeConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return finalizeConfig{}, err
	}

	resultPath, err := cmd.Flags().GetString(flagResult)
	if err != nil {
		return finalizeConfig{}, err
	}
	if resultPath == "" {
		return finalizeConfig{}, fmt.Errorf("--%s is required", flagResult)
	}
	if resultPath != resultStdin {
		return finalizeConfig{}, fmt.Errorf("--%s accepts only %s; there is no receipt file", flagResult, resultStdin)
	}

	prepared, err := parsePrepareEnvelope(options.In)
	if err != nil {
		return finalizeConfig{}, err
	}
	image, err := puboci.ParseImage(prepared.Image)
	if err != nil {
		return finalizeConfig{}, fmt.Errorf("prepare result image: %w", err)
	}
	if plainErr := requireLoopbackPlainHTTP(image, settings.PlainHTTP); plainErr != nil {
		return finalizeConfig{}, plainErr
	}

	return finalizeConfig{
		Prepared: prepared,
		Registry: resolveRegistryConfig(settings, options.LookupEnv),
	}, nil
}

// parsePrepareEnvelope decodes one successful publish-oci-prepare envelope from r.
//
// The document must use [Schema], name [commandPrepare], set ok to true, and
// contain no trailing content. Input is bounded to [jsonLimitBytes], matching
// the puboci JSON bound. The inner result is parsed with
// [puboci.ParsePrepareResult].
func parsePrepareEnvelope(r io.Reader) (puboci.OCIPrepareResult, error) {
	if r == nil {
		return puboci.OCIPrepareResult{}, errors.New("stdin is empty")
	}

	limited := &io.LimitedReader{R: r, N: jsonLimitReadBytes}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	var envelope finalizePrepareEnvelope
	err := decoder.Decode(&envelope)
	if limited.N == 0 {
		return puboci.OCIPrepareResult{}, fmt.Errorf(
			"prepare envelope exceeds the %d MiB JSON limit",
			jsonLimitMiB,
		)
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			return puboci.OCIPrepareResult{}, errors.New("stdin is empty")
		}

		return puboci.OCIPrepareResult{}, fmt.Errorf("prepare envelope: %w", err)
	}
	if decoder.More() {
		return puboci.OCIPrepareResult{}, errors.New("prepare envelope has trailing content")
	}
	if envelope.Schema != Schema {
		return puboci.OCIPrepareResult{}, fmt.Errorf("prepare envelope schema %q is unsupported", envelope.Schema)
	}
	if envelope.Command != commandPrepare {
		return puboci.OCIPrepareResult{}, fmt.Errorf(
			"prepare envelope command %q is not %q",
			envelope.Command,
			commandPrepare,
		)
	}
	if !envelope.OK {
		return puboci.OCIPrepareResult{}, errors.New("prepare envelope is not successful")
	}

	prepared, err := puboci.ParsePrepareResult(bytes.NewReader(envelope.Result))
	if err != nil {
		return puboci.OCIPrepareResult{}, err
	}

	return prepared, nil
}

// finalizePrepareEnvelope is the stdin document produced by publish oci prepare --json.
type finalizePrepareEnvelope struct {
	// Schema identifies the envelope version and must be [Schema].
	Schema string `json:"schema"`
	// Command is the producing verb path and must be [commandPrepare].
	Command string `json:"command"`
	// OK is true only for a successful prepare.
	OK bool `json:"ok"`
	// Result is the inner [puboci.OCIPrepareResult] document.
	Result json.RawMessage `json:"result"`
}

// resolveRegistryConfig combines credentials with the plain-HTTP setting.
func resolveRegistryConfig(settings Settings, lookup LookupEnv) RegistryConfig {
	return RegistryConfig{
		Credentials: resolveRegistryCredentials(lookup),
		PlainHTTP:   settings.PlainHTTP,
	}
}

// resolveCosignPath returns RELEASE_COSIGN_PATH, or empty to resolve from PATH.
func resolveCosignPath(lookup LookupEnv) string {
	if lookup == nil {
		return ""
	}
	value, ok := lookup(envCosignPath)
	if !ok {
		return ""
	}

	return value
}

// prepareWriters returns the write and sign ports for a real prepare.
//
// A dry run returns nil ports and does not construct them.
func prepareWriters(options Options, expected prepareConfig) (puboci.ContentPusher, puboci.Signer, error) {
	if expected.DryRun {
		return nil, nil, nil
	}

	pusher, err := contentPusher(options, expected.Registry)
	if err != nil {
		return nil, nil, err
	}
	signer, err := contentSigner(options, expected.CosignPath)
	if err != nil {
		return nil, nil, err
	}

	return pusher, signer, nil
}

// contentPusher returns the injected write port or constructs one.
func contentPusher(options Options, config RegistryConfig) (puboci.ContentPusher, error) {
	if options.ContentPusher != nil {
		return options.ContentPusher, nil
	}
	if options.NewContentPusher == nil {
		return nil, errors.New("content pusher factory is not configured")
	}

	pusher, err := options.NewContentPusher(config)
	if err != nil {
		return nil, UsageError(fmt.Errorf("registry client: %w", err))
	}
	if pusher == nil {
		return nil, errors.New("content pusher factory returned nil")
	}

	return pusher, nil
}

// contentSigner returns the injected signing port or constructs one.
func contentSigner(options Options, path string) (puboci.Signer, error) {
	if options.Signer != nil {
		return options.Signer, nil
	}
	if options.NewSigner == nil {
		return nil, errors.New("signer factory is not configured")
	}

	signer, err := options.NewSigner(path)
	if err != nil {
		return nil, UsageError(fmt.Errorf("cosign: %w", err))
	}
	if signer == nil {
		return nil, errors.New("signer factory returned nil")
	}

	return signer, nil
}

// tagCommitter returns the injected tag-write port or constructs one.
func tagCommitter(options Options, config RegistryConfig) (puboci.TagCommitter, error) {
	if options.TagCommitter != nil {
		return options.TagCommitter, nil
	}
	if options.NewTagCommitter == nil {
		return nil, errors.New("tag committer factory is not configured")
	}

	committer, err := options.NewTagCommitter(config)
	if err != nil {
		return nil, UsageError(fmt.Errorf("registry client: %w", err))
	}
	if committer == nil {
		return nil, errors.New("tag committer factory returned nil")
	}

	return committer, nil
}
