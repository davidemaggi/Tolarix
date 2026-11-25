package secrets

import (
	"encoding/json"
	"log/slog"
)

const redacted = "*****"

// String holds a sensitive value (PAT, API token, password). It redacts itself
// in fmt output, slog output and JSON, so it cannot leak through logs or API
// responses by accident. Call Reveal to get the plaintext.
type String struct{ v string }

// New wraps a plaintext value.
func New(v string) String { return String{v: v} }

// Reveal returns the plaintext. Use only at the point of use (an HTTP header, a hash).
func (s String) Reveal() string { return s.v }

// IsSet reports whether a non-empty value is held.
func (s String) IsSet() bool { return s.v != "" }

// String implements fmt.Stringer.
func (s String) String() string {
	if s.v == "" {
		return ""
	}
	return redacted
}

// GoString implements fmt.GoStringer so %#v is redacted too.
func (s String) GoString() string { return `secrets.String{` + s.String() + `}` }

// LogValue implements slog.LogValuer.
func (s String) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON never emits the plaintext.
func (s String) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// MarshalText never emits the plaintext.
func (s String) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalJSON accepts a plaintext value (for incoming API payloads).
func (s *String) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.v = v
	return nil
}
