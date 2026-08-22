package pkgrepo_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	gpgmocks "github.com/meigma/release/internal/adapter/gpg/mocks"
	metamocks "github.com/meigma/release/internal/adapter/pkgmeta/mocks"
	verifymocks "github.com/meigma/release/internal/adapter/pkgverify/mocks"
	generatormocks "github.com/meigma/release/internal/adapter/repogen/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

// TestBuildProducesOrderedSignedRepository proves the local orchestration contract end to end.
func TestBuildProducesOrderedSignedRepository(t *testing.T) {
	t.Parallel()

	fixture := newBuildFixture(t)
	inspector := metamocks.NewMockInspector(t)
	verifier := verifymocks.NewMockVerifier(t)
	generator := generatormocks.NewMockGenerator(t)
	signer := gpgmocks.NewMockSigner(t)

	for index, metadata := range fixture.metadata {
		format := fixture.assets[index].Format
		inspector.EXPECT().
			Inspect(mock.Anything, format, fixture.stagedPath(index, format)).
			Return(metadata, nil).
			Once()
		if format == pkgrepo.FormatRPM || format == pkgrepo.FormatAPK {
			key := fixture.producerRPMKey
			if format == pkgrepo.FormatAPK {
				key = fixture.producerAPKKey
			}
			verifier.EXPECT().Verify(mock.Anything, pkgrepo.VerificationRequest{
				Format:    format,
				Package:   fixture.stagedPath(index, format),
				PublicKey: key,
			}).Return(nil).Once()
		}
	}
	generator.EXPECT().Generate(mock.Anything, mock.MatchedBy(func(request pkgrepo.GenerateRequest) bool {
		return request.Root == fixture.outputDir &&
			request.Channel == pkgrepo.ChannelStable &&
			request.ReleaseTime.Equal(fixture.releaseTime) &&
			request.ValidUntil.Equal(fixture.validUntil)
	})).Run(func(_ context.Context, request pkgrepo.GenerateRequest) {
		writeGeneratedMetadata(t, request.Root)
	}).Return(nil).Once()
	signer.EXPECT().ClearSign(mock.Anything, mock.AnythingOfType("pkgrepo.SignRequest")).
		Run(func(_ context.Context, request pkgrepo.SignRequest) {
			assert.Equal(t, fixture.releaseTime, request.Time)
			writeTestFile(t, request.Output, "signed apt\n")
		}).Return(nil).Once()
	signer.EXPECT().DetachSign(mock.Anything, mock.AnythingOfType("pkgrepo.SignRequest")).
		Run(func(_ context.Context, request pkgrepo.SignRequest) {
			assert.Equal(t, fixture.releaseTime, request.Time)
			writeTestFile(t, request.Output, "signed rpm\n")
		}).Return(nil).Twice()

	result, err := pkgrepo.Build(context.Background(), fixture.input(), inspector, verifier, generator, signer)
	require.NoError(t, err)

	assert.Len(t, result.Artifacts, 26)
	rootCount := 0
	seenRoot := false
	for _, artifact := range result.Artifacts {
		if artifact.CommitRoot {
			seenRoot = true
			rootCount++
			assert.Equal(t, pkgrepo.CacheNoStore, artifact.Cache)
			continue
		}
		assert.False(t, seenRoot, "non-root %s appeared after a commit root", artifact.Path)
	}
	assert.Equal(t, 5, rootCount)
	assertArtifact(t, result.Artifacts, "keys/apt-repository-001.asc", pkgrepo.CacheImmutable, false)
	assertArtifact(t, result.Artifacts, "apt/dists/stable/main/binary-amd64/Packages", pkgrepo.CacheNoStore, false)
	assertArtifact(
		t,
		result.Artifacts,
		"apt/dists/stable/main/binary-amd64/by-hash/SHA256/example",
		pkgrepo.CacheImmutable,
		false,
	)
	assertArtifact(t, result.Artifacts, "apt/dists/stable/InRelease", pkgrepo.CacheNoStore, true)
	assertArtifact(t, result.Artifacts, "rpm/stable/x86_64/repodata/repomd.xml", pkgrepo.CacheNoStore, true)
	assertArtifact(t, result.Artifacts, "rpm/stable/x86_64/repodata/repomd.xml.asc", pkgrepo.CacheNoStore, false)
	assertArtifact(t, result.Artifacts, "apk/stable/main/aarch64/APKINDEX.tar.gz", pkgrepo.CacheNoStore, true)

	_, statErr := os.Stat(filepath.Join(fixture.outputDir, "apt", "dists", "stable", "Release"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	apkBytes, readErr := os.ReadFile(
		filepath.Join(fixture.outputDir, "apk", "stable", "main", "x86_64", "release-cli-1.2.3.apk"),
	)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("apk-amd64"), apkBytes)
}

// TestBuildRejectsDigestMismatchBeforeAdapters proves untrusted bytes never reach a package tool.
func TestBuildRejectsDigestMismatchBeforeAdapters(t *testing.T) {
	t.Parallel()

	fixture := newBuildFixture(t)
	fixture.assets[0].Digest = buildDigest(t, "not-the-file")
	inspector := metamocks.NewMockInspector(t)
	verifier := verifymocks.NewMockVerifier(t)
	generator := generatormocks.NewMockGenerator(t)
	signer := gpgmocks.NewMockSigner(t)

	_, err := pkgrepo.Build(context.Background(), fixture.input(), inspector, verifier, generator, signer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has digest")
}

// TestBuildRejectsGeneratorPackageMutation proves repository tools cannot alter verified bytes.
func TestBuildRejectsGeneratorPackageMutation(t *testing.T) {
	t.Parallel()

	fixture := newBuildFixture(t)
	inspector := metamocks.NewMockInspector(t)
	verifier := verifymocks.NewMockVerifier(t)
	generator := generatormocks.NewMockGenerator(t)
	signer := gpgmocks.NewMockSigner(t)
	for index, metadata := range fixture.metadata {
		format := fixture.assets[index].Format
		inspector.EXPECT().
			Inspect(mock.Anything, format, fixture.stagedPath(index, format)).
			Return(metadata, nil).
			Once()
		if format == pkgrepo.FormatRPM || format == pkgrepo.FormatAPK {
			key := fixture.producerRPMKey
			if format == pkgrepo.FormatAPK {
				key = fixture.producerAPKKey
			}
			verifier.EXPECT().Verify(mock.Anything, pkgrepo.VerificationRequest{
				Format:    format,
				Package:   fixture.stagedPath(index, format),
				PublicKey: key,
			}).Return(nil).Once()
		}
	}
	generator.EXPECT().Generate(mock.Anything, mock.AnythingOfType("pkgrepo.GenerateRequest")).
		Run(func(_ context.Context, request pkgrepo.GenerateRequest) {
			writeGeneratedMetadata(t, request.Root)
			writeTestFile(
				t,
				filepath.Join(request.Root, "apt", "pool", "main", "r", "release-cli", "release-cli_1.2.3_amd64.deb"),
				"mutated",
			)
		}).Return(nil).Once()

	_, err := pkgrepo.Build(context.Background(), fixture.input(), inspector, verifier, generator, signer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generated package")
	assert.Contains(t, err.Error(), "has digest")
}

// buildFixture owns one complete source, scratch, and output set.
type buildFixture struct {
	// sourceDir contains source assets and public keys.
	sourceDir string
	// workDir is the empty scratch directory.
	workDir string
	// outputDir is the empty generated repository directory.
	outputDir string
	// source is the open confined source root.
	source *os.Root
	// work is the open confined work root.
	work *os.Root
	// output is the open confined output root.
	output *os.Root
	// assets is the six-package release input.
	assets []pkgrepo.Asset
	// metadata is the corresponding inspected package metadata.
	metadata []pkgrepo.PackageMetadata
	// config is the reviewed repository configuration.
	config pkgrepo.Config
	// releaseTime is the deterministic metadata time.
	releaseTime time.Time
	// validUntil is the deterministic APT expiry time.
	validUntil time.Time
	// producerRPMKey is the absolute staged RPM verification key.
	producerRPMKey string
	// producerAPKKey is the absolute staged APK verification key.
	producerAPKKey string
}

// newBuildFixture creates and opens one complete local build fixture.
func newBuildFixture(t *testing.T) *buildFixture {
	t.Helper()

	sourceDir := t.TempDir()
	workDir := t.TempDir()
	outputDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(sourceDir, "keys"), 0o755))
	keys := map[string]string{
		"producer-rpm.asc":       "producer rpm key\n",
		"producer-apk.rsa.pub":   "producer apk key\n",
		"repository.asc":         "repository gpg key\n",
		"repository-apk.rsa.pub": "repository apk key\n",
	}
	for name, value := range keys {
		writeTestFile(t, filepath.Join(sourceDir, "keys", name), value)
	}

	version, err := rel.ParseVersion("1.2.3")
	require.NoError(t, err)
	assets := make([]pkgrepo.Asset, 0, 6)
	metadata := make([]pkgrepo.PackageMetadata, 0, 6)
	for _, architecture := range []pkgrepo.Architecture{pkgrepo.ArchitectureAMD64, pkgrepo.ArchitectureARM64} {
		for _, format := range []pkgrepo.Format{pkgrepo.FormatDEB, pkgrepo.FormatRPM, pkgrepo.FormatAPK} {
			value := fmt.Sprintf("%s-%s", format, architecture)
			name := value + formatExtension(format)
			writeTestFile(t, filepath.Join(sourceDir, name), value)
			assets = append(assets, pkgrepo.Asset{
				Repository: "meigma/release",
				Format:     format,
				Path:       name,
				Digest:     buildDigest(t, value),
			})
			metadata = append(metadata, pkgrepo.PackageMetadata{
				Name:         "release-cli",
				Version:      version,
				Architecture: architecture,
			})
		}
	}

	source, err := os.OpenRoot(sourceDir)
	require.NoError(t, err)
	work, err := os.OpenRoot(workDir)
	require.NoError(t, err)
	output, err := os.OpenRoot(outputDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, source.Close())
		require.NoError(t, work.Close())
		require.NoError(t, output.Close())
	})

	fixture := &buildFixture{
		sourceDir: sourceDir,
		workDir:   workDir,
		outputDir: outputDir,
		source:    source,
		work:      work,
		output:    output,
		assets:    assets,
		metadata:  metadata,
		config:    buildConfig(),
		releaseTime: time.Date(
			2026, time.August, 21, 12, 0, 0, 0, time.UTC,
		),
		validUntil: time.Date(
			2027, time.August, 21, 12, 0, 0, 0, time.UTC,
		),
	}
	fixture.producerRPMKey = filepath.Join(workDir, "keys", "release-rpm-001.asc")
	fixture.producerAPKKey = filepath.Join(workDir, "keys", "release-apk-001.rsa.pub")
	return fixture
}

// input returns the fixture's complete [pkgrepo.BuildInput].
func (f *buildFixture) input() pkgrepo.BuildInput {
	return pkgrepo.BuildInput{
		Config:      f.config,
		Request:     pkgrepo.Request{Repository: "meigma/release", Tag: "v1.2.3"},
		Assets:      f.assets,
		Source:      f.source,
		Work:        f.work,
		Output:      f.output,
		ReleaseTime: f.releaseTime,
		ValidUntil:  f.validUntil,
	}
}

// stagedPath returns the deterministic absolute scratch package path.
func (f *buildFixture) stagedPath(index int, format pkgrepo.Format) string {
	return filepath.Join(f.workDir, "assets", fmt.Sprintf("%06d%s", index, formatExtension(format)))
}

// buildConfig returns the fixture's reviewed producer and key allowlist.
func buildConfig() pkgrepo.Config {
	return pkgrepo.Config{
		Channel: pkgrepo.ChannelStable,
		Producers: []pkgrepo.Producer{{
			Repository: "meigma/release",
			Packages:   []pkgrepo.PackageName{"release-cli"},
			RPMKey:     pkgrepo.PublicKey{Source: "keys/producer-rpm.asc", Published: "release-rpm-001.asc"},
			APKKey:     pkgrepo.PublicKey{Source: "keys/producer-apk.rsa.pub", Published: "release-apk-001.rsa.pub"},
		}},
		APTKey: pkgrepo.PublicKey{Source: "keys/repository.asc", Published: "apt-repository-001.asc"},
		RPMKey: pkgrepo.PublicKey{Source: "keys/repository.asc", Published: "rpm-repository-001.asc"},
		APKKey: pkgrepo.PublicKey{Source: "keys/repository-apk.rsa.pub", Published: "apk-index-001.rsa.pub"},
	}
}

// formatExtension returns one test source-package suffix.
func formatExtension(format pkgrepo.Format) string {
	return "." + string(format)
}

// buildDigest returns the canonical SHA-256 digest of value.
func buildDigest(t *testing.T, value string) rel.Digest {
	t.Helper()

	sum := sha256.Sum256([]byte(value))
	digest, err := rel.ParseDigest(fmt.Sprintf("sha256:%x", sum))
	require.NoError(t, err)
	return digest
}

// writeGeneratedMetadata writes the minimal observable output of the repository generator port.
func writeGeneratedMetadata(t *testing.T, root string) {
	t.Helper()

	for _, architecture := range []string{"amd64", "arm64"} {
		directory := filepath.Join(root, "apt", "dists", "stable", "main", "binary-"+architecture)
		require.NoError(t, os.MkdirAll(filepath.Join(directory, "by-hash", "SHA256"), 0o755))
		writeTestFile(t, filepath.Join(directory, "Packages"), "packages\n")
		writeTestFile(t, filepath.Join(directory, "Packages.gz"), "compressed\n")
		writeTestFile(t, filepath.Join(directory, "by-hash", "SHA256", "example"), "hash\n")
	}
	writeTestFile(t, filepath.Join(root, "apt", "dists", "stable", "Release"), "release\n")
	for _, architecture := range []string{"x86_64", "aarch64"} {
		directory := filepath.Join(root, "rpm", "stable", architecture, "repodata")
		require.NoError(t, os.MkdirAll(directory, 0o755))
		writeTestFile(t, filepath.Join(directory, "primary-example.xml.gz"), "primary\n")
		writeTestFile(t, filepath.Join(directory, "repomd.xml"), "repomd\n")
	}
	for _, architecture := range []string{"x86_64", "aarch64"} {
		writeTestFile(t, filepath.Join(root, "apk", "stable", "main", architecture, "APKINDEX.tar.gz"), "apk index\n")
	}
}

// writeTestFile creates parent directories and writes one fixture file.
func writeTestFile(t *testing.T, name, value string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(t, os.WriteFile(name, []byte(value), 0o644))
}

// assertArtifact checks one generated artifact's public semantics.
func assertArtifact(
	t *testing.T,
	artifacts []pkgrepo.Artifact,
	name string,
	cache pkgrepo.CachePolicy,
	commitRoot bool,
) {
	t.Helper()

	for _, artifact := range artifacts {
		if artifact.Path == name {
			assert.Equal(t, cache, artifact.Cache)
			assert.Equal(t, commitRoot, artifact.CommitRoot)
			return
		}
	}
	t.Errorf("artifact %s not found", name)
}
