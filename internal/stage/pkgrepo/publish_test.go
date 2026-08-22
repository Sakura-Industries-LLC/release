package pkgrepo_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	cosignmocks "github.com/meigma/release/internal/adapter/cosign/mocks"
	ghattestmocks "github.com/meigma/release/internal/adapter/ghattest/mocks"
	ghrelmocks "github.com/meigma/release/internal/adapter/ghrel/mocks"
	gpgmocks "github.com/meigma/release/internal/adapter/gpg/mocks"
	pkginstallmocks "github.com/meigma/release/internal/adapter/pkginstall/mocks"
	metamocks "github.com/meigma/release/internal/adapter/pkgmeta/mocks"
	verifymocks "github.com/meigma/release/internal/adapter/pkgverify/mocks"
	r2mocks "github.com/meigma/release/internal/adapter/r2/mocks"
	generatormocks "github.com/meigma/release/internal/adapter/repogen/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/pkgrepo"
	"github.com/meigma/release/internal/stage/pubgh"
)

// TestPublisherPublishesVerifiedRelease proves the complete package-repository orchestration contract.
func TestPublisherPublishesVerifiedRelease(t *testing.T) {
	t.Parallel()

	fixture := newPublisherFixture(t)
	releases := ghrelmocks.NewMockReleaseSource(t)
	bundles := cosignmocks.NewMockBlobVerifier(t)
	attestations := ghattestmocks.NewMockAttestor(t)
	store := r2mocks.NewMockStore(t)
	inspector := metamocks.NewMockInspector(t)
	verifier := verifymocks.NewMockVerifier(t)
	generator := generatormocks.NewMockGenerator(t)
	signer := gpgmocks.NewMockSigner(t)
	installer := pkginstallmocks.NewMockInstaller(t)

	releases.EXPECT().Fetch(mock.Anything, pkgrepo.ReleaseRequest{
		Repository: "meigma/release",
		Tag:        "v1.2.3",
	}, mock.Anything).RunAndReturn(fixture.fetchRelease).Once()
	bundles.EXPECT().Verify(mock.Anything, mock.MatchedBy(func(request pubgh.BlobVerification) bool {
		return request.Identity == "https://github.com/shared/workflows/.github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567"
	})).Return(nil).Once()
	attestations.EXPECT().Verify(mock.Anything, mock.MatchedBy(func(request pkgrepo.AttestationRequest) bool {
		return request.Repository == "meigma/release" &&
			request.SignerWorkflow == "shared/workflows/.github/workflows/publish-github-release.yml"
	})).Return(nil).Times(len(fixture.assets))
	store.EXPECT().List(mock.Anything).Return(nil, nil).Once()
	for index, asset := range fixture.assets {
		inspector.EXPECT().Inspect(
			mock.Anything,
			asset.format,
			filepath.Join(fixture.workDir, "assets", fmt.Sprintf("%06d.%s", index, asset.format)),
		).Return(asset.metadata, nil).Once()
		if asset.format == pkgrepo.FormatRPM || asset.format == pkgrepo.FormatAPK {
			verifier.EXPECT().Verify(mock.Anything, mock.Anything).Return(nil).Once()
		}
	}
	generator.EXPECT().
		Generate(mock.Anything, mock.Anything).
		Run(func(_ context.Context, request pkgrepo.GenerateRequest) {
			writeGeneratedMetadata(t, request.Root)
		}).
		Return(nil).
		Once()
	signer.EXPECT().ClearSign(mock.Anything, mock.Anything).Run(func(_ context.Context, request pkgrepo.SignRequest) {
		writeTestFile(t, request.Output, "signed apt\n")
	}).Return(nil).Once()
	signer.EXPECT().DetachSign(mock.Anything, mock.Anything).Run(func(_ context.Context, request pkgrepo.SignRequest) {
		writeTestFile(t, request.Output, "signed rpm\n")
	}).Return(nil).Twice()
	installer.EXPECT().Verify(mock.Anything, mock.MatchedBy(func(request pkgrepo.InstallRequest) bool {
		return request.Root != nil && request.APTKey == "keys/apt-repository-001.asc" &&
			assert.Equal(t, []string{"keys/rpm-repository-001.asc", "keys/release-rpm-001.asc"}, request.RPMKeys) &&
			assert.Equal(t, []string{"keys/apk-index-001.rsa.pub", "keys/release-apk-001.rsa.pub"}, request.APKKeys)
	})).Return(nil).Once()
	store.EXPECT().Stat(mock.Anything, mock.Anything).Return(pkgrepo.StoredContent{}, false, nil).Times(26)
	store.EXPECT().Upload(mock.Anything, mock.Anything).Return(nil).Times(26)
	installer.EXPECT().Verify(mock.Anything, mock.MatchedBy(func(request pkgrepo.InstallRequest) bool {
		return request.Root == nil && request.Origin == "https://pkgs.meigma.dev" &&
			assert.ElementsMatch(t, []pkgrepo.PackageName{"release-cli"}, request.Packages)
	})).Return(nil).Once()

	publisher := pkgrepo.NewPublisher(pkgrepo.PublisherOptions{
		Releases:     releases,
		Bundles:      bundles,
		Attestations: attestations,
		Store:        store,
		Inspector:    inspector,
		Verifier:     verifier,
		Generator:    generator,
		Signer:       signer,
		Installer:    installer,
	})
	result, err := publisher.Publish(context.Background(), fixture.input())
	require.NoError(t, err)
	assert.Equal(t, pkgrepo.PublishStatePublished, result.State)
	assert.Equal(t, pkgrepo.Repository("meigma/release"), result.Repository)
	assert.Equal(t, "v1.2.3", result.Tag)
	assert.Equal(t, 26, result.Artifacts)
	assert.Equal(t, 26, result.Uploaded)
}

// publisherAsset describes one downloaded native package fixture.
type publisherAsset struct {
	name     string
	content  string
	format   pkgrepo.Format
	metadata pkgrepo.PackageMetadata
	digest   rel.Digest
}

// publisherFixture owns one complete publication's confined roots and release assets.
type publisherFixture struct {
	keysDir   string
	sourceDir string
	workDir   string
	outputDir string
	keys      *os.Root
	source    *os.Root
	work      *os.Root
	output    *os.Root
	assets    []publisherAsset
}

// newPublisherFixture creates one complete six-package release and reviewed key set.
func newPublisherFixture(t *testing.T) *publisherFixture {
	t.Helper()

	parent := t.TempDir()
	fixture := &publisherFixture{
		keysDir:   filepath.Join(parent, "keys"),
		sourceDir: filepath.Join(parent, "source"),
		workDir:   filepath.Join(parent, "work"),
		outputDir: filepath.Join(parent, "output"),
	}
	for _, directory := range []string{fixture.keysDir, fixture.sourceDir, fixture.workDir, fixture.outputDir} {
		require.NoError(t, os.Mkdir(directory, 0o755))
	}
	for _, key := range []string{"repository.asc", "repository-apk.rsa.pub", "producer-rpm.asc", "producer-apk.rsa.pub"} {
		writeTestFile(t, filepath.Join(fixture.keysDir, "keys", key), key+"\n")
	}
	version, err := rel.ParseVersion("1.2.3")
	require.NoError(t, err)
	for _, architecture := range []pkgrepo.Architecture{pkgrepo.ArchitectureAMD64, pkgrepo.ArchitectureARM64} {
		for _, format := range []pkgrepo.Format{pkgrepo.FormatDEB, pkgrepo.FormatRPM, pkgrepo.FormatAPK} {
			content := fmt.Sprintf("%s-%s", format, architecture)
			fixture.assets = append(fixture.assets, publisherAsset{
				name:    fmt.Sprintf("release-cli_1.2.3_linux_%s.%s", architecture, format),
				content: content,
				format:  format,
				metadata: pkgrepo.PackageMetadata{
					Name:         "release-cli",
					Version:      version,
					Architecture: architecture,
				},
				digest: buildDigest(t, content),
			})
		}
	}
	fixture.keys, err = os.OpenRoot(fixture.keysDir)
	require.NoError(t, err)
	fixture.source, err = os.OpenRoot(fixture.sourceDir)
	require.NoError(t, err)
	fixture.work, err = os.OpenRoot(fixture.workDir)
	require.NoError(t, err)
	fixture.output, err = os.OpenRoot(fixture.outputDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, fixture.keys.Close())
		require.NoError(t, fixture.source.Close())
		require.NoError(t, fixture.work.Close())
		require.NoError(t, fixture.output.Close())
	})
	return fixture
}

// input returns the complete publication request for this fixture.
func (f *publisherFixture) input() pkgrepo.PublishInput {
	return pkgrepo.PublishInput{
		Config: pkgrepo.PublicationConfig{
			Origin:     "https://pkgs.meigma.dev",
			Repository: buildConfig(),
			Sources: []pkgrepo.SourcePolicy{
				{
					Repository:        "meigma/release",
					ChecksumIdentity:  "https://github.com/shared/workflows/.github/workflows/go-pre-publish.yml@0123456789abcdef0123456789abcdef01234567",
					AttestationSigner: "shared/workflows/.github/workflows/publish-github-release.yml",
				},
			},
		},
		Request: pkgrepo.Request{Repository: "meigma/release", Tag: "v1.2.3"},
		Keys:    f.keys,
		Source:  f.source,
		Work:    f.work,
		Output:  f.output,
	}
}

// fetchRelease writes and returns the fixture's exact closed GitHub Release asset set.
func (f *publisherFixture) fetchRelease(
	_ context.Context,
	_ pkgrepo.ReleaseRequest,
	destination *os.Root,
) (pkgrepo.Release, error) {
	assets := make([]pkgrepo.ReleaseAsset, 0, len(f.assets)+2)
	var checksums strings.Builder
	for _, asset := range f.assets {
		writeRootFile(destination, asset.name, asset.content)
		fmt.Fprintf(&checksums, "%s  %s\n", strings.TrimPrefix(asset.digest.String(), "sha256:"), asset.name)
		name, _ := stage.ParseAssetName(asset.name)
		assets = append(
			assets,
			pkgrepo.ReleaseAsset{Name: name, Path: asset.name, Digest: asset.digest, Size: int64(len(asset.content))},
		)
	}
	controls := map[string]string{
		"checksums.txt":               checksums.String(),
		"checksums.txt.sigstore.json": "{}\n",
	}
	for name, content := range controls {
		writeRootFile(destination, name, content)
		digest := digestString(content)
		assetName, _ := stage.ParseAssetName(name)
		assets = append(
			assets,
			pkgrepo.ReleaseAsset{Name: assetName, Path: name, Digest: digest, Size: int64(len(content))},
		)
	}
	return pkgrepo.Release{
		Repository:  "meigma/release",
		Tag:         "v1.2.3",
		Commit:      strings.Repeat("a", 40),
		PublishedAt: time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC),
		Assets:      assets,
	}, nil
}

// writeRootFile writes one confined release asset.
func writeRootFile(root *os.Root, name, content string) {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		panic(err)
	}
	if _, err = file.WriteString(content); err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}
}

// digestString returns the canonical SHA-256 digest of content.
func digestString(content string) rel.Digest {
	sum := sha256.Sum256([]byte(content))
	digest, err := rel.ParseDigest(fmt.Sprintf("sha256:%x", sum))
	if err != nil {
		panic(err)
	}
	return digest
}
