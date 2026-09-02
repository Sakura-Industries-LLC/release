package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveGitHub(t *testing.T) {
	t.Parallel()

	dist := t.TempDir()
	tests := []struct {
		name    string
		ref     string
		wantTag string
		wantErr string
	}{
		{name: "plain v tag", ref: "v0.1.4", wantTag: "v0.1.4"},
		{name: "prefixed v tag", ref: "cli/v0.1.4", wantTag: "cli/v0.1.4"},
		{name: "trailing space", ref: "v1.2.3 ", wantErr: "git tag"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveGitHub(Options{
				LookupEnv: githubResolveLookup(test.ref),
				settings:  &Settings{Dist: dist},
			})
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.wantTag, got.Tag.String())
		})
	}
}

// githubResolveLookup returns the required publish-github environment.
func githubResolveLookup(ref string) LookupEnv {
	values := map[string]string{
		envRepository: "Sakura-Industries-LLC/release",
		envRefName:    ref,
		envCommitSHA:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		envAppToken:   "ghs_test",
	}

	return func(key string) (string, bool) {
		value, ok := values[key]

		return value, ok
	}
}
