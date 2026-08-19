package cli_test

import (
	"context"
	"crypto/sha256"
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

	cosignmocks "github.com/meigma/release/internal/adapter/cosign/mocks"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	// bundleIdentity is a valid certificate identity URL fixture.
	bundleIdentity = "https://github.com/owner/repo/.github/workflows/go-pre-publish.yml@refs/heads/main"
	// bundleIssuer is the documented default OIDC issuer.
	bundleIssuer = "https://token.actions.githubusercontent.com"
	// bundleAltIssuer is an explicit issuer override fixture.
	bundleAltIssuer = "https://issuer.example.com"
	// bundleToken is a credential that must never appear in output.
	bundleToken = "ghs_should_never_appear"
	// bundleCommand is the envelope command path for verify bundle.
	bundleCommand = "verify bundle"
	// bundleFirstName is the first checksums.txt payload fixture.
	bundleFirstName = "release-cli_1.2.3_linux_amd64.tar.gz"
	// bundleSecondName is the second checksums.txt payload fixture.
	bundleSecondName = "release-cli_1.2.3_linux_arm64.tar.gz"
	// bundleFirstData is the first payload contents.
	bundleFirstData = "amd64-archive"
	// bundleSecondData is the second payload contents.
	bundleSecondData = "arm64-archive"
	// bundleSigstore is the detached bundle fixture contents.
	bundleSigstore = "{bundle}"
	// bundleCosignPath is the RELEASE_COSIGN_PATH fixture.
	bundleCosignPath = "/opt/cosign"
)

func TestVerifyBundleMissingDistIsUsage(t *testing.T) {
	t.Parallel()

	called := false
	stdout, err := executeBundleFactory(t, map[string]string{
		"GITHUB_TOKEN": bundleToken,
	}, []string{"verify", "bundle", "--identity", bundleIdentity, "--json"},
		trackingBundleFactory(t, &called),
	)
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "--dist is required")
	assert.False(t, called)
	assertBundleFailureEnvelope(t, stdout, "--dist is required")
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundleMissingIdentityIsUsage(t *testing.T) {
	t.Parallel()

	called := false
	stdout, err := executeBundleFactory(t, map[string]string{
		"GITHUB_TOKEN": bundleToken,
	}, []string{"verify", "bundle", "--dist", t.TempDir(), "--json"},
		trackingBundleFactory(t, &called),
	)
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "--identity is required")
	assert.False(t, called)
	assertBundleFailureEnvelope(t, stdout, "--identity is required")
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundleInvalidDistPath(t *testing.T) {
	t.Parallel()

	fileDist := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(fileDist, []byte("file"), 0o644))

	tests := []struct {
		name string
		dist string
	}{
		{
			name: "missing directory",
			dist: filepath.Join(t.TempDir(), "missing"),
		},
		{
			name: "path is a file",
			dist: fileDist,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			stdout, err := executeBundleFactory(t, map[string]string{
				"GITHUB_TOKEN": bundleToken,
			}, []string{
				"verify", "bundle",
				"--dist", tt.dist,
				"--identity", bundleIdentity,
				"--json",
			}, trackingBundleFactory(t, &called))
			require.Error(t, err)
			assert.Equal(t, 1, cli.ExitCode(err))
			assert.Contains(t, err.Error(), "open dist")
			assert.Contains(t, err.Error(), tt.dist)
			assert.False(t, called)
			assertBundleFailureEnvelope(t, stdout, "open dist")
			assert.NotContains(t, stdout, bundleToken)
		})
	}
}

func TestVerifyBundleJSONSuccess(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	var got pubgh.BlobVerification
	stdout, stderr, err := executeBundle(t, map[string]string{
		"GITHUB_TOKEN": bundleToken,
	}, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--json",
	}, capturingVerifier(t, &got))
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, bundleToken)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, bundleCommand, envelope.Command)
	assert.True(t, envelope.OK)

	raw, marshalErr := json.Marshal(envelope.Result)
	require.NoError(t, marshalErr)
	var result cli.BundleResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, fixture.dir, result.Dist)
	assert.Equal(t, bundleIdentity, result.Identity)
	assert.Equal(t, bundleIssuer, result.Issuer)
	assert.Equal(t, []cli.BundleFileResult{
		{Name: bundleFirstName, Digest: fixture.firstDigest},
		{Name: bundleSecondName, Digest: fixture.secondDigest},
	}, result.Payloads)
	assert.Equal(t, []cli.BundleFileResult{
		{Name: "checksums.txt", Digest: fixture.checksumsDigest},
		{Name: "checksums.txt.sigstore.json", Digest: fixture.bundleDigest},
	}, result.Controls)

	assert.Equal(t, "checksums.txt", got.Payload)
	assert.Equal(t, "checksums.txt.sigstore.json", got.Bundle)
	assert.Equal(t, bundleIdentity, got.Identity)
	assert.Equal(t, bundleIssuer, got.Issuer)
}

func TestVerifyBundleIssuerOverrideReachesVerifier(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	var got pubgh.BlobVerification
	_, _, err := executeBundle(t, nil, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--issuer", bundleAltIssuer,
	}, capturingVerifier(t, &got))
	require.NoError(t, err)
	assert.Equal(t, bundleIdentity, got.Identity)
	assert.Equal(t, bundleAltIssuer, got.Issuer)
}

func TestVerifyBundleFlagOverridesEnv(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	var gotDir string
	var got pubgh.BlobVerification
	stdout, err := executeBundleFactory(t, map[string]string{
		"RELEASE_DIST":     t.TempDir(),
		"RELEASE_IDENTITY": "https://github.com/other/repo/.github/workflows/go-pre-publish.yml@refs/heads/main",
		"RELEASE_ISSUER":   "https://wrong.example.com",
		"GITHUB_TOKEN":     bundleToken,
	}, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--issuer", bundleAltIssuer,
		"--json",
	}, func(_, dir string) (pubgh.BlobVerifier, error) {
		gotDir = dir
		return capturingVerifier(t, &got), nil
	})
	require.NoError(t, err)
	assert.Equal(t, fixture.dir, gotDir)
	assert.Equal(t, bundleIdentity, got.Identity)
	assert.Equal(t, bundleAltIssuer, got.Issuer)

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	raw, marshalErr := json.Marshal(envelope.Result)
	require.NoError(t, marshalErr)
	var result cli.BundleResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, fixture.dir, result.Dist)
	assert.Equal(t, bundleIdentity, result.Identity)
	assert.Equal(t, bundleAltIssuer, result.Issuer)
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundleClosedSetFailureNeverCallsVerifier(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	writeFile(t, filepath.Join(fixture.dir, "extra.bin"), "unexpected")

	stdout, _, err := executeBundle(t, map[string]string{
		"GITHUB_TOKEN": bundleToken,
	}, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--json",
	}, unusedVerifier(t))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "extra.bin")
	assertBundleFailureEnvelope(t, stdout, "extra.bin")
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundlePayloadSymlinkNeverCallsVerifier(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, bundleSecondName), []byte(bundleFirstData), 0o644))
	checksums := fixture.firstDigest + "  " + bundleFirstName + "\n" + fixture.firstDigest + "  " + bundleSecondName + "\n"
	writeFile(t, filepath.Join(fixture.dir, "checksums.txt"), checksums)
	link := filepath.Join(fixture.dir, bundleFirstName)
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(bundleSecondName, link))

	stdout, _, err := executeBundle(t, map[string]string{
		"GITHUB_TOKEN": bundleToken,
	}, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--json",
	}, unusedVerifier(t))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), bundleFirstName)
	assertBundleFailureEnvelope(t, stdout, bundleFirstName)
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundleCosignPathReachesFactory(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	var gotPath string
	var gotDir string
	stdout, err := executeBundleFactory(t, map[string]string{
		"RELEASE_COSIGN_PATH": bundleCosignPath,
		"GITHUB_TOKEN":        bundleToken,
	}, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--json",
	}, func(path, dir string) (pubgh.BlobVerifier, error) {
		gotPath = path
		gotDir = dir
		return acceptingVerifier(t), nil
	})
	require.NoError(t, err)
	assert.Equal(t, bundleCosignPath, gotPath)
	assert.Equal(t, fixture.dir, gotDir)
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundleVerifierFailure(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	stdout, _, err := executeBundle(t, map[string]string{
		"GITHUB_TOKEN": bundleToken,
	}, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
		"--json",
	}, failingVerifier(t))
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "signature rejected")
	assert.Equal(t, 1, countJSONDocuments(stdout))
	assertBundleFailureEnvelope(t, stdout, "signature rejected")
	assert.NotContains(t, stdout, bundleToken)
}

func TestVerifyBundleSilentSuccessWithoutJSON(t *testing.T) {
	t.Parallel()

	fixture := writeClosedBundle(t)
	stdout, stderr, err := executeBundle(t, nil, []string{
		"verify", "bundle",
		"--dist", fixture.dir,
		"--identity", bundleIdentity,
	}, acceptingVerifier(t))
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

// closedBundle is a valid on-disk release bundle used by verify-bundle tests.
type closedBundle struct {
	// dir is the distribution directory path.
	dir string
	// firstDigest is the SHA-256 of the first payload.
	firstDigest string
	// secondDigest is the SHA-256 of the second payload.
	secondDigest string
	// checksumsDigest is the SHA-256 of checksums.txt.
	checksumsDigest string
	// bundleDigest is the SHA-256 of checksums.txt.sigstore.json.
	bundleDigest string
}

// writeClosedBundle writes a two-payload closed distribution directory.
func writeClosedBundle(t *testing.T) closedBundle {
	t.Helper()

	root := filepath.Join(t.TempDir(), "dist")
	firstDigest := sha256Hex(bundleFirstData)
	secondDigest := sha256Hex(bundleSecondData)
	checksums := firstDigest + "  " + bundleFirstName + "\n" + secondDigest + "  " + bundleSecondName + "\n"
	writeFile(t, filepath.Join(root, bundleFirstName), bundleFirstData)
	writeFile(t, filepath.Join(root, bundleSecondName), bundleSecondData)
	writeFile(t, filepath.Join(root, "checksums.txt"), checksums)
	writeFile(t, filepath.Join(root, "checksums.txt.sigstore.json"), bundleSigstore)

	return closedBundle{
		dir:             root,
		firstDigest:     firstDigest,
		secondDigest:    secondDigest,
		checksumsDigest: sha256Hex(checksums),
		bundleDigest:    sha256Hex(bundleSigstore),
	}
}

// sha256Hex returns the lowercase SHA-256 hex digest of data.
func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))

	return hex.EncodeToString(sum[:])
}

// unusedVerifier returns a generated mock that fails if Verify is called.
func unusedVerifier(t *testing.T) *cosignmocks.MockBlobVerifier {
	t.Helper()

	return cosignmocks.NewMockBlobVerifier(t)
}

// acceptingVerifier returns a generated mock that accepts one verification.
func acceptingVerifier(t *testing.T) *cosignmocks.MockBlobVerifier {
	t.Helper()

	verifier := cosignmocks.NewMockBlobVerifier(t)
	verifier.EXPECT().
		Verify(mock.Anything, mock.Anything).
		Return(nil).
		Once()

	return verifier
}

// capturingVerifier records the verification request and accepts it.
func capturingVerifier(t *testing.T, got *pubgh.BlobVerification) *cosignmocks.MockBlobVerifier {
	t.Helper()

	verifier := cosignmocks.NewMockBlobVerifier(t)
	verifier.EXPECT().
		Verify(mock.Anything, mock.Anything).
		Run(func(_ context.Context, request pubgh.BlobVerification) {
			*got = request
		}).
		Return(nil).
		Once()

	return verifier
}

// failingVerifier returns a generated mock that rejects verification.
func failingVerifier(t *testing.T) *cosignmocks.MockBlobVerifier {
	t.Helper()

	verifier := cosignmocks.NewMockBlobVerifier(t)
	verifier.EXPECT().
		Verify(mock.Anything, mock.Anything).
		Return(errors.New("signature rejected")).
		Once()

	return verifier
}

// trackingBundleFactory records whether the verifier factory was invoked.
func trackingBundleFactory(t *testing.T, called *bool) func(string, string) (pubgh.BlobVerifier, error) {
	t.Helper()

	return func(string, string) (pubgh.BlobVerifier, error) {
		*called = true

		return unusedVerifier(t), nil
	}
}

// executeBundle runs verify bundle with an injected verification port.
func executeBundle(
	t *testing.T,
	env map[string]string,
	args []string,
	verifier pubgh.BlobVerifier,
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
		BlobVerifier: verifier,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), stderr.String(), err
}

// executeBundleFactory runs verify bundle with an observing factory and
// returns stdout.
func executeBundleFactory(
	t *testing.T,
	env map[string]string,
	args []string,
	newVerifier func(string, string) (pubgh.BlobVerifier, error),
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
		NewBlobVerifier: newVerifier,
	})
	command.SetArgs(args)
	err := command.Execute()

	return stdout.String(), err
}

// assertBundleFailureEnvelope checks stdout is one ok:false verify-bundle envelope.
func assertBundleFailureEnvelope(t *testing.T, stdout, wantError string) {
	t.Helper()
	assert.Equal(t, 1, countJSONDocuments(stdout))

	var envelope cli.Envelope
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope))
	assert.Equal(t, cli.Schema, envelope.Schema)
	assert.Equal(t, bundleCommand, envelope.Command)
	assert.False(t, envelope.OK)

	raw, err := json.Marshal(envelope.Result)
	require.NoError(t, err)
	var result cli.ErrorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Contains(t, result.Error, wantError)
	assert.NotContains(t, stdout, bundleToken)
}
