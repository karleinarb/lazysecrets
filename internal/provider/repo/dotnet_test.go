package repo

import (
	"context"
	"maps"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func TestIsDotnet(t *testing.T) {
	for f, want := range map[string]bool{
		"appsettings.json":                    true,
		"src/Api/appsettings.Production.json": true,
		"appsettings.Example.json":            true, // listed hidden
		"appsettings.json.bak":                false,
		"settings.json":                       false,
		"package.json":                        false,
	} {
		if IsDotnet(f) != want {
			t.Errorf("%s: %t", f, !want)
		}
	}
}

func TestParseDotnet(t *testing.T) {
	keys, err := parseDotnet("", []byte("\xef\xbb\xbf"+`{
  "ConnectionStrings": { "Default": "Server=db;Password=hunter2" },
  "AzureAd": { "ClientId": "abc", "ClientSecret": "@Microsoft.KeyVault(SecretUri=https://kv.vault.azure.net/secrets/ad)" },
  "Servers": [ { "Name": "a", "ApiKey": "k" } ],
  "Logging": { "LogLevel": { "Default": "Information" } }
}`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, k := range keys {
		ref := ""
		if k.ref {
			ref = " (ref)"
		}
		got[k.name] = k.value + ref
	}
	want := map[string]string{
		"ConnectionStrings:Default": "Server=db;Password=hunter2",
		"AzureAd:ClientId":          "abc",
		"AzureAd:ClientSecret":      "@Microsoft.KeyVault(SecretUri=https://kv.vault.azure.net/secrets/ad) (ref)",
		"Servers:0:Name":            "a",
		"Servers:0:ApiKey":          "k",
		"Logging:LogLevel:Default":  "Information",
	}
	if !maps.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestDotnetFiles(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"src/Api/appsettings.json": `{
  "Logging": { "LogLevel": { "Default": "Information" } },
  "ConnectionStrings": { "Default": "Server=db;User Id=app;Password=hunter2" },
  "AzureAd": { "ClientSecret": "@Microsoft.KeyVault(VaultName=kv;SecretName=ad)" }
}`,
		"src/Api/appsettings.Development.json": `{ "Logging": { "LogLevel": { "Default": "Debug" } } }`,
	})
	t.Setenv("JASYPT_ENCRYPTOR_PASSWORD", "")
	ctx := context.Background()
	p := New(Settings{Dir: root})
	srcs, _ := p.Sources(ctx)
	cs, err := p.Containers(ctx, srcs[0])
	if err != nil || len(cs) != 2 {
		t.Fatalf("files: %+v %v", cs, err)
	}
	for _, c := range cs {
		if c.Detail != "dotnet · json" || c.Hidden != (c.Name == "src/Api/appsettings.Development.json") {
			t.Errorf("%s: %q hidden %t", c.Name, c.Detail, c.Hidden)
		}
	}
	settings := cs[1]

	keys, _ := p.Objects(ctx, settings, provider.TypeSecret)
	got := map[string]string{}
	for _, k := range shown(keys) {
		got[k.Name] = k.Detail
	}
	// The connection string by its value, the client secret by its name.
	if want := map[string]string{"ConnectionStrings:Default": "", "AzureAd:ClientSecret": "reference"}; !maps.Equal(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	link := provider.Object{Container: provider.Container{Source: srcs[0], Name: "src/Api/appsettings.json"}, Name: "ConnectionStrings:Default"}
	if v, err := p.Value(ctx, link); err != nil || v.Reveal() != "Server=db;User Id=app;Password=hunter2" {
		t.Errorf("value: %q %v", v.Reveal(), err)
	}
	if f, _ := p.File(settings, &link); f.Line != 3 {
		t.Errorf("line %d", f.Line)
	}
	if d, plain := p.configDetails(dotnetType, settings); !plain || d[1].Value != "1, not matched by the secret patterns" {
		t.Errorf("details: %+v %t", d, plain)
	}
}
