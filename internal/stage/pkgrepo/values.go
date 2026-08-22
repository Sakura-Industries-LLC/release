package pkgrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/meigma/release/internal/rel"
)

const (
	// ChannelStable is the only repository channel supported by the first schema.
	ChannelStable Channel = "stable"

	// FormatDEB identifies a Debian package.
	FormatDEB Format = "deb"
	// FormatRPM identifies an RPM package.
	FormatRPM Format = "rpm"
	// FormatAPK identifies an Alpine package.
	FormatAPK Format = "apk"

	// ArchitectureAMD64 is the normalized 64-bit x86 architecture.
	ArchitectureAMD64 Architecture = "amd64"
	// ArchitectureARM64 is the normalized 64-bit Arm architecture.
	ArchitectureARM64 Architecture = "arm64"

	// CacheImmutable selects year-long immutable public caching.
	CacheImmutable CachePolicy = "immutable"
	// CacheNoStore prevents public caching of replaceable metadata.
	CacheNoStore CachePolicy = "no-store"
)

const (
	// aggregateKeyCount is the number of repository-level public keys.
	aggregateKeyCount = 3
	// producerKeyCount is the number of package-signing public keys per producer.
	producerKeyCount = 2
)

var (
	// repositoryPattern accepts the deliberately narrow GitHub owner/name grammar.
	repositoryPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,38})/[a-z0-9](?:[a-z0-9._-]{0,99})$`)
	// packagePattern accepts the common lowercase DEB, RPM, and APK name grammar.
	packagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	// keyNamePattern accepts one versioned public-key filename without directories.
	keyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*\.(?:asc|rsa\.pub)$`)
)

// Channel is one published package-repository channel.
type Channel string

// Format is one supported native package format.
type Format string

// Architecture is the format-independent package architecture.
type Architecture string

// Repository is a canonical lowercase GitHub owner/name pair.
type Repository string

// PackageName is a normalized native package name.
type PackageName string

// CachePolicy controls the HTTP cache metadata used when an artifact is uploaded.
type CachePolicy string

// PublicKey maps one confined source key onto its versioned public filename.
type PublicKey struct {
	// Source is the source-root-relative public-key path.
	Source string
	// Published is the basename written beneath the repository keys directory.
	Published string
}

// Producer is one allowlisted GitHub repository and its package-signing keys.
type Producer struct {
	// Repository is the producer's canonical GitHub owner/name.
	Repository Repository
	// Packages is the nonempty package-name allowlist owned exclusively by this producer.
	Packages []PackageName
	// RPMKey verifies RPM package signatures from this producer.
	RPMKey PublicKey
	// APKKey verifies APK package signatures from this producer.
	APKKey PublicKey
}

// Config is the reviewed local repository schema consumed by [Build].
type Config struct {
	// Channel is the single published channel and must equal [ChannelStable].
	Channel Channel
	// Producers is the nonempty producer allowlist.
	Producers []Producer
	// APTKey is the aggregate APT metadata public key.
	APTKey PublicKey
	// RPMKey is the aggregate RPM metadata public key.
	RPMKey PublicKey
	// APKKey is the aggregate APK index public key.
	APKKey PublicKey
}

// Request identifies the producer release whose completeness must be proven.
type Request struct {
	// Repository is the allowlisted producer repository.
	Repository Repository
	// Tag is an exact stable vMAJOR.MINOR.PATCH tag.
	Tag string
}

// Asset is one source package with an authoritative SHA-256 digest.
type Asset struct {
	// Repository identifies the producer that owns the package.
	Repository Repository
	// Format declares the package container format inspected after staging.
	Format Format
	// Path is the confined source-root-relative package path.
	Path string
	// Digest is the expected digest of the exact package bytes.
	Digest rel.Digest
}

// PackageMetadata is normalized metadata read from one native package.
type PackageMetadata struct {
	// Name is the native package name.
	Name PackageName
	// Version is the stable package version without a v prefix or package release suffix.
	Version rel.Version
	// Architecture is the normalized package architecture.
	Architecture Architecture
}

// InspectedAsset combines an input asset with its verified package metadata.
type InspectedAsset struct {
	// Asset is the original package input.
	Asset Asset
	// Metadata is the normalized package metadata read from Asset.
	Metadata PackageMetadata
	// StagedPath is the absolute confined scratch copy passed to adapters.
	StagedPath string
}

// PackagePlan is one canonical package object selected for the local tree.
type PackagePlan struct {
	// Repository is the configured producer owner.
	Repository Repository
	// Format is the native package format.
	Format Format
	// Metadata is the inspected package identity.
	Metadata PackageMetadata
	// Source is the absolute staged source package.
	Source string
	// Destination is the slash-separated repository-relative object path.
	Destination string
	// Digest is the verified SHA-256 package digest.
	Digest rel.Digest
	// VerificationKey is the absolute staged producer key for RPM or APK verification.
	VerificationKey string
}

// Plan is the deterministic local repository layout selected from inspected assets.
type Plan struct {
	// Packages is sorted by destination and contains no path conflicts.
	Packages []PackagePlan
}

// GenerateRequest is the complete local tree request passed to a metadata generator.
type GenerateRequest struct {
	// Root is the absolute repository-tree root containing canonical package objects.
	Root string
	// Channel is the configured repository channel.
	Channel Channel
	// ReleaseTime is the deterministic metadata creation time.
	ReleaseTime time.Time
	// ValidUntil is the APT metadata expiry time and must follow ReleaseTime.
	ValidUntil time.Time
}

// SignRequest identifies one metadata document and its deterministic signature output.
type SignRequest struct {
	// Input is the absolute metadata document to sign.
	Input string
	// Output is the absolute signature output path.
	Output string
	// Time is the deterministic signature creation time.
	Time time.Time
}

// VerificationRequest identifies one native package signature check.
type VerificationRequest struct {
	// Format is [FormatRPM] or [FormatAPK].
	Format Format
	// Package is the absolute staged package path.
	Package string
	// PublicKey is the absolute staged producer public-key path.
	PublicKey string
}

// Artifact is one generated public object and its upload semantics.
type Artifact struct {
	// Path is the slash-separated repository-relative object path.
	Path string
	// Digest is the SHA-256 digest of the generated file.
	Digest rel.Digest
	// Size is the exact file size in bytes.
	Size int64
	// Cache is the public cache policy for the object.
	Cache CachePolicy
	// CommitRoot reports whether the object activates a generated repository view.
	CommitRoot bool
}

// BuildResult describes the complete generated public tree.
type BuildResult struct {
	// Artifacts is sorted in safe publication order: non-roots first, commit roots last.
	Artifacts []Artifact
}

// ParseRepository validates and constructs a [Repository].
func ParseRepository(value string) (Repository, error) {
	if !repositoryPattern.MatchString(value) {
		return "", fmt.Errorf("repository %q must be a lowercase owner/name", value)
	}

	return Repository(value), nil
}

// ParsePackageName validates and constructs a [PackageName].
func ParsePackageName(value string) (PackageName, error) {
	if !packagePattern.MatchString(value) {
		return "", fmt.Errorf("package name %q is invalid", value)
	}

	return PackageName(value), nil
}

// ParseTag validates an exact stable vMAJOR.MINOR.PATCH tag.
func ParseTag(value string) (rel.Version, error) {
	versionText, found := strings.CutPrefix(value, "v")
	if !found {
		return rel.Version{}, fmt.Errorf("tag %q is missing the v prefix", value)
	}

	version, err := rel.ParseVersion(versionText)
	if err != nil {
		return rel.Version{}, fmt.Errorf("tag %q: %w", value, err)
	}

	return version, nil
}

// Validate checks the complete reviewed repository configuration.
func (c Config) Validate() error {
	if c.Channel != ChannelStable {
		return fmt.Errorf("channel %q is unsupported, want %q", c.Channel, ChannelStable)
	}
	if len(c.Producers) == 0 {
		return errors.New("producers are empty")
	}
	publishedKeys := make(map[string]struct{}, len(c.Producers)*producerKeyCount+aggregateKeyCount)
	if err := validateUniquePublicKey("APT repository key", c.APTKey, publishedKeys); err != nil {
		return err
	}
	if err := validateUniquePublicKey("RPM repository key", c.RPMKey, publishedKeys); err != nil {
		return err
	}
	if err := validateUniquePublicKey("APK repository key", c.APKKey, publishedKeys); err != nil {
		return err
	}

	repositories := make(map[Repository]struct{}, len(c.Producers))
	packages := make(map[PackageName]Repository)
	for index, producer := range c.Producers {
		if err := validateProducer(index, producer, repositories, packages, publishedKeys); err != nil {
			return err
		}
	}

	return nil
}

// validateProducer checks one producer and reserves its repository, packages, and keys.
func validateProducer(
	index int,
	producer Producer,
	repositories map[Repository]struct{},
	packages map[PackageName]Repository,
	publishedKeys map[string]struct{},
) error {
	if _, err := ParseRepository(string(producer.Repository)); err != nil {
		return fmt.Errorf("producer %d: %w", index, err)
	}
	if _, exists := repositories[producer.Repository]; exists {
		return fmt.Errorf("producer repository %q is duplicated", producer.Repository)
	}
	repositories[producer.Repository] = struct{}{}
	if len(producer.Packages) == 0 {
		return fmt.Errorf("producer %q packages are empty", producer.Repository)
	}
	for _, name := range producer.Packages {
		if _, err := ParsePackageName(string(name)); err != nil {
			return fmt.Errorf("producer %q: %w", producer.Repository, err)
		}
		if owner, exists := packages[name]; exists {
			return fmt.Errorf("package %q is owned by both %q and %q", name, owner, producer.Repository)
		}
		packages[name] = producer.Repository
	}
	if err := validateUniquePublicKey("RPM producer key", producer.RPMKey, publishedKeys); err != nil {
		return fmt.Errorf("producer %q: %w", producer.Repository, err)
	}
	if err := validateUniquePublicKey("APK producer key", producer.APKKey, publishedKeys); err != nil {
		return fmt.Errorf("producer %q: %w", producer.Repository, err)
	}

	return nil
}

// validatePublicKey checks one confined source path and one published basename.
func validatePublicKey(label string, key PublicKey) error {
	if !fs.ValidPath(key.Source) || key.Source == "." {
		return fmt.Errorf("%s source %q is not a confined relative path", label, key.Source)
	}
	if path.Base(key.Published) != key.Published || !keyNamePattern.MatchString(key.Published) {
		return fmt.Errorf("%s published name %q is invalid", label, key.Published)
	}

	return nil
}

// validateUniquePublicKey checks one public key and reserves its published name.
func validateUniquePublicKey(label string, key PublicKey, publishedKeys map[string]struct{}) error {
	if err := validatePublicKey(label, key); err != nil {
		return err
	}
	if _, exists := publishedKeys[key.Published]; exists {
		return fmt.Errorf("published key name %q is duplicated", key.Published)
	}
	publishedKeys[key.Published] = struct{}{}

	return nil
}
