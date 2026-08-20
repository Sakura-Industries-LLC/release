package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apkomocks "github.com/meigma/release/internal/adapter/apko/mocks"
	melangemocks "github.com/meigma/release/internal/adapter/melange/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/image"
)

const (
	// imageCommand is the envelope command path for image build.
	imageCommand = "image build"
	// imageBuildDate is a valid RFC 3339 timestamp fixture.
	imageBuildDate = "2024-01-02T03:04:05Z"
	// imageSHA is the GITHUB_SHA fixture.
	imageSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// imageRepo is the GITHUB_REPOSITORY fixture.
	imageRepo = "meigma/release"
	// imageOwner is the GITHUB_REPOSITORY_OWNER fixture.
	imageOwner = "meigma"
	// imageServer is the GITHUB_SERVER_URL fixture.
	imageServer = "https://github.com"
	// imageBinaryName is the staged binary filename.
	imageBinaryName = "release-cli"
	// imageVersion is the candidate version fixture.
	imageVersion = "1.2.3"
	// imageMelangePath is the RELEASE_MELANGE_PATH fixture.
	imageMelangePath = "/opt/melange"
	// imageApkoPath is the RELEASE_APKO_PATH fixture.
	imageApkoPath = "/opt/apko"
	// elfHeaderSize is the ELF64 header size in bytes.
	elfHeaderSize = 64
	// elfProgramHeaderSize is the ELF64 program header size in bytes.
	elfProgramHeaderSize = 56
	// elfExecType is ET_EXEC.
	elfExecType = 2
	// elfMachineAMD64 is EM_X86_64.
	elfMachineAMD64 = 62
	// elfMachineARM64 is EM_AARCH64.
	elfMachineARM64 = 183
	// elfLoadType is PT_LOAD.
	elfLoadType = 1
	// elfReadExec is PF_R|PF_X.
	elfReadExec = 5
	// elfLoadAddr is the virtual address of the single PT_LOAD segment.
	elfLoadAddr = 0x400000
	// elfAlign is the PT_LOAD alignment.
	elfAlign = 0x1000
)

// imagePorts is the injected image-build command ports.
type imagePorts struct {
	// apk is the Melange APK-build port.
	apk image.APKBuilder
	// composer is the apko compose port.
	composer image.Composer
}

// imageFactories constructs image-build ports from resolved binary paths.
type imageFactories struct {
	// newAPK constructs the Melange APK-build port.
	newAPK func(string) (image.APKBuilder, error)
	// newComposer constructs the apko compose port.
	newComposer func(string) (image.Composer, error)
}

// trackingImageFactories records whether any image-build factory was invoked.
func trackingImageFactories(t *testing.T, called *bool) imageFactories {
	t.Helper()

	return imageFactories{
		newAPK: func(string) (image.APKBuilder, error) {
			*called = true
			return unusedAPKBuilder(t), nil
		},
		newComposer: func(string) (image.Composer, error) {
			*called = true
			return unusedComposer(t), nil
		},
	}
}

// unusedAPKBuilder returns a generated mock that fails if the port is called.
func unusedAPKBuilder(t *testing.T) *melangemocks.MockAPKBuilder {
	t.Helper()

	return melangemocks.NewMockAPKBuilder(t)
}

// unusedComposer returns a generated mock that fails if the port is called.
func unusedComposer(t *testing.T) *apkomocks.MockComposer {
	t.Helper()

	return apkomocks.NewMockComposer(t)
}

// expectSuccessfulImageBuild writes the artifacts [image.Build] requires.
func expectSuccessfulImageBuild(
	t *testing.T,
	apk *melangemocks.MockAPKBuilder,
	composer *apkomocks.MockComposer,
) {
	t.Helper()

	apk.EXPECT().
		Build(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request image.APKBuildRequest) (image.APKRepositories, error) {
			require.Equal(t, "docker", request.Runner)
			require.Equal(t, imageOwner, request.Namespace)
			require.Equal(t, imageServer+"/"+imageRepo, request.GitRepoURL)
			require.Equal(t, imageSHA, request.GitCommit)
			require.Equal(t, imageBuildDate, request.BuildDate)
			require.NoError(t, os.WriteFile(request.KeyPath+".pub", []byte("pubkey\n"), 0o644))
			for _, source := range request.Sources {
				dir := filepath.Join(request.OutDir, source.Arch.String())
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "APKINDEX.tar.gz"), []byte("index\n"), 0o644))
				require.NoError(t, os.WriteFile(
					filepath.Join(dir, "release-cli-1.2.3-r0.apk"),
					[]byte("apk\n"),
					0o644,
				))
			}

			return image.APKRepositories{
				Dir:       request.OutDir,
				PublicKey: request.KeyPath + ".pub",
			}, nil
		}).
		Once()
	composer.EXPECT().
		Build(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request image.ComposeRequest) error {
			require.Equal(t, "local/release:"+imageVersion, request.Reference)
			require.NoError(t, os.WriteFile(filepath.Join(request.Dir, request.Lockfile), []byte("lock\n"), 0o644))
			require.NoError(t, os.MkdirAll(filepath.Join(request.Dir, "layout"), 0o755))
			require.NoError(
				t,
				os.WriteFile(filepath.Join(request.Dir, "layout", "index.json"), []byte("index\n"), 0o644),
			)
			require.NoError(
				t,
				os.WriteFile(filepath.Join(request.Dir, "layout", "oci-layout"), []byte("layout\n"), 0o644),
			)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(request.Dir, request.SBOMPath, "sbom-x86_64.spdx.json"),
					[]byte("sbom\n"),
					0o644,
				),
			)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(request.Dir, request.SBOMPath, "sbom-aarch64.spdx.json"),
					[]byte("sbom\n"),
					0o644,
				),
			)

			return nil
		}).
		Once()
}

// executeImageBuild runs image build with injected ports.
func executeImageBuild(
	t *testing.T,
	env map[string]string,
	args []string,
	ports imagePorts,
) (string, string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		APKBuilder: ports.apk,
		Composer:   ports.composer,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executeImageBuildFactory runs image build with observing factories.
func executeImageBuildFactory(
	t *testing.T,
	env map[string]string,
	args []string,
	factories imageFactories,
) (string, error) {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	stdout := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out: stdout,
		Err: &strings.Builder{},
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		NewAPKBuilder: factories.newAPK,
		NewComposer:   factories.newComposer,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), err
}

// decodeImageBuildResult unmarshals the envelope result as [image.BuildResult].
func decodeImageBuildResult(t *testing.T, stdout string) image.BuildResult {
	t.Helper()

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result image.BuildResult
	require.NoError(t, json.Unmarshal(raw, &result))

	return result
}

// assertImageFailureEnvelope checks stdout is one ok:false image-build envelope.
func assertImageFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, imageCommand, envelope.Command)
	assert.False(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
}

// imageEnv returns the required Actions environment for image build.
func imageEnv() map[string]string {
	return map[string]string{
		"GITHUB_REPOSITORY_OWNER": imageOwner,
		"GITHUB_SERVER_URL":       imageServer,
		"GITHUB_REPOSITORY":       imageRepo,
		"GITHUB_SHA":              imageSHA,
	}
}

// imageConfigFiles holds Melange and apko configuration paths.
type imageConfigFiles struct {
	// melange is the Melange configuration path.
	melange string
	// apko is the apko configuration path.
	apko string
}

// writeImageConfigFiles writes dummy Melange and apko configs.
func writeImageConfigFiles(t *testing.T) imageConfigFiles {
	t.Helper()

	root := t.TempDir()
	melange := filepath.Join(root, "melange.yaml")
	apko := filepath.Join(root, "apko.yaml")
	writeFile(t, melange, "package: release-cli\n")
	writeFile(t, apko, "contents: {}\n")

	return imageConfigFiles{melange: melange, apko: apko}
}

// imageInputTree is a valid extracted oci-input artifact root.
type imageInputTree struct {
	// input is the artifact root.
	input string
	// melange is the Melange configuration path.
	melange string
	// apko is the apko configuration path.
	apko string
	// amd64Path is the confined amd64 binary path.
	amd64Path string
	// arm64Path is the confined arm64 binary path.
	arm64Path string
	// amd64Digest is the canonical amd64 digest.
	amd64Digest string
	// arm64Digest is the canonical arm64 digest.
	arm64Digest string
}

// writeImageInputTree writes binaries, a projection, and config files.
func writeImageInputTree(t *testing.T) imageInputTree {
	t.Helper()

	input := t.TempDir()
	amd64Path := filepath.Join("release-cli_linux_amd64_v1", imageBinaryName)
	arm64Path := filepath.Join("release-cli_linux_arm64_v1", imageBinaryName)
	amd64 := staticELF(elfMachineAMD64)
	arm64 := staticELF(elfMachineARM64)
	writeExec(t, filepath.Join(input, amd64Path), amd64)
	writeExec(t, filepath.Join(input, arm64Path), arm64)

	amd64Digest := sha256Digest(amd64)
	arm64Digest := sha256Digest(arm64)
	projection := stage.ImageInputs{
		Schema:  stage.ImageInputsSchema,
		Profile: "go",
		Binaries: []stage.ImageInputBinary{
			{Platform: "linux/amd64", Name: imageBinaryName, Path: filepath.ToSlash(amd64Path), Digest: amd64Digest},
			{Platform: "linux/arm64", Name: imageBinaryName, Path: filepath.ToSlash(arm64Path), Digest: arm64Digest},
		},
	}
	file, err := os.Create(filepath.Join(input, stage.ImageInputsName))
	require.NoError(t, err)
	require.NoError(t, stage.EncodeImageInputs(file, projection))
	require.NoError(t, file.Close())

	configs := writeImageConfigFiles(t)

	return imageInputTree{
		input:       input,
		melange:     configs.melange,
		apko:        configs.apko,
		amd64Path:   filepath.ToSlash(amd64Path),
		arm64Path:   filepath.ToSlash(arm64Path),
		amd64Digest: amd64Digest,
		arm64Digest: arm64Digest,
	}
}

// staticELF returns a minimal static 64-bit little-endian ET_EXEC for machine.
func staticELF(machine uint16) []byte {
	buf := make([]byte, elfHeaderSize+elfProgramHeaderSize)
	copy(buf[0:], []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(buf[16:], elfExecType)
	binary.LittleEndian.PutUint16(buf[18:], machine)
	binary.LittleEndian.PutUint32(buf[20:], 1)
	binary.LittleEndian.PutUint64(buf[24:], elfLoadAddr+uint64(len(buf)))
	binary.LittleEndian.PutUint64(buf[32:], elfHeaderSize)
	binary.LittleEndian.PutUint16(buf[52:], elfHeaderSize)
	binary.LittleEndian.PutUint16(buf[54:], elfProgramHeaderSize)
	binary.LittleEndian.PutUint16(buf[56:], 1)
	binary.LittleEndian.PutUint32(buf[elfHeaderSize:], elfLoadType)
	binary.LittleEndian.PutUint32(buf[elfHeaderSize+4:], elfReadExec)
	binary.LittleEndian.PutUint64(buf[elfHeaderSize+16:], elfLoadAddr)
	binary.LittleEndian.PutUint64(buf[elfHeaderSize+24:], elfLoadAddr)
	binary.LittleEndian.PutUint64(buf[elfHeaderSize+32:], uint64(len(buf)))
	binary.LittleEndian.PutUint64(buf[elfHeaderSize+40:], uint64(len(buf)))
	binary.LittleEndian.PutUint64(buf[elfHeaderSize+48:], elfAlign)

	return buf
}

// sha256Digest returns the canonical sha256:<hex> digest of data.
func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// assertPathAbsent requires path not to exist.
func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}
