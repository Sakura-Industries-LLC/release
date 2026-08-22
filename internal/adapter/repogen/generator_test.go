package repogen

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage/pkgrepo"
)

const fakeGeneratorScript = `#!/bin/sh
{
  printf '%s\0' "$@"
  printf '\n---\n'
} >> "$REPOGEN_RECORD"
if [ -n "${REPOGEN_STDERR:-}" ]; then
  printf '%s' "$REPOGEN_STDERR" >&2
fi
exit "${REPOGEN_EXIT:-0}"
`

// TestGenerateInvokesAcceptedToolsAndPublishesStrongByHash proves the spike contract.
func TestGenerateInvokesAcceptedToolsAndPublishesStrongByHash(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newGeneratorFixture(t)
	err := fixture.generator.Generate(context.Background(), fixture.request())
	require.NoError(t, err)

	invocations := readInvocations(t, fixture.record)
	require.Len(t, invocations, 3)
	assert.Equal(t, "debian@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258", invocations[0][4])
	assert.Equal(t, []string{"sh", "-ceu"}, invocations[0][5:7])
	assert.Contains(t, invocations[0][7], "apt-ftparchive")
	assert.Contains(t, invocations[0][7], "Acquire-By-Hash=yes")
	assert.Contains(t, invocations[0][7], "Fri, 21 Aug 2026 12:00:00 GMT")
	assert.Contains(t, invocations[0][7], `owner="$(stat -c '%u:%g' /repo)"`)
	assert.Contains(t, invocations[0][7], `chown -R "$owner" /repo/apt`)
	assert.Equal(t, "fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814", invocations[1][4])
	assert.Contains(t, invocations[1][7], `touch -d "@1787313600"`)
	assert.Contains(t, invocations[1][7], "--unique-md-filenames --revision 1787313600")
	assert.Contains(t, invocations[1][7], `owner="$(stat -c '%u:%g' /repo)"`)
	assert.Contains(t, invocations[1][7], `chown -R "$owner" /repo/rpm`)
	assert.Equal(t, "-v", invocations[2][4])
	assert.Equal(t, fixture.key+":/keys/apk-index-001.rsa:ro", invocations[2][5])
	assert.Equal(t, "alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce", invocations[2][6])
	assert.Contains(t, invocations[2][9], "SOURCE_DATE_EPOCH=1787313600")
	assert.Contains(t, invocations[2][9], "cp /repo/keys/*.rsa.pub /etc/apk/keys/")
	assert.Contains(t, invocations[2][9], `abuild-sign -k "/keys/apk-index-001.rsa"`)
	assert.Contains(t, invocations[2][9], `owner="$(stat -c '%u:%g' /repo)"`)
	assert.Contains(t, invocations[2][9], `chown -R "$owner" /repo/apk`)

	for _, architecture := range []string{"amd64", "arm64"} {
		for _, name := range []string{"Packages", "Packages.gz"} {
			content := []byte(architecture + "-" + name)
			sha256Digest := fmt.Sprintf("%x", sha256.Sum256(content))
			sha512Digest := fmt.Sprintf("%x", sha512.Sum512(content))
			base := filepath.Join(fixture.root, "apt", "dists", "stable", "main", "binary-"+architecture, "by-hash")
			assertFileBytes(t, filepath.Join(base, "SHA256", sha256Digest), content)
			assertFileBytes(t, filepath.Join(base, "SHA512", sha512Digest), content)
		}
	}
}

// TestCreateAPTByHashRejectsMissingStrongFamily proves clients never receive a partial hash set.
func TestCreateAPTByHashRejectsMissingStrongFamily(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	writeAPTIndexes(t, rootDir)
	release := buildRelease(t, rootDir, false)
	writeFile(t, filepath.Join(rootDir, "apt", "dists", "stable", "Release"), []byte(release))
	root, err := os.OpenRoot(rootDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	err = createAPTByHash(root, pkgrepo.ChannelStable)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want 8")
}

// TestGenerateFailureRedactsPrivateKeyPath proves command failures never echo argv.
func TestGenerateFailureRedactsPrivateKeyPath(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newGeneratorFixture(t)
	fixture.generator.environ = append(fixture.generator.environ, "REPOGEN_EXIT=8", "REPOGEN_STDERR=tool failed")
	err := fixture.generator.Generate(context.Background(), fixture.request())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool failed")
	assert.NotContains(t, err.Error(), fixture.key)
	assert.NotContains(t, err.Error(), "abuild-sign")
}

// TestGenerateRejectsExposedAPKPrivateKey proves index keys must remain owner-only.
func TestGenerateRejectsExposedAPKPrivateKey(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newGeneratorFixture(t)
	require.NoError(t, os.Chmod(fixture.key, 0o644))

	err := fixture.generator.Generate(context.Background(), fixture.request())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow group or other access")
}

// TestGenerateRejectsUnsafeAPKSigningKeyName keeps the key mount inside its fixed directory.
func TestGenerateRejectsUnsafeAPKSigningKeyName(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newGeneratorFixture(t)
	request := fixture.request()
	request.APKSigningKeyName = "../outside.rsa"

	err := fixture.generator.Generate(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APK signing key name")
}

// generatorFixture owns one fake three-format generation run.
type generatorFixture struct {
	// generator is the configured adapter under test.
	generator *Generator
	// root is the writable repository tree.
	root string
	// key is the aggregate APK private key path.
	key string
	// record receives NUL-delimited Docker argv.
	record string
}

// newGeneratorFixture creates deterministic indexes, keys, and a fake Docker process.
func newGeneratorFixture(t *testing.T) *generatorFixture {
	t.Helper()

	directory := t.TempDir()
	root := filepath.Join(directory, "repository")
	require.NoError(t, os.Mkdir(root, 0o755))
	writeAPTIndexes(t, root)
	writeFile(t, filepath.Join(root, "apt", "dists", "stable", "Release"), []byte(buildRelease(t, root, true)))
	fake := filepath.Join(directory, "docker")
	key := filepath.Join(directory, "materialized-private-key.pem")
	record := filepath.Join(directory, "args")
	writeFile(t, fake, []byte(fakeGeneratorScript))
	require.NoError(t, os.Chmod(fake, 0o755))
	writeFile(t, key, []byte("private key sentinel"))
	environ := append(os.Environ(), "REPOGEN_RECORD="+record)
	return &generatorFixture{
		generator: New(Options{
			DockerPath:    fake,
			APKSigningKey: key,
			Environ:       environ,
		}),
		root:   root,
		key:    key,
		record: record,
	}
}

// request returns the deterministic generation request used by the accepted spike.
func (f *generatorFixture) request() pkgrepo.GenerateRequest {
	return pkgrepo.GenerateRequest{
		Root:              f.root,
		Channel:           pkgrepo.ChannelStable,
		APKSigningKeyName: "apk-index-001.rsa",
		ReleaseTime:       time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC),
		ValidUntil:        time.Date(2027, time.August, 21, 12, 0, 0, 0, time.UTC),
	}
}

// writeAPTIndexes writes both canonical index variants for both architectures.
func writeAPTIndexes(t *testing.T, root string) {
	t.Helper()

	for _, architecture := range []string{"amd64", "arm64"} {
		for _, name := range []string{"Packages", "Packages.gz"} {
			writeFile(
				t,
				filepath.Join(root, "apt", "dists", "stable", "main", "binary-"+architecture, name),
				[]byte(architecture+"-"+name),
			)
		}
	}
}

// buildRelease renders strong digest sections for the canonical APT indexes.
func buildRelease(t *testing.T, root string, includeSHA512 bool) string {
	t.Helper()

	var output strings.Builder
	output.WriteString("Origin: Meigma\nSHA256:\n")
	for _, architecture := range []string{"amd64", "arm64"} {
		for _, name := range []string{"Packages", "Packages.gz"} {
			content, err := os.ReadFile(
				filepath.Join(root, "apt", "dists", "stable", "main", "binary-"+architecture, name),
			)
			require.NoError(t, err)
			fmt.Fprintf(&output, " %x %d main/binary-%s/%s\n", sha256.Sum256(content), len(content), architecture, name)
		}
	}
	if includeSHA512 {
		output.WriteString("SHA512:\n")
		for _, architecture := range []string{"amd64", "arm64"} {
			for _, name := range []string{"Packages", "Packages.gz"} {
				content, err := os.ReadFile(
					filepath.Join(root, "apt", "dists", "stable", "main", "binary-"+architecture, name),
				)
				require.NoError(t, err)
				fmt.Fprintf(
					&output,
					" %x %d main/binary-%s/%s\n",
					sha512.Sum512(content),
					len(content),
					architecture,
					name,
				)
			}
		}
	}
	return output.String()
}

// readInvocations decodes all fake Docker argument records.
func readInvocations(t *testing.T, name string) [][]string {
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

// writeFile creates parent directories and writes one file.
func writeFile(t *testing.T, name string, content []byte) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(t, os.WriteFile(name, content, 0o600))
}

// assertFileBytes checks one generated by-hash object.
func assertFileBytes(t *testing.T, name string, expected []byte) {
	t.Helper()

	actual, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

// skipWindows skips shell fixture tests on Windows.
func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtures are not supported on Windows")
	}
}
