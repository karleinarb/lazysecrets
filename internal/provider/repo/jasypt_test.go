package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// hunter2, encrypted by Jasypt with the password "test-password".
const jasyptHunter2 = "ENC(5IQUKT4B7j38TnZJ+BlWqo/zSkKHTjl4XRJyaQEHgkCy4r00avLt4dWqFB74X0Zw)"

func TestJasyptPassword(t *testing.T) {
	dir := t.TempDir()
	value := func(name, body string) (string, error) {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		key := "app.token"
		if !IsSpring(name) {
			key = "TOKEN"
		}
		v, err := (&Provider{}).Value(context.Background(), provider.Object{ID: file + "\x00" + key, Name: key})
		return v.Reveal(), err
	}
	t.Setenv("JASYPT_ENCRYPTOR_PASSWORD", "")
	t.Setenv("MY_PASSWORD", "")
	spring := func(password string) string {
		return "jasypt.encryptor.password: " + password + "\napp.token: " + jasyptHunter2 + "\n"
	}

	for _, c := range []struct{ name, file, body, env, want string }{
		{"the file's own", "application.yml", spring("test-password"), "", "hunter2"},
		{"a ${VAR}", "application.yml", spring("${MY_PASSWORD}"), "test-password", "hunter2"},
		{"a ${VAR}'s default", "application.yml", spring("${MY_PASSWORD:test-password}"), "", "hunter2"},
		{"properties", "application-local.properties", "jasypt.encryptor.password=test-password\napp.token=" + jasyptHunter2 + "\n", "", "hunter2"},
		{"env file", ".env", "JASYPT_ENCRYPTOR_PASSWORD=test-password\nTOKEN=" + jasyptHunter2 + "\n", "", "hunter2"},
		{"not jasypt", ".env", "TOKEN=hunter2\n", "", "hunter2"},
	} {
		t.Setenv("MY_PASSWORD", c.env)
		if got, err := value(c.file, c.body); err != nil || got != c.want {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}

	// $JASYPT_ENCRYPTOR_PASSWORD wins over the file's, as in Spring.
	t.Setenv("JASYPT_ENCRYPTOR_PASSWORD", "test-password")
	if got, err := value("application.yml", spring("wrong")); err != nil || got != "hunter2" {
		t.Errorf("environment first: %q %v", got, err)
	}
	t.Setenv("JASYPT_ENCRYPTOR_PASSWORD", "wrong")
	if _, err := value("application.yml", spring("test-password")); err == nil || !strings.Contains(err.Error(), "the password from $JASYPT_ENCRYPTOR_PASSWORD") {
		t.Errorf("a wrong one says where it's from: %v", err)
	}

	t.Setenv("JASYPT_ENCRYPTOR_PASSWORD", "")
	t.Setenv("MY_PASSWORD", "")
	for body, want := range map[string]string{
		spring("${MY_PASSWORD}"):      "$MY_PASSWORD isn't set",
		"app.token: " + jasyptHunter2: "set $JASYPT_ENCRYPTOR_PASSWORD, or jasypt.encryptor.password in application.yml",
	} {
		if _, err := value("application.yml", body); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("no password: %v, want %q", err, want)
		}
	}
}

func TestJasyptEnvFile(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"jasypt.env": "TOKEN=" + jasyptHunter2 + "\n",
		"mixed.env":  "TOKEN=" + jasyptHunter2 + "\nURL=https://example.com\n",
	})
	ctx := context.Background()
	p := New(Settings{Dir: root})
	srcs, _ := p.Sources(ctx)
	cs, _ := p.Containers(ctx, srcs[0])
	for _, c := range cs {
		attrs, _ := p.Details(ctx, c)
		got := map[string]provider.Attr{}
		for _, a := range attrs {
			got[a.Label] = a
		}
		switch c.Name {
		case "jasypt.env":
			if got["encryption"].Value != "jasypt: 1 of 1 values" || got["git"].Warn {
				t.Errorf("all jasypt: %+v", got)
			}
			keys, _ := p.Objects(ctx, c, provider.TypeSecret)
			if len(keys) != 1 || keys[0].Attrs[1].Value != "jasypt" || keys[0].Detail != "jasypt" {
				t.Errorf("keys: %+v", keys)
			}
		case "mixed.env":
			if got["encryption"].Value != "jasypt: 1 of 2 values" || !got["git"].Warn {
				t.Errorf("mixed: %+v", got)
			}
		}
	}
}
