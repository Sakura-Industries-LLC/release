package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
	climocks "github.com/meigma/release/internal/cli/mocks"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const validPackagePublicationYAML = `channel: stable
origin: https://pkgs.meigma.dev
keys:
  apt:
    source: keys/repository-apt.asc
    published: apt-repository-001.asc
  rpm:
    source: keys/repository-rpm.asc
    published: rpm-repository-001.asc
  apk:
    source: keys/repository-apk.rsa.pub
    published: apk-index-001.rsa.pub
producers:
  - repository: meigma/release
    packages:
      - release-cli
    checksum_workflow: .github/workflows/go-pre-publish.yml
    attestation_workflow: .github/workflows/publish-github-release.yml
    rpm_key:
      source: keys/release-rpm.asc
      published: release-rpm-001.asc
    apk_key:
      source: keys/release-apk.rsa.pub
      published: release-apk-001.rsa.pub
`

func TestPackageRepositoryCommandPublishesResolvedRequest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "package-repository.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(validPackagePublicationYAML), 0o644))
	keysPath := filepath.Join(directory, "keys")
	require.NoError(t, os.Mkdir(keysPath, 0o755))
	publisher := climocks.NewMockPackageRepositoryPublisher(t)
	var scratchPaths []string
	publisher.EXPECT().
		Publish(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, input pkgrepo.PublishInput) (pkgrepo.PublishResult, error) {
			assert.Equal(t, pkgrepo.Repository("meigma/release"), input.Request.Repository)
			assert.Equal(t, "v1.2.3", input.Request.Tag)
			assert.Equal(t, "https://pkgs.meigma.dev", input.Config.Origin)
			require.NotNil(t, input.Keys)
			require.NotNil(t, input.Source)
			require.NotNil(t, input.Work)
			require.NotNil(t, input.Output)
			sourceInfo, sourceStatErr := os.Stat(input.Source.Name())
			require.NoError(t, sourceStatErr)
			assert.Equal(t, os.FileMode(0o750), sourceInfo.Mode().Perm())
			workInfo, workStatErr := os.Stat(input.Work.Name())
			require.NoError(t, workStatErr)
			assert.Equal(t, os.FileMode(0o750), workInfo.Mode().Perm())
			outputInfo, outputStatErr := os.Stat(input.Output.Name())
			require.NoError(t, outputStatErr)
			assert.Equal(t, os.FileMode(0o755), outputInfo.Mode().Perm())
			scratchPaths = []string{input.Source.Name(), input.Work.Name(), input.Output.Name()}

			return pkgrepo.PublishResult{
				State:      pkgrepo.PublishStatePublished,
				Repository: "meigma/release",
				Tag:        "v1.2.3",
				Artifacts:  19,
				Uploaded:   7,
			}, nil
		}).
		Once()
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	command := cli.NewRootCommand(cli.Options{
		Out:                        stdout,
		Err:                        stderr,
		LookupEnv:                  mapLookup(nil),
		PackageRepositoryPublisher: publisher,
	})
	command.SetArgs([]string{
		"publish", "package-repository",
		"--repository", "meigma/release",
		"--tag", "v1.2.3",
		"--config", configPath,
		"--keys", keysPath,
		"--json",
	})

	err := command.Execute()
	require.NoError(t, err)
	assert.Empty(t, stderr.String())
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &envelope))
	assert.Equal(t, "release.dev/result/v1", envelope["schema"])
	assert.Equal(t, "publish package-repository", envelope["command"])
	assert.Equal(t, true, envelope["ok"])
	require.Len(t, scratchPaths, 3)
	for _, name := range scratchPaths {
		_, statErr := os.Stat(name)
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	}
}

func TestPackageRepositoryCommandRejectsMissingRepositoryBeforePublication(t *testing.T) {
	t.Parallel()

	publisher := climocks.NewMockPackageRepositoryPublisher(t)
	command := cli.NewRootCommand(cli.Options{
		Out:                        &strings.Builder{},
		Err:                        &strings.Builder{},
		LookupEnv:                  mapLookup(nil),
		PackageRepositoryPublisher: publisher,
	})
	command.SetArgs([]string{"publish", "package-repository", "--json"})

	err := command.Execute()
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "--repository or RELEASE_REPOSITORY is required")
}

// mapLookup returns an isolated environment lookup.
func mapLookup(values map[string]string) cli.LookupEnv {
	return func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	}
}
