package cli

import (
	"fmt"
	"os"
	"path"
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
		Short: "Verify a profile-specific dist directory",
		Args:  usageNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runStage(options)
		},
	}
	cmd.Flags().String(flagProfile, "", "release profile (only go is supported)")
	cmd.Flags().String(flagDist, "", "path to the GoReleaser dist directory")

	return cmd
}

// runStage validates flags and verifies the Go profile dist directory.
func runStage(options Options) error {
	settings := *options.settings
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

	root, err := os.OpenRoot(settings.Dist)
	if err != nil {
		return writeCommandResult(options, "stage", nil, fmt.Errorf("open dist %s: %w", settings.Dist, err))
	}
	defer root.Close()

	rootName, err := goprof.ParseRootName(path.Base(settings.Dist))
	if err != nil {
		return writeCommandResult(options, "stage", nil, err)
	}
	report, err := stage.Stage(root.FS(), rootName)
	if err != nil {
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
