package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/meigma/release/internal/rel"
)

const (
	// BuildSchema is the versioned image-build result identifier.
	BuildSchema = "release.dev/image-build/v2"
	// sourcesDir is the scratch directory that holds per-architecture trees.
	sourcesDir = "sources"
	// configurationDir is the output directory for copied build configs.
	configurationDir = "configuration"
	// packagesDir is the APK repository output directory.
	packagesDir = "packages"
	// layoutDir is the OCI layout output directory.
	layoutDir = "layout"
	// layoutRel is the Dir-relative layout path passed to [Composer].
	layoutRel = "layout/"
	// sbomsDir is the SBOM output directory.
	sbomsDir = "sboms"
	// varsFile is the Melange vars file written into the scratch workspace.
	varsFile = "vars.json"
	// signingKeyFile is the ephemeral APK signing key path in the workspace.
	signingKeyFile = "apk-signing.rsa"
	// signingPubFile is the generated APK signing public key basename.
	signingPubFile = "apk-signing.rsa.pub"
	// melangeConfigFile is the copied Melange configuration basename.
	melangeConfigFile = "melange.yaml"
	// apkoConfigFile is the copied apko configuration basename.
	apkoConfigFile = "apko.yaml"
	// lockfileName is the Dir-relative apko lock output path.
	lockfileName = "apko.lock.json"
	// checksumsFile is the GNU sha256sum listing of staged binary files.
	checksumsFile = "canonical-binaries.sha256"
	// apkIndexFile is the APK index basename written per architecture.
	apkIndexFile = "APKINDEX.tar.gz"
	// apkSuffix is the APK package filename suffix.
	apkSuffix = ".apk"
	// dockerRunner is the container runner passed to [APKBuilder].
	dockerRunner = "docker"
	// annotationVersion is the OCI version annotation key.
	annotationVersion = "org.opencontainers.image.version"
	// annotationRevision is the OCI revision annotation key.
	annotationRevision = "org.opencontainers.image.revision"
	// fileModeExecutable is the mode of each staged binary file.
	fileModeExecutable = 0o755
	// fileModeFile is the mode of copied configuration, checksums, and keys.
	fileModeFile = 0o644
	// dirMode is the mode of directories created under work and output.
	dirMode = 0o755
)

// BuildBinary is one canonical Linux binary fact supplied to [Build].
type BuildBinary struct {
	// Platform is the Linux OCI platform this binary was built for.
	Platform Platform
	// Name is the binary filename for this platform.
	Name string
	// Path is the Source-relative confined path of the staged file.
	Path string
	// Digest is the expected canonical digest of the file at Path.
	Digest rel.Digest
}

// BuildInput is the staged binaries, configs, and roots [Build] consumes.
type BuildInput struct {
	// Binaries are the canonical Linux facts. [Build] requires a nonempty
	// identical name set on [PlatformAMD64] and [PlatformARM64].
	Binaries []BuildBinary
	// Source is the extracted oci-input artifact root.
	Source fs.FS
	// Work is the scratch workspace root.
	Work *os.Root
	// Output is the authoritative artifact output root.
	Output *os.Root
	// Version is the candidate MAJOR.MINOR.PATCH release.
	Version rel.Version
	// BuildDate is the RFC 3339 reproducible build timestamp.
	BuildDate string
	// Namespace is the APK namespace, typically the GitHub owner.
	Namespace string
	// SourceURL is the provenance repository URL.
	SourceURL string
	// Commit is the provenance commit SHA.
	Commit string
	// Reference is the local image reference, e.g. "local/release:1.2.3".
	Reference string
	// MelangeConfig is the Melange configuration document to copy.
	MelangeConfig io.Reader
	// ApkoConfig is the apko configuration document to copy.
	ApkoConfig io.Reader
}

// BuildResult is the versioned document produced by [Build].
type BuildResult struct {
	// Schema identifies the image-build result version and is always [BuildSchema].
	Schema string `json:"schema"`
	// Version is the candidate MAJOR.MINOR.PATCH version.
	Version string `json:"version"`
	// Binaries are the staged binary filenames, sorted name-ascending.
	Binaries []string `json:"binaries"`
	// Work is the scratch workspace path, [os.Root.Name] of the work root.
	Work string `json:"work"`
	// Output is the authoritative output path, [os.Root.Name] of the output root.
	Output string `json:"output"`
	// BuildDate is the RFC 3339 reproducible build timestamp.
	BuildDate string `json:"build_date"`
	// Packages are the per-architecture APK facts in canonical platform order.
	Packages []PackageResult `json:"packages"`
}

// PackageResult is one architecture's signed APK recorded by [Build].
type PackageResult struct {
	// Platform is the Linux OCI platform, such as linux/amd64.
	Platform string `json:"platform"`
	// Arch is the APK architecture, such as x86_64.
	Arch string `json:"arch"`
	// Package is the output-relative APK path, packages/<arch>/<file>.apk.
	Package string `json:"package"`
	// BinaryDigests are the verified canonical digests, sorted by name.
	BinaryDigests []BinaryDigest `json:"binary_digests"`
}

// BinaryDigest is one staged binary's verified digest.
type BinaryDigest struct {
	// Name is the binary filename.
	Name string `json:"name"`
	// Digest is the verified canonical digest of the staged file.
	Digest string `json:"digest"`
}

// stagedBinary is one platform's validated input plus its verified digest.
type stagedBinary struct {
	// platform is the Linux OCI platform.
	platform Platform
	// arch is the APK architecture that platform maps onto.
	arch APKArch
	// name is the binary filename for this platform.
	name string
	// path is the Source-relative confined path.
	path string
	// digest is the verified canonical digest of the staged file.
	digest rel.Digest
}

// Build stages binaries, builds signed APK repositories, and composes an OCI layout.
//
// It fails closed before any write when the input is incomplete or malformed:
// a nil context, root, reader, or port; a nonempty binary list whose name set
// is not identical across [PlatformAMD64] and [PlatformARM64]; a duplicate
// (platform, name) pair; a zero version or digest; a BuildDate that is not
// RFC 3339; or an empty Namespace, SourceURL, Commit, or Reference. Both the
// work and output roots must contain no entries; a pre-existing entry is
// refused and names the populated root and one offending entry. Workspace and
// output directories are then created with [os.Root.Mkdir] and must not
// already exist; only the intermediate work/sources element uses MkdirAll.
//
// Each binary is streamed once through SHA-256 into
// work/sources/<apkarch>/<binary-name> at mode 0755. The computed digest must
// match the expected digest. The written file is then parsed as ELF and must
// be a static 64-bit little-endian ET_EXEC for that architecture.
//
// After staging, Build writes work/vars.json, copies the Melange and apko
// configs to output/configuration, and writes output/canonical-binaries.sha256
// in GNU coreutils form with x86_64 first and names ascending within each
// architecture. It then calls [APKBuilder.Build] and requires the returned
// repository root and public key to match the request, plus exactly one
// nonempty APK and a nonempty APKINDEX.tar.gz per architecture. The public
// key is copied to the output root. [Composer.Build] then locks and writes
// the layout. Build requires apko.lock.json, layout/index.json,
// layout/oci-layout, and both architecture SBOMs to be nonempty regular
// files. It does not parse those files and does not write image-digest.txt.
//
// Platforms are processed in canonical order: linux/amd64, then linux/arm64.
func Build(ctx context.Context, input BuildInput, apk APKBuilder, composer Composer) (BuildResult, error) {
	staged, err := validateBuild(ctx, input, apk, composer)
	if err != nil {
		return BuildResult{}, err
	}
	err = createWorkspace(input.Work, input.Output)
	if err != nil {
		return BuildResult{}, err
	}

	for i := range staged {
		err = stageBinary(input.Source, input.Work, &staged[i])
		if err != nil {
			return BuildResult{}, err
		}
	}

	err = writeVars(input.Work, input.Version)
	if err != nil {
		return BuildResult{}, err
	}
	err = copyConfig(input.Output, path.Join(configurationDir, melangeConfigFile), input.MelangeConfig)
	if err != nil {
		return BuildResult{}, fmt.Errorf("copy Melange config: %w", err)
	}
	err = copyConfig(input.Output, path.Join(configurationDir, apkoConfigFile), input.ApkoConfig)
	if err != nil {
		return BuildResult{}, fmt.Errorf("copy apko config: %w", err)
	}
	err = writeChecksums(input.Output, staged)
	if err != nil {
		return BuildResult{}, err
	}

	request := apkRequest(input, staged)
	repos, err := apk.Build(ctx, request)
	if err != nil {
		return BuildResult{}, fmt.Errorf("build APK repositories: %w", err)
	}
	packages, err := checkRepositories(input.Output, request, repos, staged)
	if err != nil {
		return BuildResult{}, err
	}
	err = copyPublicKey(input.Work, input.Output)
	if err != nil {
		return BuildResult{}, err
	}

	compose := composeRequest(input)
	err = composer.Build(ctx, compose)
	if err != nil {
		return BuildResult{}, fmt.Errorf("compose image: %w", err)
	}
	err = requireLayout(input.Output)
	if err != nil {
		return BuildResult{}, err
	}

	return BuildResult{
		Schema:    BuildSchema,
		Version:   input.Version.String(),
		Binaries:  stagedBinaryNames(staged),
		Work:      input.Work.Name(),
		Output:    input.Output.Name(),
		BuildDate: input.BuildDate,
		Packages:  packages,
	}, nil
}

// validateBuild rejects a nil context, root, reader, or port and malformed facts.
func validateBuild(ctx context.Context, input BuildInput, apk APKBuilder, composer Composer) ([]stagedBinary, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if input.Source == nil {
		return nil, errors.New("source is nil")
	}
	if input.Work == nil {
		return nil, errors.New("work root is nil")
	}
	if input.Output == nil {
		return nil, errors.New("output root is nil")
	}
	if input.MelangeConfig == nil {
		return nil, errors.New("melange configuration reader is nil")
	}
	if input.ApkoConfig == nil {
		return nil, errors.New("apko config is nil")
	}
	if apk == nil {
		return nil, errors.New("apk builder is nil")
	}
	if composer == nil {
		return nil, errors.New("composer is nil")
	}
	if input.Version == (rel.Version{}) {
		return nil, errors.New("version is zero")
	}
	if _, err := time.Parse(time.RFC3339, input.BuildDate); err != nil {
		return nil, fmt.Errorf("build date %q is not RFC 3339: %w", input.BuildDate, err)
	}
	if input.Namespace == "" {
		return nil, errors.New("namespace is empty")
	}
	if input.SourceURL == "" {
		return nil, errors.New("source URL is empty")
	}
	if input.Commit == "" {
		return nil, errors.New("commit is empty")
	}
	if input.Reference == "" {
		return nil, errors.New("reference is empty")
	}

	return validateBinaries(input.Binaries)
}

// validateBinaries requires a nonempty identical name set on both platforms.
func validateBinaries(binaries []BuildBinary) ([]stagedBinary, error) {
	if len(binaries) == 0 {
		return nil, errors.New("binaries is empty")
	}

	type platformName struct {
		// platform is the Linux OCI platform.
		platform Platform
		// name is the binary filename.
		name string
	}
	seen := make(map[platformName]struct{}, len(binaries))
	namesByPlatform := map[Platform]map[string]struct{}{
		PlatformAMD64: {},
		PlatformARM64: {},
	}
	byKey := make(map[platformName]BuildBinary, len(binaries))
	for _, binary := range binaries {
		platform, err := ParsePlatform(binary.Platform.String())
		if err != nil {
			return nil, err
		}
		if err := validateBinaryName(binary.Name); err != nil {
			return nil, err
		}
		key := platformName{platform: platform, name: binary.Name}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate platform %s name %q", platform, binary.Name)
		}
		seen[key] = struct{}{}
		if !filepath.IsLocal(binary.Path) {
			return nil, fmt.Errorf("path %q is not a confined local path", binary.Path)
		}
		if binary.Digest == "" {
			return nil, fmt.Errorf("digest for %s %q is empty", platform, binary.Name)
		}
		namesByPlatform[platform][binary.Name] = struct{}{}
		byKey[key] = binary
	}

	names, err := matchingBinaryNames(namesByPlatform)
	if err != nil {
		return nil, err
	}

	staged := make([]stagedBinary, 0, len(requiredPlatforms())*len(names))
	for _, platform := range requiredPlatforms() {
		for _, name := range names {
			binary := byKey[platformName{platform: platform, name: name}]
			staged = append(staged, stagedBinary{
				platform: platform,
				arch:     platform.APKArch(),
				name:     binary.Name,
				path:     binary.Path,
				digest:   binary.Digest,
			})
		}
	}

	return staged, nil
}

// matchingBinaryNames requires every name on both required platforms.
func matchingBinaryNames(namesByPlatform map[Platform]map[string]struct{}) ([]string, error) {
	union := make(map[string]struct{})
	for _, names := range namesByPlatform {
		for name := range names {
			union[name] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(union))
	for name := range union {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	if len(ordered) == 0 {
		return nil, errors.New("binaries is empty")
	}

	for _, platform := range requiredPlatforms() {
		have := namesByPlatform[platform]
		for _, name := range ordered {
			if _, ok := have[name]; !ok {
				return nil, fmt.Errorf("missing binary %q for %s", name, platform)
			}
		}
	}

	return ordered, nil
}

// validateBinaryName rejects an empty name or a name that contains a path separator.
func validateBinaryName(name string) error {
	if name == "" {
		return errors.New("binary name is empty")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("binary name %q contains a path separator", name)
	}

	return nil
}

// requiredPlatforms returns the closed platform set in canonical order.
func requiredPlatforms() []Platform {
	return []Platform{PlatformAMD64, PlatformARM64}
}

// createWorkspace creates the scratch and output directories.
//
// Both roots must be empty before any child is created so a populated
// workspace cannot leak into the uploaded artifact. An already-existing
// leaf directory is then an error. Only work/sources is created with MkdirAll.
func createWorkspace(work, output *os.Root) error {
	if err := requireEmptyRoot(work, "work"); err != nil {
		return err
	}
	if err := requireEmptyRoot(output, "output"); err != nil {
		return err
	}
	if err := work.MkdirAll(sourcesDir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", sourcesDir, err)
	}
	for _, arch := range []APKArch{ArchX8664, ArchAArch64} {
		name := path.Join(sourcesDir, arch.String())
		if err := work.Mkdir(name, dirMode); err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
	}
	for _, name := range []string{configurationDir, packagesDir, layoutDir, sbomsDir} {
		if err := output.Mkdir(name, dirMode); err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
	}

	return nil
}

// requireEmptyRoot refuses a work or output root that already contains an entry.
func requireEmptyRoot(root *os.Root, label string) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return fmt.Errorf("read %s root: %w", label, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s root is populated: %s", label, entries[0].Name())
	}

	return nil
}

// stageBinary streams one source file into the workspace and verifies it.
func stageBinary(source fs.FS, work *os.Root, binary *stagedBinary) error {
	info, err := fs.Stat(source, binary.path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", binary.path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", binary.path)
	}

	src, err := source.Open(binary.path)
	if err != nil {
		return fmt.Errorf("open %s: %w", binary.path, err)
	}
	defer src.Close()

	stagedPath := path.Join(sourcesDir, binary.arch.String(), binary.name)
	dest, err := work.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileModeExecutable)
	if err != nil {
		return fmt.Errorf("create %s: %w", stagedPath, err)
	}

	sum := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(dest, sum), src)
	chmodErr := dest.Chmod(fileModeExecutable)
	closeErr := dest.Close()
	if copyErr != nil {
		return fmt.Errorf("stage %s: %w", stagedPath, copyErr)
	}
	if chmodErr != nil {
		return fmt.Errorf("chmod %s: %w", stagedPath, chmodErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", stagedPath, closeErr)
	}

	actual, err := rel.ParseDigest("sha256:" + hex.EncodeToString(sum.Sum(nil)))
	if err != nil {
		return fmt.Errorf("digest %s: %w", stagedPath, err)
	}
	if actual != binary.digest {
		return fmt.Errorf(
			"binary %s for %s has digest %s, expected %s",
			binary.name,
			binary.platform,
			actual,
			binary.digest,
		)
	}
	binary.digest = actual

	staged, err := work.Open(stagedPath)
	if err != nil {
		return fmt.Errorf("open staged %s: %w", stagedPath, err)
	}
	defer staged.Close()

	readerAt, ok := any(staged).(io.ReaderAt)
	if !ok {
		return fmt.Errorf("staged %s does not provide io.ReaderAt", stagedPath)
	}
	if err := verifyELF(binary.arch, readerAt); err != nil {
		return fmt.Errorf("binary %s for %s: %w", binary.name, binary.platform, err)
	}

	return nil
}

// writeVars writes work/vars.json as {"version":"<version>"} plus a newline.
func writeVars(work *os.Root, version rel.Version) error {
	payload := []byte(`{"version":"` + version.String() + `"}` + "\n")
	if err := work.WriteFile(varsFile, payload, fileModeFile); err != nil {
		return fmt.Errorf("write %s: %w", varsFile, err)
	}

	return nil
}

// copyConfig writes r to output/name at mode 0644.
func copyConfig(output *os.Root, name string, r io.Reader) error {
	file, err := output.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileModeFile)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}

	_, copyErr := io.Copy(file, r)
	chmodErr := file.Chmod(fileModeFile)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write %s: %w", name, copyErr)
	}
	if chmodErr != nil {
		return fmt.Errorf("chmod %s: %w", name, chmodErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", name, closeErr)
	}

	return nil
}

// writeChecksums writes output/canonical-binaries.sha256 in GNU coreutils form.
func writeChecksums(output *os.Root, staged []stagedBinary) error {
	var b strings.Builder
	for _, binary := range staged {
		hexDigest, ok := strings.CutPrefix(binary.digest.String(), "sha256:")
		if !ok {
			return fmt.Errorf("digest %s is missing the sha256: prefix", binary.digest)
		}
		relPath := path.Join(sourcesDir, binary.arch.String(), binary.name)
		b.WriteString(hexDigest)
		b.WriteString("  ")
		b.WriteString(relPath)
		b.WriteByte('\n')
	}
	if err := output.WriteFile(checksumsFile, []byte(b.String()), fileModeFile); err != nil {
		return fmt.Errorf("write %s: %w", checksumsFile, err)
	}

	return nil
}

// apkRequest builds the [APKBuilder] request from validated input.
func apkRequest(input BuildInput, staged []stagedBinary) APKBuildRequest {
	seen := make(map[APKArch]struct{}, len(requiredPlatforms()))
	sources := make([]APKBuildSource, 0, len(requiredPlatforms()))
	for _, binary := range staged {
		if _, exists := seen[binary.arch]; exists {
			continue
		}
		seen[binary.arch] = struct{}{}
		sources = append(sources, APKBuildSource{
			Arch: binary.arch,
			Dir:  filepath.Join(input.Work.Name(), sourcesDir, binary.arch.String()),
		})
	}

	return APKBuildRequest{
		Config:     filepath.Join(input.Output.Name(), configurationDir, melangeConfigFile),
		VarsFile:   filepath.Join(input.Work.Name(), varsFile),
		KeyPath:    filepath.Join(input.Work.Name(), signingKeyFile),
		OutDir:     filepath.Join(input.Output.Name(), packagesDir),
		Sources:    sources,
		Runner:     dockerRunner,
		Namespace:  input.Namespace,
		BuildDate:  input.BuildDate,
		GitRepoURL: input.SourceURL,
		GitCommit:  input.Commit,
	}
}

// checkRepositories cross-checks the builder output and records each APK.
func checkRepositories(
	output *os.Root,
	request APKBuildRequest,
	repos APKRepositories,
	staged []stagedBinary,
) ([]PackageResult, error) {
	if repos.Dir != request.OutDir {
		return nil, fmt.Errorf("apk repository dir %q does not match %q", repos.Dir, request.OutDir)
	}
	wantKey := request.KeyPath + ".pub"
	if repos.PublicKey != wantKey {
		return nil, fmt.Errorf("apk public key %q does not match %q", repos.PublicKey, wantKey)
	}

	packages := make([]PackageResult, 0, len(requiredPlatforms()))
	for _, platform := range requiredPlatforms() {
		arch := platform.APKArch()
		pkg, err := requirePackage(output, arch)
		if err != nil {
			return nil, err
		}
		packages = append(packages, PackageResult{
			Platform:      platform.String(),
			Arch:          arch.String(),
			Package:       pkg,
			BinaryDigests: packageBinaryDigests(staged, platform),
		})
	}

	return packages, nil
}

// packageBinaryDigests returns name-sorted digests for platform.
func packageBinaryDigests(staged []stagedBinary, platform Platform) []BinaryDigest {
	out := make([]BinaryDigest, 0)
	for _, binary := range staged {
		if binary.platform != platform {
			continue
		}
		out = append(out, BinaryDigest{Name: binary.name, Digest: binary.digest.String()})
	}
	slices.SortFunc(out, func(a, b BinaryDigest) int {
		return strings.Compare(a.Name, b.Name)
	})

	return out
}

// stagedBinaryNames returns unique binary names in ascending order.
func stagedBinaryNames(staged []stagedBinary) []string {
	seen := make(map[string]struct{}, len(staged))
	var names []string
	for _, binary := range staged {
		if _, exists := seen[binary.name]; exists {
			continue
		}
		seen[binary.name] = struct{}{}
		names = append(names, binary.name)
	}
	slices.Sort(names)

	return names
}

// requirePackage requires a nonempty APKINDEX and exactly one nonempty APK for arch.
func requirePackage(output *os.Root, arch APKArch) (string, error) {
	index := path.Join(packagesDir, arch.String(), apkIndexFile)
	if err := requireRegularNonempty(output.FS(), index); err != nil {
		return "", err
	}

	dir := path.Join(packagesDir, arch.String())
	entries, err := fs.ReadDir(output.FS(), dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}

	var apks []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), apkSuffix) {
			continue
		}
		apks = append(apks, entry.Name())
	}
	if len(apks) != 1 {
		return "", fmt.Errorf("%s: found %d apk files, want 1", dir, len(apks))
	}

	pkg := path.Join(dir, apks[0])
	if err := requireRegularNonempty(output.FS(), pkg); err != nil {
		return "", err
	}

	return pkg, nil
}

// copyPublicKey copies the generated signing public key onto the output root.
func copyPublicKey(work, output *os.Root) error {
	src, err := work.Open(signingPubFile)
	if err != nil {
		return fmt.Errorf("open %s: %w", signingPubFile, err)
	}
	defer src.Close()

	if err := copyConfig(output, signingPubFile, src); err != nil {
		return fmt.Errorf("copy %s: %w", signingPubFile, err)
	}

	return nil
}

// composeRequest builds the [Composer] request from validated input.
func composeRequest(input BuildInput) ComposeRequest {
	return ComposeRequest{
		Dir:        input.Output.Name(),
		Config:     path.Join(configurationDir, apkoConfigFile),
		Repository: packagesDir,
		Keyring:    signingPubFile,
		Lockfile:   lockfileName,
		SBOMPath:   sbomsDir,
		Layout:     layoutRel,
		Reference:  input.Reference,
		BuildDate:  input.BuildDate,
		Arches:     []APKArch{ArchX8664, ArchAArch64},
		Annotations: []Annotation{
			{Key: annotationVersion, Value: input.Version.String()},
			{Key: annotationRevision, Value: input.Commit},
		},
	}
}

// requireLayout requires the lockfile, layout files, and both SBOMs.
func requireLayout(output *os.Root) error {
	for _, name := range []string{
		lockfileName,
		path.Join(layoutDir, "index.json"),
		path.Join(layoutDir, "oci-layout"),
		path.Join(sbomsDir, "sbom-"+ArchX8664.String()+".spdx.json"),
		path.Join(sbomsDir, "sbom-"+ArchAArch64.String()+".spdx.json"),
	} {
		if err := requireRegularNonempty(output.FS(), name); err != nil {
			return err
		}
	}

	return nil
}

// requireRegularNonempty requires name to exist as a nonempty regular file.
func requireRegularNonempty(fsys fs.FS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty", name)
	}

	return nil
}
