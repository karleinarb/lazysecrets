package provider

import (
	"fmt"
	"log/slog"
)

const masked = "••••••"

// SecretValue holds a secret's plaintext. Every formatting, logging and
// encoding path renders it masked; the plaintext is only available through
// Reveal, so it cannot leak through a stray Printf, slog call or error.
type SecretValue struct {
	b []byte
}

func NewSecretValue(b []byte) SecretValue {
	return SecretValue{b: append([]byte(nil), b...)}
}

// Reveal returns the plaintext. Call it only to display or copy the value.
func (v SecretValue) Reveal() string { return string(v.b) }

func (v SecretValue) Len() int { return len(v.b) }

func (v SecretValue) String() string   { return masked }
func (v SecretValue) GoString() string { return masked }

// Format covers every fmt verb, including %#v and %x.
func (v SecretValue) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(masked)) }

func (v SecretValue) LogValue() slog.Value { return slog.StringValue(masked) }

func (v SecretValue) MarshalText() ([]byte, error) { return []byte(masked), nil }
