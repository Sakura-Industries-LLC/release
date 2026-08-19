package rel

// redacted is the public stand-in for a Secret's contents.
const redacted = "[REDACTED]"

// Secret holds a credential that must not appear in logs or encodings.
//
// [Secret.Reveal] is the only way to read the payload. String, GoString, and
// the marshal methods always return [redacted], including for an empty value.
type Secret struct {
	// value is the secret payload. It must only be read through Reveal.
	value string
}

// NewSecret constructs a [Secret] that wraps value.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Reveal returns the wrapped payload.
//
// Callers should use Reveal only at adapter composition edges that need the
// real credential.
func (s Secret) Reveal() string {
	return s.value
}

// IsEmpty reports whether the wrapped payload is empty.
func (s Secret) IsEmpty() bool {
	return s.value == ""
}

// String returns [redacted].
func (s Secret) String() string {
	return redacted
}

// GoString returns [redacted].
func (s Secret) GoString() string {
	return redacted
}

// MarshalText returns [redacted].
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}

// MarshalJSON returns the JSON string [redacted].
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + redacted + `"`), nil
}
