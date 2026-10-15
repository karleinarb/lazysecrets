package repo

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func TestIsSpring(t *testing.T) {
	for f, want := range map[string]bool{
		"src/main/resources/application.yml":        true,
		"application-prod.properties":               true,
		"config/application-local.yaml":             true,
		"bootstrap.yml":                             true,
		"applicationset/dev/applications.yaml":      false, // Argo CD's
		"chart/templates/application.yaml":          false, // Helm's
		"application.yml.example":                   false,
		"application.json":                          false,
		"src/main/resources/applicationContext.xml": false,
	} {
		if IsSpring(f) != want {
			t.Errorf("%s: %t", f, !want)
		}
	}
}

func springNames(keys []springKey) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k.name] = k.value
	}
	return out
}

func TestParseProperties(t *testing.T) {
	got := springNames(parseProperties([]byte(`# a comment
! another
a=1
b : 2
c 3
d\:e=4
long=one, \
     two
unicode=café
empty=
#---
spring.config.activate.on-profile=prod
a=prod-1
`)))
	want := map[string]string{
		"a": "1", "b": "2", "c": "3", "d:e": "4", "long": "one, two", "unicode": "café", "empty": "",
		"spring.config.activate.on-profile (prod)": "prod", "a (prod)": "prod-1",
	}
	if !maps.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestParseSpringYAML(t *testing.T) {
	keys, err := parseSpringYAML([]byte(`info.version: @project.version@
artifacts:
  - @project.artifactId@
spring:
  datasource:
    password: ${DB_PASSWORD}
servers:
  - name: a
    secret: x
logging.level:
  com.example: DEBUG
---
spring:
  config.activate.on-profile: prod
  datasource:
    password: hunter2
---
other: y
`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"info.version": "@project.version@", "artifacts[0]": "@project.artifactId@",
		"spring.datasource.password": "${DB_PASSWORD}", "servers[0].name": "a", "servers[0].secret": "x",
		"logging.level.com.example":                "DEBUG",
		"spring.config.activate.on-profile (prod)": "prod", "spring.datasource.password (prod)": "hunter2",
		"other (document 3)": "y",
	}
	if got := springNames(keys); !maps.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestSpringDefaults(t *testing.T) {
	f, err := (&Provider{}).filterFor(t.TempDir(), "spring")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"spring.datasource.password":                                 "x",
		"spring.security.oauth2.client.registration.a.client-secret": "x",
		"profapp.payment.checkoutKey":                                "x",
		"app.clients.sgig.sgig-passord":                              "x",
		"jasypt.encryptor.password":                                  "${JASYPT_ENCRYPTOR_PASSWORD}",
		"spring.datasource.url":                                      "jdbc:postgresql://app:hunter2@db:5432/app",
		"app.servicebus.connection":                                  "Endpoint=sb://x/;SharedAccessKeyName=a;SharedAccessKey=b",
		"app.name":                                                   "ENC(abc==)",
	} {
		if !f.secret(key, value) {
			t.Errorf("%s=%s isn't listed", key, value)
		}
	}
	for key, value := range map[string]string{
		"spring.security.oauth2.client.provider.a.token-uri":                    "https://idp/token",
		"spring.security.oauth2.client.registration.a.authorization-grant-type": "client_credentials",
		"app.frontend.keycloak.url":                                             "https://keycloak",
		"server.port":                                                           "8080",
		"spring.datasource.url":                                                 "jdbc:postgresql://db:5432/app",
	} {
		if f.secret(key, value) {
			t.Errorf("%s=%s is listed", key, value)
		}
	}
}

func TestSpringFiles(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"src/main/resources/application.yml": `server:
  port: 8080
spring:
  datasource:
    url: jdbc:postgresql://db/app
    password: ${DB_PASSWORD}
jasypt:
  encryptor:
    password: ${JASYPT_ENCRYPTOR_PASSWORD}
app:
  api-key: ENC(5IQUKT4B7j38TnZJ+BlWqo/zSkKHTjl4XRJyaQEHgkCy4r00avLt4dWqFB74X0Zw)
`,
		"src/main/resources/application-local.properties": "server.port=8081\nspring.datasource.password=hunter2\n",
		"applicationset/applications.yaml":                "kind: ApplicationSet\n",
	})
	ctx := context.Background()
	details := func(p *Provider, c provider.Container) map[string]provider.Attr {
		attrs, err := p.Details(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]provider.Attr{}
		for _, a := range attrs {
			out[a.Label] = a
		}
		return out
	}

	p := New(Settings{Dir: root})
	srcs, _ := p.Sources(ctx)
	cs, err := p.Containers(ctx, srcs[0])
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, c := range cs {
		files = append(files, c.Name+" · "+c.Detail)
	}
	if want := []string{
		"src/main/resources/application-local.properties · spring · properties",
		"src/main/resources/application.yml · spring · yaml",
	}; !slices.Equal(files, want) {
		t.Fatalf("files %v\nwant  %v", files, want)
	}
	local, yml := cs[0], cs[1]

	listed, _ := p.Objects(ctx, yml, provider.TypeSecret)
	keys := shown(listed)
	var names []string
	for _, k := range keys {
		names = append(names, strings.TrimSpace(k.Name+" "+k.Detail))
	}
	if len(listed) != 5 || !listed[0].Hidden || listed[0].Name != "server.port" {
		t.Errorf("hidden keys: %+v", listed)
	}
	if want := []string{"spring.datasource.password reference", "jasypt.encryptor.password reference", "app.api-key jasypt"}; !slices.Equal(names, want) {
		t.Errorf("keys %v, want %v", names, want)
	}
	t.Setenv("JASYPT_ENCRYPTOR_PASSWORD", "test-password")
	if v, err := p.Value(ctx, keys[2]); err != nil || v.Reveal() != "hunter2" {
		t.Errorf("value: %q %v", v.Reveal(), err)
	}
	link := provider.Object{Container: provider.Container{Source: srcs[0], Name: "src/main/resources/application.yml"}, Name: "spring.datasource.password"}
	if v, err := p.Value(ctx, link); err != nil || v.Reveal() != "${DB_PASSWORD}" {
		t.Errorf("from a link: %q %v", v.Reveal(), err)
	}
	if f, _ := p.File(yml, &keys[1]); f.Line != 9 || f.Sops != "" {
		t.Errorf("file: %+v", f)
	}

	// References and Jasypt aren't plain text in git; a password is.
	if d := details(p, yml); d["hidden keys"].Value != "2, not matched by the secret patterns" || d["git"].Warn || d["encryption"].Value != "jasypt: 1 of 5 values" {
		t.Errorf("yml: %+v", d)
	}
	if d := details(p, local); !d["git"].Warn {
		t.Errorf("local: %+v", d)
	}

	// The config's patterns, and the repo's over them.
	all := []string{".*"}
	p = New(Settings{Dir: root, SecretPatterns: SecretPatterns{Keys: &all}})
	if keys, _ := p.Objects(ctx, yml, provider.TypeSecret); len(shown(keys)) != 5 {
		t.Errorf("every key: %d", len(shown(keys)))
	}
	lazysecrets := filepath.Join(root, ".lazysecrets.yaml")
	os.WriteFile(lazysecrets, []byte("spring:\n  secretPatterns:\n    keys: [\"url$\"]\n    values: []\n"), 0o644)
	if keys, _ := p.Objects(ctx, yml, provider.TypeSecret); len(shown(keys)) != 1 || shown(keys)[0].Name != "spring.datasource.url" {
		t.Errorf("repo's patterns: %v", keys)
	}
	os.WriteFile(lazysecrets, []byte("spring:\n  secretPatterns:\n    keys: [\"(\"]\n"), 0o644)
	if _, err := p.Objects(ctx, yml, provider.TypeSecret); err == nil || !strings.Contains(err.Error(), ".lazysecrets.yaml: spring.secretPatterns.keys[0]") {
		t.Errorf("bad pattern: %v", err)
	}
}

func shown(objects []provider.Object) []provider.Object {
	return slices.DeleteFunc(slices.Clone(objects), func(o provider.Object) bool { return o.Hidden })
}

func TestHiddenFiles(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a/application.yml": "server:\n  port: 8080\n",
		"b/application.yml": "spring.datasource.password: x\n",
	})
	ctx := context.Background()
	p := New(Settings{Dir: root})
	srcs, _ := p.Sources(ctx)
	if len(srcs) != 1 || srcs[0].Hidden {
		t.Fatalf("sources: %+v", srcs)
	}
	cs, _ := p.Containers(ctx, srcs[0])
	if len(cs) != 2 || !cs[0].Hidden || cs[1].Hidden {
		t.Errorf("files: %+v", cs)
	}

	// A repo with only hidden files is hidden too.
	root = gitRepo(t, map[string]string{"application.yml": "server:\n  port: 8080\n"})
	srcs, _ = New(Settings{Dir: root}).Sources(ctx)
	if len(srcs) != 1 || !srcs[0].Hidden {
		t.Errorf("hidden repo: %+v", srcs)
	}
}
