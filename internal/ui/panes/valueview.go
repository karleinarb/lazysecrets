package panes

import (
	"bytes"
	"crypto/sha1"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// A revealed value is shown as it reads best: JSON indented, and PEM
// certificates as what they say (subject, issuer, dates, names) rather
// than base64, with any other PEM block, a private key say, named but not
// shown. Anything else is shown as it is. y copies it as stored.

// valueView is how to show v, and what's been done to it ("" when it's
// shown as it is).
func valueView(v string) (lines []string, note string) {
	if lines, ok := pemView(v); ok {
		return lines, "certificate"
	}
	if t := strings.TrimSpace(v); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)) {
		var b bytes.Buffer
		if json.Indent(&b, []byte(t), "", "  ") == nil {
			return strings.Split(b.String(), "\n"), "JSON, formatted"
		}
	}
	return strings.Split(v, "\n"), ""
}

// pemView describes the PEM blocks in v, if it's PEM with a certificate.
func pemView(v string) ([]string, bool) {
	rest := []byte(strings.TrimSpace(v))
	var out []string
	certs := 0
	for len(rest) > 0 {
		block, after := pem.Decode(rest)
		if block == nil {
			return nil, false // text that isn't PEM: show it all
		}
		rest = bytes.TrimSpace(after)
		if len(out) > 0 {
			out = append(out, "")
		}
		if block.Type != "CERTIFICATE" {
			out = append(out, block.Type+" (not shown)")
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, false
		}
		certs++
		out = append(out, certLines(cert)...)
	}
	return out, certs > 0
}

func certLines(c *x509.Certificate) []string {
	day := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	out := []string{
		"subject  " + c.Subject.String(),
		"issuer   " + c.Issuer.String(),
		"valid    " + day(c.NotBefore) + " to " + day(c.NotAfter),
	}
	names := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	names = append(names, c.EmailAddresses...)
	if len(names) > 0 {
		out = append(out, "names    "+strings.Join(names, ", "))
	}
	sum := sha1.Sum(c.Raw) // what Azure and Windows call the thumbprint
	return append(out, "SHA-1    "+fmt.Sprintf("%X", sum[:]))
}
