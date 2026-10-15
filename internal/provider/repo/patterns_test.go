package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// Each list comes from the first that sets it: the repo's spring, the
// repo's, the config's spring, the config's, the defaults.
func TestSecretPatternsOrder(t *testing.T) {
	list := func(s ...string) *[]string { return &s }
	root := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(root, ".lazysecrets.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := New(Settings{
		SecretPatterns: SecretPatterns{Keys: list("^config$"), Values: list("^config$")},
		Types:          map[string]FileType{"spring": {SecretPatterns: SecretPatterns{Keys: list("^config-spring$")}}},
	})
	secret := func(key, value string) bool {
		t.Helper()
		f, err := p.filterFor(root, "spring")
		if err != nil {
			t.Fatal(err)
		}
		return f.secret(key, value)
	}

	if !secret("config-spring", "") || secret("config", "") || !secret("", "config") {
		t.Error("the config's spring keys, its shared values")
	}
	write("secretPatterns:\n  keys: [\"^repo$\"]\n")
	if !secret("repo", "") || secret("config-spring", "") || !secret("", "config") {
		t.Error("the repo's shared keys over the config's")
	}
	write("secretPatterns:\n  keys: [\"^repo$\"]\nspring:\n  secretPatterns:\n    keys: [\"^repo-spring$\"]\n    values: []\n")
	if !secret("repo-spring", "") || secret("repo", "") || secret("", "config") {
		t.Error("the repo's spring over everything; an empty list matches nothing")
	}

	p = New(Settings{})
	write("")
	if !secret("spring.datasource.password", "") || secret("server.port", "") {
		t.Error("the defaults")
	}
}
