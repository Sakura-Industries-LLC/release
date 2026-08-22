package ghattest

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
	// testToken is a recognizable secret used to prove argv and errors remain redacted.
	testToken = "attestation-secret-token"
	// fakeScript records argv and confirms the token exists only in the environment.
	fakeScript = `#!/bin/sh
set -eu
printf '%s\0' "$@" > "$ATTEST_RECORD"
test "$GH_TOKEN" = '` + testToken + `'
if [ "${ATTEST_FAIL:-}" = 1 ]; then
  printf 'verification failed for fixture\n' >&2
  exit 7
fi
`
)

func TestVerifyBindsArtifactToExactGitHubProvenance(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newVerifierFixture(t)
	err := fixture.verifier.Verify(context.Background(), fixture.request)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"attestation",
		"verify",
		fixture.request.Path,
		"--repo",
		"meigma/release",
		"--signer-workflow",
		"meigma/release/.github/workflows/publish-github-release.yml",
		"--source-ref",
		"refs/tags/v1.2.3",
		"--source-digest",
		"0123456789abcdef0123456789abcdef01234567",
		"--deny-self-hosted-runners",
	}, recordedArguments(t, fixture.record))
}

func TestVerifyRedactsTokenFromFailure(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newVerifierFixture(t)
	fixture.verifier.environ = func() []string {
		return []string{"ATTEST_RECORD=" + fixture.record, "ATTEST_FAIL=1", tokenVariable + "=" + testToken}
	}

	err := fixture.verifier.Verify(context.Background(), fixture.request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gh exited with code 7")
	assert.Contains(t, err.Error(), "verification failed for fixture")
	assert.NotContains(t, err.Error(), testToken)
}

func TestVerifyRejectsUnboundRequestsBeforeExecution(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	fixture := newVerifierFixture(t)
	tests := []struct {
		name    string
		mutate  func(*pkgrepo.AttestationRequest)
		wantErr string
	}{
		{
			name:    "relative artifact",
			mutate:  func(request *pkgrepo.AttestationRequest) { request.Path = "package.rpm" },
			wantErr: "not absolute",
		},
		{
			name:    "branch source ref",
			mutate:  func(request *pkgrepo.AttestationRequest) { request.SourceRef = "refs/heads/main" },
			wantErr: "exact tag ref",
		},
		{
			name:    "short source digest",
			mutate:  func(request *pkgrepo.AttestationRequest) { request.SourceDigest = "deadbeef" },
			wantErr: "full lowercase SHA",
		},
		{name: "other signer repository", mutate: func(request *pkgrepo.AttestationRequest) {
			request.SignerWorkflow = "other/repo/.github/workflows/publish.yml"
		}, wantErr: "does not belong"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := fixture.request
			test.mutate(&request)

			err := fixture.verifier.Verify(context.Background(), request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			_, statErr := os.Stat(fixture.record)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

// verifierFixture owns one fake gh process invocation.
type verifierFixture struct {
	// verifier is the adapter under test.
	verifier *Verifier
	// request is one fully bound attestation request.
	request pkgrepo.AttestationRequest
	// record receives the fake process argv.
	record string
}

// newVerifierFixture constructs one isolated fake gh invocation.
func newVerifierFixture(t *testing.T) verifierFixture {
	t.Helper()

	directory := t.TempDir()
	fake := filepath.Join(directory, "gh")
	record := filepath.Join(directory, "args")
	payload := filepath.Join(directory, "package.rpm")
	require.NoError(t, os.WriteFile(fake, []byte(fakeScript), 0o755))
	require.NoError(t, os.WriteFile(payload, []byte("package"), 0o644))

	return verifierFixture{
		verifier: New(Options{
			Path:    fake,
			Token:   rel.NewSecret(testToken),
			Environ: []string{"ATTEST_RECORD=" + record},
		}),
		request: pkgrepo.AttestationRequest{
			Path:           payload,
			Repository:     "meigma/release",
			SourceRef:      "refs/tags/v1.2.3",
			SourceDigest:   "0123456789abcdef0123456789abcdef01234567",
			SignerWorkflow: "meigma/release/.github/workflows/publish-github-release.yml",
		},
		record: record,
	}
}

// recordedArguments reads one NUL-delimited fake process record.
func recordedArguments(t *testing.T, name string) []string {
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
