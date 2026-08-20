package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func TestImageBuildMissingValuesAreUsage(t *testing.T) {
	t.Parallel()

	fixture := writeImageConfigFiles(t)
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "missing input",
			env:  imageEnv(),
			args: []string{
				"image", "build",
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "--input is required",
		},
		{
			name: "missing work",
			env:  imageEnv(),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "--work is required",
		},
		{
			name: "missing output",
			env:  imageEnv(),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "--output is required",
		},
		{
			name: "missing build date",
			env:  imageEnv(),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--version", imageVersion,
			},
			want: "--build-date is required",
		},
		{
			name: "non-RFC-3339 build date",
			env:  imageEnv(),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", "yesterday",
				"--version", imageVersion,
			},
			want: "--build-date must be RFC 3339",
		},
		{
			name: "malformed version",
			env:  imageEnv(),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", "v1.2.3",
			},
			want: "v prefix",
		},
		{
			name: "missing GITHUB_SHA",
			env:  omitEnv(imageEnv(), "GITHUB_SHA"),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "GITHUB_SHA is required",
		},
		{
			name: "missing GITHUB_REPOSITORY_OWNER",
			env:  omitEnv(imageEnv(), "GITHUB_REPOSITORY_OWNER"),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "GITHUB_REPOSITORY_OWNER is required",
		},
		{
			name: "missing GITHUB_SERVER_URL",
			env:  omitEnv(imageEnv(), "GITHUB_SERVER_URL"),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "GITHUB_SERVER_URL is required",
		},
		{
			name: "missing GITHUB_REPOSITORY",
			env:  omitEnv(imageEnv(), "GITHUB_REPOSITORY"),
			args: []string{
				"image", "build",
				"--input", t.TempDir(),
				"--work", t.TempDir(),
				"--output", t.TempDir(),
				"--melange-config", fixture.melange,
				"--apko-config", fixture.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			},
			want: "GITHUB_REPOSITORY is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executeImageBuildFactory(t, tt.env, tt.args, trackingImageFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Empty(t, stdout)
			assert.Contains(t, err.Error(), tt.want)
			assert.False(t, called)
		})
	}
}

func TestImageBuildJSONConfigFailure(t *testing.T) {
	t.Parallel()

	called := false
	stdout, err := executeImageBuildFactory(t, imageEnv(), []string{
		"image", "build",
		"--json",
		"--work", t.TempDir(),
		"--output", t.TempDir(),
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, trackingImageFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.False(t, called)
	assertImageFailureEnvelope(t, stdout, "--input is required")
}

func TestImageBuildMissingProjection(t *testing.T) {
	t.Parallel()

	fixture := writeImageConfigFiles(t)
	input := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	output := filepath.Join(t.TempDir(), "output")
	called := false
	stdout, err := executeImageBuildFactory(t, imageEnv(), []string{
		"image", "build",
		"--json",
		"--input", input,
		"--work", work,
		"--output", output,
		"--melange-config", fixture.melange,
		"--apko-config", fixture.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, trackingImageFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.False(t, called)
	assert.Contains(t, err.Error(), stage.ImageInputsName)
	assertImageFailureEnvelope(t, stdout, stage.ImageInputsName)
	assertPathAbsent(t, work)
	assertPathAbsent(t, output)
}

func TestImageBuildMalformedProjection(t *testing.T) {
	t.Parallel()

	fixture := writeImageConfigFiles(t)
	input := t.TempDir()
	writeFile(t, filepath.Join(input, stage.ImageInputsName), `{"schema":"nope"}`)
	work := filepath.Join(t.TempDir(), "work")
	output := filepath.Join(t.TempDir(), "output")

	called := false
	stdout, err := executeImageBuildFactory(t, imageEnv(), []string{
		"image", "build",
		"--json",
		"--input", input,
		"--work", work,
		"--output", output,
		"--melange-config", fixture.melange,
		"--apko-config", fixture.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, trackingImageFactories(t, &called))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.False(t, called)
	assertImageFailureEnvelope(t, stdout, "oci-build-inputs")
	assertPathAbsent(t, work)
	assertPathAbsent(t, output)
}

func TestImageBuildOverlappingRootsAreUsage(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	tests := []struct {
		name string
		work func(parent string) string
		out  func(parent string) string
	}{
		{
			name: "work equals output",
			work: func(parent string) string { return filepath.Join(parent, "shared") },
			out:  func(parent string) string { return filepath.Join(parent, "shared") },
		},
		{
			name: "work under output",
			work: func(parent string) string { return filepath.Join(parent, "out", "scratch") },
			out:  func(parent string) string { return filepath.Join(parent, "out") },
		},
		{
			name: "output under work",
			work: func(parent string) string { return filepath.Join(parent, "work") },
			out:  func(parent string) string { return filepath.Join(parent, "work", "out") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			work := tt.work(parent)
			output := tt.out(parent)
			called := false
			stdout, err := executeImageBuildFactory(t, imageEnv(), []string{
				"image", "build",
				"--json",
				"--input", tree.input,
				"--work", work,
				"--output", output,
				"--melange-config", tree.melange,
				"--apko-config", tree.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			}, trackingImageFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.False(t, called)
			assert.Contains(t, err.Error(), "must be disjoint")
			assertImageFailureEnvelope(t, stdout, "must be disjoint")
			assertPathAbsent(t, work)
			assertPathAbsent(t, output)
		})
	}
}

func TestImageBuildSiblingPrefixRootsSucceed(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	parent := t.TempDir()
	work := filepath.Join(parent, "out")
	output := filepath.Join(parent, "outx")
	apk := unusedAPKBuilder(t)
	composer := unusedComposer(t)
	expectSuccessfulImageBuild(t, apk, composer)

	stdout, stderr, err := executeImageBuild(t, imageEnv(), []string{
		"image", "build",
		"--json",
		"--input", tree.input,
		"--work", work,
		"--output", output,
		"--melange-config", tree.melange,
		"--apko-config", tree.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, imagePorts{
		apk:      apk,
		composer: composer,
	})
	require.NoError(t, err)
	assert.Empty(t, stderr)
	result := decodeImageBuildResult(t, stdout)
	assert.Equal(t, work, result.Work)
	assert.Equal(t, output, result.Output)
}

func TestImageBuildFactoryNotConfigured(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	work := filepath.Join(t.TempDir(), "work")
	output := filepath.Join(t.TempDir(), "output")
	stdout, stderr, err := executeImageBuild(t, imageEnv(), []string{
		"image", "build",
		"--json",
		"--input", tree.input,
		"--work", work,
		"--output", output,
		"--melange-config", tree.melange,
		"--apko-config", tree.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, imagePorts{})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "apk builder factory is not configured")
	assert.Empty(t, stderr)
	assertImageFailureEnvelope(t, stdout, "apk builder factory is not configured")
	assertPathAbsent(t, work)
	assertPathAbsent(t, output)
}

func TestImageBuildMissingConfigLeavesRootsAbsent(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	tests := []struct {
		name    string
		melange string
		apko    string
		want    string
	}{
		{
			name:    "missing Melange config",
			melange: filepath.Join(t.TempDir(), "missing-melange.yaml"),
			apko:    tree.apko,
			want:    "open Melange config",
		},
		{
			name:    "missing apko config",
			melange: tree.melange,
			apko:    filepath.Join(t.TempDir(), "missing-apko.yaml"),
			want:    "open apko config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			work := filepath.Join(t.TempDir(), "work")
			output := filepath.Join(t.TempDir(), "output")
			called := false
			stdout, err := executeImageBuildFactory(t, imageEnv(), []string{
				"image", "build",
				"--json",
				"--input", tree.input,
				"--work", work,
				"--output", output,
				"--melange-config", tt.melange,
				"--apko-config", tt.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			}, trackingImageFactories(t, &called))
			require.Error(t, err)
			assert.Equal(t, 1, cli.ExitCode(err))
			assert.False(t, called)
			assert.Contains(t, err.Error(), tt.want)
			assertImageFailureEnvelope(t, stdout, tt.want)
			assertPathAbsent(t, work)
			assertPathAbsent(t, output)
		})
	}
}

func TestImageBuildFactoryErrorsAreUsage(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	tests := []struct {
		name      string
		factories imageFactories
		want      string
	}{
		{
			name: "melange factory error",
			factories: imageFactories{
				newAPK: func(string) (image.APKBuilder, error) {
					return nil, errors.New("binary missing")
				},
				newComposer: func(string) (image.Composer, error) {
					t.Fatal("composer factory must not run after a Melange factory error")

					return nil, errors.New("unreachable")
				},
			},
			want: "melange:",
		},
		{
			name: "apko factory error",
			factories: imageFactories{
				newAPK: func(string) (image.APKBuilder, error) {
					return unusedAPKBuilder(t), nil
				},
				newComposer: func(string) (image.Composer, error) {
					return nil, errors.New("binary missing")
				},
			},
			want: "apko:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			work := filepath.Join(t.TempDir(), "work")
			output := filepath.Join(t.TempDir(), "output")
			stdout, err := executeImageBuildFactory(t, imageEnv(), []string{
				"image", "build",
				"--json",
				"--input", tree.input,
				"--work", work,
				"--output", output,
				"--melange-config", tree.melange,
				"--apko-config", tree.apko,
				"--build-date", imageBuildDate,
				"--version", imageVersion,
			}, tt.factories)
			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, err.Error(), tt.want)
			assertImageFailureEnvelope(t, stdout, tt.want)
			assertPathAbsent(t, work)
			assertPathAbsent(t, output)
		})
	}
}

func TestImageBuildEngineFailure(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	require.NoError(t, os.Remove(filepath.Join(tree.input, tree.amd64Path)))

	stdout, _, err := executeImageBuild(t, imageEnv(), []string{
		"image", "build",
		"--json",
		"--input", tree.input,
		"--work", t.TempDir(),
		"--output", t.TempDir(),
		"--melange-config", tree.melange,
		"--apko-config", tree.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, imagePorts{
		apk:      unusedAPKBuilder(t),
		composer: unusedComposer(t),
	})
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), tree.amd64Path)
	assertImageFailureEnvelope(t, stdout, tree.amd64Path)
}

func TestImageBuildJSONSuccess(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	work := t.TempDir()
	output := t.TempDir()
	apk := unusedAPKBuilder(t)
	composer := unusedComposer(t)
	expectSuccessfulImageBuild(t, apk, composer)

	stdout, stderr, err := executeImageBuild(t, withEnv(imageEnv(), "RELEASE_MELANGE_PATH", imageMelangePath), []string{
		"image", "build",
		"--json",
		"--input", tree.input,
		"--work", work,
		"--output", output,
		"--melange-config", tree.melange,
		"--apko-config", tree.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, imagePorts{
		apk:      apk,
		composer: composer,
	})
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, imageCommand, envelope.Command)
	assert.True(t, envelope.OK)

	result := decodeImageBuildResult(t, stdout)
	assert.Equal(t, image.BuildSchema, result.Schema)
	assert.Equal(t, imageVersion, result.Version)
	assert.Equal(t, imageBinaryName, result.Binary)
	assert.Equal(t, work, result.Work)
	assert.Equal(t, output, result.Output)
	assert.Equal(t, imageBuildDate, result.BuildDate)
	assert.Equal(t, []image.PackageResult{
		{
			Platform:     "linux/amd64",
			Arch:         "x86_64",
			Package:      "packages/x86_64/release-cli-1.2.3-r0.apk",
			BinaryDigest: tree.amd64Digest,
		},
		{
			Platform:     "linux/arm64",
			Arch:         "aarch64",
			Package:      "packages/aarch64/release-cli-1.2.3-r0.apk",
			BinaryDigest: tree.arm64Digest,
		},
	}, result.Packages)
}

func TestImageBuildSilentSuccess(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	apk := unusedAPKBuilder(t)
	composer := unusedComposer(t)
	expectSuccessfulImageBuild(t, apk, composer)

	stdout, stderr, err := executeImageBuild(t, imageEnv(), []string{
		"image", "build",
		"--input", tree.input,
		"--work", t.TempDir(),
		"--output", t.TempDir(),
		"--melange-config", tree.melange,
		"--apko-config", tree.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, imagePorts{
		apk:      apk,
		composer: composer,
	})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestImageBuildFactoryPaths(t *testing.T) {
	t.Parallel()

	tree := writeImageInputTree(t)
	apk := unusedAPKBuilder(t)
	composer := unusedComposer(t)
	expectSuccessfulImageBuild(t, apk, composer)

	var gotMelange string
	var gotApko string
	stdout, err := executeImageBuildFactory(t, map[string]string{
		"GITHUB_REPOSITORY_OWNER": imageOwner,
		"GITHUB_SERVER_URL":       imageServer,
		"GITHUB_REPOSITORY":       imageRepo,
		"GITHUB_SHA":              imageSHA,
		"RELEASE_MELANGE_PATH":    imageMelangePath,
		"RELEASE_APKO_PATH":       imageApkoPath,
	}, []string{
		"image", "build",
		"--json",
		"--input", tree.input,
		"--work", t.TempDir(),
		"--output", t.TempDir(),
		"--melange-config", tree.melange,
		"--apko-config", tree.apko,
		"--build-date", imageBuildDate,
		"--version", imageVersion,
	}, imageFactories{
		newAPK: func(path string) (image.APKBuilder, error) {
			gotMelange = path
			return apk, nil
		},
		newComposer: func(path string) (image.Composer, error) {
			gotApko = path
			return composer, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, imageMelangePath, gotMelange)
	assert.Equal(t, imageApkoPath, gotApko)
	assert.Equal(t, imageBinaryName, decodeImageBuildResult(t, stdout).Binary)
}

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
