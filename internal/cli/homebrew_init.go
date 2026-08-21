package cli

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/stage/pubbrew"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// commandInitHomebrewTap is the envelope command path for init homebrew-tap.
	commandInitHomebrewTap = "init homebrew-tap"
	// homebrewRepositoryPrefix is GitHub's required repository prefix for named taps.
	homebrewRepositoryPrefix = "homebrew-"
	// scaffoldDirectoryMode makes generated public-repository directories traversable.
	scaffoldDirectoryMode os.FileMode = 0o755
	// scaffoldFileMode makes generated public-repository source readable.
	scaffoldFileMode os.FileMode = 0o644
)

// homebrewTapInitConfig is the validated initializer configuration.
type homebrewTapInitConfig struct {
	// Tap is the target Homebrew tap repository.
	Tap pubbrew.Repository
	// Name is the Homebrew tap name derived from the repository.
	Name string
	// Output is the clean output directory path.
	Output string
	// Commit is the release-cli source commit used to pin the managed workflow.
	Commit pubgh.CommitSHA
}

// scaffoldFile is one deterministic tap file.
type scaffoldFile struct {
	// Path is the slash-separated path beneath the scaffold root.
	Path string
	// Content is the complete file content.
	Content string
}

// newInitCommand constructs the init parent verb.
func newInitCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize release infrastructure",
		Args:  requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("an init subcommand is required"))
		},
	}
	cmd.AddCommand(newHomebrewTapInitCommand(options))

	return cmd
}

// newHomebrewTapInitCommand constructs the init homebrew-tap verb.
func newHomebrewTapInitCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "homebrew-tap",
		Short: "Write a cask-only Homebrew tap scaffold",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHomebrewTapInit(cmd, options)
		},
	}
	cmd.Flags().String(flagTap, "", "tap repository as owner/homebrew-name")
	cmd.Flags().String(flagOutput, "", "new or empty output directory")

	return cmd
}

// runHomebrewTapInit validates the local request and writes the scaffold.
func runHomebrewTapInit(cmd *cobra.Command, options Options) error {
	config, err := resolveHomebrewTapInit(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandInitHomebrewTap, nil, UsageError(err))
	}
	files := renderHomebrewTapScaffold(config)
	if err := writeHomebrewTapScaffold(config.Output, files); err != nil {
		return writeCommandResult(options, commandInitHomebrewTap, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path
	}
	result := HomebrewTapInitResult{
		Tap:    config.Tap.String(),
		Output: config.Output,
		Files:  paths,
	}

	return writeCommandResult(options, commandInitHomebrewTap, result, nil)
}

// resolveHomebrewTapInit validates flags and the release-cli build commit without I/O.
func resolveHomebrewTapInit(cmd *cobra.Command, options Options) (homebrewTapInitConfig, error) {
	tapRaw, err := requiredCommandFlag(cmd, flagTap)
	if err != nil {
		return homebrewTapInitConfig{}, err
	}
	tap, err := pubbrew.ParseRepository(tapRaw)
	if err != nil {
		return homebrewTapInitConfig{}, fmt.Errorf("--%s: %w", flagTap, err)
	}
	name, err := homebrewTapName(tap)
	if err != nil {
		return homebrewTapInitConfig{}, fmt.Errorf("--%s: %w", flagTap, err)
	}
	output, err := requiredCommandFlag(cmd, flagOutput)
	if err != nil {
		return homebrewTapInitConfig{}, err
	}
	commit, err := pubgh.ParseCommitSHA(options.Build.Commit)
	if err != nil {
		return homebrewTapInitConfig{}, fmt.Errorf("release-cli build commit: %w", err)
	}

	return homebrewTapInitConfig{
		Tap:    tap,
		Name:   name,
		Output: filepath.Clean(output),
		Commit: commit,
	}, nil
}

// homebrewTapName derives the install-facing tap name from homebrew-<name>.
func homebrewTapName(repository pubbrew.Repository) (string, error) {
	name, ok := strings.CutPrefix(repository.Name, homebrewRepositoryPrefix)
	if !ok || name == "" {
		return "", fmt.Errorf("repository name %q must use homebrew-<name>", repository.Name)
	}

	return name, nil
}

// renderHomebrewTapScaffold returns the complete deterministic cask-only scaffold.
func renderHomebrewTapScaffold(config homebrewTapInitConfig) []scaffoldFile {
	workflow := fmt.Sprintf(`name: Cask CI

on:
  pull_request:
    paths:
      - 'Casks/**'

permissions: {}

jobs:
  casks:
    permissions:
      contents: read
    uses: meigma/release/.github/workflows/homebrew-tap-ci.yml@%s
`, config.Commit.String())
	readme := fmt.Sprintf(`# %s

This repository publishes Homebrew casks through protected pull requests.

## Install a cask

Use the tap name %[2]s/%[3]s:

`+"```console"+`
brew install --cask %[2]s/%[3]s/<cask>
`+"```"+`

Cask pull requests are validated on macOS and Linux before merge.
`, config.Tap.String(), config.Tap.Owner, config.Name)

	return []scaffoldFile{
		{
			Path: ".github/dependabot.yml",
			Content: `version: 2
updates:
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
`,
		},
		{Path: ".github/workflows/casks.yml", Content: workflow},
		{Path: "Casks/.gitkeep", Content: ""},
		{Path: "README.md", Content: readme},
	}
}

// writeHomebrewTapScaffold atomically publishes files into a new or empty output directory.
func writeHomebrewTapScaffold(output string, files []scaffoldFile) error {
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve output %s: %w", output, err)
	}
	existed, err := requireEmptyOutput(absoluteOutput)
	if err != nil {
		return err
	}
	parent := filepath.Dir(absoluteOutput)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("stat output parent %s: %w", parent, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("output parent %s is not a directory", parent)
	}

	temporary, err := os.MkdirTemp(parent, ".release-cli-homebrew-tap-*")
	if err != nil {
		return fmt.Errorf("create temporary scaffold in %s: %w", parent, err)
	}
	defer os.RemoveAll(temporary)
	if chmodErr := os.Chmod(temporary, scaffoldDirectoryMode); chmodErr != nil {
		return fmt.Errorf("set temporary scaffold permissions: %w", chmodErr)
	}
	root, err := os.OpenRoot(temporary)
	if err != nil {
		return fmt.Errorf("open temporary scaffold: %w", err)
	}
	for _, file := range files {
		if writeErr := writeScaffoldFile(root, file); writeErr != nil {
			return errors.Join(writeErr, root.Close())
		}
	}
	if err := root.Close(); err != nil {
		return fmt.Errorf("close temporary scaffold: %w", err)
	}
	if err := installScaffoldDirectory(temporary, absoluteOutput, existed); err != nil {
		return err
	}

	return nil
}

// requireEmptyOutput rejects a non-directory, symlink, or nonempty output path.
func requireEmptyOutput(output string) (bool, error) {
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect output %s: %w", output, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("output %s is not a directory", output)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return false, fmt.Errorf("read output %s: %w", output, err)
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("output %s is not empty", output)
	}

	return true, nil
}

// writeScaffoldFile writes one root-confined scaffold file and its parent directories.
func writeScaffoldFile(root *os.Root, file scaffoldFile) error {
	directory := path.Dir(file.Path)
	if directory != "." {
		if err := root.MkdirAll(directory, scaffoldDirectoryMode); err != nil {
			return fmt.Errorf("create scaffold directory %s: %w", directory, err)
		}
	}
	if err := root.WriteFile(file.Path, []byte(file.Content), scaffoldFileMode); err != nil {
		return fmt.Errorf("write scaffold file %s: %w", file.Path, err)
	}

	return nil
}

// installScaffoldDirectory renames the complete temporary scaffold into place.
func installScaffoldDirectory(temporary, output string, outputExists bool) error {
	if !outputExists {
		if err := os.Rename(temporary, output); err != nil {
			return fmt.Errorf("install scaffold at %s: %w", output, err)
		}
		return nil
	}
	if err := os.Rename(temporary, output); err == nil {
		return nil
	}
	if _, err := requireEmptyOutput(output); err != nil {
		return err
	}
	if err := os.Remove(output); err != nil {
		return fmt.Errorf("replace empty output %s: %w", output, err)
	}
	if err := os.Rename(temporary, output); err != nil {
		if recreateErr := os.Mkdir(output, scaffoldDirectoryMode); recreateErr != nil {
			return errors.Join(
				fmt.Errorf("install scaffold at %s: %w", output, err),
				fmt.Errorf("restore empty output %s: %w", output, recreateErr),
			)
		}
		return fmt.Errorf("install scaffold at %s: %w", output, err)
	}

	return nil
}
