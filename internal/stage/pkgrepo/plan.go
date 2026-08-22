package pkgrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"

	"github.com/meigma/release/internal/rel"
)

const (
	// formatCount is the number of required package formats per release architecture.
	formatCount = 3
	// architectureCount is the number of required release architectures.
	architectureCount = 2
	// completeReleaseMask has one bit for every required format and architecture pair.
	completeReleaseMask = 1<<(formatCount*architectureCount) - 1
)

const (
	// formatDEBIndex is the completeness-mask index for DEB packages.
	formatDEBIndex uint8 = iota
	// formatRPMIndex is the completeness-mask index for RPM packages.
	formatRPMIndex
	// formatAPKIndex is the completeness-mask index for APK packages.
	formatAPKIndex
)

// releaseKey identifies one producer-owned package version.
type releaseKey struct {
	// repository is the configured producer.
	repository Repository
	// name is the producer-owned package name.
	name PackageName
	// version is the canonical stable package version.
	version rel.Version
}

// PlanPackages validates inspected assets and returns their canonical layout.
//
// Every package name must be uniquely owned by its configured producer. Each
// producer/name/version group present in assets must contain exactly one DEB,
// RPM, and APK for amd64 and arm64. An exact duplicate digest converges to one
// object; a duplicate destination with different bytes fails closed. The
// requested producer and tag must contain a complete release for every package
// on that producer's allowlist.
func PlanPackages(config Config, request Request, assets []InspectedAsset) (Plan, error) {
	if err := config.Validate(); err != nil {
		return Plan{}, fmt.Errorf("validate config: %w", err)
	}
	requestedVersion, err := ParseTag(request.Tag)
	if err != nil {
		return Plan{}, err
	}

	producers, owners := indexConfig(config)
	requestedProducer, exists := producers[request.Repository]
	if !exists {
		return Plan{}, fmt.Errorf("request repository %q is not allowlisted", request.Repository)
	}
	if len(assets) == 0 {
		return Plan{}, errors.New("assets are empty")
	}

	byDestination := make(map[string]PackagePlan, len(assets))
	completeness := make(map[releaseKey]uint8)
	for index, asset := range assets {
		planned, bit, planErr := planAsset(config.Channel, owners, asset)
		if planErr != nil {
			return Plan{}, fmt.Errorf("asset %d: %w", index, planErr)
		}
		if addErr := addPlannedAsset(byDestination, completeness, planned, bit); addErr != nil {
			return Plan{}, fmt.Errorf("asset %d: %w", index, addErr)
		}
	}

	for key, mask := range completeness {
		if mask != completeReleaseMask {
			return Plan{}, fmt.Errorf(
				"package %q version %s from %q is incomplete: have mask %#02x, want %#02x",
				key.name,
				key.version,
				key.repository,
				mask,
				completeReleaseMask,
			)
		}
	}
	for _, name := range requestedProducer.Packages {
		key := releaseKey{repository: request.Repository, name: name, version: requestedVersion}
		if completeness[key] != completeReleaseMask {
			return Plan{}, fmt.Errorf(
				"requested package %q version %s from %q is incomplete",
				name,
				requestedVersion,
				request.Repository,
			)
		}
	}

	packages := make([]PackagePlan, 0, len(byDestination))
	for _, planned := range byDestination {
		packages = append(packages, planned)
	}
	sort.Slice(packages, func(left, right int) bool {
		return packages[left].Destination < packages[right].Destination
	})

	return Plan{Packages: packages}, nil
}

// addPlannedAsset converges exact replays and records one completeness bit.
func addPlannedAsset(
	byDestination map[string]PackagePlan,
	completeness map[releaseKey]uint8,
	planned PackagePlan,
	bit uint8,
) error {
	if existing, duplicate := byDestination[planned.Destination]; duplicate {
		if existing.Digest != planned.Digest {
			return fmt.Errorf(
				"package path %q has conflicting digests %q and %q",
				planned.Destination,
				existing.Digest,
				planned.Digest,
			)
		}
		return nil
	}

	key := releaseKey{
		repository: planned.Repository,
		name:       planned.Metadata.Name,
		version:    planned.Metadata.Version,
	}
	if completeness[key]&bit != 0 {
		return fmt.Errorf(
			"package %q repeats format %q architecture %q",
			planned.Metadata.Name,
			planned.Format,
			planned.Metadata.Architecture,
		)
	}
	byDestination[planned.Destination] = planned
	completeness[key] |= bit

	return nil
}

// indexConfig returns producer and package-owner lookup tables for validated config.
func indexConfig(config Config) (map[Repository]Producer, map[PackageName]Repository) {
	producers := make(map[Repository]Producer, len(config.Producers))
	owners := make(map[PackageName]Repository)
	for _, producer := range config.Producers {
		producers[producer.Repository] = producer
		for _, name := range producer.Packages {
			owners[name] = producer.Repository
		}
	}

	return producers, owners
}

// planAsset validates one inspected package and derives its canonical path and completeness bit.
func planAsset(
	channel Channel,
	owners map[PackageName]Repository,
	inspected InspectedAsset,
) (PackagePlan, uint8, error) {
	asset := inspected.Asset
	if _, err := ParseRepository(string(asset.Repository)); err != nil {
		return PackagePlan{}, 0, err
	}
	if !fs.ValidPath(asset.Path) || asset.Path == "." {
		return PackagePlan{}, 0, fmt.Errorf("source path %q is not confined", asset.Path)
	}
	if !filepath.IsAbs(inspected.StagedPath) {
		return PackagePlan{}, 0, fmt.Errorf("staged path %q is not absolute", inspected.StagedPath)
	}
	if _, err := rel.ParseDigest(asset.Digest.String()); err != nil {
		return PackagePlan{}, 0, fmt.Errorf("package digest: %w", err)
	}
	if _, err := ParsePackageName(string(inspected.Metadata.Name)); err != nil {
		return PackagePlan{}, 0, err
	}
	owner, exists := owners[inspected.Metadata.Name]
	if !exists {
		return PackagePlan{}, 0, fmt.Errorf("package %q is not allowlisted", inspected.Metadata.Name)
	}
	if owner != asset.Repository {
		return PackagePlan{}, 0, fmt.Errorf(
			"package %q belongs to %q, not %q",
			inspected.Metadata.Name,
			owner,
			asset.Repository,
		)
	}

	destination, bit, err := packageDestination(channel, asset.Format, inspected.Metadata)
	if err != nil {
		return PackagePlan{}, 0, err
	}

	return PackagePlan{
		Repository:  asset.Repository,
		Format:      asset.Format,
		Metadata:    inspected.Metadata,
		Source:      inspected.StagedPath,
		Destination: destination,
		Digest:      asset.Digest,
	}, bit, nil
}

// packageDestination maps normalized metadata onto one canonical package object path.
func packageDestination(channel Channel, format Format, metadata PackageMetadata) (string, uint8, error) {
	formatIndex, err := formatBitIndex(format)
	if err != nil {
		return "", 0, err
	}
	architectureIndex, formatArch, err := architectureValues(format, metadata.Architecture)
	if err != nil {
		return "", 0, err
	}

	name := string(metadata.Name)
	version := metadata.Version.String()
	var destination string
	switch format {
	case FormatDEB:
		destination = path.Join(
			"apt",
			"pool",
			"main",
			name[:1],
			name,
			fmt.Sprintf("%s_%s_%s.deb", name, version, formatArch),
		)
	case FormatRPM:
		destination = path.Join(
			"rpm",
			string(channel),
			formatArch,
			"packages",
			fmt.Sprintf("%s-%s-1.%s.rpm", name, version, formatArch),
		)
	case FormatAPK:
		destination = path.Join("apk", string(channel), "main", formatArch, fmt.Sprintf("%s-%s.apk", name, version))
	default:
		return "", 0, fmt.Errorf("package format %q is unsupported", format)
	}

	bit := uint8(1 << (architectureIndex*formatCount + formatIndex))
	return destination, bit, nil
}

// formatBitIndex returns the stable completeness-mask index for format.
func formatBitIndex(format Format) (uint8, error) {
	switch format {
	case FormatDEB:
		return formatDEBIndex, nil
	case FormatRPM:
		return formatRPMIndex, nil
	case FormatAPK:
		return formatAPKIndex, nil
	default:
		return 0, fmt.Errorf("package format %q is unsupported", format)
	}
}

// architectureValues returns the completeness index and format-native architecture.
func architectureValues(format Format, architecture Architecture) (uint8, string, error) {
	switch architecture {
	case ArchitectureAMD64:
		switch format {
		case FormatDEB:
			return 0, "amd64", nil
		case FormatRPM, FormatAPK:
			return 0, "x86_64", nil
		}
	case ArchitectureARM64:
		switch format {
		case FormatDEB:
			return 1, "arm64", nil
		case FormatRPM, FormatAPK:
			return 1, "aarch64", nil
		}
	default:
		return 0, "", fmt.Errorf("package architecture %q is unsupported", architecture)
	}

	return 0, "", fmt.Errorf("package format %q is unsupported", format)
}
