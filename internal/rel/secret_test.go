package rel

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretNeverLeaks(t *testing.T) {
	t.Parallel()

	const payload = "super-secret-token"

	tests := []struct {
		name   string
		secret Secret
	}{
		{name: "populated", secret: NewSecret(payload)},
		{name: "empty", secret: NewSecret("")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, redacted, test.secret.String())
			assert.Equal(t, redacted, test.secret.GoString())
			assert.Equal(t, redacted, fmt.Sprintf("%v", test.secret))
			assert.Equal(t, "token="+redacted, fmt.Sprintf("token=%s", test.secret))
			assert.Equal(t, `"`+redacted+`"`, fmt.Sprintf("%q", test.secret))
			assert.Equal(t, redacted, fmt.Sprintf("%#v", test.secret))

			text, err := test.secret.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, []byte(redacted), text)

			payloadJSON, err := json.Marshal(struct {
				Token Secret `json:"token"`
			}{Token: test.secret})
			require.NoError(t, err)
			assert.JSONEq(t, `{"token":"[REDACTED]"}`, string(payloadJSON))
		})
	}
}

func TestSecretRevealAndIsEmpty(t *testing.T) {
	t.Parallel()

	secret := NewSecret("super-secret-token")
	assert.Equal(t, "super-secret-token", secret.Reveal())
	assert.False(t, secret.IsEmpty())

	empty := NewSecret("")
	assert.Empty(t, empty.Reveal())
	assert.True(t, empty.IsEmpty())
}
