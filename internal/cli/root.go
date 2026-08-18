package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// BuildInfo describes linker-injected build metadata printed by --version.
type BuildInfo struct {
	// Version is the release version.
	Version string
	// Commit is the source commit used to build the binary.
	Commit string
}

// Options customizes root command construction.
type Options struct {
	// In receives command input.
	In io.Reader
	// Out receives command output.
	Out io.Writer
	// Err receives diagnostics.
	Err io.Writer
	// Build controls the root command version output.
	Build BuildInfo
}

// NewRootCommand creates the release-mvp Cobra command tree.
func NewRootCommand(options Options) *cobra.Command {
	options = options.withDefaults()

	root := &cobra.Command{
		Use:           "release-mvp",
		Short:         "Exercise the Meigma release pipeline",
		Version:       options.Build.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(fmt.Sprintf(
		"release-mvp %s (%s)\n",
		options.Build.Version,
		options.Build.Commit,
	))
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.AddCommand(newGreetCommand())

	return root
}

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
	if strings.TrimSpace(options.Build.Version) == "" {
		options.Build.Version = "dev"
	}
	if strings.TrimSpace(options.Build.Commit) == "" {
		options.Build.Commit = "none"
	}

	return options
}

func newGreetCommand() *cobra.Command {
	var uppercase bool

	cmd := &cobra.Command{
		Use:   "greet [name]",
		Short: "Print a greeting",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "world"
			if len(args) == 1 {
				name = args[0]
			}

			greeting := fmt.Sprintf("Hello, %s!", name)
			if uppercase {
				greeting = strings.ToUpper(greeting)
			}

			if _, err := fmt.Fprintln(cmd.OutOrStdout(), greeting); err != nil {
				return fmt.Errorf("write greeting: %w", err)
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&uppercase, "uppercase", false, "print the greeting in uppercase")

	return cmd
}
