package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/stage/pubgh"
	"github.com/meigma/release/internal/stage/pubscoop"
)

const (
	// commandInitScoopBucket is the envelope command path for init scoop-bucket.
	commandInitScoopBucket = "init scoop-bucket"
)

// scoopBucketInitConfig is the validated initializer configuration.
type scoopBucketInitConfig struct {
	// Bucket is the target Scoop bucket repository.
	Bucket pubscoop.Repository
	// Output is the clean output directory path.
	Output string
	// Commit is the release-cli source commit used to pin the managed workflow.
	Commit pubgh.CommitSHA
}

// newScoopBucketInitCommand constructs the init scoop-bucket verb.
func newScoopBucketInitCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scoop-bucket",
		Short: "Write a protected Scoop bucket scaffold",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScoopBucketInit(cmd, options)
		},
	}
	cmd.Flags().String(flagBucket, "", "bucket repository as owner/repository")
	cmd.Flags().String(flagOutput, "", "new or empty output directory")

	return cmd
}

// runScoopBucketInit validates the local request and writes the scaffold.
func runScoopBucketInit(cmd *cobra.Command, options Options) error {
	config, err := resolveScoopBucketInit(cmd, options)
	if err != nil {
		return writeCommandResult(options, commandInitScoopBucket, nil, UsageError(err))
	}
	files := renderScoopBucketScaffold(config)
	if err := writeScaffold(config.Output, files); err != nil {
		return writeCommandResult(options, commandInitScoopBucket, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path
	}
	result := ScoopBucketInitResult{
		Bucket: config.Bucket.String(),
		Output: config.Output,
		Files:  paths,
	}

	return writeCommandResult(options, commandInitScoopBucket, result, nil)
}

// resolveScoopBucketInit validates flags and the release-cli build commit without I/O.
func resolveScoopBucketInit(cmd *cobra.Command, options Options) (scoopBucketInitConfig, error) {
	bucketRaw, err := requiredCommandFlag(cmd, flagBucket)
	if err != nil {
		return scoopBucketInitConfig{}, err
	}
	bucket, err := pubscoop.ParseRepository(bucketRaw)
	if err != nil {
		return scoopBucketInitConfig{}, fmt.Errorf("--%s: %w", flagBucket, err)
	}
	output, err := requiredCommandFlag(cmd, flagOutput)
	if err != nil {
		return scoopBucketInitConfig{}, err
	}
	commit, err := pubgh.ParseCommitSHA(options.Build.Commit)
	if err != nil {
		return scoopBucketInitConfig{}, fmt.Errorf("release-cli build commit: %w", err)
	}

	return scoopBucketInitConfig{
		Bucket: bucket,
		Output: filepath.Clean(output),
		Commit: commit,
	}, nil
}

// renderScoopBucketScaffold returns the complete deterministic root-layout scaffold.
func renderScoopBucketScaffold(config scoopBucketInitConfig) []scaffoldFile {
	workflow := fmt.Sprintf(`name: Manifest CI

on:
  pull_request:
    paths:
      - '*.json'

permissions: {}

jobs:
  manifests:
    permissions:
      contents: read
    uses: meigma/release/.github/workflows/scoop-bucket-ci.yml@%s
`, config.Commit.String())
	readme := fmt.Sprintf(`# %s

This repository publishes Scoop manifests through protected pull requests.

## Add the bucket

`+"```powershell"+`
scoop bucket add %s https://github.com/%s
`+"```"+`

## Install an app

`+"```powershell"+`
scoop install %s/<manifest>
`+"```"+`

Manifest pull requests are validated on Windows AMD64 and ARM64 before merge.
`, config.Bucket.String(), config.Bucket.Name, config.Bucket.String(), config.Bucket.Name)

	return []scaffoldFile{
		{
			Path: ".gitattributes",
			Content: `# Scoop's pinned bucket tests require CRLF text in the Windows checkout.
* text=auto eol=crlf
`,
		},
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
		{Path: ".github/workflows/manifests.yml", Content: workflow},
		{Path: "README.md", Content: readme},
	}
}
