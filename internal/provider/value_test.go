package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretValueNeverFormatsPlaintext(t *testing.T) {
	const plain = "hunter2"
	v := NewSecretValue([]byte(plain))
	wrapped := struct{ V SecretValue }{v}

	var outputs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		outputs = append(outputs, fmt.Sprintf(verb, v), fmt.Sprintf(verb, wrapped))
	}
	outputs = append(outputs, fmt.Sprint(v), fmt.Errorf("failed: %v", v).Error())

	js, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(js))

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "value", v)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "value", v)
	outputs = append(outputs, buf.String())

	for _, out := range outputs {
		if strings.Contains(out, plain) || strings.Contains(out, fmt.Sprintf("%x", plain)) {
			t.Errorf("plaintext leaked: %q", out)
		}
	}
	if v.Reveal() != plain {
		t.Errorf("Reveal() = %q, want %q", v.Reveal(), plain)
	}
}

func TestNewSecretValueCopiesInput(t *testing.T) {
	b := []byte("abc")
	v := NewSecretValue(b)
	b[0] = 'x'
	if v.Reveal() != "abc" {
		t.Errorf("value aliased caller's slice: %q", v.Reveal())
	}
}
