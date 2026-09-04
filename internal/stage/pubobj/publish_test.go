package pubobj_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	r2mocks "github.com/Sakura-Industries-LLC/release/internal/adapter/r2/mocks"
	"github.com/Sakura-Industries-LLC/release/internal/rel"
	"github.com/Sakura-Industries-LLC/release/internal/stage"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pkgrepo"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pubgh"
	"github.com/Sakura-Industries-LLC/release/internal/stage/pubobj"
)

const (
	payloadName   = "gamma.bin"
	checksumName  = "checksums.txt"
	bundleName    = "checksums.txt.sigstore.json"
	payloadData   = "payload-bytes"
	checksumData  = "checksums-bytes"
	bundleData    = "sigstore-bytes"
	wrongDigest   = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	testProject   = "release"
	versionTag    = "v0.2.0"
	monorepoTag   = "cli/v0.2.0"
	versionPrefix = "release/v0.2.0"
)

func TestPublish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tag     string
		setup   func(t *testing.T, tc *publishHarness)
		want    func(t *testing.T, tc *publishHarness, got pubobj.PublishResult)
		wantErr string
	}{
		{
			name: "fresh bucket uploads all names in order with immutable cache",
			tag:  versionTag,
			setup: func(t *testing.T, tc *publishHarness) {
				t.Helper()
				expectFreshUploads(t, tc)
			},
			want: func(t *testing.T, tc *publishHarness, got pubobj.PublishResult) {
				t.Helper()
				assert.Equal(t, pubobj.Project(testProject), got.Project)
				assert.Equal(t, tc.input.Tag, got.Tag)
				assert.Equal(t, pubobj.Prefix(versionPrefix), got.Prefix)
				assert.Equal(t, tc.names, got.Uploaded)
				assert.Empty(t, got.Unchanged)
				assert.Equal(t, pubobj.PublishStatePublished, got.State)
				assert.Equal(t, expectedOps(tc, true), tc.ops)
			},
		},
		{
			name: "converged bucket uploads nothing and reports unchanged",
			tag:  versionTag,
			setup: func(t *testing.T, tc *publishHarness) {
				t.Helper()
				expectConverged(t, tc)
			},
			want: func(t *testing.T, tc *publishHarness, got pubobj.PublishResult) {
				t.Helper()
				assert.Equal(t, pubobj.Prefix(versionPrefix), got.Prefix)
				assert.Empty(t, got.Uploaded)
				assert.Equal(t, tc.names, got.Unchanged)
				assert.Equal(t, pubobj.PublishStateUnchanged, got.State)
				assert.Equal(t, expectedOps(tc, false), tc.ops)
			},
		},
		{
			name: "digest mismatch fails and uploads nothing further",
			tag:  versionTag,
			setup: func(t *testing.T, tc *publishHarness) {
				t.Helper()
				wrong, err := rel.ParseDigest("sha256:" + wrongDigest)
				require.NoError(t, err)
				tc.store.EXPECT().
					Stat(mock.Anything, tc.key(tc.names[0])).
					Return(pkgrepo.StoredContent{Digest: wrong, Size: 1}, true, nil).
					Once()
			},
			wantErr: "immutable object \"" + versionPrefix + "/" + payloadName + "\"",
		},
		{
			name: "stat error is surfaced",
			tag:  versionTag,
			setup: func(t *testing.T, tc *publishHarness) {
				t.Helper()
				tc.store.EXPECT().
					Stat(mock.Anything, tc.key(tc.names[0])).
					Return(pkgrepo.StoredContent{}, false, errors.New("store unavailable")).
					Once()
			},
			wantErr: "stat " + versionPrefix + "/" + payloadName,
		},
		{
			name: "prefix derivation for v0.2.0",
			tag:  versionTag,
			setup: func(t *testing.T, tc *publishHarness) {
				t.Helper()
				expectConverged(t, tc)
			},
			want: func(t *testing.T, _ *publishHarness, got pubobj.PublishResult) {
				t.Helper()
				assert.Equal(t, pubobj.Prefix(versionPrefix), got.Prefix)
				assert.Equal(t, pubobj.PublishStateUnchanged, got.State)
			},
		},
		{
			name: "prefix derivation for cli/v0.2.0",
			tag:  monorepoTag,
			setup: func(t *testing.T, tc *publishHarness) {
				t.Helper()
				expectConverged(t, tc)
			},
			want: func(t *testing.T, _ *publishHarness, got pubobj.PublishResult) {
				t.Helper()
				assert.Equal(t, pubobj.Prefix(versionPrefix), got.Prefix)
				assert.Equal(t, rel.GitTag(monorepoTag), got.Tag)
				assert.Equal(t, pubobj.PublishStateUnchanged, got.State)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := newPublishHarness(t, tt.tag)
			tt.setup(t, tc)
			got, err := pubobj.Publish(context.Background(), tc.input, tc.store)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				tc.store.AssertNotCalled(t, "Upload", mock.Anything, mock.Anything)

				return
			}
			require.NoError(t, err)
			tt.want(t, tc, got)
		})
	}
}

func TestParseProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "accepts a single lowercase letter", value: "a"},
		{name: "accepts dotted and hyphenated names", value: "release-cli.v2"},
		{name: "accepts the maximum length", value: "a" + strings.Repeat("b", 63)},
		{name: "rejects empty", value: "", wantErr: true},
		{name: "rejects a leading hyphen", value: "-release", wantErr: true},
		{name: "rejects uppercase", value: "Release", wantErr: true},
		{name: "rejects one character too long", value: "a" + strings.Repeat("b", 64), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := pubobj.ParseProject(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "project")

				return
			}
			require.NoError(t, err)
			assert.Equal(t, pubobj.Project(tt.value), got)
		})
	}
}

func TestPublishRejectsInvalidProject(t *testing.T) {
	t.Parallel()

	tc := newPublishHarness(t, versionTag)
	tc.input.Project = "Release"
	_, err := pubobj.Publish(context.Background(), tc.input, tc.store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project")
}

// publishHarness holds the ports, input, and recorded operations for one [pubobj.Publish] test.
type publishHarness struct {
	store    *r2mocks.MockObjectStore
	input    pubobj.PublishInput
	ops      []string
	contents map[string]string
	digests  map[string]rel.Digest
	names    []string
	prefix   string
}

// newPublishHarness constructs a valid [pubobj.PublishInput] and generated mock.
func newPublishHarness(t *testing.T, tagName string) *publishHarness {
	t.Helper()

	project, err := pubobj.ParseProject(testProject)
	require.NoError(t, err)
	tag, err := rel.ParseGitTag(tagName)
	require.NoError(t, err)
	contents := map[string]string{
		payloadName:  payloadData,
		checksumName: checksumData,
		bundleName:   bundleData,
	}
	digests := map[string]rel.Digest{
		payloadName:  mustDigest(t, payloadData),
		checksumName: mustDigest(t, checksumData),
		bundleName:   mustDigest(t, bundleData),
	}
	bundle := pubgh.Bundle{
		Payloads: []pubgh.BundleEntry{
			{Name: payloadName, Digest: stage.Digest(sha256Hex(payloadData))},
		},
		Controls: []pubgh.BundleEntry{
			{Name: checksumName, Digest: stage.Digest(sha256Hex(checksumData))},
			{Name: bundleName, Digest: stage.Digest(sha256Hex(bundleData))},
		},
	}
	tc := &publishHarness{
		store:    r2mocks.NewMockObjectStore(t),
		contents: contents,
		digests:  digests,
		names:    bundle.Names(),
		prefix:   versionPrefix,
	}
	tc.input = pubobj.PublishInput{
		Project:  project,
		Tag:      tag,
		Expected: bundle,
		Assets:   writePublishAssets(t, bundle, contents),
	}

	return tc
}

// key returns the object key for one bundle name.
func (tc *publishHarness) key(name string) string {
	return path.Join(tc.prefix, name)
}

// expectFreshUploads expects a Stat miss and immutable upload for every bundle name.
func expectFreshUploads(t *testing.T, tc *publishHarness) {
	t.Helper()

	tc.store.EXPECT().
		Stat(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, name string) (pkgrepo.StoredContent, bool, error) {
			tc.ops = append(tc.ops, "stat:"+name)

			return pkgrepo.StoredContent{}, false, nil
		}).
		Times(len(tc.names))
	tc.store.EXPECT().
		Upload(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, request pkgrepo.UploadRequest) error {
			tc.ops = append(tc.ops, "upload:"+request.Path)
			base := path.Base(request.Path)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			assert.Equal(t, pkgrepo.CacheImmutable, request.Cache)
			assert.Equal(t, tc.digests[base], request.Digest)
			assert.Equal(t, int64(len(tc.contents[base])), request.Size)
			assert.Equal(t, tc.contents[base], string(body))

			return nil
		}).
		Times(len(tc.names))
}

// expectConverged expects a matching Stat for every bundle name and no uploads.
func expectConverged(t *testing.T, tc *publishHarness) {
	t.Helper()

	tc.store.EXPECT().
		Stat(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, name string) (pkgrepo.StoredContent, bool, error) {
			tc.ops = append(tc.ops, "stat:"+name)
			base := path.Base(name)

			return pkgrepo.StoredContent{Digest: tc.digests[base], Size: int64(len(tc.contents[base]))}, true, nil
		}).
		Times(len(tc.names))
}

// expectedOps returns Stat-then-Upload or Stat-only keys in bundle order.
func expectedOps(tc *publishHarness, upload bool) []string {
	ops := make([]string, 0, len(tc.names)*2)
	for _, name := range tc.names {
		key := tc.key(name)
		ops = append(ops, "stat:"+key)
		if upload {
			ops = append(ops, "upload:"+key)
		}
	}

	return ops
}

// writePublishAssets writes each bundle name into a temp directory and returns paths in order.
func writePublishAssets(t *testing.T, bundle pubgh.Bundle, contents map[string]string) []pubgh.AssetPath {
	t.Helper()

	dir := t.TempDir()
	paths := make([]pubgh.AssetPath, 0, len(bundle.Names()))
	for _, name := range bundle.Names() {
		filePath := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(filePath, []byte(contents[name]), 0o644))
		paths = append(paths, pubgh.AssetPath(filePath))
	}

	return paths
}

// mustDigest constructs the canonical digest of content.
func mustDigest(t *testing.T, content string) rel.Digest {
	t.Helper()

	digest, err := rel.ParseDigest("sha256:" + sha256Hex(content))
	require.NoError(t, err)

	return digest
}

// sha256Hex returns the lowercase SHA-256 hex digest of data.
func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))

	return hex.EncodeToString(sum[:])
}
