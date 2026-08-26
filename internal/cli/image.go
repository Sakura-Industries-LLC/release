package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/image"
)

const (
	// commandImageBuild is the envelope command path for image build.
	commandImageBuild = "image build"
	// commandImageVerify is the envelope command path for image verify.
	commandImageVerify = "image verify"

	// flagInput is the image-build input-directory flag name.
	flagInput = "input"
	// flagWork is the image-build scratch-workspace flag name.
	flagWork = "work"
	// flagOutput is the image-build output-directory flag name.
	flagOutput = "output"
	// flagMelangeConfig is the Melange configuration flag name.
	flagMelangeConfig = "melange-config"
	// flagApkoConfig is the apko configuration flag name.
	flagApkoConfig = "apko-config"
	// flagBuildDate is the reproducible build-date flag name.
	flagBuildDate = "build-date"
	// flagBinary is the staged-binary-name flag name.
	flagBinary = "binary"

	// defaultMelangeConfig is the default Melange configuration path.
	defaultMelangeConfig = "melange.yaml"
	// defaultApkoConfig is the default apko configuration path.
	defaultApkoConfig = "apko.yaml"
	// envMelangePath is the Melange binary path override.
	envMelangePath = "RELEASE_MELANGE_PATH"
	// envApkoPath is the apko binary path override.
	envApkoPath = "RELEASE_APKO_PATH"
	// localImagePrefix is the local image reference prefix used by today's YAML.
	localImagePrefix = "local/"
	// dirMode is the permission used when creating work and output roots.
	dirMode = 0o755
	// imageLayoutDir is the output-relative OCI layout directory.
	imageLayoutDir = "layout"
	// imageSbomsDir is the output-relative SBOM directory.
	imageSbomsDir = "sboms"
	// imageDigestFile is the output-relative index digest artifact.
	imageDigestFile = "image-digest.txt"
	// fileMode is the permission used when writing image-digest.txt.
	fileMode = 0o644
)

// newImageCommand constructs the image parent verb.
func newImageCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Build OCI images from staged binaries",
		Args:  requireSubcommand,
		RunE: func(_ *cobra.Command, _ []string) error {
			return UsageError(errors.New("an image subcommand is required"))
		},
	}
	cmd.AddCommand(newImageBuildCommand(options))
	cmd.AddCommand(newImageVerifyCommand(options))

	return cmd
}

// newImageBuildCommand constructs the image build verb.
func newImageBuildCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build signed APK repositories and a locked OCI layout",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runImageBuild(cmd, options)
		},
	}
	cmd.Flags().String(flagInput, "", "path to the extracted oci-input artifact root")
	cmd.Flags().String(flagWork, "", "path to the scratch workspace")
	cmd.Flags().String(flagOutput, "", "path to the authoritative artifact output root")
	cmd.Flags().String(flagMelangeConfig, defaultMelangeConfig, "path to the Melange configuration")
	cmd.Flags().String(flagApkoConfig, defaultApkoConfig, "path to the apko configuration")
	cmd.Flags().String(flagBuildDate, "", "RFC 3339 reproducible build timestamp")
	cmd.Flags().String(flagVersion, "", "stable MAJOR.MINOR.PATCH version")

	return cmd
}

// newImageVerifyCommand constructs the image verify verb.
func newImageVerifyCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify an authoritative OCI layout against staged binaries",
		Args:  usageNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runImageVerify(options)
		},
	}
	cmd.Flags().String(flagOutput, "", "path to the authoritative artifact output root")
	cmd.Flags().String(flagWork, "", "path to the scratch workspace")
	cmd.Flags().String(flagVersion, "", "stable MAJOR.MINOR.PATCH version")

	return cmd
}

// runImageVerify validates configuration and verifies an OCI layout.
//
// Missing or malformed configuration is [ErrUsage] and is raised before any
// file is opened. Opening the output and work roots is confined to those
// trees. Layout, SBOM, and digest mismatches are command failures. Success
// writes <output>/image-digest.txt and, without --json, writes nothing to
// stdout. The --json envelope result is the [image.VerifyResult] itself.
func runImageVerify(options Options) error {
	expected, err := resolveImageVerify(options)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, UsageError(err))
	}

	output, err := os.OpenRoot(expected.Output)
	if err != nil {
		return writeCommandResult(
			options,
			commandImageVerify,
			nil,
			fmt.Errorf("open output %s: %w", expected.Output, err),
		)
	}
	defer output.Close()
	work, err := os.OpenRoot(expected.Work)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, fmt.Errorf("open work %s: %w", expected.Work, err))
	}
	defer work.Close()

	err = requireImageVerifyDir(output, imageLayoutDir)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}
	err = requireImageVerifyDir(output, imageSbomsDir)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}

	layoutFS, err := fs.Sub(output.FS(), imageLayoutDir)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, fmt.Errorf("open %s: %w", imageLayoutDir, err))
	}
	sbomsFS, err := fs.Sub(output.FS(), imageSbomsDir)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, fmt.Errorf("open %s: %w", imageSbomsDir, err))
	}

	arches := imageVerifyArches()
	names, err := listStagedBinaryNames(work.FS())
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}
	canonical, err := image.CanonicalDigests(work.FS(), arches, names)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}
	want := image.ExpectedImage{
		Version:   expected.Version,
		Binaries:  names,
		Revision:  expected.Revision,
		Source:    expected.Source,
		Canonical: canonical,
	}
	verified, err := image.VerifyLayout(layoutFS, want)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}
	err = image.VerifySBOMs(sbomsFS, expected.Version, arches)
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}
	err = writeImageDigest(output, verified.IndexDigest())
	if err != nil {
		return writeCommandResult(options, commandImageVerify, nil, err)
	}

	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandImageVerify, verified.Result(want), nil)
}

// writeImageDigest writes <output>/image-digest.txt as digest plus a newline.
func writeImageDigest(output *os.Root, digest rel.Digest) error {
	if err := output.WriteFile(imageDigestFile, []byte(digest.String()+"\n"), fileMode); err != nil {
		return fmt.Errorf("write %s: %w", imageDigestFile, err)
	}

	return nil
}

// imageVerifyConfig is the resolved image-verify configuration.
type imageVerifyConfig struct {
	// Output is the authoritative artifact output root.
	Output string
	// Work is the scratch workspace.
	Work string
	// Version is the candidate stable release version.
	Version rel.Version
	// Revision is the expected org.opencontainers.image.revision value.
	Revision string
	// Source is the expected org.opencontainers.image.source URL.
	Source string
}

// resolveImageVerify parses flags and Actions environment into an image-verify config.
//
// It performs no I/O.
func resolveImageVerify(options Options) (imageVerifyConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return imageVerifyConfig{}, err
	}
	if settings.Output == "" {
		return imageVerifyConfig{}, fmt.Errorf("--%s is required", flagOutput)
	}
	if settings.Work == "" {
		return imageVerifyConfig{}, fmt.Errorf("--%s is required", flagWork)
	}

	version, err := resolvePlanVersion(settings, options.LookupEnv)
	if err != nil {
		return imageVerifyConfig{}, err
	}
	revision, err := requiredEnv(options.LookupEnv, envCommitSHA)
	if err != nil {
		return imageVerifyConfig{}, err
	}
	serverURL, err := requiredEnv(options.LookupEnv, envServerURL)
	if err != nil {
		return imageVerifyConfig{}, err
	}
	repository, err := requiredEnv(options.LookupEnv, envRepository)
	if err != nil {
		return imageVerifyConfig{}, err
	}

	return imageVerifyConfig{
		Output:   settings.Output,
		Work:     settings.Work,
		Version:  version,
		Revision: revision,
		Source:   strings.TrimRight(serverURL, "/") + "/" + repository,
	}, nil
}

// validateImageVerifyBinary rejects an empty or path-like binary filename.
//
// Names must be nonempty, must not contain a path separator, and must not
// be "." or "..".
func validateImageVerifyBinary(name string) error {
	if name == "" {
		return errors.New("binary name is empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("binary name %q is not a filename", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("binary name %q contains a path separator", name)
	}

	return nil
}

// listStagedBinaryNames reads work/sources/<arch> and requires the same nonempty name set.
func listStagedBinaryNames(work fs.FS) ([]string, error) {
	arches := imageVerifyArches()
	namesByArch := make(map[image.APKArch]map[string]struct{}, len(arches))
	union := make(map[string]struct{})
	for _, arch := range arches {
		dir := path.Join("sources", arch.String())
		entries, err := fs.ReadDir(work, dir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		names := make(map[string]struct{})
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if err := validateImageVerifyBinary(name); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
			}
			names[name] = struct{}{}
			union[name] = struct{}{}
		}
		namesByArch[arch] = names
	}
	if len(union) == 0 {
		return nil, errors.New("staged binary name list is empty")
	}
	ordered := make([]string, 0, len(union))
	for name := range union {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	for _, arch := range arches {
		have := namesByArch[arch]
		for _, name := range ordered {
			if _, ok := have[name]; !ok {
				return nil, fmt.Errorf("missing staged binary %q for %s", name, arch)
			}
		}
	}

	return ordered, nil
}

// imageVerifyArches returns the closed APK architecture set in canonical order.
func imageVerifyArches() []image.APKArch {
	return []image.APKArch{image.ArchX8664, image.ArchAArch64}
}

// requireImageVerifyDir requires name to exist as a directory on root.
func requireImageVerifyDir(root *os.Root, name string) error {
	info, err := root.Stat(name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", name)
	}

	return nil
}

// runImageBuild validates configuration and builds an OCI layout.
//
// Missing or malformed configuration is [ErrUsage] and is raised before any
// directory is created or port constructed. Opening the input root and
// configuration files is read-only. Projection decoding and engine failures
// are command failures. Success without --json writes nothing. The --json
// envelope result is the [image.BuildResult] itself.
func runImageBuild(cmd *cobra.Command, options Options) error {
	expected, err := resolveImageBuild(options)
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, UsageError(err))
	}

	input, err := os.OpenRoot(expected.Input)
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, fmt.Errorf("open input %s: %w", expected.Input, err))
	}
	defer input.Close()

	binaries, err := loadImageBuildBinaries(input)
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, err)
	}
	melangeConfig, apkoConfig, err := openImageBuildConfigs(expected)
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, err)
	}
	defer melangeConfig.Close()
	defer apkoConfig.Close()

	apk, composer, err := imageBuildPorts(options, expected)
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, err)
	}

	work, err := createImageBuildRoot(expected.Work, "work")
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, err)
	}
	defer work.Close()
	output, err := createImageBuildRoot(expected.Output, "output")
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, err)
	}
	defer output.Close()

	result, err := image.Build(cmd.Context(), image.BuildInput{
		Binaries:      binaries,
		Source:        input.FS(),
		Work:          work,
		Output:        output,
		Version:       expected.Version,
		BuildDate:     expected.BuildDate,
		Namespace:     expected.Namespace,
		SourceURL:     expected.SourceURL,
		Commit:        expected.Commit,
		Reference:     expected.Reference,
		MelangeConfig: melangeConfig,
		ApkoConfig:    apkoConfig,
	}, apk, composer)
	if err != nil {
		return writeCommandResult(options, commandImageBuild, nil, err)
	}
	if options.settings == nil || !options.settings.JSON {
		return nil
	}

	return writeCommandResult(options, commandImageBuild, result, nil)
}

// loadImageBuildBinaries decodes the staged projection and converts it for [image.Build].
func loadImageBuildBinaries(input *os.Root) ([]image.BuildBinary, error) {
	projection, err := openImageInputs(input)
	if err != nil {
		return nil, err
	}

	return imageBuildBinaries(projection)
}

// openImageBuildConfigs opens the Melange and apko configuration files.
func openImageBuildConfigs(expected imageBuildConfig) (*os.File, *os.File, error) {
	melangeConfig, err := os.Open(expected.MelangeConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("open Melange config %s: %w", expected.MelangeConfig, err)
	}
	apkoConfig, err := os.Open(expected.ApkoConfig)
	if err != nil {
		_ = melangeConfig.Close()
		return nil, nil, fmt.Errorf("open apko config %s: %w", expected.ApkoConfig, err)
	}

	return melangeConfig, apkoConfig, nil
}

// imageBuildPorts returns the injected or constructed APK and compose ports.
func imageBuildPorts(options Options, expected imageBuildConfig) (image.APKBuilder, image.Composer, error) {
	apk, err := apkBuilder(options, expected.MelangePath)
	if err != nil {
		return nil, nil, err
	}
	composer, err := imageComposer(options, expected.ApkoPath)
	if err != nil {
		return nil, nil, err
	}

	return apk, composer, nil
}

// createImageBuildRoot creates path and opens it as a confined root.
func createImageBuildRoot(path, kind string) (*os.Root, error) {
	if err := os.MkdirAll(path, dirMode); err != nil {
		return nil, fmt.Errorf("create %s %s: %w", kind, path, err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open %s %s: %w", kind, path, err)
	}

	return root, nil
}

// imageBuildConfig is the resolved image-build configuration.
type imageBuildConfig struct {
	// Input is the extracted oci-input artifact root.
	Input string
	// Work is the scratch workspace.
	Work string
	// Output is the authoritative artifact output root.
	Output string
	// MelangeConfig is the Melange configuration path.
	MelangeConfig string
	// ApkoConfig is the apko configuration path.
	ApkoConfig string
	// BuildDate is the RFC 3339 reproducible build timestamp.
	BuildDate string
	// Version is the candidate stable release version.
	Version rel.Version
	// Namespace is the APK namespace from GITHUB_REPOSITORY_OWNER.
	Namespace string
	// SourceURL is the provenance repository URL.
	SourceURL string
	// Commit is the provenance commit SHA from GITHUB_SHA.
	Commit string
	// Reference is the local image reference.
	Reference string
	// MelangePath is RELEASE_MELANGE_PATH. Empty resolves melange from PATH.
	MelangePath string
	// ApkoPath is RELEASE_APKO_PATH. Empty resolves apko from PATH.
	ApkoPath string
}

// resolveImageBuild parses flags and Actions environment into an image-build config.
//
// It performs no I/O.
func resolveImageBuild(options Options) (imageBuildConfig, error) {
	settings := Settings{}
	if options.settings != nil {
		settings = *options.settings
	}
	if err := settings.err; err != nil {
		return imageBuildConfig{}, err
	}
	if settings.Input == "" {
		return imageBuildConfig{}, fmt.Errorf("--%s is required", flagInput)
	}
	if settings.Work == "" {
		return imageBuildConfig{}, fmt.Errorf("--%s is required", flagWork)
	}
	if settings.Output == "" {
		return imageBuildConfig{}, fmt.Errorf("--%s is required", flagOutput)
	}
	if err := requireDisjointImageRoots(settings.Work, settings.Output); err != nil {
		return imageBuildConfig{}, err
	}
	if settings.BuildDate == "" {
		return imageBuildConfig{}, fmt.Errorf("--%s is required", flagBuildDate)
	}
	if _, err := time.Parse(time.RFC3339, settings.BuildDate); err != nil {
		return imageBuildConfig{}, fmt.Errorf("--%s must be RFC 3339: %w", flagBuildDate, err)
	}

	version, err := resolvePlanVersion(settings, options.LookupEnv)
	if err != nil {
		return imageBuildConfig{}, err
	}
	namespace, err := requiredEnv(options.LookupEnv, envRepositoryOwner)
	if err != nil {
		return imageBuildConfig{}, err
	}
	serverURL, err := requiredEnv(options.LookupEnv, envServerURL)
	if err != nil {
		return imageBuildConfig{}, err
	}
	repository, err := requiredEnv(options.LookupEnv, envRepository)
	if err != nil {
		return imageBuildConfig{}, err
	}
	commit, err := requiredEnv(options.LookupEnv, envCommitSHA)
	if err != nil {
		return imageBuildConfig{}, err
	}
	reference, err := localImageReference(repository, version)
	if err != nil {
		return imageBuildConfig{}, err
	}

	return imageBuildConfig{
		Input:         settings.Input,
		Work:          settings.Work,
		Output:        settings.Output,
		MelangeConfig: settings.MelangeConfig,
		ApkoConfig:    settings.ApkoConfig,
		BuildDate:     settings.BuildDate,
		Version:       version,
		Namespace:     namespace,
		SourceURL:     strings.TrimRight(serverURL, "/") + "/" + repository,
		Commit:        commit,
		Reference:     reference,
		MelangePath:   lookupValue(options.LookupEnv, envMelangePath),
		ApkoPath:      lookupValue(options.LookupEnv, envApkoPath),
	}, nil
}

// requireDisjointImageRoots rejects a work root that overlaps the output root.
//
// Equal paths and either path nested under the other would put the ephemeral
// APK signing private key inside the published output tree. Comparison uses
// cleaned absolute paths and a separator-aware prefix so /a/outx is not
// treated as nested under /a/out.
func requireDisjointImageRoots(work, output string) error {
	absWork, err := absoluteImageRoot(work)
	if err != nil {
		return fmt.Errorf("--%s path: %w", flagWork, err)
	}
	absOutput, err := absoluteImageRoot(output)
	if err != nil {
		return fmt.Errorf("--%s path: %w", flagOutput, err)
	}
	if absWork == absOutput || nestedImageRoot(absWork, absOutput) || nestedImageRoot(absOutput, absWork) {
		return fmt.Errorf("--%s and --%s must be disjoint", flagWork, flagOutput)
	}

	return nil
}

// absoluteImageRoot returns the cleaned absolute form of path.
func absoluteImageRoot(path string) (string, error) {
	return filepath.Abs(filepath.Clean(path))
}

// nestedImageRoot reports whether path is inside root using a path separator.
func nestedImageRoot(path, root string) bool {
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

// localImageReference builds local/<name-after-slash>:<version> from GITHUB_REPOSITORY.
func localImageReference(repository string, version rel.Version) (string, error) {
	_, name, found := strings.Cut(repository, "/")
	if !found || name == "" {
		return "", fmt.Errorf("%s %q is missing a repository name", envRepository, repository)
	}

	return localImagePrefix + name + ":" + version.String(), nil
}

// openImageInputs decodes <input>/oci-build-inputs.json from root.
func openImageInputs(root *os.Root) (stage.ImageInputs, error) {
	file, err := root.Open(stage.ImageInputsName)
	if err != nil {
		return stage.ImageInputs{}, fmt.Errorf("open %s: %w", stage.ImageInputsName, err)
	}
	inputs, err := stage.DecodeImageInputs(file)
	closeErr := file.Close()
	if err != nil {
		return stage.ImageInputs{}, err
	}
	if closeErr != nil {
		return stage.ImageInputs{}, fmt.Errorf("close %s: %w", stage.ImageInputsName, closeErr)
	}

	return inputs, nil
}

// imageBuildBinaries converts a decoded projection into engine binaries.
func imageBuildBinaries(inputs stage.ImageInputs) ([]image.BuildBinary, error) {
	binaries := make([]image.BuildBinary, 0, len(inputs.Binaries))
	for _, binary := range inputs.Binaries {
		platform, err := image.ParsePlatform(binary.Platform)
		if err != nil {
			return nil, err
		}
		digest, err := rel.ParseDigest(binary.Digest)
		if err != nil {
			return nil, err
		}
		binaries = append(binaries, image.BuildBinary{
			Platform: platform,
			Name:     binary.Name,
			Path:     binary.Path,
			Digest:   digest,
		})
	}

	return binaries, nil
}

// apkBuilder returns the injected APK-build port or constructs one.
func apkBuilder(options Options, path string) (image.APKBuilder, error) {
	if options.APKBuilder != nil {
		return options.APKBuilder, nil
	}
	if options.NewAPKBuilder == nil {
		return nil, errors.New("apk builder factory is not configured")
	}

	builder, err := options.NewAPKBuilder(path)
	if err != nil {
		return nil, UsageError(fmt.Errorf("melange: %w", err))
	}
	if builder == nil {
		return nil, errors.New("apk builder factory returned nil")
	}

	return builder, nil
}

// imageComposer returns the injected compose port or constructs one.
func imageComposer(options Options, path string) (image.Composer, error) {
	if options.Composer != nil {
		return options.Composer, nil
	}
	if options.NewComposer == nil {
		return nil, errors.New("composer factory is not configured")
	}

	composer, err := options.NewComposer(path)
	if err != nil {
		return nil, UsageError(fmt.Errorf("apko: %w", err))
	}
	if composer == nil {
		return nil, errors.New("composer factory returned nil")
	}

	return composer, nil
}
