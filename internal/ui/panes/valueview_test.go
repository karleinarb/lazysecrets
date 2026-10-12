package panes

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestValueViewJSON(t *testing.T) {
	lines, note := valueView(`{"user":"app","port":5432,"tags":["a"]}`)
	want := []string{"{", `  "user": "app",`, `  "port": 5432,`, `  "tags": [`, `    "a"`, "  ]", "}"}
	if !slices.Equal(lines, want) || note != "JSON, formatted" {
		t.Errorf("lines %q, note %q", lines, note)
	}
	for _, v := range []string{"plain value", "{not json", "42", "line one\nline two"} {
		if lines, note := valueView(v); note != "" || strings.Join(lines, "\n") != v {
			t.Errorf("%q shown as %q (%q)", v, lines, note)
		}
	}
}

func TestValueViewCertificate(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.com"},
		DNSNames:  []string{"app.example.com", "www.app.example.com"},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	bundle := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	lines, note := valueView(bundle)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"subject  CN=app.example.com", "valid    2026-01-01 to 2027-01-01",
		"names    app.example.com, www.app.example.com", "SHA-1    ", "EC PRIVATE KEY (not shown)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if note != "certificate" || strings.Contains(text, "BEGIN") {
		t.Errorf("note %q, or PEM shown:\n%s", note, text)
	}
	// A private key alone isn't a certificate: shown as it is.
	if _, note := valueView(string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))); note != "" {
		t.Errorf("a key alone: note %q", note)
	}
}
