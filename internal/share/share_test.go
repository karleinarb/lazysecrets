package share

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
)

func init() { workFactor = 10 } // fast; opening accepts any up to maxWorkFactor

var (
	key  = ref.Ref{Kind: provider.KindAzure, Host: "kv-team.vault.azure.net", Type: provider.TypeSecret, Name: "env-share", Version: "91d3e0"}
	pass = []byte("0123456789abcdef0123456789abcdef")
)

func TestSealOpen(t *testing.T) {
	files := []File{
		{Path: ".env", Data: []byte("DB_PASSWORD=hunter2\n")},
		{Path: "apps/api/.env.local", Data: []byte("TOKEN=\"a b\" # quoted\n")},
	}
	link, err := Seal(Header{Key: key, Repo: "git.example.com/team/app"}, pass, files, Rules{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(link.String(), "hunter2") || strings.Contains(link.String(), "apps") {
		t.Errorf("link shows what it shares: %s", link)
	}
	// It goes through ref.Parse like any link.
	r, err := ref.Parse(link.String())
	if err != nil || !r.IsShare() {
		t.Fatalf("parse: %v, %+v", err, r)
	}
	s, err := Read(r)
	if err != nil {
		t.Fatal(err)
	}
	if s.Key != key || s.Repo != "git.example.com/team/app" || s.Files != 2 {
		t.Errorf("header %+v", s.Header)
	}
	got, err := s.Open(pass)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Path != files[1].Path || string(got[1].Data) != string(files[1].Data) {
		t.Errorf("opened %+v", got)
	}
	if _, err := s.Open([]byte("another key, long enough to use")); err == nil || !strings.Contains(err.Error(), "env-share at version 91d3e0 doesn't decrypt it") {
		t.Errorf("wrong key: %v", err)
	}
}

func TestWrongKeySaysWhy(t *testing.T) {
	files := []File{{Path: ".env", Data: []byte("A=1\n")}}
	for link, want := range map[string]string{
		"lazysecrets://databricks/adb-1.1.azuredatabricks.net/team/env-share": "env-share has changed since the link was made",
		"lazysecrets://repo/git.example.com%2Fteam%2Fapp/secrets.yaml/share":  "share has changed since the link was made",
		"lazysecrets://azure/kv-team.vault.azure.net/secrets/env-share":       "env-share's current version isn't the one the link was made with",
	} {
		k, err := ParseKey(link)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Seal(Header{Key: k}, pass, files, Rules{})
		if err != nil {
			t.Fatal(err)
		}
		s, _ := Read(r)
		if _, err := s.Open([]byte("rotated since, and long enough to be a key")); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", link, err, want)
		}
	}
}

func TestSealRefuses(t *testing.T) {
	ok := []File{{Path: ".env", Data: []byte("A=1")}}
	for name, c := range map[string]struct {
		h     Header
		pass  []byte
		files []File
		want  string
	}{
		"short key":    {Header{Key: key}, []byte("short"), ok, "needs 32 (minLength)"},
		"vault as key": {Header{Key: ref.Ref{Kind: provider.KindAzure, Host: "kv.vault.azure.net"}}, pass, ok, "isn't a secret"},
		"no files":     {Header{Key: key}, pass, nil, "no files"},
		"not env":      {Header{Key: key}, pass, []File{{Path: "main.go", Data: []byte("x")}}, "isn't an env file"},
		"escapes":      {Header{Key: key}, pass, []File{{Path: "../.env", Data: []byte("x")}}, "inside a repo"},
		"binary":       {Header{Key: key}, pass, []File{{Path: ".env", Data: []byte{0xff, 0xfe}}}, "isn't text"},
		"too big":      {Header{Key: key}, pass, []File{{Path: ".env", Data: []byte(random(2 * MaxLink))}}, "more than the 16 KB"},
	} {
		if _, err := Seal(c.h, c.pass, c.files, Rules{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// random is text that doesn't compress.
func random(n int) string {
	var b strings.Builder
	x := uint32(1)
	for b.Len() < n {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b.WriteByte("abcdefghijklmnopqrstuvwxyz0123456789"[x%36])
	}
	return b.String()
}

func TestRules(t *testing.T) {
	for _, c := range []struct {
		r    Rules
		key  string
		pass bool
	}{
		{Rules{}, "q2Hd8bT4Lw1sZp9Kf6Vx3Nc7Rm0Ya5Ue", true},                                 // base64-like, 32
		{Rules{}, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", true}, // hex
		{Rules{}, "correct-horse-battery-staple", false},                                    // 28: short
		{Rules{}, strings.Repeat("a", 40), false},                                           // one kind
		{Rules{MinLength: 16, MinClasses: 1}, strings.Repeat("a", 16), true},
		{Rules{MinLength: 4}, "abc123", false}, // never under MinKey
		{Rules{MinClasses: 4}, "q2Hd8bT4Lw1sZp9Kf6Vx3Nc7Rm0Ya5Ue", false},
		{Rules{MinClasses: 4}, "q2Hd8bT4Lw1sZp9Kf6Vx3Nc7Rm0Ya5U+", true},
	} {
		if err := c.r.Check([]byte(c.key)); (err == nil) != c.pass {
			t.Errorf("%+v %q: %v", c.r, c.key, err)
		} else if err != nil && strings.Contains(err.Error(), c.key) {
			t.Errorf("the error shows the key: %v", err)
		}
	}
}

func TestReadGarbled(t *testing.T) {
	link, err := Seal(Header{Key: key}, pass, []File{{Path: ".env", Data: []byte("A=1")}}, Rules{})
	if err != nil {
		t.Fatal(err)
	}
	cut := ref.Ref{Share: link.Share[:len(link.Share)/2]}
	s, err := Read(cut)
	if err == nil {
		_, err = s.Open(pass)
	}
	if err == nil {
		t.Error("a link cut in half opened")
	}
	if _, err := Read(ref.Ref{Share: "AgAAAAAAAAAAAAAAAAAAAA"}); err == nil || !strings.Contains(err.Error(), "newer lazysecrets") {
		t.Errorf("version 2: %v", err)
	}
}

func TestCheckPath(t *testing.T) {
	for _, p := range []string{".env", "apps/api/.env.local", "prod.env"} {
		if err := CheckPath(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"", "/etc/.env", "../.env", "a/../.env", ".git/.env", "a/.git/x.env", `a\.env`, ".env.example", "node_modules/x/.env", "Makefile"} {
		if err := CheckPath(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestPlanWrite(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skip("git:", string(out))
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("OLD=1\n"), 0o644)
	targets, err := Plan(dir, []File{
		{Path: ".env", Data: []byte("NEW=1\n")},
		{Path: "apps/api/.env.local", Data: []byte("B=2\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !targets[0].Exists || targets[0].Exposed {
		t.Errorf(".env: %+v", targets[0])
	}
	if targets[1].Exists || !targets[1].Exposed {
		t.Errorf("apps/api/.env.local: %+v", targets[1])
	}
	for _, tg := range targets {
		if err := tg.Write(); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != "NEW=1\n" {
		t.Errorf(".env is %q", b)
	}
	info, err := os.Stat(filepath.Join(dir, "apps", "api", ".env.local"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("new file: %v, %v", info.Mode(), err)
	}
}

func TestPlanSymlinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "apps")); err != nil {
		t.Skip(err)
	}
	if _, err := Plan(dir, []File{{Path: "apps/.env"}}); err == nil || !strings.Contains(err.Error(), "out of the folder") {
		t.Errorf("through a linked folder: %v", err)
	}
	os.Symlink(filepath.Join(outside, "x"), filepath.Join(dir, ".env"))
	if _, err := Plan(dir, []File{{Path: ".env"}}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("onto a symlink: %v", err)
	}
}

func TestParseKey(t *testing.T) {
	for _, s := range []string{
		"lazysecrets://azure/kv-team.vault.azure.net/secrets/env-share",
		"https://kv-team.vault.azure.net/secrets/env-share",
		"@Microsoft.KeyVault(VaultName=kv-team;SecretName=env-share)",
		"lazysecrets://databricks/adb-1.1.azuredatabricks.net/team/env-share",
	} {
		if k, err := ParseKey(s); err != nil || k.Name != "env-share" {
			t.Errorf("%s: %+v, %v", s, k, err)
		}
	}
	for s, want := range map[string]string{
		"{{secrets/team/env-share}}":                  "names no workspace",
		"https://kv-team.vault.azure.net/keys/sops":   "isn't a secret",
		"https://kv-team.vault.azure.net/":            "isn't a secret",
		"lazysecrets://azure/subscriptions/0000-1111": "isn't a secret",
	} {
		if _, err := ParseKey(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", s, err, want)
		}
	}
}

func TestDefaultKey(t *testing.T) {
	dir := t.TempDir()
	general := "lazysecrets://azure/kv-team.vault.azure.net/secrets/env-share"
	k, ok, err := DefaultKey(dir, general)
	if err != nil || !ok || k.Name != "env-share" {
		t.Errorf("general: %+v %v %v", k, ok, err)
	}
	os.WriteFile(filepath.Join(dir, ".lazysecrets.yaml"), []byte("sharing:\n  key: lazysecrets://azure/kv-app.vault.azure.net/secrets/app-share\n"), 0o644)
	if k, _, _ := DefaultKey(dir, general); k.Name != "app-share" {
		t.Errorf("the repo's should win: %+v", k)
	}
	if _, ok, _ := DefaultKey(t.TempDir(), ""); ok {
		t.Error("a key with none set")
	}
	unpinned := key
	unpinned.Version = ""
	if p := Pin(unpinned, "abc"); p.Version != "abc" {
		t.Errorf("pin: %+v", p)
	}
}
