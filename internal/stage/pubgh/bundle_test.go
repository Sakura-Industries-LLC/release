package pubgh_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	cosignmocks "github.com/meigma/release/internal/adapter/cosign/mocks"
	"github.com/meigma/release/internal/stage"
	"github.com/meigma/release/internal/stage/pubgh"
)

const (
	validIdentity = "https://github.com/owner/repo/.github/workflows/go-pre-publish.yml@refs/heads/main"
	defaultIssuer = "https://token.actions.githubusercontent.com"
	bundleBytes   = "{bundle}"
)

func TestVerifyBundleHappyPath(t *testing.T) {
	t.Parallel()

	fsys := closedDist(t)
	verifier := cosignmocks.NewMockBlobVerifier(t)
	verifier.EXPECT().
		Verify(mock.Anything, pubgh.BlobVerification{
			Payload:  "checksums.txt",
			Bundle:   "checksums.txt.sigstore.json",
			Identity: validIdentity,
			Issuer:   defaultIssuer,
		}).
		Return(nil).
		Once()

	got, err := pubgh.VerifyBundle(context.Background(), fsys, verifier, pubgh.TrustPolicy{
		Identity: validIdentity,
	})
	require.NoError(t, err)

	want := expectedBundle(fsys)
	assert.Equal(t, want, got)
	assert.Equal(t, []string{
		"gamma.bin",
		"alpha.bin",
		"beta.bin",
		"checksums.txt",
		"checksums.txt.sigstore.json",
	}, got.Names())
}

func TestVerifyBundleClosedSetFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fsys    func(t *testing.T) fstest.MapFS
		wantErr string
	}{
		{
			name: "extra unlisted file",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				fsys["extra.txt"] = &fstest.MapFile{Data: []byte("nope")}

				return fsys
			},
			wantErr: "unlisted or invalid release bundle entry: extra.txt",
		},
		{
			name: "subdirectory",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				fsys["nested"] = &fstest.MapFile{Mode: fs.ModeDir}

				return fsys
			},
			wantErr: "unlisted or invalid release bundle entry: nested",
		},
		{
			name: "irregular entry",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				fsys["link"] = &fstest.MapFile{Mode: fs.ModeSymlink}

				return fsys
			},
			wantErr: "unlisted or invalid release bundle entry: link",
		},
		{
			name: "claimed payload is a symlink",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				fsys["gamma.bin"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("gamma")}

				return fsys
			},
			wantErr: "unlisted or invalid release bundle entry: gamma.bin",
		},
		{
			name: "missing payload",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				delete(fsys, "gamma.bin")

				return fsys
			},
			wantErr: "gamma.bin",
		},
		{
			name: "payload bytes changed",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				fsys["gamma.bin"] = &fstest.MapFile{Data: []byte("changed")}

				return fsys
			},
			wantErr: "gamma.bin",
		},
		{
			name: "missing sigstore bundle",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				delete(fsys, "checksums.txt.sigstore.json")

				return fsys
			},
			wantErr: "checksums.txt.sigstore.json",
		},
		{
			name: "empty bundle file",
			fsys: func(t *testing.T) fstest.MapFS {
				t.Helper()
				fsys := closedDist(t)
				fsys["checksums.txt.sigstore.json"] = &fstest.MapFile{Data: []byte{}}

				return fsys
			},
			wantErr: "checksums.txt.sigstore.json is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			verifier := cosignmocks.NewMockBlobVerifier(t)
			got, err := pubgh.VerifyBundle(
				context.Background(),
				test.fsys(t),
				verifier,
				pubgh.TrustPolicy{Identity: validIdentity},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			assert.Empty(t, got.Payloads)
			assert.Empty(t, got.Controls)
			verifier.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
		})
	}
}

func TestVerifyBundleRejectsListedControls(t *testing.T) {
	t.Parallel()

	const listedDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	tests := []struct {
		name    string
		claim   string
		wantErr string
	}{
		{
			name:    "lists checksums.txt",
			claim:   listedDigest + "  checksums.txt\n",
			wantErr: "control file checksums.txt must not be listed",
		},
		{
			name:    "lists checksums.txt.sigstore.json",
			claim:   listedDigest + "  checksums.txt.sigstore.json\n",
			wantErr: "control file checksums.txt.sigstore.json must not be listed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{
				"checksums.txt": {Data: []byte(test.claim)},
			}
			verifier := cosignmocks.NewMockBlobVerifier(t)
			got, err := pubgh.VerifyBundle(
				context.Background(),
				fsys,
				verifier,
				pubgh.TrustPolicy{Identity: validIdentity},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			assert.Empty(t, got.Payloads)
			assert.Empty(t, got.Controls)
			verifier.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
		})
	}
}

func TestVerifyBundleRejectsChecksumGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claim   string
		wantErr string
	}{
		{
			name:    "empty checksums.txt",
			claim:   "",
			wantErr: "does not list any release payloads",
		},
		{
			name:    "malformed checksums.txt",
			claim:   "aaaa  archive.tar.gz\n",
			wantErr: "malformed entry",
		},
		{
			name:    "invalid payload name charset",
			claim:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  .hidden\n",
			wantErr: "is not a valid release payload name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{
				"checksums.txt": {Data: []byte(test.claim)},
			}
			verifier := cosignmocks.NewMockBlobVerifier(t)
			got, err := pubgh.VerifyBundle(
				context.Background(),
				fsys,
				verifier,
				pubgh.TrustPolicy{Identity: validIdentity},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			assert.Empty(t, got.Payloads)
			assert.Empty(t, got.Controls)
			verifier.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
		})
	}
}

func TestVerifyBundleVerifierError(t *testing.T) {
	t.Parallel()

	fsys := closedDist(t)
	verifier := cosignmocks.NewMockBlobVerifier(t)
	verifier.EXPECT().
		Verify(mock.Anything, mock.Anything).
		Return(errors.New("signature rejected")).
		Once()

	got, err := pubgh.VerifyBundle(context.Background(), fsys, verifier, pubgh.TrustPolicy{
		Identity: validIdentity,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature rejected")
	assert.Empty(t, got.Payloads)
	assert.Empty(t, got.Controls)
}

func TestVerifyBundleRejectsNilInputs(t *testing.T) {
	t.Parallel()

	fsys := closedDist(t)
	verifier := cosignmocks.NewMockBlobVerifier(t)
	trust := pubgh.TrustPolicy{Identity: validIdentity}

	var missing context.Context
	got, err := pubgh.VerifyBundle(missing, fsys, verifier, trust)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is nil")
	assert.Empty(t, got.Names())

	got, err = pubgh.VerifyBundle(context.Background(), nil, verifier, trust)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filesystem is nil")
	assert.Empty(t, got.Names())

	got, err = pubgh.VerifyBundle(context.Background(), fsys, nil, trust)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blob verifier is nil")
	assert.Empty(t, got.Names())
}

func TestBuildBundleRejectsNilFilesystem(t *testing.T) {
	t.Parallel()

	claim := mustParseClaim(t, digestOf([]byte("gamma"))+"  gamma.bin\n")
	got, err := pubgh.BuildBundle(nil, claim)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filesystem is nil")
	assert.Empty(t, got.Names())
}

func TestBuildBundleOrdering(t *testing.T) {
	t.Parallel()

	fsys := closedDist(t)
	claim := mustParseClaim(t, string(fsys["checksums.txt"].Data))

	got, err := pubgh.BuildBundle(fsys, claim)
	require.NoError(t, err)
	assert.Equal(t, expectedBundle(fsys), got)
	assert.Equal(t, []string{
		"gamma.bin",
		"alpha.bin",
		"beta.bin",
		"checksums.txt",
		"checksums.txt.sigstore.json",
	}, got.Names())
}

func TestTrustPolicyNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  pubgh.TrustPolicy
		want    pubgh.TrustPolicy
		wantErr string
	}{
		{
			name:   "default issuer",
			policy: pubgh.TrustPolicy{Identity: validIdentity},
			want: pubgh.TrustPolicy{
				Identity: validIdentity,
				Issuer:   defaultIssuer,
			},
		},
		{
			name: "trims identity and preserves issuer",
			policy: pubgh.TrustPolicy{
				Identity: "  " + validIdentity + "  ",
				Issuer:   "https://accounts.google.com",
			},
			want: pubgh.TrustPolicy{
				Identity: validIdentity,
				Issuer:   "https://accounts.google.com",
			},
		},
		{
			name:    "empty identity",
			policy:  pubgh.TrustPolicy{},
			wantErr: "certificate identity is empty",
		},
		{
			name:    "whitespace identity",
			policy:  pubgh.TrustPolicy{Identity: "   "},
			wantErr: "certificate identity is empty",
		},
		{
			name:    "non-URL identity",
			policy:  pubgh.TrustPolicy{Identity: "not a url"},
			wantErr: "is not an absolute URL",
		},
		{
			name:    "relative identity",
			policy:  pubgh.TrustPolicy{Identity: "/owner/repo/.github/workflows/go-pre-publish.yml"},
			wantErr: "is not an absolute URL",
		},
		{
			name:    "non-https identity",
			policy:  pubgh.TrustPolicy{Identity: "http://github.com/owner/repo/.github/workflows/x.yml"},
			wantErr: "is not an https URL",
		},
		{
			name: "non-URL issuer",
			policy: pubgh.TrustPolicy{
				Identity: validIdentity,
				Issuer:   "not a url",
			},
			wantErr: "is not an absolute URL",
		},
		{
			name: "non-https issuer",
			policy: pubgh.TrustPolicy{
				Identity: validIdentity,
				Issuer:   "http://token.actions.githubusercontent.com",
			},
			wantErr: "is not an https URL",
		},
		{
			name: "empty issuer after spaces uses default",
			policy: pubgh.TrustPolicy{
				Identity: validIdentity,
				Issuer:   "   ",
			},
			want: pubgh.TrustPolicy{
				Identity: validIdentity,
				Issuer:   defaultIssuer,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.policy.Normalize()
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				assert.Zero(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

// closedDist builds a MapFS with three payloads, checksums.txt, and the
// Sigstore bundle. Payload names are deliberately non-lexical so ordering
// follows checksums.txt rather than the directory listing.
func closedDist(t *testing.T) fstest.MapFS {
	t.Helper()

	payloads := []struct {
		name string
		data []byte
	}{
		{name: "gamma.bin", data: []byte("gamma")},
		{name: "alpha.bin", data: []byte("alpha")},
		{name: "beta.bin", data: []byte("beta")},
	}

	fsys := fstest.MapFS{}
	var claim strings.Builder
	for _, payload := range payloads {
		fsys[payload.name] = &fstest.MapFile{Data: payload.data}
		claim.WriteString(digestOf(payload.data) + "  " + payload.name + "\n")
	}
	fsys["checksums.txt"] = &fstest.MapFile{Data: []byte(claim.String())}
	fsys["checksums.txt.sigstore.json"] = &fstest.MapFile{Data: []byte(bundleBytes)}

	return fsys
}

// expectedBundle reconstructs the closed Bundle for a closedDist filesystem.
func expectedBundle(fsys fstest.MapFS) pubgh.Bundle {
	return pubgh.Bundle{
		Payloads: []pubgh.BundleEntry{
			{Name: "gamma.bin", Digest: stage.Digest(digestOf([]byte("gamma")))},
			{Name: "alpha.bin", Digest: stage.Digest(digestOf([]byte("alpha")))},
			{Name: "beta.bin", Digest: stage.Digest(digestOf([]byte("beta")))},
		},
		Controls: []pubgh.BundleEntry{
			{Name: "checksums.txt", Digest: stage.Digest(digestOf(fsys["checksums.txt"].Data))},
			{
				Name:   "checksums.txt.sigstore.json",
				Digest: stage.Digest(digestOf(fsys["checksums.txt.sigstore.json"].Data)),
			},
		},
	}
}

// mustParseClaim parses checksums text or fails the test.
func mustParseClaim(t *testing.T, text string) stage.ChecksumSet {
	t.Helper()

	claim, err := stage.ParseChecksums(strings.NewReader(text))
	require.NoError(t, err)

	return claim
}

// digestOf returns the lowercase SHA-256 hex digest of data.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
