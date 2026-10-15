package repo

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func TestIsCompose(t *testing.T) {
	for f, want := range map[string]bool{
		"docker-compose.yml":         true,
		"deploy/compose.yaml":        true,
		"docker-compose.test.yml":    true,
		"docker-compose-dev.yaml":    true,
		"docker-compose.example.yml": true, // listed hidden
		"applications.yaml":          false,
		"composer.json":              false,
		"compose.yml.bak":            false,
	} {
		if IsCompose(f) != want {
			t.Errorf("%s: %t", f, !want)
		}
	}
}

func TestComposePlaceholder(t *testing.T) {
	for v, want := range map[string]bool{
		"${DB_PASSWORD}": true, "$DB_PASSWORD": true, "${DB_PASSWORD:?set it}": true, "${DB_PASSWORD?}": true,
		"${DB_PASSWORD:-hunter2}": false, "${DB_PASSWORD-hunter2}": false, // the default is in the file
		"hunter2": false, "pre-${DB_PASSWORD}": false, "$": false,
	} {
		if got := reference(composePlaceholder, v); got != want {
			t.Errorf("%s: reference %t", v, got)
		}
	}
	if name, def, hasDef, _ := composePlaceholder("${PW:-x}"); name != "PW" || def != "x" || !hasDef {
		t.Errorf("default: %s %s %t", name, def, hasDef)
	}
}

func TestParseCompose(t *testing.T) {
	keys, err := parseCompose("", []byte(`x-db: &db
  DB_USER: app
  DB_PASSWORD: shared
services:
  api:
    image: api
    environment:
      <<: *db
      DB_PASSWORD: own
      API_KEY: ${API_KEY}
      FROM_HOST:
  worker:
    environment:
      - QUEUE_PASSWORD=hunter2
      - TOKEN
      - EMPTY=
  web:
    image: web
`))
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
		"api.DB_USER": "app", "api.DB_PASSWORD": "own", "api.API_KEY": "${API_KEY} (ref)", "api.FROM_HOST": " (ref)",
		"worker.QUEUE_PASSWORD": "hunter2", "worker.TOKEN": " (ref)", "worker.EMPTY": "",
	}
	if !maps.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
	for _, k := range keys {
		if k.name == "worker.QUEUE_PASSWORD" && (k.line != 14 || k.key != "QUEUE_PASSWORD") {
			t.Errorf("%s: line %d, key %s", k.name, k.line, k.key)
		}
	}
}

func TestComposeFiles(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"docker-compose.yml": `services:
  api:
    environment:
      PORT: "8080"
      DB_PASSWORD: ${DB_PASSWORD}
      JASYPT_ENCRYPTOR_PASSWORD: test-password
      API_TOKEN: ` + jasyptHunter2 + `
`,
		"docker-compose.example.yml": "services:\n  api:\n    environment:\n      DB_PASSWORD: changeme\n",
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
		if c.Detail != "compose · yaml" || c.Hidden != (c.Name == "docker-compose.example.yml") {
			t.Errorf("%s: %q hidden %t", c.Name, c.Detail, c.Hidden)
		}
	}
	compose := cs[1] // after docker-compose.example.yml

	keys, _ := p.Objects(ctx, compose, provider.TypeSecret)
	got := map[string]string{}
	for _, k := range shown(keys) {
		got[k.Name] = k.Detail
	}
	if want := map[string]string{"api.DB_PASSWORD": "reference", "api.JASYPT_ENCRYPTOR_PASSWORD": "", "api.API_TOKEN": "jasypt"}; !maps.Equal(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	// The service's JASYPT_ENCRYPTOR_PASSWORD decrypts its values.
	link := provider.Object{Container: provider.Container{Source: srcs[0], Name: "docker-compose.yml"}, Name: "api.API_TOKEN"}
	if v, err := p.Value(ctx, link); err != nil || v.Reveal() != "hunter2" {
		t.Errorf("jasypt: %q %v", v.Reveal(), err)
	}
	if f, _ := p.File(compose, &link); f.Line != 7 {
		t.Errorf("line %d", f.Line)
	}

	// compose.secretPatterns over the shared ones.
	os.WriteFile(filepath.Join(root, ".lazysecrets.yaml"), []byte("secretPatterns:\n  keys: [\"^nothing$\"]\ncompose:\n  secretPatterns:\n    keys: [\"^PORT$\"]\n    values: []\n"), 0o644)
	keys, _ = p.Objects(ctx, compose, provider.TypeSecret)
	if s := shown(keys); len(s) != 1 || s[0].Name != "api.PORT" {
		t.Errorf("compose's patterns: %v", s)
	}
}
