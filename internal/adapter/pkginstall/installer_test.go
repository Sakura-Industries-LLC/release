package pkginstall

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// fakeDockerScript records each NUL-delimited docker invocation.
	fakeDockerScript = `#!/bin/sh
set -eu
printf 'CALL\0' >> "$INSTALL_RECORD"
printf '%s\0' "$@" >> "$INSTALL_RECORD"
`
)

func TestVerifyRunsThreeSignedLocalClientsWithoutNetworking(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newInstallerFixture(t, true)
	err := fixture.installer.Verify(context.Background(), fixture.request)
	require.NoError(t, err)

	arguments := readRecord(t, fixture.record)
	assert.Equal(t, 3, countArgument(arguments, "CALL"))
	assert.Equal(t, 3, countArgument(arguments, "--network=none"))
	assert.Contains(t, arguments, defaultDebianImage)
	assert.Contains(t, arguments, defaultFedoraImage)
	assert.Contains(t, arguments, defaultAlpineImage)
	assert.Equal(t, 3, countArgument(arguments, "release-cli"))
	joined := strings.Join(arguments, "\n")
	assert.Contains(t, joined, "signed-by=%s")
	assert.Contains(t, joined, "gpgcheck=1")
	assert.Contains(t, joined, "repo_gpgcheck=1")
	assert.Contains(t, joined, "apk update")
	assert.Contains(
		t,
		arguments,
		rpmKeyURLsVariable+"=file:///keys/keys/rpm-repository.asc file:///keys/keys/rpm-package.asc",
	)
	assert.Contains(
		t,
		arguments,
		apkKeysVariable+"=/keys/keys/apk-index.pub:/keys/keys/apk-package.pub",
	)
	assert.Contains(t, joined, "src="+fixture.request.Root.Name()+",dst=/repo,readonly")
	assert.Contains(t, joined, "src="+fixture.request.Keys.Name()+",dst=/keys,readonly")
}

func TestVerifyPublicClientsUseHTTPSAndNetworking(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newInstallerFixture(t, false)
	err := fixture.installer.Verify(context.Background(), fixture.request)
	require.NoError(t, err)

	arguments := readRecord(t, fixture.record)
	assert.Equal(t, 3, countArgument(arguments, "CALL"))
	assert.NotContains(t, arguments, "--network=none")
	assert.Equal(t, 3, countArgument(arguments, repositoryRootVariable+"=https://pkgs.meigma.dev"))
}

func TestVerifyRejectsMissingKeyBeforeDocker(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newInstallerFixture(t, true)
	fixture.request.APKKeys[1] = "keys/missing.pub"

	err := fixture.installer.Verify(context.Background(), fixture.request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat APK installation key")
	_, statErr := os.Stat(fixture.record)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// installerFixture owns one fake Docker client and confined repository roots.
type installerFixture struct {
	// installer is the adapter under test.
	installer *Installer
	// request is one complete installation request.
	request pkgrepo.InstallRequest
	// record receives fake Docker argv.
	record string
}

// newInstallerFixture constructs one isolated native client request.
func newInstallerFixture(t *testing.T, local bool) installerFixture {
	t.Helper()

	directory := t.TempDir()
	fake := filepath.Join(directory, "docker")
	record := filepath.Join(directory, "docker.args")
	require.NoError(t, os.WriteFile(fake, []byte(fakeDockerScript), 0o755))
	keysDirectory := filepath.Join(directory, "keys-root")
	require.NoError(t, os.MkdirAll(filepath.Join(keysDirectory, "keys"), 0o755))
	for _, name := range []string{
		"apt.gpg",
		"rpm-repository.asc",
		"rpm-package.asc",
		"apk-index.pub",
		"apk-package.pub",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(keysDirectory, "keys", name), []byte("key"), 0o644))
	}
	keys, err := os.OpenRoot(keysDirectory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keys.Close()) })
	version, err := rel.ParseVersion("1.2.3")
	require.NoError(t, err)
	name, err := pkgrepo.ParsePackageName("release-cli")
	require.NoError(t, err)
	request := pkgrepo.InstallRequest{
		Keys:     keys,
		Origin:   "https://pkgs.meigma.dev",
		Channel:  pkgrepo.ChannelStable,
		Packages: []pkgrepo.PackageName{name},
		Version:  version,
		APTKey:   "keys/apt.gpg",
		RPMKeys:  []string{"keys/rpm-repository.asc", "keys/rpm-package.asc"},
		APKKeys:  []string{"keys/apk-index.pub", "keys/apk-package.pub"},
	}
	if local {
		repositoryPath := filepath.Join(directory, "repository")
		require.NoError(t, os.Mkdir(repositoryPath, 0o755))
		repository, openErr := os.OpenRoot(repositoryPath)
		require.NoError(t, openErr)
		t.Cleanup(func() { require.NoError(t, repository.Close()) })
		request.Root = repository
	}

	return installerFixture{
		installer: New(Options{
			DockerPath: fake,
			Environ:    []string{"INSTALL_RECORD=" + record},
		}),
		request: request,
		record:  record,
	}
}

// readRecord parses every NUL-delimited fake Docker argument.
func readRecord(t *testing.T, name string) []string {
	t.Helper()

	content, err := os.ReadFile(name)
	require.NoError(t, err)

	return strings.Split(strings.TrimSuffix(string(content), "\x00"), "\x00")
}

// countArgument counts exact argument occurrences.
func countArgument(arguments []string, wanted string) int {
	count := 0
	for _, argument := range arguments {
		if argument == wanted {
			count++
		}
	}

	return count
}

// skipWindows skips shell fixture tests on Windows.
func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtures are not supported on Windows")
	}
}
