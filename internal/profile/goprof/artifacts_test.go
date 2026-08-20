package goprof_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/profile/goprof"
)

func TestParseArtifactsMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := goprof.ParseArtifacts(strings.NewReader("{not-an-array"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode artifacts.json")
}

func TestSelectBinaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rootName string
		records  []goprof.Record
		want     []goprof.CanonicalBinary
		wantErr  string
	}{
		{
			name:     "selects one linux binary per required architecture",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinary("amd64", "dist/release-cli_linux_amd64/release-cli"),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
				{Type: "Archive", GOOS: "linux", GOARCH: "amd64", Path: "dist/archive.tar.gz", Name: "archive.tar.gz"},
				{
					Type:   "Binary",
					GOOS:   "darwin",
					GOARCH: "amd64",
					Path:   "dist/release-cli_darwin_amd64/release-cli",
					Name:   "release-cli",
				},
			},
			want: []goprof.CanonicalBinary{
				canonical("amd64", "dist/release-cli_linux_amd64/release-cli", "release-cli_linux_amd64/release-cli"),
				canonical("arm64", "dist/release-cli_linux_arm64/release-cli", "release-cli_linux_arm64/release-cli"),
			},
		},
		{
			name:     "non-dist directory name",
			rootName: "build",
			records: []goprof.Record{
				linuxBinary("amd64", "build/app_linux_amd64/app"),
				linuxBinary("arm64", "build/app_linux_arm64/app"),
			},
			want: []goprof.CanonicalBinary{
				canonical("amd64", "build/app_linux_amd64/app", "app_linux_amd64/app"),
				canonical("arm64", "build/app_linux_arm64/app", "app_linux_arm64/app"),
			},
		},
		{
			name:     "wrong type is ignored then missing architecture fails",
			rootName: "dist",
			records: []goprof.Record{
				{Type: "Archive", GOOS: "linux", GOARCH: "amd64", Path: "dist/a.tar.gz", Name: "a.tar.gz"},
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "missing linux Binary record for amd64; found arm64",
		},
		{
			name:     "wrong goos is ignored then missing architecture fails",
			rootName: "dist",
			records: []goprof.Record{
				{
					Type:   "Binary",
					GOOS:   "darwin",
					GOARCH: "amd64",
					Path:   "dist/release-cli_darwin_amd64/release-cli",
					Name:   "release-cli",
				},
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "missing linux Binary record for amd64; found arm64",
		},
		{
			name:     "duplicate architecture",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinary("amd64", "dist/release-cli_linux_amd64/release-cli"),
				linuxBinary("amd64", "dist/release-cli_linux_amd64_alt/release-cli"),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "duplicate linux/amd64 Binary record",
		},
		{
			name:     "missing architecture",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinary("amd64", "dist/release-cli_linux_amd64/release-cli"),
			},
			wantErr: "missing linux Binary record for arm64; found amd64",
		},
		{
			name:     "unexpected extra architecture",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinary("amd64", "dist/release-cli_linux_amd64/release-cli"),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
				linuxBinary("s390x", "dist/release-cli_linux_s390x/release-cli"),
			},
			wantErr: "unexpected linux/s390x Binary record",
		},
		{
			name:     "lexical escape",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinary("amd64", "dist/../secret"),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "escapes the dist root",
		},
		{
			name:     "absolute path",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinary("amd64", "/etc/passwd"),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "is not dist/-relative",
		},
		{
			name:     "differing binary names",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinaryNamed("amd64", "dist/release-cli_linux_amd64/release-cli", "release-cli"),
				linuxBinaryNamed("arm64", "dist/release-cli_linux_arm64/other", "other"),
			},
			wantErr: `linux architecture binaries have different names "release-cli" and "other"`,
		},
		{
			name:     "empty binary name",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinaryNamed("amd64", "dist/release-cli_linux_amd64/release-cli", ""),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "binary name is empty",
		},
		{
			name:     "binary name contains a path separator",
			rootName: "dist",
			records: []goprof.Record{
				linuxBinaryNamed("amd64", "dist/release-cli_linux_amd64/release-cli", "foo/bar"),
				linuxBinary("arm64", "dist/release-cli_linux_arm64/release-cli"),
			},
			wantErr: "contains a path separator",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := goprof.SelectBinaries(test.records, mustRoot(t, test.rootName))
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestParseArtifactsRealFixture(t *testing.T) {
	t.Parallel()

	// Refresh with:
	//   mise exec -- goreleaser build --snapshot --clean
	// then copy dist/artifacts.json over testdata/goreleaser-2.17.1-artifacts.json
	// and delete dist/.
	file, err := os.Open(filepath.Join("testdata", "goreleaser-2.17.1-artifacts.json"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, file.Close())
	})

	records, err := goprof.ParseArtifacts(file)
	require.NoError(t, err)
	require.NotEmpty(t, records)

	var binaries []goprof.Record
	for _, record := range records {
		if record.Type != "Binary" {
			continue
		}
		assert.NotEmpty(t, record.Path)
		assert.NotEmpty(t, record.Name)
		assert.NotEmpty(t, record.GOOS)
		assert.NotEmpty(t, record.GOARCH)
		binaries = append(binaries, record)
	}
	require.NotEmpty(t, binaries, "fixture must contain Binary records")

	selected, err := goprof.SelectBinaries(records, mustRoot(t, "dist"))
	require.NoError(t, err)
	require.Len(t, selected, 2)
	assert.Equal(t, "amd64", selected[0].Arch.String())
	assert.Equal(t, "arm64", selected[1].Arch.String())
	assert.Equal(t, "release-cli", selected[0].Name.String())
	assert.Equal(t, "release-cli", selected[1].Name.String())
	assert.True(t, strings.HasPrefix(selected[0].Path.String(), "dist/"))
	assert.True(t, strings.HasPrefix(selected[1].Path.String(), "dist/"))
}

func TestVerifyBinariesMapFS(t *testing.T) {
	t.Parallel()

	binaries := []goprof.CanonicalBinary{
		canonical("amd64", "dist/app_linux_amd64/app", "app_linux_amd64/app"),
		canonical("arm64", "dist/app_linux_arm64/app", "app_linux_arm64/app"),
	}

	err := goprof.VerifyBinaries(fstest.MapFS{
		"app_linux_amd64/app": {Data: []byte("amd64"), Mode: 0o755},
		"app_linux_arm64/app": {Data: []byte("arm64"), Mode: 0o755},
	}, binaries)
	require.NoError(t, err)

	err = goprof.VerifyBinaries(fstest.MapFS{
		"app_linux_amd64/app": {Data: []byte("amd64"), Mode: 0o755},
	}, binaries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app_linux_arm64/app")
}

func TestVerifyBinariesTempDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	amd64Path := filepath.Join("app_linux_amd64", "app")
	arm64Path := filepath.Join("app_linux_arm64", "app")
	writeExec(t, filepath.Join(root, amd64Path), []byte("amd64"))
	writeExec(t, filepath.Join(root, arm64Path), []byte("arm64"))

	binaries := []goprof.CanonicalBinary{
		canonical("amd64", "dist/app_linux_amd64/app", filepath.ToSlash(amd64Path)),
		canonical("arm64", "dist/app_linux_arm64/app", filepath.ToSlash(arm64Path)),
	}
	require.NoError(t, goprof.VerifyBinaries(os.DirFS(root), binaries))

	require.NoError(t, os.Chmod(filepath.Join(root, arm64Path), 0o644))
	err := goprof.VerifyBinaries(os.DirFS(root), binaries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not executable")
}

func TestCanonicalBinaryJSONRoundTripFields(t *testing.T) {
	t.Parallel()

	raw := `[{"type":"Binary","goos":"linux","goarch":"amd64","path":"dist/app","name":"app","extra":{"ignored":true}}]`
	records, err := goprof.ParseArtifacts(strings.NewReader(raw))
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "Binary", records[0].Type)
	assert.Equal(t, "linux", records[0].GOOS)
	assert.Equal(t, "amd64", records[0].GOARCH)
	assert.Equal(t, "dist/app", records[0].Path)
	assert.Equal(t, "app", records[0].Name)

	encoded, err := json.Marshal(records[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"type":"Binary"`)
}

// linuxBinary builds a linux Binary record for tests.
func linuxBinary(arch, path string) goprof.Record {
	return linuxBinaryNamed(arch, path, "release-cli")
}

// linuxBinaryNamed builds a linux Binary record with an explicit filename.
func linuxBinaryNamed(arch, path, name string) goprof.Record {
	return goprof.Record{
		Type:   "Binary",
		GOOS:   "linux",
		GOARCH: arch,
		Path:   path,
		Name:   name,
	}
}

// canonical builds a CanonicalBinary from already-valid test strings.
func canonical(arch, path, relative string) goprof.CanonicalBinary {
	return goprof.CanonicalBinary{
		Arch:         goprof.Arch(arch),
		Path:         goprof.ArtifactPath(path),
		RelativePath: goprof.RelativePath(relative),
		Name:         goprof.BinaryName("release-cli"),
	}
}

// mustRoot parses a RootName and fails the test on error.
func mustRoot(t *testing.T, name string) goprof.RootName {
	t.Helper()
	root, err := goprof.ParseRootName(name)
	require.NoError(t, err)
	return root
}

// writeExec creates an owner-executable file at path.
func writeExec(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o755))
	require.NoError(t, os.Chmod(path, 0o755))
}

func TestConfineRejectsDotDotOnDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o755))
	t.Cleanup(func() {
		_ = os.Remove(outside)
	})

	err := goprof.VerifyBinaries(os.DirFS(root), []goprof.CanonicalBinary{
		canonical("amd64", "dist/../outside", "../outside"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the dist root")
}
