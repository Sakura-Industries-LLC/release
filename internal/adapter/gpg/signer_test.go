package gpg

import (
	"context"
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

const (
	// testPassphrase is a sentinel that must never enter argv or returned errors.
	testPassphrase = "repository-passphrase-do-not-print"
	// fakeGPGScript records arguments and emits configured diagnostics.
	fakeGPGScript = `#!/bin/sh
printf '%s\0' "$@" > "$GPG_RECORD"
if [ -n "${GPG_STDERR:-}" ]; then
  printf '%s' "$GPG_STDERR" >&2
fi
exit "${GPG_EXIT:-0}"
`
)

// TestClearSignUsesPassphraseFileAndFixedTime proves exact safe argument mapping.
func TestClearSignUsesPassphraseFileAndFixedTime(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newSignerFixture(t)
	request := fixture.request("InRelease")
	err := fixture.signer.ClearSign(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, append(fixture.commonArgs(),
		"--clearsign", "--output", request.Output, request.Input,
	), readArguments(t, fixture.record))
	assert.NotContains(t, strings.Join(readArguments(t, fixture.record), "\n"), testPassphrase)
}

// TestDetachSignUsesArmoredOperation proves RPM metadata receives a detached signature.
func TestDetachSignUsesArmoredOperation(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newSignerFixture(t)
	request := fixture.request("repomd.xml.asc")
	err := fixture.signer.DetachSign(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, append(fixture.commonArgs(),
		"--armor", "--detach-sign", "--output", request.Output, request.Input,
	), readArguments(t, fixture.record))
}

// TestSignFailureRedactsPassphrase proves returned diagnostics cannot expose secret contents.
func TestSignFailureRedactsPassphrase(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newSignerFixture(t)
	fixture.signer.environ = append(fixture.signer.environ, "GPG_EXIT=4", "GPG_STDERR=signing failed")
	err := fixture.signer.ClearSign(context.Background(), fixture.request("InRelease"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing failed")
	assert.NotContains(t, err.Error(), testPassphrase)
}

// TestSignRejectsExposedPassphraseFile proves secrets must remain owner-only.
func TestSignRejectsExposedPassphraseFile(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newSignerFixture(t)
	require.NoError(t, os.Chmod(fixture.passphraseFile, 0o644))

	err := fixture.signer.ClearSign(context.Background(), fixture.request("InRelease"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow group or other access")
}

// signerFixture owns one fake signer invocation.
type signerFixture struct {
	// signer is the configured adapter under test.
	signer *Signer
	// home is the configured GnuPG home path.
	home string
	// passphraseFile is the configured secret file path.
	passphraseFile string
	// record receives NUL-delimited argv.
	record string
	// signatureTime is the deterministic signature timestamp.
	signatureTime time.Time
	// directory owns input and output paths.
	directory string
}

// newSignerFixture creates an isolated fake GnuPG process and secret file.
func newSignerFixture(t *testing.T) *signerFixture {
	t.Helper()

	directory := t.TempDir()
	fake := filepath.Join(directory, "gpg")
	record := filepath.Join(directory, "args")
	home := filepath.Join(directory, "gnupg")
	passphraseFile := filepath.Join(directory, "passphrase")
	require.NoError(t, os.Mkdir(home, 0o700))
	require.NoError(t, os.WriteFile(fake, []byte(fakeGPGScript), 0o755))
	require.NoError(t, os.WriteFile(passphraseFile, []byte(testPassphrase), 0o600))
	environ := append(os.Environ(), "GPG_RECORD="+record)
	return &signerFixture{
		signer: New(Options{
			Path:           fake,
			Home:           home,
			KeyID:          "0123456789ABCDEF",
			PassphraseFile: passphraseFile,
			Environ:        environ,
		}),
		home:           home,
		passphraseFile: passphraseFile,
		record:         record,
		signatureTime:  time.Unix(1_700_000_000, 0).UTC(),
		directory:      directory,
	}
}

// request returns one deterministic absolute signature request.
func (f *signerFixture) request(outputName string) pkgrepo.SignRequest {
	return pkgrepo.SignRequest{
		Input:  filepath.Join(f.directory, "metadata"),
		Output: filepath.Join(f.directory, outputName),
		Time:   f.signatureTime,
	}
}

// commonArgs returns the fixed safe GnuPG argument prefix.
func (f *signerFixture) commonArgs() []string {
	return []string{
		"--homedir", f.home,
		"--batch", "--yes",
		"--pinentry-mode", "loopback",
		"--passphrase-file", f.passphraseFile,
		"--faked-system-time", "1700000000",
		"--local-user", "0123456789ABCDEF",
	}
}

// readArguments reads one NUL-delimited fake invocation.
func readArguments(t *testing.T, name string) []string {
	t.Helper()

	content, err := os.ReadFile(name)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(content), "\x00"), "\x00")
}

// skipWindows skips shell fixture tests on Windows.
func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtures are not supported on Windows")
	}
}
