package rel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Version
		wantErr string
	}{
		{
			name:  "zero triple",
			input: "0.0.0",
			want:  Version{},
		},
		{
			name:  "stable triple",
			input: "1.2.3",
			want:  Version{Major: 1, Minor: 2, Patch: 3},
		},
		{
			name:  "multi-digit components",
			input: "10.20.30",
			want:  Version{Major: 10, Minor: 20, Patch: 30},
		},
		{
			name:  "max uint64 components",
			input: "18446744073709551615.18446744073709551615.18446744073709551615",
			want: Version{
				Major: 18446744073709551615,
				Minor: 18446744073709551615,
				Patch: 18446744073709551615,
			},
		},
		{
			name:    "leading zero in major",
			input:   "01.2.3",
			wantErr: `version "01.2.3" has a leading zero in the major component`,
		},
		{
			name:    "leading zero in minor",
			input:   "1.02.3",
			wantErr: `version "1.02.3" has a leading zero in the minor component`,
		},
		{
			name:    "leading zero in patch",
			input:   "1.2.03",
			wantErr: `version "1.2.03" has a leading zero in the patch component`,
		},
		{
			name:    "v prefix",
			input:   "v1.2.3",
			wantErr: `version "v1.2.3" has a v prefix`,
		},
		{
			name:    "uppercase V prefix",
			input:   "V1.2.3",
			wantErr: `version "V1.2.3" has a v prefix`,
		},
		{
			name:    "prerelease",
			input:   "1.2.3-rc.1",
			wantErr: `version "1.2.3-rc.1" has a prerelease suffix`,
		},
		{
			name:    "build metadata",
			input:   "1.2.3+build.1",
			wantErr: `version "1.2.3+build.1" has build metadata`,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: `version "" is empty`,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: `version "   " is empty`,
		},
		{
			name:    "two components",
			input:   "1.2",
			wantErr: `version "1.2" has 2 components, want 3`,
		},
		{
			name:    "four components",
			input:   "1.2.3.4",
			wantErr: `version "1.2.3.4" has 4 components, want 3`,
		},
		{
			name:    "non-numeric major",
			input:   "a.2.3",
			wantErr: `version "a.2.3" has a non-numeric major component`,
		},
		{
			name:    "signed major",
			input:   "+1.2.3",
			wantErr: `version "+1.2.3" has a signed major component`,
		},
		{
			name:    "negative patch",
			input:   "1.2.-3",
			wantErr: `version "1.2.-3" has a signed patch component`,
		},
		{
			name:    "component larger than uint64",
			input:   "1.2.99999999999999999999",
			wantErr: `version "1.2.99999999999999999999" has a patch component that exceeds uint64`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseVersion(test.input)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestVersionCompare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		left  Version
		right Version
		want  int
	}{
		{
			name:  "equal",
			left:  Version{Major: 1, Minor: 2, Patch: 3},
			right: Version{Major: 1, Minor: 2, Patch: 3},
			want:  0,
		},
		{
			name:  "major less",
			left:  Version{Major: 1, Minor: 9, Patch: 9},
			right: Version{Major: 2, Minor: 0, Patch: 0},
			want:  -1,
		},
		{
			name:  "major greater",
			left:  Version{Major: 2, Minor: 0, Patch: 0},
			right: Version{Major: 1, Minor: 9, Patch: 9},
			want:  1,
		},
		{
			name:  "minor less",
			left:  Version{Major: 1, Minor: 1, Patch: 9},
			right: Version{Major: 1, Minor: 2, Patch: 0},
			want:  -1,
		},
		{
			name:  "minor greater",
			left:  Version{Major: 1, Minor: 2, Patch: 0},
			right: Version{Major: 1, Minor: 1, Patch: 9},
			want:  1,
		},
		{
			name:  "patch less",
			left:  Version{Major: 1, Minor: 2, Patch: 3},
			right: Version{Major: 1, Minor: 2, Patch: 4},
			want:  -1,
		},
		{
			name:  "patch greater",
			left:  Version{Major: 1, Minor: 2, Patch: 4},
			right: Version{Major: 1, Minor: 2, Patch: 3},
			want:  1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, test.left.Compare(test.right))
		})
	}
}

func TestVersionStringAndTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version Version
		want    string
	}{
		{name: "zero", version: Version{}, want: "0.0.0"},
		{name: "stable", version: Version{Major: 1, Minor: 2, Patch: 3}, want: "1.2.3"},
		{name: "multi-digit", version: Version{Major: 10, Minor: 20, Patch: 30}, want: "10.20.30"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, test.version.String())
			assert.Equal(t, Tag(test.want), test.version.Tag())

			parsed, err := ParseVersion(test.version.String())
			require.NoError(t, err)
			assert.Equal(t, test.version, parsed)
		})
	}
}
