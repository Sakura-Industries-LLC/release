package pkgrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// releaseAssetsDir confines the downloaded GitHub Release closed set.
	releaseAssetsDir = "release"
	// existingPackagesDir confines package objects mirrored from storage.
	existingPackagesDir = "existing"
	// aptValidity is the deterministic APT metadata lifetime after publication.
	aptValidity = 365 * 24 * time.Hour
)

// PublisherOptions supplies every boundary required by [Publisher].
type PublisherOptions struct {
	// Releases downloads exact public GitHub Releases.
	Releases ReleaseSource
	// Bundles verifies checksums.txt Sigstore bundles.
	Bundles pubgh.BlobVerifier
	// Attestations verifies GitHub build provenance for native packages.
	Attestations Attestor
	// Store reads and writes the static repository object namespace.
	Store Store
	// Inspector reads normalized native package metadata.
	Inspector Inspector
	// Verifier checks producer-native RPM and APK signatures.
	Verifier Verifier
	// Generator regenerates deterministic APT, RPM, and APK metadata.
	Generator Generator
	// Signer signs aggregate APT and RPM metadata.
	Signer Signer
	// Installer verifies native installation before and after publication.
	Installer Installer
}

// Publisher verifies one public release and converges one static package repository.
type Publisher struct {
	// releases downloads exact public GitHub Releases.
	releases ReleaseSource
	// bundles verifies checksums.txt Sigstore bundles.
	bundles pubgh.BlobVerifier
	// attestations verifies GitHub build provenance for native packages.
	attestations Attestor
	// store reads and writes the static repository object namespace.
	store Store
	// inspector reads normalized native package metadata.
	inspector Inspector
	// verifier checks producer-native RPM and APK signatures.
	verifier Verifier
	// generator regenerates deterministic repository metadata.
	generator Generator
	// signer signs aggregate APT and RPM metadata.
	signer Signer
	// installer verifies native installation behavior.
	installer Installer
}

// NewPublisher constructs a repository publisher from narrow boundary ports.
func NewPublisher(options PublisherOptions) *Publisher {
	return &Publisher{
		releases:     options.Releases,
		bundles:      options.Bundles,
		attestations: options.Attestations,
		store:        options.Store,
		inspector:    options.Inspector,
		verifier:     options.Verifier,
		generator:    options.Generator,
		signer:       options.Signer,
		installer:    options.Installer,
	}
}

// Publish verifies, regenerates, installs, and safely reconciles one repository release.
func (p *Publisher) Publish(ctx context.Context, input PublishInput) (PublishResult, error) {
	if err := p.validate(ctx, input); err != nil {
		return PublishResult{}, err
	}
	policy, err := input.Config.PolicyFor(input.Request.Repository)
	if err != nil {
		return PublishResult{}, err
	}
	producer, err := input.Config.ProducerFor(input.Request.Repository)
	if err != nil {
		return PublishResult{}, err
	}
	version, err := ParseTag(input.Request.Tag)
	if err != nil {
		return PublishResult{}, err
	}
	copyErr := copyPublicationKeys(input.Keys, input.Source, input.Config.Repository)
	if copyErr != nil {
		return PublishResult{}, copyErr
	}

	releaseRoot, err := createScratchRoot(input.Source, releaseAssetsDir)
	if err != nil {
		return PublishResult{}, err
	}
	defer releaseRoot.Close()
	release, err := p.fetchRelease(ctx, input.Request, releaseRoot)
	if err != nil {
		return PublishResult{}, err
	}

	if policyErr := policy.Validate(); policyErr != nil {
		return PublishResult{}, policyErr
	}
	bundle, err := pubgh.VerifyBundle(ctx, releaseRoot.FS(), p.bundles, pubgh.TrustPolicy{
		Identity: string(policy.ChecksumIdentity),
	})
	if err != nil {
		return PublishResult{}, fmt.Errorf("verify release bundle: %w", err)
	}
	incoming, incomingDigests, err := p.verifyReleasePackages(ctx, input, policy, releaseRoot, release, bundle)
	if err != nil {
		return PublishResult{}, err
	}
	existing, err := p.mirrorExistingPackages(ctx, input, incomingDigests)
	if err != nil {
		return PublishResult{}, err
	}
	existing = append(existing, incoming...)

	built, err := Build(ctx, BuildInput{
		Config:      input.Config.Repository,
		Request:     input.Request,
		Assets:      existing,
		Source:      input.Source,
		Work:        input.Work,
		Output:      input.Output,
		ReleaseTime: release.PublishedAt.UTC(),
		ValidUntil:  release.PublishedAt.UTC().Add(aptValidity),
	}, p.inspector, p.verifier, p.generator, p.signer)
	if err != nil {
		return PublishResult{}, fmt.Errorf("build repository: %w", err)
	}

	install := publicationInstallRequest(input, producer, version)
	installErr := p.installer.Verify(ctx, install)
	if installErr != nil {
		return PublishResult{}, fmt.Errorf("verify local installation: %w", installErr)
	}
	uploaded, err := p.reconcile(ctx, input.Output, built.Artifacts)
	if err != nil {
		return PublishResult{}, err
	}
	install.Root = nil
	installErr = p.installer.Verify(ctx, install)
	if installErr != nil {
		return PublishResult{}, fmt.Errorf("verify public installation: %w", installErr)
	}

	state := publicationState(uploaded)
	return PublishResult{
		State:      state,
		Repository: input.Request.Repository,
		Tag:        input.Request.Tag,
		Artifacts:  len(built.Artifacts),
		Uploaded:   uploaded,
	}, nil
}

// fetchRelease downloads and validates one release against its exact request.
func (p *Publisher) fetchRelease(
	ctx context.Context,
	request Request,
	releaseRoot *os.Root,
) (Release, error) {
	releaseRequest := ReleaseRequest(request)
	release, err := p.releases.Fetch(ctx, releaseRequest, releaseRoot)
	if err != nil {
		return Release{}, fmt.Errorf("download GitHub Release: %w", err)
	}
	if validateErr := release.Validate(releaseRequest); validateErr != nil {
		return Release{}, fmt.Errorf("validate GitHub Release: %w", validateErr)
	}

	return release, nil
}

// publicationState maps the upload count onto the observable publication result.
func publicationState(uploaded int) PublishState {
	if uploaded == 0 {
		return PublishStateUnchanged
	}

	return PublishStatePublished
}

// publicationInstallRequest builds the native client trust set for one producer.
func publicationInstallRequest(input PublishInput, producer Producer, version rel.Version) InstallRequest {
	return InstallRequest{
		Root:     input.Output,
		Keys:     input.Output,
		Origin:   input.Config.Origin,
		Channel:  input.Config.Repository.Channel,
		Packages: producer.Packages,
		Version:  version,
		APTKey:   path.Join(keysDir, input.Config.Repository.APTKey.Published),
		RPMKeys: []string{
			path.Join(keysDir, input.Config.Repository.RPMKey.Published),
			path.Join(keysDir, producer.RPMKey.Published),
		},
		APKKeys: []string{
			path.Join(keysDir, input.Config.Repository.APKKey.Published),
			path.Join(keysDir, producer.APKKey.Published),
		},
	}
}

// validate rejects incomplete dependencies, malformed policy, and unsafe scratch roots.
func (p *Publisher) validate(ctx context.Context, input PublishInput) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if p == nil {
		return errors.New("publisher is nil")
	}
	if p.releases == nil || p.bundles == nil || p.attestations == nil || p.store == nil ||
		p.inspector == nil || p.verifier == nil || p.generator == nil || p.signer == nil || p.installer == nil {
		return errors.New("publisher dependency is nil")
	}
	if err := input.Config.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if _, err := ParseRepository(string(input.Request.Repository)); err != nil {
		return err
	}
	if _, err := ParseTag(input.Request.Tag); err != nil {
		return err
	}
	if input.Keys == nil || input.Source == nil || input.Work == nil || input.Output == nil {
		return errors.New("publication root is nil")
	}
	if err := requireDistinctRoots(input.Keys, input.Source, input.Work, input.Output); err != nil {
		return err
	}
	if err := requireEmptyRoot(input.Source, "source"); err != nil {
		return err
	}
	if err := requireEmptyRoot(input.Work, "work"); err != nil {
		return err
	}
	if err := requireEmptyRoot(input.Output, "output"); err != nil {
		return err
	}

	return nil
}

// copyPublicationKeys copies each distinct reviewed public-key source into the package source root.
func copyPublicationKeys(keys, source *os.Root, config Config) error {
	seen := make(map[string]struct{})
	for _, key := range configuredPublicKeys(config) {
		if _, exists := seen[key.Source]; exists {
			continue
		}
		seen[key.Source] = struct{}{}
		if err := source.MkdirAll(path.Dir(key.Source), directoryMode); err != nil {
			return fmt.Errorf("create source key directory for %s: %w", key.Source, err)
		}
		if err := copyRootFile(keys, key.Source, source, key.Source, nil); err != nil {
			return fmt.Errorf("copy public key %s: %w", key.Source, err)
		}
	}

	return nil
}

// createScratchRoot creates and opens one fixed child beneath parent.
func createScratchRoot(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, directoryMode); err != nil {
		return nil, fmt.Errorf("create scratch directory %s: %w", name, err)
	}
	root, err := os.OpenRoot(path.Join(parent.Name(), name))
	if err != nil {
		return nil, fmt.Errorf("open scratch directory %s: %w", name, err)
	}

	return root, nil
}

// verifyReleasePackages selects and attests every native package in the closed release bundle.
func (p *Publisher) verifyReleasePackages(
	ctx context.Context,
	input PublishInput,
	policy SourcePolicy,
	releaseRoot *os.Root,
	release Release,
	bundle pubgh.Bundle,
) ([]Asset, map[string]struct{}, error) {
	releaseAssets := make(map[string]ReleaseAsset, len(release.Assets))
	for _, asset := range release.Assets {
		releaseAssets[asset.Name.String()] = asset
	}
	assets := make([]Asset, 0)
	digests := make(map[string]struct{})
	for _, entry := range bundle.Payloads {
		format, include := releasePackageFormat(entry.Name)
		if !include {
			continue
		}
		digest, err := rel.ParseDigest("sha256:" + entry.Digest.String())
		if err != nil {
			return nil, nil, fmt.Errorf("parse release package digest %s: %w", entry.Name, err)
		}
		transport, exists := releaseAssets[entry.Name]
		if !exists {
			return nil, nil, fmt.Errorf("release package %q has no transport asset", entry.Name)
		}
		if transport.Digest != digest {
			return nil, nil, fmt.Errorf(
				"release package %q digest %s does not match GitHub digest %s",
				entry.Name,
				digest,
				transport.Digest,
			)
		}
		if err := p.attestations.Verify(ctx, AttestationRequest{
			Path:           absoluteRootPath(releaseRoot, entry.Name),
			Repository:     input.Request.Repository,
			SourceRef:      "refs/tags/" + input.Request.Tag,
			SourceDigest:   release.Commit,
			SignerWorkflow: policy.AttestationSigner,
		}); err != nil {
			return nil, nil, fmt.Errorf("verify package attestation %s: %w", entry.Name, err)
		}
		assets = append(assets, Asset{
			Repository: input.Request.Repository,
			Format:     format,
			Path:       path.Join(releaseAssetsDir, entry.Name),
			Digest:     digest,
		})
		digests[packageDigestKey(format, digest)] = struct{}{}
	}
	if len(assets) == 0 {
		return nil, nil, errors.New("release bundle contains no native packages")
	}

	return assets, digests, nil
}

// mirrorExistingPackages downloads, hashes, and assigns every canonical stored package.
func (p *Publisher) mirrorExistingPackages(
	ctx context.Context,
	input PublishInput,
	incoming map[string]struct{},
) ([]Asset, error) {
	objects, err := p.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repository objects: %w", err)
	}
	sort.Slice(objects, func(left, right int) bool { return objects[left].Path < objects[right].Path })
	seen := make(map[string]struct{}, len(objects))
	assets := make([]Asset, 0)
	for _, object := range objects {
		if _, duplicate := seen[object.Path]; duplicate {
			return nil, fmt.Errorf("stored object %q is duplicated", object.Path)
		}
		seen[object.Path] = struct{}{}
		format, include := packageObjectFormat(object.Path, input.Config.Repository.Channel)
		if !include {
			continue
		}
		if object.Size < 0 {
			return nil, fmt.Errorf("stored package %q has negative size %d", object.Path, object.Size)
		}
		sourceName := path.Join(existingPackagesDir, object.Path)
		if err := input.Source.MkdirAll(path.Dir(sourceName), directoryMode); err != nil {
			return nil, fmt.Errorf("create mirror directory for %s: %w", object.Path, err)
		}
		content, digest, err := p.downloadStoredPackage(ctx, input.Source, sourceName, object)
		if err != nil {
			return nil, err
		}
		if _, replayed := incoming[packageDigestKey(format, digest)]; replayed {
			continue
		}
		metadata, err := p.inspector.Inspect(ctx, format, absoluteRootPath(input.Source, sourceName))
		if err != nil {
			return nil, fmt.Errorf("inspect stored package %s: %w", object.Path, err)
		}
		producer, err := ownerForPackage(input.Config.Repository, metadata.Name)
		if err != nil {
			return nil, fmt.Errorf("stored package %s: %w", object.Path, err)
		}
		assets = append(assets, Asset{
			Repository: producer,
			Format:     format,
			Path:       sourceName,
			Digest:     digest,
		})
		_ = content
	}

	return assets, nil
}

// downloadStoredPackage streams one stored package into source and verifies size and digest metadata.
func (p *Publisher) downloadStoredPackage(
	ctx context.Context,
	source *os.Root,
	sourceName string,
	object StoredObject,
) (StoredContent, rel.Digest, error) {
	file, err := source.OpenFile(sourceName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return StoredContent{}, "", fmt.Errorf("create mirrored package %s: %w", object.Path, err)
	}
	hasher := sha256.New()
	count := &countingWriter{writer: io.MultiWriter(file, hasher)}
	content, downloadErr := p.store.Download(ctx, object.Path, count)
	closeErr := file.Close()
	if downloadErr != nil {
		return StoredContent{}, "", fmt.Errorf("download stored package %s: %w", object.Path, downloadErr)
	}
	if closeErr != nil {
		return StoredContent{}, "", fmt.Errorf("close mirrored package %s: %w", object.Path, closeErr)
	}
	if content.Size != object.Size || count.written != object.Size {
		return StoredContent{}, "", fmt.Errorf(
			"stored package %q size mismatch: list %d, metadata %d, downloaded %d",
			object.Path,
			object.Size,
			content.Size,
			count.written,
		)
	}
	digest, err := rel.ParseDigest("sha256:" + hex.EncodeToString(hasher.Sum(nil)))
	if err != nil {
		return StoredContent{}, "", fmt.Errorf("parse stored package digest %s: %w", object.Path, err)
	}
	if digest != content.Digest {
		return StoredContent{}, "", fmt.Errorf(
			"stored package %q has digest %s, metadata says %s",
			object.Path,
			digest,
			content.Digest,
		)
	}

	return content, digest, nil
}

// reconcile uploads missing or changed generated artifacts in their pre-sorted safe order.
func (p *Publisher) reconcile(ctx context.Context, output *os.Root, artifacts []Artifact) (int, error) {
	uploaded := 0
	for _, artifact := range artifacts {
		stored, exists, err := p.store.Stat(ctx, artifact.Path)
		if err != nil {
			return 0, fmt.Errorf("stat repository object %s: %w", artifact.Path, err)
		}
		if exists && stored.Digest == artifact.Digest && stored.Size == artifact.Size {
			continue
		}
		if exists && artifact.Cache == CacheImmutable {
			return 0, fmt.Errorf(
				"immutable repository object %q conflicts: remote %s/%d, generated %s/%d",
				artifact.Path,
				stored.Digest,
				stored.Size,
				artifact.Digest,
				artifact.Size,
			)
		}
		file, err := output.Open(artifact.Path)
		if err != nil {
			return 0, fmt.Errorf("open generated artifact %s: %w", artifact.Path, err)
		}
		uploadErr := p.store.Upload(ctx, UploadRequest{
			Path:   artifact.Path,
			Body:   file,
			Digest: artifact.Digest,
			Size:   artifact.Size,
			Cache:  artifact.Cache,
		})
		closeErr := file.Close()
		if uploadErr != nil {
			return 0, fmt.Errorf("upload repository object %s: %w", artifact.Path, uploadErr)
		}
		if closeErr != nil {
			return 0, fmt.Errorf("close generated artifact %s: %w", artifact.Path, closeErr)
		}
		uploaded++
	}

	return uploaded, nil
}

// releasePackageFormat maps one flat release asset suffix onto a native format.
func releasePackageFormat(name string) (Format, bool) {
	switch {
	case strings.HasSuffix(name, ".deb"):
		return FormatDEB, true
	case strings.HasSuffix(name, ".rpm"):
		return FormatRPM, true
	case strings.HasSuffix(name, ".apk"):
		return FormatAPK, true
	default:
		return "", false
	}
}

// packageDigestKey returns one format-qualified content identity.
func packageDigestKey(format Format, digest rel.Digest) string {
	return string(format) + ":" + digest.String()
}

// ownerForPackage returns the unique configured owner for one inspected package name.
func ownerForPackage(config Config, name PackageName) (Repository, error) {
	for _, producer := range config.Producers {
		if slices.Contains(producer.Packages, name) {
			return producer.Repository, nil
		}
	}

	return "", fmt.Errorf("package %q is not allowlisted", name)
}

// countingWriter records the exact number of bytes accepted by writer.
type countingWriter struct {
	// writer receives streamed object bytes.
	writer io.Writer
	// written is the total successfully written byte count.
	written int64
}

// Write implements [io.Writer].
func (w *countingWriter) Write(value []byte) (int, error) {
	count, err := w.writer.Write(value)
	w.written += int64(count)

	return count, err
}
