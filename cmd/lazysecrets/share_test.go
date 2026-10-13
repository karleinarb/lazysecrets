package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/share"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// demoRules take the demo key, made up to be remembered.
var demoRules = share.Rules{MinLength: 16, MinClasses: 1}

func TestShareFiles(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@git.example.com:team/app.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skip("git:", string(out))
		}
	}
	os.WriteFile(filepath.Join(root, ".env"), []byte("A=1\n"), 0o600)
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)
	providers := []provider.Provider{fake.Azure(0)}
	st, _ := store.Load("")
	ctx := context.Background()
	key := "lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password"

	var out bytes.Buffer
	if err := shareFiles(ctx, providers, st, key, demoRules, []string{filepath.Join(root, ".env")}, &out); err != nil {
		t.Fatal(err)
	}
	r, err := ref.Parse(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	s, err := share.Read(r)
	if err != nil || s.Repo != "git.example.com/team/app" || s.Key.Name != "db-password" {
		t.Fatalf("%+v, %v", s.Header, err)
	}
	files, err := s.Open([]byte("correct-horse-battery-staple"))
	if err != nil || len(files) != 1 || files[0].Path != ".env" || string(files[0].Data) != "A=1\n" {
		t.Errorf("%+v, %v", files, err)
	}

	for _, c := range []struct {
		general string
		args    []string
		want    string
	}{
		{"", []string{filepath.Join(root, ".env")}, "which secret"},
		{key, []string{filepath.Join(root, "main.go")}, "isn't an env file"},
		{key, nil, "usage"},
		{"", []string{"--key", "lazysecrets://azure/kv-app-prod.vault.azure.net", filepath.Join(root, ".env")}, "isn't a secret"},
	} {
		if err := shareFiles(ctx, providers, st, c.general, demoRules, c.args, &out); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}
