package pkgverify

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage/pkgrepo"
)

const fakeVerifierScript = `#!/bin/sh
{
  printf '%s\0' "$@"
  printf '\n---\n'
} >> "$PKGVERIFY_RECORD"
if [ -n "${PKGVERIFY_STDERR:-}" ]; then
  printf '%s' "$PKGVERIFY_STDERR" >&2
fi
exit "${PKGVERIFY_EXIT:-0}"
`

// TestVerifyRPMUsesEphemeralDatabase proves import precedes verification with fixed arguments.
func TestVerifyRPMUsesEphemeralDatabase(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fake, record, environ := verifierFake(t)
	request := pkgrepo.VerificationRequest{
		Format:    pkgrepo.FormatRPM,
		Package:   filepath.Join(t.TempDir(), "release-cli.rpm"),
		PublicKey: filepath.Join(t.TempDir(), "release-rpm-001.asc"),
	}
	err := New(Options{DockerPath: fake, Environ: environ}).Verify(context.Background(), request)
	require.NoError(t, err)

	invocations := recordedInvocations(t, record)
	require.Len(t, invocations, 2)
	databaseMount := invocations[0][9]
	assert.Contains(t, databaseMount, string(filepath.Separator)+"release-rpmdb-")
	assert.True(t, strings.HasSuffix(databaseMount, ":/rpmdb"))
	assert.Equal(t, []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
		"-v",
		request.PublicKey + ":/key.asc:ro",
		"-v",
		databaseMount,
		"fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814",
		"rpmkeys",
		"--dbpath",
		"/rpmdb",
		"--import",
		"/key.asc",
	}, invocations[0])
	assert.Equal(t, []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
		"-v",
		request.Package + ":/package.rpm:ro",
		"-v",
		databaseMount,
		"fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814",
		"rpmkeys",
		"--dbpath",
		"/rpmdb",
		"--checksig",
		"/package.rpm",
	}, invocations[1])
}

// TestVerifyAPKUsesSignatureDeclaredKeyBasename proves config filenames need not match producer key names.
func TestVerifyAPKUsesSignatureDeclaredKeyBasename(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fake, record, environ := verifierFake(t)
	packagePath := filepath.Join(t.TempDir(), "release-cli.apk")
	writeAPKSignature(t, packagePath, "meigma-release-001.rsa.pub")
	request := pkgrepo.VerificationRequest{
		Format:    pkgrepo.FormatAPK,
		Package:   packagePath,
		PublicKey: filepath.Join(t.TempDir(), "repository-apk.rsa.pub"),
	}
	err := New(Options{DockerPath: fake, Environ: environ}).Verify(context.Background(), request)
	require.NoError(t, err)

	invocations := recordedInvocations(t, record)
	require.Len(t, invocations, 1)
	assert.Equal(t, []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
		"-v",
		request.Package + ":/package.apk:ro",
		"-v",
		request.PublicKey + ":/keys/meigma-release-001.rsa.pub:ro",
		"alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce",
		"apk",
		"verify",
		"--keys-dir",
		"/keys",
		"/package.apk",
	}, invocations[0])
}

// TestVerifyFailureDoesNotEchoPaths proves adapter errors retain diagnostics but not argv.
func TestVerifyFailureDoesNotEchoPaths(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fake, _, environ := verifierFake(t)
	environ = append(environ, "PKGVERIFY_EXIT=7", "PKGVERIFY_STDERR=signature rejected")
	request := pkgrepo.VerificationRequest{
		Format:    pkgrepo.FormatAPK,
		Package:   filepath.Join(t.TempDir(), "sensitive-package.apk"),
		PublicKey: filepath.Join(t.TempDir(), "sensitive-key.rsa.pub"),
	}
	writeAPKSignature(t, request.Package, "sensitive-key.rsa.pub")
	err := New(Options{DockerPath: fake, Environ: environ}).Verify(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature rejected")
	assert.NotContains(t, err.Error(), request.Package)
	assert.NotContains(t, err.Error(), request.PublicKey)
}

// writeAPKSignature writes the bounded signature stream needed by verifier tests.
func writeAPKSignature(t *testing.T, packagePath, keyName string) {
	t.Helper()

	file, err := os.Create(packagePath)
	require.NoError(t, err)
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	signature := []byte("signature")
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Name: apkSignaturePrefix + keyName,
		Mode: 0o644,
		Size: int64(len(signature)),
	}))
	_, err = tarWriter.Write(signature)
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, file.Close())
}

// verifierFake writes one fake Docker executable and returns its isolated environment.
func verifierFake(t *testing.T) (string, string, []string) {
	t.Helper()

	directory := t.TempDir()
	fake := filepath.Join(directory, "docker")
	record := filepath.Join(directory, "args")
	require.NoError(t, os.WriteFile(fake, []byte(fakeVerifierScript), 0o755))
	return fake, record, append(os.Environ(), "PKGVERIFY_RECORD="+record)
}

// recordedInvocations decodes the fake's NUL-delimited invocation records.
func recordedInvocations(t *testing.T, name string) [][]string {
	t.Helper()

	content, err := os.ReadFile(name)
	require.NoError(t, err)
	records := strings.Split(strings.TrimSuffix(string(content), "\n---\n"), "\n---\n")
	invocations := make([][]string, 0, len(records))
	for _, record := range records {
		invocations = append(invocations, strings.Split(strings.TrimSuffix(record, "\x00"), "\x00"))
	}
	return invocations
}

// skipWindows skips shell fixture tests on Windows.
func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtures are not supported on Windows")
	}
}
