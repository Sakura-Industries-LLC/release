package cli_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
)

func TestGreet(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args []string
		want string
	}{
		"default name": {
			args: []string{"greet"},
			want: "Hello, world!\n",
		},
		"provided name": {
			args: []string{"greet", "Meigma"},
			want: "Hello, Meigma!\n",
		},
		"uppercase": {
			args: []string{"greet", "Meigma", "--uppercase"},
			want: "HELLO, MEIGMA!\n",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			output := &bytes.Buffer{}
			command := cli.NewRootCommand(cli.Options{Out: output})
			command.SetArgs(test.args)

			require.NoError(t, command.Execute())
			assert.Equal(t, test.want, output.String())
		})
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	command := cli.NewRootCommand(cli.Options{
		Out: output,
		Build: cli.BuildInfo{
			Version: "1.2.3",
			Commit:  "abc1234",
		},
	})
	command.SetArgs([]string{"--version"})

	require.NoError(t, command.Execute())
	assert.Equal(t, "release-mvp 1.2.3 (abc1234)\n", output.String())
}
