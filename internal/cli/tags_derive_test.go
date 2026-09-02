package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lookup  LookupEnv
		want    string
		wantErr string
	}{
		{
			name:   "plain v tag",
			lookup: refLookup("v0.1.4"),
			want:   "0.1.4",
		},
		{
			name:   "prefixed v tag",
			lookup: refLookup("cli/v0.1.4"),
			want:   "0.1.4",
		},
		{
			name:    "nil lookup",
			wantErr: "GITHUB_REF_NAME is unset",
		},
		{
			name:    "empty ref",
			lookup:  refLookup(""),
			wantErr: "GITHUB_REF_NAME is unset",
		},
		{
			name:    "invalid git tag",
			lookup:  refLookup("v1.2.3 "),
			wantErr: "git tag",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := deriveVersion(test.lookup)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got.String())
		})
	}
}

// refLookup returns GITHUB_REF_NAME as value.
func refLookup(value string) LookupEnv {
	return func(key string) (string, bool) {
		if key != envRefName {
			return "", false
		}
		if value == "" {
			return "", false
		}

		return value, true
	}
}
