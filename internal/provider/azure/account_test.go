package azure

import (
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func TestAccount(t *testing.T) {
	claims := map[string]any{
		"name": "Alice Example", "oid": "11111111-2222-3333-4444-555555555555",
		"amr":  []any{"pwd", "mfa"},
		"wids": []any{"62e90394-69f5-4237-9190-012177145e10", memberRole, "aaaaaaaa-0000-0000-0000-000000000000"},
	}
	p := &Provider{account: &accountDetails{
		account: account{user: "alice@contoso.com", tenant: "t1", tenantName: "Contoso", subscriptions: 3, defaultSub: "platform"},
		claims:  func() map[string]any { return claims },
	}}
	a, ok := p.Account()
	if !ok {
		t.Fatal("no account")
	}
	got := map[string]string{}
	for _, f := range a.Fields {
		got[f.Label] = f.Value
	}
	want := map[string]string{
		"name": "Alice Example", "user": "alice@contoso.com", "tenant": "Contoso", "tenant ID": "t1",
		"object ID": "11111111-2222-3333-4444-555555555555", "sign-in": "MFA · password",
		"roles": "Global Administrator, role aaaaaaaa", "subs": "3 · default platform",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("fields %v", got)
	}
	if a.Copy != claims["oid"] || a.CopyLabel != "object ID" {
		t.Errorf("copy %q %q", a.Copy, a.CopyLabel)
	}

	// Before any token: what az's list says.
	claims = nil
	a, _ = p.Account()
	if len(a.Fields) != 4 || a.Copy != "" {
		t.Errorf("without a token: %+v", a)
	}
	if _, ok := (&Provider{}).Account(); ok {
		t.Error("a service principal from AZURE_* has no account details")
	}
	var _ provider.Accounts = p
}

func TestSignInMethods(t *testing.T) {
	for want, amr := range map[string][]any{
		"MFA · password":                     {"pwd", "mfa"},
		"MFA · Windows Hello or certificate": {"rsa", "ngcmfa"},
		"no MFA · password":                  {"pwd"},
		"":                                   nil,
	} {
		if got := signInMethods(amr); got != want {
			t.Errorf("%v: %q, want %q", amr, got, want)
		}
	}
}
