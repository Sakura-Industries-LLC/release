package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCommand constructs the version verb.
func newVersionCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and protocol",
		Args:  usageNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runVersion(options)
		},
	}
}

// runVersion writes the human or JSON version identity.
func runVersion(options Options) error {
	if options.settings != nil && options.settings.JSON {
		return writeCommandResult(options, "version", VersionResult{
			Version:  options.Build.Version,
			Commit:   options.Build.Commit,
			Protocol: options.Build.Protocol,
		}, nil)
	}

	_, err := fmt.Fprintf(
		options.Out,
		"%s %s (%s, protocol %d)\n",
		commandName,
		options.Build.Version,
		options.Build.Commit,
		options.Build.Protocol,
	)
	if err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	return nil
}
