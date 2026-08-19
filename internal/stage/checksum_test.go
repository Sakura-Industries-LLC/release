package stage_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage"
)

func TestParseChecksums(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    []stage.ChecksumEntry
		wantErr string
	}{
		{
			name:  "two-space GNU form",
			input: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  archive.tar.gz\n",
			want: []stage.ChecksumEntry{
				{Name: "archive.tar.gz", Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
		},
		{
			name:  "binary marker",
			input: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa *archive.tar.gz\n",
			want: []stage.ChecksumEntry{
				{Name: "archive.tar.gz", Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
		},
		{
			name:  "uppercase hex is normalized",
			input: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA  archive.tar.gz\n",
			want: []stage.ChecksumEntry{
				{Name: "archive.tar.gz", Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
		},
		{
			name:  "CRLF is tolerated",
			input: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  archive.tar.gz\r\n",
			want: []stage.ChecksumEntry{
				{Name: "archive.tar.gz", Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: "does not list any release payloads",
		},
		{
			name: "duplicate name",
			input: "" +
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  archive.tar.gz\n" +
				"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  archive.tar.gz\n",
			wantErr: "duplicate checksums.txt entry: archive.tar.gz",
		},
		{
			name:    "self-listed control file",
			input:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  checksums.txt\n",
			wantErr: "control file checksums.txt must not be listed",
		},
		{
			name:    "path separator in name",
			input:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  nested/archive.tar.gz\n",
			wantErr: "contains a path separator",
		},
		{
			name:    "bad digest length",
			input:   "aaaa  archive.tar.gz\n",
			wantErr: "malformed entry",
		},
		{
			name:    "bad digest charset",
			input:   "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz  archive.tar.gz\n",
			wantErr: "is not hexadecimal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := stage.ParseChecksums(strings.NewReader(test.input))
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.Entries())
		})
	}
}

func TestVerifyBundle(t *testing.T) {
	t.Parallel()

	const digest = "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"

	claim, err := stage.ParseChecksums(strings.NewReader(digest + "  payload.bin\n"))
	require.NoError(t, err)

	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{
			name: "matching regular payload and nonempty bundle",
			fsys: fstest.MapFS{
				"payload.bin":                 {Data: []byte("foo")},
				"checksums.txt.sigstore.json": {Data: []byte("{bundle}")},
			},
		},
		{
			name: "missing payload",
			fsys: fstest.MapFS{
				"checksums.txt.sigstore.json": {Data: []byte("{bundle}")},
			},
			wantErr: "payload.bin",
		},
		{
			name: "empty payload hash mismatch",
			fsys: fstest.MapFS{
				"payload.bin":                 {Data: []byte{}},
				"checksums.txt.sigstore.json": {Data: []byte("{bundle}")},
			},
			wantErr: "payload.bin",
		},
		{
			name: "non-regular payload",
			fsys: fstest.MapFS{
				"payload.bin":                 {Mode: fs.ModeDir},
				"checksums.txt.sigstore.json": {Data: []byte("{bundle}")},
			},
			wantErr: "payload.bin is not a regular file",
		},
		{
			name: "missing sigstore bundle",
			fsys: fstest.MapFS{
				"payload.bin": {Data: []byte("foo")},
			},
			wantErr: "checksums.txt.sigstore.json",
		},
		{
			name: "empty sigstore bundle",
			fsys: fstest.MapFS{
				"payload.bin":                 {Data: []byte("foo")},
				"checksums.txt.sigstore.json": {Data: []byte{}},
			},
			wantErr: "checksums.txt.sigstore.json is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := stage.VerifyBundle(test.fsys, claim)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}
