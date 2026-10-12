package ref

import (
	"reflect"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

const (
	kvHost = "kv-app-prod.vault.azure.net"
	dbHost = "adb-1111111111111111.11.azuredatabricks.net"
)

func TestParse(t *testing.T) {
	secret := Ref{Kind: provider.KindAzure, Host: kvHost, Type: provider.TypeSecret, Name: "db-password"}
	versioned := secret
	versioned.Version = "91d3e0"
	vault := Ref{Kind: provider.KindAzure, Host: kvHost}
	keysTab := vault
	keysTab.Type = provider.TypeKey
	cert := Ref{Kind: provider.KindAzure, Host: kvHost, Type: provider.TypeCertificate, Name: "app-tls"}
	scope := Ref{Kind: provider.KindDatabricks, Host: dbHost, Scope: "app"}
	dbxSecret := Ref{Kind: provider.KindDatabricks, Host: dbHost, Scope: "app", Name: "storage-key", Type: provider.TypeSecret}

	for in, want := range map[string]Ref{
		// lazysecrets links
		"lazysecrets://azure/" + kvHost:                                           vault,
		"lazysecrets://azure/" + kvHost + "/":                                     vault,
		"lazysecrets://azure/" + kvHost + "/keys":                                 keysTab,
		"lazysecrets://azure/" + kvHost + "/secrets/db-password":                  secret,
		"lazysecrets://azure/" + kvHost + "/secrets/db-password/91d3e0":           versioned,
		"lazysecrets://azure/" + kvHost + "/secrets/db-password?action=copy":      secret, // early links: ignored
		"LazySecrets://Azure/" + strings.ToUpper(kvHost) + "/secrets/db-password": secret,
		"lazysecrets://databricks/" + dbHost + "/app":                             scope,
		"lazysecrets://databricks/" + dbHost + "/app/storage-key":                 dbxSecret,
		// formats people already share
		"https://" + kvHost + "/secrets/db-password":                                             secret,
		"https://" + kvHost + "/secrets/db-password/91d3e0":                                      versioned,
		"https://" + kvHost + "/":                                                                vault,
		"https://" + kvHost + "/certificates/app-tls":                                            cert,
		"@Microsoft.KeyVault(SecretUri=https://" + kvHost + "/secrets/db-password/)":             secret,
		"@Microsoft.KeyVault(VaultName=kv-app-prod;SecretName=db-password)":                      secret,
		"@Microsoft.KeyVault(VaultName=kv-app-prod;SecretName=db-password;SecretVersion=91d3e0)": versioned,
		"  {{secrets/app/storage-key}}  ":                                                        {Kind: provider.KindDatabricks, Scope: "app", Name: "storage-key", Type: provider.TypeSecret},
		"https://portal.azure.com/#@t/resource/subscriptions/s/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv-app-prod/overview": vault,
		"https://portal.azure.com/#@t/asset/Microsoft_Azure_KeyVault/Certificate/https://" + kvHost + "/certificates/app-tls":              cert,
	} {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", in, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"hunter2",
		"https://example.com/secrets/x",
		"lazysecrets://aws/secret",
		"lazysecrets://azure/",
		"lazysecrets://azure/" + kvHost + "/passwords/x",
		"lazysecrets://azure/" + kvHost + "/secrets/a/b/c",
		"lazysecrets://azure/subscriptions/",
		"lazysecrets://azure/subscriptions/a/b",
		"@Microsoft.KeyVault(VaultName=kv)",
	} {
		if r, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", in, r)
		}
	}
}

func TestLinksRoundTrip(t *testing.T) {
	kv := provider.Container{
		Source: provider.Source{Kind: provider.KindAzure, ID: "sub"},
		Name:   "kv-app-prod", Detail: "https://" + kvHost + "/",
	}
	scope := provider.Container{
		Source: provider.Source{Kind: provider.KindDatabricks, ID: "prod", Detail: dbHost},
		Name:   "team@data",
	}
	key := provider.Object{Container: kv, Type: provider.TypeKey, Name: "signing-key"}
	dbx := provider.Object{Container: scope, Type: provider.TypeSecret, Name: "storage.key"}

	links := map[string]func() (Ref, error){
		"lazysecrets://azure/" + kvHost:                                 func() (Ref, error) { return ForContainer(kv, provider.TypeSecret) },
		"lazysecrets://azure/" + kvHost + "/keys":                       func() (Ref, error) { return ForContainer(kv, provider.TypeKey) },
		"lazysecrets://azure/" + kvHost + "/keys/signing-key/v9":        func() (Ref, error) { return ForObject(key, "v9") },
		"lazysecrets://databricks/" + dbHost + "/team@data":             func() (Ref, error) { return ForContainer(scope, provider.TypeSecret) },
		"lazysecrets://databricks/" + dbHost + "/team@data/storage.key": func() (Ref, error) { return ForObject(dbx, "") },
	}
	for want, build := range links {
		r, err := build()
		if err != nil {
			t.Fatal(err)
		}
		if got := r.String(); got != want {
			t.Errorf("link = %q, want %q", got, want)
		}
		back, err := Parse(r.String())
		if err != nil {
			t.Fatalf("Parse(%q): %v", r.String(), err)
		}
		if !reflect.DeepEqual(back, r) {
			t.Errorf("round trip of %q\n got %+v\nwant %+v", want, back, r)
		}
	}

}

func TestSlashInNamesIsEscaped(t *testing.T) {
	r := Ref{Kind: provider.KindDatabricks, Host: dbHost, Scope: "a/b", Name: "c", Type: provider.TypeSecret}
	back, err := Parse(r.String())
	if err != nil || back.Scope != "a/b" || back.Name != "c" {
		t.Errorf("%q → %+v, %v", r.String(), back, err)
	}
}

func TestIdentifierURL(t *testing.T) {
	for in, want := range map[string]string{
		"lazysecrets://azure/" + kvHost + "/secrets/db-password/91d3e0": "https://" + kvHost + "/secrets/db-password/91d3e0",
		"lazysecrets://azure/" + kvHost + "/keys/signing-key":           "https://" + kvHost + "/keys/signing-key",
		"lazysecrets://azure/" + kvHost:                                 "https://" + kvHost + "/",
		"lazysecrets://databricks/" + dbHost + "/app/k":                 "",
	} {
		r, err := Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.IdentifierURL(); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestSourceLinks(t *testing.T) {
	az := provider.Source{Kind: provider.KindAzure, ID: "4d3073a0-1111"}
	dbx := provider.Source{Kind: provider.KindDatabricks, ID: "prod", Detail: dbHost}
	for src, want := range map[provider.Source]string{
		az:  "lazysecrets://azure/subscriptions/4d3073a0-1111",
		dbx: "lazysecrets://databricks/" + dbHost,
	} {
		r, err := ForSource(src)
		if err != nil {
			t.Fatal(err)
		}
		if r.String() != want || !r.IsSource() || r.IsContainer() {
			t.Errorf("%s: %q source=%v container=%v", src.Kind, r.String(), r.IsSource(), r.IsContainer())
		}
		back, err := Parse(want)
		if err != nil || !reflect.DeepEqual(back, r) {
			t.Errorf("round trip %q: %+v, %v", want, back, err)
		}
	}
	// Vault and scope links aren't source links.
	for _, link := range []string{"lazysecrets://azure/" + kvHost, "lazysecrets://databricks/" + dbHost + "/app"} {
		if r, _ := Parse(link); r.IsSource() || !r.IsContainer() {
			t.Errorf("%s: source=%v", link, r.IsSource())
		}
	}
}

func TestRepoLinks(t *testing.T) {
	src := provider.Source{Kind: provider.KindRepo, Detail: "git.kebcloud.no/keb/nix-config"}
	c := provider.Container{Source: src, Name: "secrets/server.yaml"}
	r, err := ForObject(provider.Object{Container: c, Name: "db.password", Type: provider.TypeSecret}, "")
	if err != nil {
		t.Fatal(err)
	}
	link := r.String()
	if link != "lazysecrets://repo/git.kebcloud.no%2Fkeb%2Fnix-config/secrets%2Fserver.yaml/db.password" {
		t.Errorf("link %s", link)
	}
	back, err := Parse(link)
	if err != nil || back.Kind != provider.KindRepo || back.Host != "git.kebcloud.no/keb/nix-config" ||
		back.Scope != "secrets/server.yaml" || back.Name != "db.password" {
		t.Errorf("parsed %+v, %v", back, err)
	}
	if fr, _ := ForSource(src); !fr.IsSource() || fr.String() != "lazysecrets://repo/git.kebcloud.no%2Fkeb%2Fnix-config" {
		t.Errorf("source link %s", fr.String())
	}
	if cr, _ := ForContainer(c, provider.TypeSecret); !cr.IsContainer() || cr.ContainerName() != "secrets/server.yaml" {
		t.Errorf("file link %+v", cr)
	}
}
