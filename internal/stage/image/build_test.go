package image_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apkomocks "github.com/meigma/release/internal/adapter/apko/mocks"
	melangemocks "github.com/meigma/release/internal/adapter/melange/mocks"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/image"
)

const (
	testBinaryName = "release-cli"
	testVersion    = "1.2.3"
	testBuildDate  = "2024-01-02T03:04:05Z"
	testNamespace  = "meigma"
	testSourceURL  = "https://github.com/meigma/release"
	testCommit     = "0123456789abcdef0123456789abcdef01234567"
	testReference  = "local/release:1.2.3"
	testAMD64Path  = "release-cli_linux_amd64_v1/release-cli"
	testARM64Path  = "release-cli_linux_arm64_v1/release-cli"
	testAMD64APK   = "release-cli-1.2.3-r0.apk"
	testARM64APK   = "release-cli-1.2.3-r0.apk"
	testMelange    = "package:\n  name: release-cli\n"
	testApko       = "contents:\n  packages:\n    - release-cli\n"
)

func TestBuildHappyPath(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	wantAPK := wantAPKRequest(tc)
	wantCompose := wantComposeRequest(tc)
	expectSuccessfulAPK(t, tc, wantAPK)
	expectSuccessfulCompose(t, tc, wantCompose)

	got, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.NoError(t, err)
	assert.Equal(t, wantBuildResult(t, tc), got)
	assertStagedLayout(t, tc)
}

func TestBuildMultipleBinaries(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	agent := "incus-agent"
	server := "incus-server"
	serverAMD64 := tc.amd64
	serverARM64 := tc.arm64

	agentAMD64Path := "agent_linux_amd64/incus-agent"
	agentARM64Path := "agent_linux_arm64/incus-agent"
	serverAMD64Path := "server_linux_amd64/incus-server"
	serverARM64Path := "server_linux_arm64/incus-server"
	tc.input.Source = fstest.MapFS{
		agentAMD64Path:  {Data: tc.amd64},
		agentARM64Path:  {Data: tc.arm64},
		serverAMD64Path: {Data: serverAMD64},
		serverARM64Path: {Data: serverARM64},
	}
	tc.input.Binaries = []image.BuildBinary{
		{Platform: image.PlatformAMD64, Name: agent, Path: agentAMD64Path, Digest: digestOf(t, tc.amd64)},
		{Platform: image.PlatformAMD64, Name: server, Path: serverAMD64Path, Digest: digestOf(t, serverAMD64)},
		{Platform: image.PlatformARM64, Name: server, Path: serverARM64Path, Digest: digestOf(t, serverARM64)},
		{Platform: image.PlatformARM64, Name: agent, Path: agentARM64Path, Digest: digestOf(t, tc.arm64)},
	}

	wantAPK := wantAPKRequest(tc)
	wantCompose := wantComposeRequest(tc)
	expectSuccessfulAPK(t, tc, wantAPK)
	expectSuccessfulCompose(t, tc, wantCompose)

	got, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.NoError(t, err)
	assert.Equal(t, []string{agent, server}, got.Binaries)
	assert.Equal(t, []image.BinaryDigest{
		{Name: agent, Digest: digestOf(t, tc.amd64).String()},
		{Name: server, Digest: digestOf(t, serverAMD64).String()},
	}, got.Packages[0].BinaryDigests)
	assertMode(t, tc.work, filepath.Join("sources", "x86_64", agent), 0o755)
	assertMode(t, tc.work, filepath.Join("sources", "x86_64", server), 0o755)
	assertMode(t, tc.work, filepath.Join("sources", "aarch64", agent), 0o755)
	assertMode(t, tc.work, filepath.Join("sources", "aarch64", server), 0o755)

	checksums, err := tc.output.ReadFile("canonical-binaries.sha256")
	require.NoError(t, err)
	assert.Equal(t,
		hexDigest(t, tc.amd64)+"  sources/x86_64/"+agent+"\n"+
			hexDigest(t, serverAMD64)+"  sources/x86_64/"+server+"\n"+
			hexDigest(t, tc.arm64)+"  sources/aarch64/"+agent+"\n"+
			hexDigest(t, serverARM64)+"  sources/aarch64/"+server+"\n",
		string(checksums),
	)
}

func TestBuildDigestMismatch(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	tc.input.Binaries[0].Digest = mustDigest(t, []byte("not-the-binary"))
	expectIdlePorts(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has digest")
	assert.Contains(t, err.Error(), "expected")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildELFFailure(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	wrong := staticELF(elf.EM_AARCH64)
	tc.input.Source = fstest.MapFS{
		testAMD64Path: {Data: wrong},
		testARM64Path: {Data: tc.arm64},
	}
	tc.input.Binaries[0].Digest = digestOf(t, wrong)
	expectIdlePorts(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ELF machine")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildRejectsPopulatedRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seed    func(t *testing.T, tc *buildTest)
		wantErr string
	}{
		{
			name: "unrelated file in work root",
			seed: func(t *testing.T, tc *buildTest) {
				t.Helper()
				require.NoError(t, tc.work.WriteFile("stale.txt", []byte("old"), 0o644))
			},
			wantErr: "work root is populated: stale.txt",
		},
		{
			name: "unrelated file in output root",
			seed: func(t *testing.T, tc *buildTest) {
				t.Helper()
				require.NoError(t, tc.output.WriteFile("leftover.bin", []byte("old"), 0o644))
			},
			wantErr: "output root is populated: leftover.bin",
		},
		{
			name: "managed sources directory already exists",
			seed: func(t *testing.T, tc *buildTest) {
				t.Helper()
				require.NoError(t, tc.work.MkdirAll(filepath.Join("sources", "x86_64"), 0o755))
			},
			wantErr: "work root is populated: sources",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tc := newBuildTest(t)
			test.seed(t, tc)
			expectIdlePorts(tc)

			_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			assert.NotContains(t, err.Error(), "must not run")
		})
	}
}

func TestBuildMissingAPKIndex(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	tc.apk.EXPECT().
		Build(mock.Anything, wantAPKRequest(tc)).
		Run(func(_ context.Context, request image.APKBuildRequest) {
			writeSigningKey(t, request)
			writeAPK(t, request.OutDir, image.ArchX8664, testAMD64APK)
			writeFile(t, filepath.Join(request.OutDir, "x86_64", testAMD64APK), []byte("apk"))
			writeAPK(t, request.OutDir, image.ArchAArch64, testARM64APK)
			writeFile(t, filepath.Join(request.OutDir, "aarch64", "APKINDEX.tar.gz"), []byte("idx"))
		}).
		Return(wantRepositories(tc), nil).
		Once()
	expectIdleComposer(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APKINDEX.tar.gz")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildEmptyAPKIndex(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	tc.apk.EXPECT().
		Build(mock.Anything, wantAPKRequest(tc)).
		Run(func(_ context.Context, request image.APKBuildRequest) {
			writeSigningKey(t, request)
			writeCompletePackages(t, request.OutDir)
			writeFile(t, filepath.Join(request.OutDir, "x86_64", "APKINDEX.tar.gz"), nil)
		}).
		Return(wantRepositories(tc), nil).
		Once()
	expectIdleComposer(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APKINDEX.tar.gz")
	assert.Contains(t, err.Error(), "is empty")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildEmptyAPK(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	tc.apk.EXPECT().
		Build(mock.Anything, wantAPKRequest(tc)).
		Run(func(_ context.Context, request image.APKBuildRequest) {
			writeSigningKey(t, request)
			writeCompletePackages(t, request.OutDir)
			writeFile(t, filepath.Join(request.OutDir, "x86_64", testAMD64APK), nil)
		}).
		Return(wantRepositories(tc), nil).
		Once()
	expectIdleComposer(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), testAMD64APK)
	assert.Contains(t, err.Error(), "is empty")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildTwoAPKs(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	tc.apk.EXPECT().
		Build(mock.Anything, wantAPKRequest(tc)).
		Run(func(_ context.Context, request image.APKBuildRequest) {
			writeSigningKey(t, request)
			writeAPK(t, request.OutDir, image.ArchX8664, testAMD64APK)
			writeFile(t, filepath.Join(request.OutDir, "x86_64", "APKINDEX.tar.gz"), []byte("idx"))
			writeAPK(t, request.OutDir, image.ArchX8664, "extra.apk")
			writeAPK(t, request.OutDir, image.ArchAArch64, testARM64APK)
			writeFile(t, filepath.Join(request.OutDir, "aarch64", "APKINDEX.tar.gz"), []byte("idx"))
		}).
		Return(wantRepositories(tc), nil).
		Once()
	expectIdleComposer(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "found 2 apk files, want 1")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildMissingLayout(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	expectSuccessfulAPK(t, tc, wantAPKRequest(tc))
	tc.composer.EXPECT().
		Build(mock.Anything, wantComposeRequest(tc)).
		Return(nil).
		Once()

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apko.lock.json")
}

func TestBuildEmptyLayoutIndex(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	expectSuccessfulAPK(t, tc, wantAPKRequest(tc))
	tc.composer.EXPECT().
		Build(mock.Anything, wantComposeRequest(tc)).
		Run(func(_ context.Context, request image.ComposeRequest) {
			writeFile(t, filepath.Join(request.Dir, request.Lockfile), []byte("lock"))
			writeFile(t, filepath.Join(request.Dir, "layout", "index.json"), nil)
			writeFile(t, filepath.Join(request.Dir, "layout", "oci-layout"), []byte("oci"))
			writeFile(t, filepath.Join(request.Dir, request.SBOMPath, "sbom-x86_64.spdx.json"), []byte("sbom64"))
			writeFile(t, filepath.Join(request.Dir, request.SBOMPath, "sbom-aarch64.spdx.json"), []byte("sbomarm"))
		}).
		Return(nil).
		Once()

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "layout/index.json")
	assert.Contains(t, err.Error(), "is empty")
}

func TestBuildMissingSBOM(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	expectSuccessfulAPK(t, tc, wantAPKRequest(tc))
	tc.composer.EXPECT().
		Build(mock.Anything, wantComposeRequest(tc)).
		Run(func(_ context.Context, request image.ComposeRequest) {
			writeFile(t, filepath.Join(request.Dir, request.Lockfile), []byte("lock"))
			writeFile(t, filepath.Join(request.Dir, "layout", "index.json"), []byte("index"))
			writeFile(t, filepath.Join(request.Dir, "layout", "oci-layout"), []byte("oci"))
			writeFile(t, filepath.Join(request.Dir, request.SBOMPath, "sbom-x86_64.spdx.json"), []byte("sbom"))
		}).
		Return(nil).
		Once()

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sbom-aarch64.spdx.json")
}

func TestBuildRepositoriesCrossCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		repos   func(tc *buildTest) image.APKRepositories
		wantErr string
	}{
		{
			name: "dir mismatch",
			repos: func(tc *buildTest) image.APKRepositories {
				return image.APKRepositories{
					Dir:       filepath.Join(tc.output.Name(), "other"),
					PublicKey: wantRepositories(tc).PublicKey,
				}
			},
			wantErr: "apk repository dir",
		},
		{
			name: "public key mismatch",
			repos: func(tc *buildTest) image.APKRepositories {
				return image.APKRepositories{
					Dir:       wantRepositories(tc).Dir,
					PublicKey: filepath.Join(tc.work.Name(), "other.pub"),
				}
			},
			wantErr: "apk public key",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tc := newBuildTest(t)
			tc.apk.EXPECT().
				Build(mock.Anything, wantAPKRequest(tc)).
				Run(func(_ context.Context, request image.APKBuildRequest) {
					writeSigningKey(t, request)
					writeCompletePackages(t, request.OutDir)
				}).
				Return(test.repos(tc), nil).
				Once()
			expectIdleComposer(tc)

			_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			assert.NotContains(t, err.Error(), "must not run")
		})
	}
}

func TestBuildAPKFailureAbortsComposer(t *testing.T) {
	t.Parallel()

	tc := newBuildTest(t)
	tc.apk.EXPECT().
		Build(mock.Anything, mock.AnythingOfType("image.APKBuildRequest")).
		Return(image.APKRepositories{}, errors.New("melange failed")).
		Once()
	expectIdleComposer(tc)

	_, err := image.Build(context.Background(), tc.input, tc.apk, tc.composer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "melange failed")
	assert.NotContains(t, err.Error(), "must not run")
}

func TestBuildRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := newBuildTest(t)

	tests := []struct {
		name    string
		ctx     context.Context
		input   image.BuildInput
		apk     image.APKBuilder
		compose image.Composer
		wantErr string
	}{
		{
			name:    "nil context",
			input:   valid.input,
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "context is nil",
		},
		{
			name: "nil source",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Source = nil

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "source is nil",
		},
		{
			name: "nil work",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Work = nil

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "work root is nil",
		},
		{
			name: "nil output",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Output = nil

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "output root is nil",
		},
		{
			name: "nil Melange config",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.MelangeConfig = nil

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "melange configuration reader is nil",
		},
		{
			name: "nil apko config",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.ApkoConfig = nil

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "apko config is nil",
		},
		{
			name:    "nil APK builder",
			ctx:     context.Background(),
			input:   valid.input,
			compose: valid.composer,
			wantErr: "apk builder is nil",
		},
		{
			name:    "nil composer",
			ctx:     context.Background(),
			input:   valid.input,
			apk:     valid.apk,
			wantErr: "composer is nil",
		},
		{
			name: "zero version",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Version = rel.Version{}

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "version is zero",
		},
		{
			name: "bad build date",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.BuildDate = "not-rfc3339"

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "build date",
		},
		{
			name: "empty namespace",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Namespace = ""

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "namespace is empty",
		},
		{
			name: "empty source URL",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.SourceURL = ""

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "source URL is empty",
		},
		{
			name: "empty commit",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Commit = ""

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "commit is empty",
		},
		{
			name: "empty reference",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Reference = ""

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "reference is empty",
		},
		{
			name: "one binary",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Binaries = in.Binaries[:1]

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: `missing binary "release-cli" for linux/arm64`,
		},
		{
			name: "asymmetric names",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Binaries = []image.BuildBinary{valid.input.Binaries[0], valid.input.Binaries[1]}
				in.Binaries[1].Name = "other"

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: `missing binary "other" for linux/amd64`,
		},
		{
			name: "escaping path",
			ctx:  context.Background(),
			input: func() image.BuildInput {
				in := valid.input
				in.Binaries = []image.BuildBinary{valid.input.Binaries[0], valid.input.Binaries[1]}
				in.Binaries[0].Path = "../escape"

				return in
			}(),
			apk:     valid.apk,
			compose: valid.composer,
			wantErr: "not a confined local path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := image.Build(test.ctx, test.input, test.apk, test.compose)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

// buildTest is one Build invocation and the collaborators it needs.
type buildTest struct {
	// input is the candidate image build request.
	input image.BuildInput
	// work is the scratch workspace root.
	work *os.Root
	// output is the authoritative artifact output root.
	output *os.Root
	// apk is the Melange port.
	apk *melangemocks.MockAPKBuilder
	// composer is the apko port.
	composer *apkomocks.MockComposer
	// amd64 is the staged linux/amd64 ELF fixture.
	amd64 []byte
	// arm64 is the staged linux/arm64 ELF fixture.
	arm64 []byte
}

// newBuildTest builds a two-platform source tree and unused write ports.
func newBuildTest(t *testing.T) *buildTest {
	t.Helper()

	amd64 := staticELF(elf.EM_X86_64)
	arm64 := staticELF(elf.EM_AARCH64)
	work, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, work.Close())
	})
	output, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, output.Close())
	})

	version, err := rel.ParseVersion(testVersion)
	require.NoError(t, err)

	return &buildTest{
		input: image.BuildInput{
			Binaries: []image.BuildBinary{
				{
					Platform: image.PlatformAMD64,
					Name:     testBinaryName,
					Path:     testAMD64Path,
					Digest:   digestOf(t, amd64),
				},
				{
					Platform: image.PlatformARM64,
					Name:     testBinaryName,
					Path:     testARM64Path,
					Digest:   digestOf(t, arm64),
				},
			},
			Source: fstest.MapFS{
				testAMD64Path: {Data: amd64},
				testARM64Path: {Data: arm64},
			},
			Work:          work,
			Output:        output,
			Version:       version,
			BuildDate:     testBuildDate,
			Namespace:     testNamespace,
			SourceURL:     testSourceURL,
			Commit:        testCommit,
			Reference:     testReference,
			MelangeConfig: bytes.NewBufferString(testMelange),
			ApkoConfig:    bytes.NewBufferString(testApko),
		},
		work:     work,
		output:   output,
		apk:      melangemocks.NewMockAPKBuilder(t),
		composer: apkomocks.NewMockComposer(t),
		amd64:    amd64,
		arm64:    arm64,
	}
}

// wantAPKRequest is the exact [image.APKBuildRequest] Build must send.
func wantAPKRequest(tc *buildTest) image.APKBuildRequest {
	return image.APKBuildRequest{
		Config:   filepath.Join(tc.output.Name(), "configuration", "melange.yaml"),
		VarsFile: filepath.Join(tc.work.Name(), "vars.json"),
		KeyPath:  filepath.Join(tc.work.Name(), "apk-signing.rsa"),
		OutDir:   filepath.Join(tc.output.Name(), "packages"),
		Sources: []image.APKBuildSource{
			{Arch: image.ArchX8664, Dir: filepath.Join(tc.work.Name(), "sources", "x86_64")},
			{Arch: image.ArchAArch64, Dir: filepath.Join(tc.work.Name(), "sources", "aarch64")},
		},
		Runner:     "docker",
		Namespace:  testNamespace,
		BuildDate:  testBuildDate,
		GitRepoURL: testSourceURL,
		GitCommit:  testCommit,
	}
}

// wantComposeRequest is the exact [image.ComposeRequest] Build must send.
func wantComposeRequest(tc *buildTest) image.ComposeRequest {
	return image.ComposeRequest{
		Dir:        tc.output.Name(),
		Config:     "configuration/apko.yaml",
		Repository: "packages",
		Keyring:    "apk-signing.rsa.pub",
		Lockfile:   "apko.lock.json",
		SBOMPath:   "sboms",
		Layout:     "layout/",
		Reference:  testReference,
		BuildDate:  testBuildDate,
		Arches:     []image.APKArch{image.ArchX8664, image.ArchAArch64},
		Annotations: []image.Annotation{
			{Key: "org.opencontainers.image.version", Value: testVersion},
			{Key: "org.opencontainers.image.revision", Value: testCommit},
		},
	}
}

// wantRepositories is the matching [image.APKRepositories] for tc.
func wantRepositories(tc *buildTest) image.APKRepositories {
	request := wantAPKRequest(tc)

	return image.APKRepositories{Dir: request.OutDir, PublicKey: request.KeyPath + ".pub"}
}

// wantBuildResult is the document Build should emit for tc.
func wantBuildResult(t *testing.T, tc *buildTest) image.BuildResult {
	t.Helper()

	return image.BuildResult{
		Schema:    image.BuildSchema,
		Version:   testVersion,
		Binaries:  []string{testBinaryName},
		Work:      tc.work.Name(),
		Output:    tc.output.Name(),
		BuildDate: testBuildDate,
		Packages: []image.PackageResult{
			{
				Platform: image.PlatformAMD64.String(),
				Arch:     image.ArchX8664.String(),
				Package:  path.Join("packages", "x86_64", testAMD64APK),
				BinaryDigests: []image.BinaryDigest{{
					Name:   testBinaryName,
					Digest: digestOf(t, tc.amd64).String(),
				}},
			},
			{
				Platform: image.PlatformARM64.String(),
				Arch:     image.ArchAArch64.String(),
				Package:  path.Join("packages", "aarch64", testARM64APK),
				BinaryDigests: []image.BinaryDigest{{
					Name:   testBinaryName,
					Digest: digestOf(t, tc.arm64).String(),
				}},
			},
		},
	}
}

// expectSuccessfulAPK writes a valid repository when the request matches want.
func expectSuccessfulAPK(t *testing.T, tc *buildTest, want image.APKBuildRequest) {
	t.Helper()

	tc.apk.EXPECT().
		Build(mock.Anything, want).
		Run(func(_ context.Context, request image.APKBuildRequest) {
			writeSigningKey(t, request)
			writeCompletePackages(t, request.OutDir)
		}).
		Return(wantRepositories(tc), nil).
		Once()
}

// expectSuccessfulCompose writes lock, layout, and SBOM files when the request matches want.
func expectSuccessfulCompose(t *testing.T, tc *buildTest, want image.ComposeRequest) {
	t.Helper()

	tc.composer.EXPECT().
		Build(mock.Anything, want).
		Run(func(_ context.Context, request image.ComposeRequest) {
			writeFile(t, filepath.Join(request.Dir, request.Lockfile), []byte("lock"))
			writeFile(t, filepath.Join(request.Dir, "layout", "index.json"), []byte("index"))
			writeFile(t, filepath.Join(request.Dir, "layout", "oci-layout"), []byte("oci"))
			writeFile(t, filepath.Join(request.Dir, request.SBOMPath, "sbom-x86_64.spdx.json"), []byte("sbom64"))
			writeFile(t, filepath.Join(request.Dir, request.SBOMPath, "sbom-aarch64.spdx.json"), []byte("sbomarm"))
		}).
		Return(nil).
		Once()
}

// expectIdlePorts fails the test if either port is invoked.
func expectIdlePorts(tc *buildTest) {
	tc.apk.EXPECT().
		Build(mock.Anything, mock.AnythingOfType("image.APKBuildRequest")).
		Return(image.APKRepositories{}, errors.New("apk must not run")).
		Maybe()
	expectIdleComposer(tc)
}

// expectIdleComposer fails the test if [image.Composer.Build] is invoked.
func expectIdleComposer(tc *buildTest) {
	tc.composer.EXPECT().
		Build(mock.Anything, mock.AnythingOfType("image.ComposeRequest")).
		Return(errors.New("composer must not run")).
		Maybe()
}

// assertStagedLayout checks workspace files, modes, checksums, and copied configs.
func assertStagedLayout(t *testing.T, tc *buildTest) {
	t.Helper()

	assertMode(t, tc.work, filepath.Join("sources", "x86_64", testBinaryName), 0o755)
	assertMode(t, tc.work, filepath.Join("sources", "aarch64", testBinaryName), 0o755)
	assertMode(t, tc.output, filepath.Join("configuration", "melange.yaml"), 0o644)
	assertMode(t, tc.output, filepath.Join("configuration", "apko.yaml"), 0o644)
	assertMode(t, tc.output, "apk-signing.rsa.pub", 0o644)

	vars, err := tc.work.ReadFile("vars.json")
	require.NoError(t, err)
	// Melange reads vars.json, so the JSON must be semantically right, and the
	// engine promises compact bytes with a single trailing newline.
	wantVars := fmt.Sprintf("{%q:%q}\n", "version", testVersion)
	assert.Equal(t, wantVars, string(vars))

	melange, err := tc.output.ReadFile(filepath.Join("configuration", "melange.yaml"))
	require.NoError(t, err)
	assert.Equal(t, []byte(testMelange), melange)

	apko, err := tc.output.ReadFile(filepath.Join("configuration", "apko.yaml"))
	require.NoError(t, err)
	assert.Equal(t, []byte(testApko), apko)

	checksums, err := tc.output.ReadFile("canonical-binaries.sha256")
	require.NoError(t, err)
	assert.Equal(t, wantChecksums(t, tc), string(checksums))
}

// wantChecksums is the GNU coreutils listing with x86_64 first.
func wantChecksums(t *testing.T, tc *buildTest) string {
	t.Helper()

	return hexDigest(t, tc.amd64) + "  sources/x86_64/" + testBinaryName + "\n" +
		hexDigest(t, tc.arm64) + "  sources/aarch64/" + testBinaryName + "\n"
}

// assertMode requires name under root to be a regular file with perm.
func assertMode(t *testing.T, root *os.Root, name string, perm fs.FileMode) {
	t.Helper()

	info, err := root.Stat(name)
	require.NoError(t, err, name)
	require.True(t, info.Mode().IsRegular(), name)
	assert.Equal(t, perm, info.Mode().Perm(), name)
}

// writeCompletePackages writes one APK and APKINDEX per architecture.
func writeCompletePackages(t *testing.T, outDir string) {
	t.Helper()

	writeAPK(t, outDir, image.ArchX8664, testAMD64APK)
	writeFile(t, filepath.Join(outDir, "x86_64", "APKINDEX.tar.gz"), []byte("idx"))
	writeAPK(t, outDir, image.ArchAArch64, testARM64APK)
	writeFile(t, filepath.Join(outDir, "aarch64", "APKINDEX.tar.gz"), []byte("idx"))
}

// writeAPK writes one nonempty APK for arch under outDir.
func writeAPK(t *testing.T, outDir string, arch image.APKArch, name string) {
	t.Helper()

	writeFile(t, filepath.Join(outDir, arch.String(), name), []byte("apk"))
}

// writeSigningKey writes the generated public key next to request.KeyPath.
func writeSigningKey(t *testing.T, request image.APKBuildRequest) {
	t.Helper()

	writeFile(t, request.KeyPath+".pub", []byte("public-key"))
}

// writeFile creates parent directories and writes data to name.
func writeFile(t *testing.T, name string, data []byte) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(t, os.WriteFile(name, data, 0o644))
}

// digestOf returns the canonical sha256 digest of data.
func digestOf(t *testing.T, data []byte) rel.Digest {
	t.Helper()

	return mustDigest(t, data)
}

// mustDigest parses the sha256 digest of data.
func mustDigest(t *testing.T, data []byte) rel.Digest {
	t.Helper()

	digest, err := rel.ParseDigest("sha256:" + hexDigest(t, data))
	require.NoError(t, err)

	return digest
}

// hexDigest returns the lowercase SHA-256 hex of data.
func hexDigest(_ *testing.T, data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// staticELF returns a minimal static 64-bit little-endian ET_EXEC for machine.
func staticELF(machine elf.Machine) []byte {
	var hdr elf.Header64
	hdr.Ident[0] = 0x7f
	hdr.Ident[1] = 'E'
	hdr.Ident[2] = 'L'
	hdr.Ident[3] = 'F'
	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	hdr.Type = uint16(elf.ET_EXEC)
	hdr.Machine = uint16(machine)
	hdr.Version = uint32(elf.EV_CURRENT)
	hdr.Ehsize = uint16(binary.Size(hdr))
	hdr.Phentsize = uint16(binary.Size(elf.Prog64{}))
	hdr.Shentsize = uint16(binary.Size(elf.Section64{}))

	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, hdr); err != nil {
		panic(err)
	}

	return buf.Bytes()
}
