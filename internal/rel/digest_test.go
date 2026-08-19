package rel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validHexLower = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	validHexUpper = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	validDigest   = digestPrefix + validHexLower
)

func TestParseDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "valid lowercase",
			input: validDigest,
			want:  validDigest,
		},
		{
			name:  "uppercase hex is normalized",
			input: digestPrefix + validHexUpper,
			want:  validDigest,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: `digest "" is empty`,
		},
		{
			name:    "missing prefix",
			input:   validHexLower,
			wantErr: `digest "` + validHexLower + `" is missing the sha256: prefix`,
		},
		{
			name:    "wrong prefix",
			input:   "sha512:" + validHexLower,
			wantErr: `digest "sha512:` + validHexLower + `" has prefix "sha512:", want "sha256:"`,
		},
		{
			name:    "short hex",
			input:   digestPrefix + "aaaa",
			wantErr: `digest "sha256:aaaa" has 4 hex digits, want 64`,
		},
		{
			name:    "long hex",
			input:   digestPrefix + validHexLower + "aa",
			wantErr: `digest "` + digestPrefix + validHexLower + `aa" has 66 hex digits, want 64`,
		},
		{
			name:    "non hex",
			input:   digestPrefix + strings.Repeat("z", 64),
			wantErr: `digest "` + digestPrefix + strings.Repeat("z", 64) + `" is not hexadecimal`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDigest(test.input)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}
