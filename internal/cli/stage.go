package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/profile/goprof"
	"github.com/meigma/release/internal/stage"
)

const (
	// profileGo is the only accepted --profile value in this slice.
	profileGo = "go"
	// flagProfile is the stage profile flag name.
	flagProfile = "profile"
	// flagDist is the stage dist-directory flag name.
	flagDist = "dist"
	// octalBase formats file modes as octal strings.
	octalBase = 8
)

// newStageCommand constructs the stage verb.
func newStageCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stage",
		Short: "Build and verify a profile-specific dist directory",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStage(cmd.Context(), options)
		},
	}
	cmd.Flags().String(flagProfile, "", "release profile (only go is supported)")
	cmd.Flags().String(flagDist, "", "path to the GoReleaser dist directory")

	return cmd
}

// runStage builds the Go profile dist directory with GoReleaser, then verifies it.
//
// A malformed RELEASE_* boolean, a missing or unknown --profile, a missing
// --dist, and a --dist that is not a basename are [ErrUsage] and are
// raised before GoReleaser runs. A GoReleaser failure is a command
// failure. GoReleaser progress and diagnostics are written to stderr so
// --json stdout stays a single envelope. Success writes the OCI
// build-input projection and, without --json, writes nothing to stdout.
func runStage(ctx context.Context, options Options) error {
	settings := *options.settings
	if err := settings.err; err != nil {
		return writeCommandResult(options, "stage", nil, UsageError(err))
	}
	if settings.Profile == "" {
		return writeCommandResult(options, "stage", nil, UsageError(fmt.Errorf("--%s is required", flagProfile)))
	}
	if settings.Profile != profileGo {
		return writeCommandResult(
			options,
			"stage",
			nil,
			UsageError(fmt.Errorf("unknown profile %q (supported: %s)", settings.Profile, profileGo)),
		)
	}

	if settings.Dist == "" {
		return writeCommandResult(options, "stage", nil, UsageError(fmt.Errorf("--%s is required", flagDist)))
	}
	rootName, err := goprof.ParseRootName(settings.Dist)
	if err != nil {
		return writeCommandResult(options, "stage", nil, UsageError(fmt.Errorf(
			"--%s %q is not a basename; GoReleaser writes its distribution directory relative to the working directory",
			flagDist,
			settings.Dist,
		)))
	}

	if options.RunGoReleaser == nil {
		return writeCommandResult(options, "stage", nil, errors.New("goreleaser runner is not configured"))
	}

	// GoReleaser is chatty; both streams go to the CLI stderr so --json
	// stdout stays exactly one envelope.
	err = options.RunGoReleaser(ctx, goprof.GoReleaserOptions{
		Path:   lookupValue(options.LookupEnv, envGoreleaserPath),
		Dist:   rootName,
		Stdout: options.Err,
		Stderr: options.Err,
	})
	if err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}

	root, err := os.OpenRoot(settings.Dist)
	if err != nil {
		return writeCommandResult(options, "stage", nil, fmt.Errorf("open dist %s: %w", settings.Dist, err))
	}
	defer root.Close()

	report, err := stage.Stage(root.FS(), rootName)
	if err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}
	if err := writeImageInputs(root, report); err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}
	if !settings.JSON {
		return nil
	}

	result := StageResult{
		Assets:   report.Assets,
		Binaries: make(map[string]BinaryResult, len(report.Binaries)),
	}
	for _, binary := range report.Binaries {
		result.Binaries[binary.Arch] = BinaryResult{
			Path: binary.Path,
			Mode: strconv.FormatUint(uint64(binary.Mode), octalBase),
		}
	}

	return writeCommandResult(options, "stage", result, nil)
}

// writeImageInputs persists the OCI build-input projection under dist.
func writeImageInputs(root *os.Root, report stage.Report) error {
	inputs, err := stage.NewImageInputs(profileGo, report)
	if err != nil {
		return err
	}

	file, err := root.Create(stage.ImageInputsName)
	if err != nil {
		return fmt.Errorf("create %s: %w", stage.ImageInputsName, err)
	}
	err = stage.EncodeImageInputs(file, inputs)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", stage.ImageInputsName, closeErr)
	}

	return nil
}
