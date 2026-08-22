package pkgrepo

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
	"sort"
	"strings"
	"time"

	"github.com/meigma/release/internal/rel"
)

const (
	// workAssetsDir holds confined package copies inspected by adapters.
	workAssetsDir = "assets"
	// keysDir is the public key directory in both work and output roots.
	keysDir = "keys"
	// directoryMode is the mode used for generated directories.
	directoryMode = 0o755
	// fileMode is the mode used for package and public-key copies.
	fileMode = 0o644
	// artifactOrderPackage publishes immutable package bytes first.
	artifactOrderPackage = 0
	// artifactOrderImmutable publishes immutable metadata and keys second.
	artifactOrderImmutable = 1
	// artifactOrderMutable publishes non-root mutable metadata third.
	artifactOrderMutable = 2
	// artifactOrderCommitRoot publishes repository activation roots last.
	artifactOrderCommitRoot = 3
)

// BuildInput supplies package assets, roots, configuration, and deterministic times.
type BuildInput struct {
	// Config is the reviewed producer and key allowlist.
	Config Config
	// Request identifies the producer tag that must be complete.
	Request Request
	// Assets is the complete package pool used to regenerate metadata.
	Assets []Asset
	// Source confines every asset and public-key input path.
	Source *os.Root
	// Work is an empty scratch root used for verified package and key copies.
	Work *os.Root
	// Output is an empty root that receives the complete public repository tree.
	Output *os.Root
	// ReleaseTime is the deterministic metadata timestamp.
	ReleaseTime time.Time
	// ValidUntil is the deterministic APT metadata expiry timestamp.
	ValidUntil time.Time
}

// stagedKeys maps reviewed key identities onto confined absolute scratch paths.
type stagedKeys struct {
	// public contains every unique public key in configuration order.
	public []PublicKey
	// byPublished maps a published key name to its absolute scratch path.
	byPublished map[string]string
	// producers maps producer repositories to their RPM and APK scratch keys.
	producers map[Repository]producerKeys
}

// producerKeys contains one producer's native package verification keys.
type producerKeys struct {
	// rpm is the absolute RPM public-key path.
	rpm string
	// apk is the absolute APK public-key path.
	apk string
}

// Build verifies package bytes and native signatures, plans canonical paths,
// and generates a complete signed local APT, RPM/DNF, and APK repository tree.
//
// Source paths are never passed directly to external adapters. Build streams
// every package and public key through [os.Root] into an empty scratch root,
// verifies each package digest, then passes only those confined copies to the
// ports. Output remains empty until configuration, package metadata, digests,
// signatures, allowlists, completeness, and path conflicts all pass.
func Build(
	ctx context.Context,
	input BuildInput,
	inspector Inspector,
	verifier Verifier,
	generator Generator,
	signer Signer,
) (BuildResult, error) {
	if err := validateBuildInput(ctx, input, inspector, verifier, generator, signer); err != nil {
		return BuildResult{}, err
	}
	if err := requireEmptyRoot(input.Work, "work"); err != nil {
		return BuildResult{}, err
	}
	if err := requireEmptyRoot(input.Output, "output"); err != nil {
		return BuildResult{}, err
	}

	keys, stageKeysErr := stagePublicKeys(input.Source, input.Work, input.Config)
	if stageKeysErr != nil {
		return BuildResult{}, stageKeysErr
	}
	inspected, inspectErr := stageAndInspectAssets(ctx, input, keys, inspector, verifier)
	if inspectErr != nil {
		return BuildResult{}, inspectErr
	}
	plan, planErr := PlanPackages(input.Config, input.Request, inspected)
	if planErr != nil {
		return BuildResult{}, planErr
	}
	if materializeErr := materializePlan(input.Work, input.Output, keys, plan); materializeErr != nil {
		return BuildResult{}, materializeErr
	}

	request := GenerateRequest{
		Root:        input.Output.Name(),
		Channel:     input.Config.Channel,
		ReleaseTime: input.ReleaseTime,
		ValidUntil:  input.ValidUntil,
	}
	if generateErr := generator.Generate(ctx, request); generateErr != nil {
		return BuildResult{}, fmt.Errorf("generate repository metadata: %w", generateErr)
	}
	if verifyErr := verifyMaterializedPackages(input.Output, plan); verifyErr != nil {
		return BuildResult{}, verifyErr
	}
	if signErr := signMetadata(ctx, input, signer); signErr != nil {
		return BuildResult{}, signErr
	}

	artifacts, inspectArtifactsErr := inspectArtifacts(input.Output, input.Config.Channel)
	if inspectArtifactsErr != nil {
		return BuildResult{}, inspectArtifactsErr
	}
	return BuildResult{Artifacts: artifacts}, nil
}

// validateBuildInput rejects incomplete or overlapping roots and malformed timestamps.
func validateBuildInput(
	ctx context.Context,
	input BuildInput,
	inspector Inspector,
	verifier Verifier,
	generator Generator,
	signer Signer,
) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if input.Source == nil {
		return errors.New("source root is nil")
	}
	if input.Work == nil {
		return errors.New("work root is nil")
	}
	if input.Output == nil {
		return errors.New("output root is nil")
	}
	if inspector == nil {
		return errors.New("package inspector is nil")
	}
	if verifier == nil {
		return errors.New("package verifier is nil")
	}
	if generator == nil {
		return errors.New("repository generator is nil")
	}
	if signer == nil {
		return errors.New("metadata signer is nil")
	}
	if err := input.Config.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if _, err := ParseTag(input.Request.Tag); err != nil {
		return err
	}
	if input.ReleaseTime.IsZero() {
		return errors.New("release time is zero")
	}
	if input.ValidUntil.IsZero() {
		return errors.New("valid-until time is zero")
	}
	if !input.ValidUntil.After(input.ReleaseTime) {
		return errors.New("valid-until time must follow release time")
	}
	if err := requireDistinctRoots(input.Source, input.Work, input.Output); err != nil {
		return err
	}

	return nil
}

// requireDistinctRoots rejects equal, nested, or symlink-aliased roots.
func requireDistinctRoots(roots ...*os.Root) error {
	resolved := make([]string, len(roots))
	for index, root := range roots {
		name, err := filepath.EvalSymlinks(root.Name())
		if err != nil {
			return fmt.Errorf("resolve root %s: %w", root.Name(), err)
		}
		resolved[index], err = filepath.Abs(name)
		if err != nil {
			return fmt.Errorf("make root absolute %s: %w", name, err)
		}
	}
	for left := range resolved {
		for right := left + 1; right < len(resolved); right++ {
			if pathsOverlap(resolved[left], resolved[right]) {
				return fmt.Errorf("roots overlap: %s and %s", resolved[left], resolved[right])
			}
		}
	}

	return nil
}

// pathsOverlap reports whether either absolute path contains the other.
func pathsOverlap(left, right string) bool {
	leftToRight, leftErr := filepath.Rel(left, right)
	rightToLeft, rightErr := filepath.Rel(right, left)
	if leftErr != nil || rightErr != nil {
		return left == right
	}

	return leftToRight == "." || !pathEscapes(leftToRight) || !pathEscapes(rightToLeft)
}

// pathEscapes reports whether a relative filesystem path leaves its base.
func pathEscapes(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
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

// stagePublicKeys copies every configured public key into the confined work root.
func stagePublicKeys(source, work *os.Root, config Config) (stagedKeys, error) {
	if err := work.Mkdir(keysDir, directoryMode); err != nil {
		return stagedKeys{}, fmt.Errorf("create work keys directory: %w", err)
	}

	keys := configuredPublicKeys(config)
	byPublished := make(map[string]string, len(keys))
	for _, key := range keys {
		destination := path.Join(keysDir, key.Published)
		if err := copyRootFile(source, key.Source, work, destination, nil); err != nil {
			return stagedKeys{}, fmt.Errorf("stage public key %s: %w", key.Published, err)
		}
		byPublished[key.Published] = absoluteRootPath(work, destination)
	}

	producerMap := make(map[Repository]producerKeys, len(config.Producers))
	for _, producer := range config.Producers {
		producerMap[producer.Repository] = producerKeys{
			rpm: byPublished[producer.RPMKey.Published],
			apk: byPublished[producer.APKKey.Published],
		}
	}

	return stagedKeys{public: keys, byPublished: byPublished, producers: producerMap}, nil
}

// configuredPublicKeys returns aggregate keys followed by producer keys in configuration order.
func configuredPublicKeys(config Config) []PublicKey {
	keys := []PublicKey{config.APTKey, config.RPMKey, config.APKKey}
	for _, producer := range config.Producers {
		keys = append(keys, producer.RPMKey, producer.APKKey)
	}

	return keys
}

// stageAndInspectAssets copies, hashes, inspects, and verifies all source packages.
func stageAndInspectAssets(
	ctx context.Context,
	input BuildInput,
	keys stagedKeys,
	inspector Inspector,
	verifier Verifier,
) ([]InspectedAsset, error) {
	if err := input.Work.Mkdir(workAssetsDir, directoryMode); err != nil {
		return nil, fmt.Errorf("create work assets directory: %w", err)
	}

	inspected := make([]InspectedAsset, 0, len(input.Assets))
	for index, asset := range input.Assets {
		item, err := stageAndInspectAsset(ctx, input, keys, inspector, verifier, index, asset)
		if err != nil {
			return nil, fmt.Errorf("asset %d: %w", index, err)
		}
		inspected = append(inspected, item)
	}

	return inspected, nil
}

// stageAndInspectAsset confines, hashes, inspects, and verifies one source package.
func stageAndInspectAsset(
	ctx context.Context,
	input BuildInput,
	keys stagedKeys,
	inspector Inspector,
	verifier Verifier,
	index int,
	asset Asset,
) (InspectedAsset, error) {
	if !fs.ValidPath(asset.Path) || asset.Path == "." {
		return InspectedAsset{}, fmt.Errorf("source path %q is not confined", asset.Path)
	}
	if _, err := rel.ParseDigest(asset.Digest.String()); err != nil {
		return InspectedAsset{}, fmt.Errorf("digest: %w", err)
	}
	extension, extensionErr := formatExtension(asset.Format)
	if extensionErr != nil {
		return InspectedAsset{}, extensionErr
	}
	stagedRelative := path.Join(workAssetsDir, fmt.Sprintf("%06d%s", index, extension))
	copyErr := copyRootFile(input.Source, asset.Path, input.Work, stagedRelative, &asset.Digest)
	if copyErr != nil {
		return InspectedAsset{}, fmt.Errorf("stage: %w", copyErr)
	}
	stagedPath := absoluteRootPath(input.Work, stagedRelative)
	metadata, inspectErr := inspector.Inspect(ctx, asset.Format, stagedPath)
	if inspectErr != nil {
		return InspectedAsset{}, fmt.Errorf("inspect: %w", inspectErr)
	}
	if asset.Format == FormatRPM || asset.Format == FormatAPK {
		producer, exists := keys.producers[asset.Repository]
		if !exists {
			return InspectedAsset{}, fmt.Errorf("repository %q is not allowlisted", asset.Repository)
		}
		verificationKey := producer.rpm
		if asset.Format == FormatAPK {
			verificationKey = producer.apk
		}
		verifyErr := verifier.Verify(ctx, VerificationRequest{
			Format:    asset.Format,
			Package:   stagedPath,
			PublicKey: verificationKey,
		})
		if verifyErr != nil {
			return InspectedAsset{}, fmt.Errorf("verify native signature: %w", verifyErr)
		}
	}

	return InspectedAsset{Asset: asset, Metadata: metadata, StagedPath: stagedPath}, nil
}

// formatExtension returns the canonical staging suffix for format.
func formatExtension(format Format) (string, error) {
	switch format {
	case FormatDEB:
		return ".deb", nil
	case FormatRPM:
		return ".rpm", nil
	case FormatAPK:
		return ".apk", nil
	default:
		return "", fmt.Errorf("package format %q is unsupported", format)
	}
}

// copyRootFile streams one confined regular source file into a confined destination.
func copyRootFile(
	source *os.Root,
	sourceName string,
	destination *os.Root,
	destinationName string,
	expected *rel.Digest,
) error {
	info, err := fs.Stat(source.FS(), sourceName)
	if err != nil {
		return fmt.Errorf("stat %s: %w", sourceName, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", sourceName)
	}

	input, err := source.Open(sourceName)
	if err != nil {
		return fmt.Errorf("open %s: %w", sourceName, err)
	}
	defer input.Close()

	output, err := destination.OpenFile(destinationName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", destinationName, err)
	}

	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hasher), input)
	chmodErr := output.Chmod(fileMode)
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %s: %w", sourceName, copyErr)
	}
	if chmodErr != nil {
		return fmt.Errorf("chmod %s: %w", destinationName, chmodErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", destinationName, closeErr)
	}
	if expected != nil {
		actual, err := rel.ParseDigest("sha256:" + hex.EncodeToString(hasher.Sum(nil)))
		if err != nil {
			return fmt.Errorf("parse digest for %s: %w", sourceName, err)
		}
		if actual != *expected {
			return fmt.Errorf("%s has digest %s, expected %s", sourceName, actual, *expected)
		}
	}

	return nil
}

// absoluteRootPath returns the native absolute path for one confined root-relative name.
func absoluteRootPath(root *os.Root, name string) string {
	return filepath.Join(root.Name(), filepath.FromSlash(name))
}

// materializePlan copies verified packages and public keys into the output tree.
func materializePlan(work, output *os.Root, keys stagedKeys, plan Plan) error {
	if err := output.Mkdir(keysDir, directoryMode); err != nil {
		return fmt.Errorf("create output keys directory: %w", err)
	}
	for _, key := range keys.public {
		sourceName := path.Join(keysDir, key.Published)
		if err := copyRootFile(work, sourceName, output, sourceName, nil); err != nil {
			return fmt.Errorf("publish key %s: %w", key.Published, err)
		}
	}

	createdDirs := make(map[string]struct{})
	for _, pkg := range plan.Packages {
		directory := path.Dir(pkg.Destination)
		if _, exists := createdDirs[directory]; !exists {
			if err := output.MkdirAll(directory, directoryMode); err != nil {
				return fmt.Errorf("create package directory %s: %w", directory, err)
			}
			createdDirs[directory] = struct{}{}
		}
		if err := materializePackage(output, pkg); err != nil {
			return err
		}
	}

	return nil
}

// materializePackage copies one verified scratch package into its canonical output path.
func materializePackage(output *os.Root, pkg PackagePlan) error {
	source, err := os.Open(pkg.Source)
	if err != nil {
		return fmt.Errorf("open staged package %s: %w", pkg.Source, err)
	}
	destination, err := output.OpenFile(pkg.Destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		createErr := fmt.Errorf("create package %s: %w", pkg.Destination, err)
		if closeErr := source.Close(); closeErr != nil {
			return errors.Join(createErr, fmt.Errorf("close staged package %s: %w", pkg.Source, closeErr))
		}
		return createErr
	}
	_, copyErr := io.Copy(destination, source)
	closeSourceErr := source.Close()
	chmodErr := destination.Chmod(fileMode)
	closeDestinationErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("copy package %s: %w", pkg.Destination, copyErr)
	}
	if closeSourceErr != nil {
		return fmt.Errorf("close staged package %s: %w", pkg.Source, closeSourceErr)
	}
	if chmodErr != nil {
		return fmt.Errorf("chmod package %s: %w", pkg.Destination, chmodErr)
	}
	if closeDestinationErr != nil {
		return fmt.Errorf("close package %s: %w", pkg.Destination, closeDestinationErr)
	}

	return nil
}

// verifyMaterializedPackages proves metadata tools preserved every verified package byte.
func verifyMaterializedPackages(output *os.Root, plan Plan) error {
	for _, pkg := range plan.Packages {
		actual, err := hashRootFile(output, pkg.Destination)
		if err != nil {
			return err
		}
		if actual != pkg.Digest {
			return fmt.Errorf("generated package %s has digest %s, expected %s", pkg.Destination, actual, pkg.Digest)
		}
	}

	return nil
}

// signMetadata signs APT and RPM roots, then removes the unpublished APT Release file.
func signMetadata(ctx context.Context, input BuildInput, signer Signer) error {
	channel := string(input.Config.Channel)
	aptRelease := path.Join("apt", "dists", channel, "Release")
	aptInRelease := path.Join("apt", "dists", channel, "InRelease")
	if err := signer.ClearSign(ctx, SignRequest{
		Input:  absoluteRootPath(input.Output, aptRelease),
		Output: absoluteRootPath(input.Output, aptInRelease),
		Time:   input.ReleaseTime,
	}); err != nil {
		return fmt.Errorf("sign APT metadata: %w", err)
	}
	for _, architecture := range []string{"x86_64", "aarch64"} {
		repomd := path.Join("rpm", channel, architecture, "repodata", "repomd.xml")
		if err := signer.DetachSign(ctx, SignRequest{
			Input:  absoluteRootPath(input.Output, repomd),
			Output: absoluteRootPath(input.Output, repomd+".asc"),
			Time:   input.ReleaseTime,
		}); err != nil {
			return fmt.Errorf("sign RPM metadata for %s: %w", architecture, err)
		}
	}
	if err := input.Output.Remove(aptRelease); err != nil {
		return fmt.Errorf("remove unsigned APT Release: %w", err)
	}

	return nil
}

// inspectArtifacts hashes regular output files and assigns cache and commit semantics.
func inspectArtifacts(root *os.Root, channel Channel) ([]Artifact, error) {
	artifacts := make([]Artifact, 0)
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat generated artifact %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("generated artifact %s is not a regular file", name)
		}
		digest, err := hashRootFile(root, name)
		if err != nil {
			return err
		}
		cache, commitRoot := artifactSemantics(name, channel)
		artifacts = append(artifacts, Artifact{
			Path:       name,
			Digest:     digest,
			Size:       info.Size(),
			Cache:      cache,
			CommitRoot: commitRoot,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect generated repository: %w", err)
	}
	if len(artifacts) == 0 {
		return nil, errors.New("generated repository is empty")
	}
	sort.Slice(artifacts, func(left, right int) bool {
		leftOrder := artifactOrder(artifacts[left])
		rightOrder := artifactOrder(artifacts[right])
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return artifacts[left].Path < artifacts[right].Path
	})

	return artifacts, nil
}

// hashRootFile streams one generated file through SHA-256.
func hashRootFile(root *os.Root, name string) (rel.Digest, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", fmt.Errorf("open generated artifact %s: %w", name, err)
	}
	defer file.Close()

	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	if copyErr != nil {
		return "", fmt.Errorf("hash generated artifact %s: %w", name, copyErr)
	}
	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(hasher.Sum(nil)))
	if err != nil {
		return "", fmt.Errorf("parse generated artifact digest %s: %w", name, err)
	}

	return digest, nil
}

// artifactSemantics assigns immutable caching and commit-root status by public path.
func artifactSemantics(name string, channel Channel) (CachePolicy, bool) {
	channelName := string(channel)
	if name == path.Join("apt", "dists", channelName, "InRelease") {
		return CacheNoStore, true
	}
	if strings.HasPrefix(name, path.Join("rpm", channelName)+"/") && strings.HasSuffix(name, "/repodata/repomd.xml") {
		return CacheNoStore, true
	}
	if strings.HasPrefix(name, path.Join("rpm", channelName)+"/") &&
		strings.HasSuffix(name, "/repodata/repomd.xml.asc") {
		return CacheNoStore, false
	}
	if strings.HasPrefix(name, path.Join("apk", channelName)+"/") && strings.HasSuffix(name, "/APKINDEX.tar.gz") {
		return CacheNoStore, true
	}
	if strings.HasPrefix(name, keysDir+"/") ||
		strings.Contains(name, "/by-hash/") ||
		strings.Contains(name, "/repodata/") ||
		strings.HasSuffix(name, ".deb") ||
		strings.HasSuffix(name, ".rpm") ||
		strings.HasSuffix(name, ".apk") {
		return CacheImmutable, false
	}

	return CacheNoStore, false
}

// artifactOrder returns the safe publication group for one generated artifact.
func artifactOrder(artifact Artifact) int {
	if artifact.CommitRoot {
		return artifactOrderCommitRoot
	}
	if strings.HasSuffix(artifact.Path, ".deb") || strings.HasSuffix(artifact.Path, ".rpm") ||
		strings.HasSuffix(artifact.Path, ".apk") {
		return artifactOrderPackage
	}
	if artifact.Cache == CacheImmutable {
		return artifactOrderImmutable
	}

	return artifactOrderMutable
}
