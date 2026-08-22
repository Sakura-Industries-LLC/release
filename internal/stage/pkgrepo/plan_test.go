package pkgrepo

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
)

// TestConfigValidateRejectsAmbiguousOwnership proves package and key ownership is unique.
func TestConfigValidateRejectsAmbiguousOwnership(t *testing.T) {
	t.Parallel()

	config := testConfig(t)
	config.Producers = append(config.Producers, Producer{
		Repository: "meigma/other",
		Packages:   []PackageName{"release-cli"},
		RPMKey: PublicKey{
			Source:    "keys/other-rpm.asc",
			Published: "other-rpm-001.asc",
		},
		APKKey: PublicKey{
			Source:    "keys/other-apk.rsa.pub",
			Published: "other-apk-001.rsa.pub",
		},
	})

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "release-cli" is owned by both`)
}

// TestConfigValidateRejectsUnconfinedKey proves reviewed keys cannot escape the source root.
func TestConfigValidateRejectsUnconfinedKey(t *testing.T) {
	t.Parallel()

	config := testConfig(t)
	config.Producers[0].RPMKey.Source = "../producer.asc"

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a confined relative path")
}

// TestConfigValidateRejectsDuplicateAggregateKeyName proves root key names cannot alias.
func TestConfigValidateRejectsDuplicateAggregateKeyName(t *testing.T) {
	t.Parallel()

	config := testConfig(t)
	config.RPMKey.Published = config.APTKey.Published

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `published key name "apt-repository-001.asc" is duplicated`)
}

// TestPlanPackagesCanonicalLayout proves all formats map onto the accepted public tree.
func TestPlanPackagesCanonicalLayout(t *testing.T) {
	t.Parallel()

	assets := testInspectedRelease(t)
	plan, err := PlanPackages(testConfig(t), Request{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, assets)
	require.NoError(t, err)

	paths := make([]string, 0, len(plan.Packages))
	for _, pkg := range plan.Packages {
		paths = append(paths, pkg.Destination)
	}
	assert.Equal(t, []string{
		"apk/stable/main/aarch64/release-cli-1.2.3.apk",
		"apk/stable/main/x86_64/release-cli-1.2.3.apk",
		"apt/pool/main/r/release-cli/release-cli_1.2.3_amd64.deb",
		"apt/pool/main/r/release-cli/release-cli_1.2.3_arm64.deb",
		"rpm/stable/aarch64/packages/release-cli-1.2.3-1.aarch64.rpm",
		"rpm/stable/x86_64/packages/release-cli-1.2.3-1.x86_64.rpm",
	}, paths)
}

// TestPlanPackagesRequiresCompleteRelease proves partial package sets fail closed.
func TestPlanPackagesRequiresCompleteRelease(t *testing.T) {
	t.Parallel()

	assets := testInspectedRelease(t)
	assets = assets[:len(assets)-1]

	_, err := PlanPackages(testConfig(t), Request{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, assets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is incomplete")
}

// TestPlanPackagesRejectsWrongProducer proves an allowlisted name cannot cross producers.
func TestPlanPackagesRejectsWrongProducer(t *testing.T) {
	t.Parallel()

	assets := testInspectedRelease(t)
	assets[0].Asset.Repository = "meigma/other"

	_, err := PlanPackages(testConfig(t), Request{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, assets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `belongs to "meigma/release", not "meigma/other"`)
}

// TestPlanPackagesCollapsesExactReplay proves a repeated identical object is unchanged.
func TestPlanPackagesCollapsesExactReplay(t *testing.T) {
	t.Parallel()

	assets := testInspectedRelease(t)
	assets = append(assets, assets[0])

	plan, err := PlanPackages(testConfig(t), Request{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, assets)
	require.NoError(t, err)
	assert.Len(t, plan.Packages, 6)
}

// TestPlanPackagesRejectsImmutableConflict proves same-path different bytes fail closed.
func TestPlanPackagesRejectsImmutableConflict(t *testing.T) {
	t.Parallel()

	assets := testInspectedRelease(t)
	conflict := assets[0]
	conflict.Asset.Digest = testDigest(t, "different bytes")
	assets = append(assets, conflict)

	_, err := PlanPackages(testConfig(t), Request{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, assets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has conflicting digests")
}

// TestPlanPackagesRejectsUnconfinedSource proves source paths are confined independently of staging.
func TestPlanPackagesRejectsUnconfinedSource(t *testing.T) {
	t.Parallel()

	assets := testInspectedRelease(t)
	assets[0].Asset.Path = "../escape.deb"

	_, err := PlanPackages(testConfig(t), Request{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, assets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not confined")
}

// testConfig returns one complete repository configuration.
func testConfig(t *testing.T) Config {
	t.Helper()

	return Config{
		Channel: ChannelStable,
		Producers: []Producer{{
			Repository: "meigma/release",
			Packages:   []PackageName{"release-cli"},
			RPMKey: PublicKey{
				Source:    "keys/producer-rpm.asc",
				Published: "release-rpm-001.asc",
			},
			APKKey: PublicKey{
				Source:    "keys/producer-apk.rsa.pub",
				Published: "release-apk-001.rsa.pub",
			},
		}},
		APTKey: PublicKey{
			Source:    "keys/repository.asc",
			Published: "apt-repository-001.asc",
		},
		RPMKey: PublicKey{
			Source:    "keys/repository.asc",
			Published: "rpm-repository-001.asc",
		},
		APKKey: PublicKey{
			Source:    "keys/repository-apk.rsa.pub",
			Published: "apk-index-001.rsa.pub",
		},
	}
}

// testInspectedRelease returns all six package facts for one stable release.
func testInspectedRelease(t *testing.T) []InspectedAsset {
	t.Helper()

	version, err := rel.ParseVersion("1.2.3")
	require.NoError(t, err)
	assets := make([]InspectedAsset, 0, formatCount*architectureCount)
	for _, architecture := range []Architecture{ArchitectureAMD64, ArchitectureARM64} {
		for _, format := range []Format{FormatDEB, FormatRPM, FormatAPK} {
			identity := fmt.Sprintf("%s-%s-%s", format, architecture, version)
			assets = append(assets, InspectedAsset{
				Asset: Asset{
					Repository: Repository("meigma/release"),
					Format:     format,
					Path:       "packages/" + identity,
					Digest:     testDigest(t, identity),
				},
				Metadata: PackageMetadata{
					Name:         "release-cli",
					Version:      version,
					Architecture: architecture,
				},
				StagedPath: filepath.Join(t.TempDir(), identity),
			})
		}
	}

	return assets
}

// testDigest returns the canonical SHA-256 digest of value.
func testDigest(t *testing.T, value string) rel.Digest {
	t.Helper()

	sum := sha256.Sum256([]byte(value))
	digest, err := rel.ParseDigest(fmt.Sprintf("sha256:%x", sum))
	require.NoError(t, err)
	return digest
}
