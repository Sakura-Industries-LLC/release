package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/image"
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
