package rel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGitTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "plain v tag", input: "v1.2.3", want: "v1.2.3"},
		{name: "prefixed v tag", input: "cli/v1.2.3", want: "cli/v1.2.3"},
		{name: "nested prefix", input: "sdk/go/v0.1.0", want: "sdk/go/v0.1.0"},
		{name: "plain version", input: "1.2.3", want: "1.2.3"},
		{name: "max length", input: strings.Repeat("a", maxGitTagLength), want: strings.Repeat("a", maxGitTagLength)},
		{name: "empty", input: "", wantErr: `git tag "" is empty`},
		{name: "trailing space", input: "v1.2.3 ", wantErr: `git tag "v1.2.3 " has an invalid character`},
		{name: "double dot", input: "a..b", wantErr: `git tag "a..b" has an invalid character sequence`},
		{name: "leading hyphen", input: "-x", wantErr: `git tag "-x" has an invalid leading character`},
		{name: "leading slash", input: "/x", wantErr: `git tag "/x" has an invalid leading character`},
		{name: "trailing slash", input: "x/", wantErr: `git tag "x/" has an invalid trailing component`},
		{name: "trailing lock", input: "x.lock", wantErr: `git tag "x.lock" has an invalid trailing component`},
		{name: "double slash", input: "a//b", wantErr: `git tag "a//b" has an invalid character sequence`},
		{
			name:    "too long",
			input:   strings.Repeat("a", maxGitTagLength+1),
			wantErr: `git tag "` + strings.Repeat("a", maxGitTagLength+1) + `" has length 256, want at most 255`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseGitTag(test.input)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}

func TestGitTagVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "plain v tag", input: "v1.2.3", want: "1.2.3"},
		{name: "plain version", input: "1.2.3", want: "1.2.3"},
		{name: "prefixed v tag", input: "cli/v1.2.3", want: "1.2.3"},
		{name: "prefixed version", input: "cli/1.2.3", want: "1.2.3"},
		{name: "nested prefix", input: "sdk/go/v0.1.0", want: "0.1.0"},
		{name: "double v prefix", input: "vv1.2.3", wantErr: `version "v1.2.3" has a v prefix`},
		{name: "empty after strip", input: "cli/v", wantErr: `version "" is empty`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tag, err := ParseGitTag(test.input)
			require.NoError(t, err)

			got, err := tag.Version()
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}
