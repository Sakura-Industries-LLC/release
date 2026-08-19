package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/cli"
)

func TestEnvelopeJSONShape(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(cli.Envelope{
		Schema:  cli.Schema,
		Command: "version",
		OK:      true,
		Result: cli.VersionResult{
			Version:  "1.0.0",
			Commit:   "deadbeef",
			Protocol: cli.Protocol,
		},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"schema":"release.dev/result/v1",
		"command":"version",
		"ok":true,
		"result":{"version":"1.0.0","commit":"deadbeef","protocol":1}
	}`, string(payload))
}

func TestProtocolConstant(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 1, cli.Protocol)
}
