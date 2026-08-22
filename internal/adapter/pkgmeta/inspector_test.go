package pkgmeta

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

const fakeDockerScript = `#!/bin/sh
printf '%s\0' "$@" > "$PKGMETA_RECORD"
case "$9" in
  debian@*)
    printf 'Package: release-cli\nVersion: 1.2.3\nArchitecture: amd64\n'
    ;;
  fedora@*)
    printf 'release-cli\n1.2.3\n1\naarch64\n'
    ;;
  *)
    printf 'unexpected image: %s\n' "$9" >&2
    exit 9
    ;;
esac
`

// TestInspectDEBInvokesFixedContainerCommand proves exact argument mapping and normalization.
func TestInspectDEBInvokesFixedContainerCommand(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fake, record, environ := metadataFake(t)
	packagePath := filepath.Join(t.TempDir(), "package.deb")
	metadata, err := New(Options{DockerPath: fake, Environ: environ}).Inspect(
		context.Background(), pkgrepo.FormatDEB, packagePath,
	)
	require.NoError(t, err)
	assert.Equal(t, packageMetadata(t, pkgrepo.ArchitectureAMD64), metadata)
	assert.Equal(t, []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
		"-v",
		packagePath + ":/package:ro",
		"debian@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258",
		"dpkg-deb",
		"--field",
		"/package",
		"Package",
		"Version",
		"Architecture",
	}, recordedArgs(t, record))
}

// TestInspectRPMInvokesFixedContainerCommand proves release validation and architecture mapping.
func TestInspectRPMInvokesFixedContainerCommand(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fake, record, environ := metadataFake(t)
	packagePath := filepath.Join(t.TempDir(), "package.rpm")
	metadata, err := New(Options{DockerPath: fake, Environ: environ}).Inspect(
		context.Background(), pkgrepo.FormatRPM, packagePath,
	)
	require.NoError(t, err)
	assert.Equal(t, packageMetadata(t, pkgrepo.ArchitectureARM64), metadata)
	assert.Equal(t, []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
		"-v",
		packagePath + ":/package:ro",
		"fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814",
		"rpm",
		"-qp",
		"--queryformat",
		"%{NAME}\n%{VERSION}\n%{RELEASE}\n%{ARCH}\n",
		"/package",
	}, recordedArgs(t, record))
}

// TestInspectAPKReadsConcatenatedStreams proves the signature stream may precede .PKGINFO.
func TestInspectAPKReadsConcatenatedStreams(t *testing.T) {
	t.Parallel()

	packagePath := filepath.Join(t.TempDir(), "package.apk")
	var apk bytes.Buffer
	writeAPKStream(t, &apk, ".SIGN.RSA.example.rsa.pub", "signature")
	writeAPKStream(t, &apk, ".PKGINFO", "pkgname = release-cli\npkgver = 1.2.3\narch = x86_64\n")
	require.NoError(t, os.WriteFile(packagePath, apk.Bytes(), 0o644))

	metadata, err := New(Options{}).Inspect(context.Background(), pkgrepo.FormatAPK, packagePath)
	require.NoError(t, err)
	assert.Equal(t, packageMetadata(t, pkgrepo.ArchitectureAMD64), metadata)
}

// TestInspectAPKRejectsDuplicateControlMembers proves ambiguous metadata fails closed.
func TestInspectAPKRejectsDuplicateControlMembers(t *testing.T) {
	t.Parallel()

	packagePath := filepath.Join(t.TempDir(), "package.apk")
	var apk bytes.Buffer
	content := "pkgname = release-cli\npkgver = 1.2.3\narch = x86_64\n"
	writeAPKStream(t, &apk, ".PKGINFO", content)
	writeAPKStream(t, &apk, ".PKGINFO", content)
	require.NoError(t, os.WriteFile(packagePath, apk.Bytes(), 0o644))

	_, err := New(Options{}).Inspect(context.Background(), pkgrepo.FormatAPK, packagePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple .PKGINFO")
}

// packageMetadata returns the expected stable metadata fixture.
func packageMetadata(t *testing.T, architecture pkgrepo.Architecture) pkgrepo.PackageMetadata {
	t.Helper()

	version, err := rel.ParseVersion("1.2.3")
	require.NoError(t, err)
	return pkgrepo.PackageMetadata{Name: "release-cli", Version: version, Architecture: architecture}
}

// metadataFake writes one fake Docker executable and returns its isolated environment.
func metadataFake(t *testing.T) (string, string, []string) {
	t.Helper()

	directory := t.TempDir()
	fake := filepath.Join(directory, "docker")
	record := filepath.Join(directory, "args")
	require.NoError(t, os.WriteFile(fake, []byte(fakeDockerScript), 0o755))
	return fake, record, append(os.Environ(), "PKGMETA_RECORD="+record)
}

// recordedArgs reads one NUL-delimited fake invocation.
func recordedArgs(t *testing.T, name string) []string {
	t.Helper()

	content, err := os.ReadFile(name)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(content), "\x00"), "\x00")
}

// writeAPKStream appends one deterministic gzip-compressed tar stream.
func writeAPKStream(t *testing.T, output *bytes.Buffer, name, content string) {
	t.Helper()

	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}))
	_, err := tarWriter.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
}

// skipWindows skips shell fixture tests on Windows.
func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtures are not supported on Windows")
	}
}
