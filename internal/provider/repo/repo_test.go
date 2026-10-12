package repo

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// A sops-shaped YAML file: listing reads its keys and metadata only.
const sopsYAML = `db:
    password: ENC[AES256_GCM,data:x,iv:x,tag:x,type:str]
    port: ENC[AES256_GCM,data:x,iv:x,tag:x,type:int]
list:
    - ENC[AES256_GCM,data:x,iv:x,tag:x,type:str]
public_unencrypted: hello
sops:
    age:
        - recipient: age1xxxx
          enc: x
    lastmodified: "2026-10-09T10:05:29Z"
    mac: ENC[AES256_GCM,data:x,iv:x,tag:x,type:str]
    version: 3.13.3
`

const sopsEnv = "API_KEY=ENC[AES256_GCM,data:x,iv:x,tag:x,type:str]\n" +
	"sops_age__list_0__map_recipient=age1xxxx\nsops_mac=ENC[x]\nsops_lastmodified=2026-10-09T10:05:29Z\nsops_version=3.13.3\n"

// gitRepo makes a git repository with files, all committed, and returns
// its root.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root) // macOS: /var is /private/var
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "files"},
		{"remote", "add", "origin", "git@git.example.com:team/infra.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return root
}

func names[T any](items []T, name func(T) string) []string {
	var out []string
	for _, it := range items {
		out = append(out, name(it))
	}
	return out
}

func TestRepoFilesAndKeys(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"secrets/app.yaml":       sopsYAML,
		"svc/billing/prod.env":   sopsEnv,
		"svc/billing/plain.yaml": "not: sops\n",
		"README.md":              "# hi\n",
	})
	p := New(Settings{Dir: filepath.Join(root, "svc", "billing")})
	ctx := context.Background()

	srcs, err := p.Sources(ctx)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("sources %v, %v", srcs, err)
	}
	src := srcs[0]
	if src.Name != filepath.Base(root) || src.Detail != "git.example.com/team/infra" || src.URL != "https://git.example.com/team/infra" {
		t.Errorf("source %+v", src)
	}

	files, err := p.Containers(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	// Only sops files; those under where it started first.
	if got := names(files, func(c provider.Container) string { return c.Name }); !slices.Equal(got, []string{"svc/billing/prod.env", "secrets/app.yaml"}) {
		t.Errorf("files %v", got)
	}
	if files[1].Detail != "sops · age" {
		t.Errorf("detail %q", files[1].Detail)
	}

	keys, err := p.Objects(ctx, files[1], provider.TypeSecret)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(keys, func(o provider.Object) string { return o.Name }); !slices.Equal(got, []string{"db.password", "db.port", "list[0]", "public_unencrypted"}) {
		t.Errorf("keys %v", got)
	}
	if keys[0].Updated.IsZero() {
		t.Error("lastmodified not read")
	}
	if _, extract, _ := strings.Cut(keys[2].ID, "\x00"); extract != `["list"][0]` {
		t.Errorf("extract %q", extract)
	}

	env, _ := p.Objects(ctx, files[0], provider.TypeSecret)
	if got := names(env, func(o provider.Object) string { return o.Name }); !slices.Equal(got, []string{"API_KEY"}) {
		t.Errorf("env keys %v", got)
	}

	if id, ok, _ := p.Locate(ctx, "git.example.com/team/infra"); !ok || id != root {
		t.Errorf("locate: %q %v", id, ok)
	}
}

func TestWhichFiles(t *testing.T) {
	files := map[string]string{
		"secrets/a.yaml":        sopsYAML,
		"other/b.yaml":          sopsYAML,
		"other/testdata/c.yaml": sopsYAML,
	}
	list := func(root string, s Settings) []string {
		s.Dir = root
		p := New(s)
		srcs, _ := p.Sources(context.Background())
		cs, err := p.Containers(context.Background(), srcs[0])
		if err != nil {
			t.Fatal(err)
		}
		got := names(cs, func(c provider.Container) string { return c.Name })
		slices.Sort(got)
		return got
	}

	// No rules: every tracked sops file.
	root := gitRepo(t, files)
	if got := list(root, Settings{}); len(got) != 3 {
		t.Errorf("whole repo: %v", got)
	}
	// The config's globs.
	if got := list(root, Settings{Include: []string{"**/*.yaml"}, Exclude: []string{"**/testdata/**"}}); !slices.Equal(got, []string{"other/b.yaml", "secrets/a.yaml"}) {
		t.Errorf("config globs: %v", got)
	}

	// .sops.yaml says where sops files go.
	withRules := map[string]string{".sops.yaml": "creation_rules:\n  - path_regex: secrets/.*\\.yaml$\n    age: age1x\n"}
	for k, v := range files {
		withRules[k] = v
	}
	root = gitRepo(t, withRules)
	if got := list(root, Settings{}); !slices.Equal(got, []string{"secrets/a.yaml"}) {
		t.Errorf(".sops.yaml rules: %v", got)
	}

	// The repo's own .lazysecrets.yaml wins over both.
	withRules[".lazysecrets.yaml"] = "include: [\"other/**\"]\nexclude: [\"**/testdata/**\"]\n"
	root = gitRepo(t, withRules)
	if got := list(root, Settings{Include: []string{"secrets/**"}}); !slices.Equal(got, []string{"other/b.yaml"}) {
		t.Errorf(".lazysecrets.yaml: %v", got)
	}
}

func TestNormalizeRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://git.kebcloud.no/keb/nix-config.git": "git.kebcloud.no/keb/nix-config",
		"git@github.com:Team/Repo.git":               "github.com/team/repo",
		"ssh://git@git.example.com:2222/team/infra":  "git.example.com/team/infra",
		"https://user@dev.azure.com/org/proj/_git/x": "dev.azure.com/org/proj/_git/x",
	} {
		if got, _ := normalizeRemote(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

// With sops and age installed: real encryption and decryption, with a
// throwaway key.
func TestDecrypt(t *testing.T) {
	for _, bin := range []string{"sops", "age-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("needs " + bin)
		}
	}
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.txt")
	if out, err := exec.Command("age-keygen", "-o", keyFile).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v %s", err, out)
	}
	b, _ := os.ReadFile(keyFile)
	var recipient string
	for _, f := range strings.Fields(string(b)) {
		if strings.HasPrefix(f, "age1") {
			recipient = f
		}
	}
	plain := filepath.Join(dir, "plain.yaml")
	os.WriteFile(plain, []byte("db:\n  password: hunter2\nlist:\n  - first\n"), 0o600)
	enc, err := exec.Command("sops", "encrypt", "--age", recipient, plain).Output()
	if err != nil {
		t.Fatal(err)
	}
	root := gitRepo(t, map[string]string{"secrets.yaml": string(enc)})
	t.Setenv("SOPS_AGE_KEY_FILE", keyFile)

	p := New(Settings{Dir: root})
	ctx := context.Background()
	srcs, _ := p.Sources(ctx)
	cs, _ := p.Containers(ctx, srcs[0])
	keys, _ := p.Objects(ctx, cs[0], provider.TypeSecret)
	for _, k := range keys {
		v, err := p.Value(ctx, k)
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		want := map[string]string{"db.password": "hunter2", "list[0]": "first"}[k.Name]
		if v.Reveal() != want {
			t.Errorf("%s = %q, want %q", k.Name, v.Reveal(), want)
		}
	}

	// From a link, without the listing: the extract path from the name.
	v, err := p.Value(ctx, provider.Object{Container: cs[0], Name: "list[0]"})
	if err != nil || v.Reveal() != "first" {
		t.Errorf("from a link: %q %v", v.Reveal(), err)
	}

	// Without the key: sops' own error.
	t.Setenv("SOPS_AGE_KEY_FILE", filepath.Join(dir, "missing.txt"))
	if _, err := p.Value(ctx, keys[0]); err == nil || !strings.Contains(err.Error(), "no identity matched any of the recipients") {
		t.Errorf("no key: %v", err)
	}
}

func TestRepoWithoutSecretsIsntListed(t *testing.T) {
	root := gitRepo(t, map[string]string{"README.md": "# no secrets\n", "config.yaml": "plain: yaml\n"})
	srcs, err := New(Settings{Dir: root}).Sources(context.Background())
	if err != nil || len(srcs) != 0 {
		t.Errorf("sources %v, %v", srcs, err)
	}
}

func TestPlainEnvFiles(t *testing.T) {
	root := gitRepo(t, map[string]string{
		".gitignore":       ".env\n.env.local\n",
		".env.example":     "TOKEN=changeme\n",
		"secrets/app.yaml": sopsYAML,
	})
	// Ignored, as they usually are: written after the commit.
	os.WriteFile(filepath.Join(root, ".env"), []byte("# local\nexport TOKEN=\"s3cr3t value\"\nURL=https://x.example # the API\nQUOTED='a#b'\n"), 0o600)
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "pkg", ".env"), []byte("X=1\n"), 0o600)

	p := New(Settings{Dir: root})
	ctx := context.Background()
	srcs, _ := p.Sources(ctx)
	files, err := p.Containers(ctx, srcs[0])
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range files {
		got[c.Name] = c.Detail
	}
	if len(got) != 2 || got[".env"] != "env · plain text" || got["secrets/app.yaml"] != "sops · age" {
		t.Fatalf("files %v (want .env and the sops file; not the example or node_modules)", got)
	}

	var env provider.Container
	for _, c := range files {
		if c.Name == ".env" {
			env = c
		}
	}
	keys, _ := p.Objects(ctx, env, provider.TypeSecret)
	want := map[string]string{"TOKEN": "s3cr3t value", "URL": "https://x.example", "QUOTED": "a#b"}
	if len(keys) != len(want) {
		t.Fatalf("keys %v", names(keys, func(o provider.Object) string { return o.Name }))
	}
	for _, k := range keys {
		v, err := p.Value(ctx, k) // no sops involved
		if err != nil || v.Reveal() != want[k.Name] {
			t.Errorf("%s = %q, %v; want %q", k.Name, v.Reveal(), err, want[k.Name])
		}
	}

	// .sops.yaml rules limit SOPS files, not plain env files.
	os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte("creation_rules:\n  - path_regex: nowhere/.*\n"), 0o644)
	exec.Command("git", "-C", root, "add", ".sops.yaml").Run()
	files, _ = New(Settings{Dir: root}).Containers(ctx, srcs[0])
	if names := names(files, func(c provider.Container) string { return c.Name }); !slices.Equal(names, []string{".env"}) {
		t.Errorf("with rules elsewhere: %v", names)
	}
}

func TestRoots(t *testing.T) {
	withSecrets := gitRepo(t, map[string]string{"secrets/a.yaml": sopsYAML})
	without := gitRepo(t, map[string]string{"README.md": "# none\n"})
	nested := gitRepo(t, map[string]string{".env.example": "X=y\n", "prod.env": sopsEnv})

	// A folder of repos: two at the top, one under an org folder.
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.Rename(withSecrets, filepath.Join(root, "infra"))
	os.Rename(without, filepath.Join(root, "docs"))
	os.MkdirAll(filepath.Join(root, "acme"), 0o755)
	os.Rename(nested, filepath.Join(root, "acme", "billing"))

	outside := t.TempDir() // not in a repo
	srcs, err := New(Settings{Dir: outside, Roots: []string{root}}).Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := names(srcs, func(s provider.Source) string { return s.Name })
	slices.Sort(got)
	if !slices.Equal(got, []string{"acme/billing", "infra"}) {
		t.Errorf("repos under the root: %v (want the two with secret files)", got)
	}

	// Inside a repo: the same repos, that one first and marked Here.
	srcs, _ = New(Settings{Dir: filepath.Join(root, "acme", "billing"), Roots: []string{root}}).Sources(context.Background())
	got = names(srcs, func(s provider.Source) string { return s.Name })
	if !slices.Equal(got, []string{"billing", "infra"}) || !srcs[0].Here || srcs[1].Here {
		t.Errorf("inside a repo: %v, want billing (here) then infra", got)
	}
}

func TestStarredReposAlongside(t *testing.T) {
	here := gitRepo(t, map[string]string{"secrets/a.yaml": sopsYAML})
	starred := gitRepo(t, map[string]string{"prod.env": sopsEnv})
	other := gitRepo(t, map[string]string{"secrets/b.yaml": sopsYAML})
	_ = other

	srcs, err := New(Settings{Dir: here, Starred: []string{starred, here}}).Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := names(srcs, func(s provider.Source) string { return s.ID })
	if !slices.Equal(got, []string{here, starred}) {
		t.Errorf("sources %v, want this repo then the starred one (not under a root)", got)
	}
}

// sops' output when no key opens the file, as sops 3.13 prints it.
const noKeyStderr = `Failed to get the data key required to decrypt the SOPS file.

Group 0: FAILED
  age1el8zh94re2488t5rrpqxk5xcxhs2cg6xfcccntz6l4ggv0rsf9yq44c0cs: FAILED
    - | failed to create reader for decrypting sops data key with
      | age: no identity matched any of the recipients. Did not find
      | keys in locations 'SOPS_AGE_SSH_PRIVATE_KEY_FILE'.
  https://kv-app.vault.azure.net/keys/sops/abc: FAILED
    - | failed to decrypt sops data key with Azure Key Vault key
      | 'https://kv-app.vault.azure.net/keys/sops/abc': DefaultAzureCredential: failed to acquire a token.

Recovery failed because no master key was able to decrypt the file. In
order for SOPS to recover the file, at least one key has to be successful,
but none were.
`

func TestSopsError(t *testing.T) {
	got := sopsError(noKeyStderr, errors.New("exit status 128"))
	if got != "sops couldn't decrypt it:\n"+strings.TrimSpace(noKeyStderr) {
		t.Errorf("not sops' whole message:\n%s", got)
	}
	if got := sopsError("", errors.New("exit status 1")); got != "sops couldn't decrypt it:\nexit status 1" {
		t.Errorf("without output: %q", got)
	}
}

func TestOriginURL(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "config")
		os.WriteFile(p, []byte(s), 0o644)
		return p
	}
	for _, c := range []struct {
		config, url string
		ok          bool
	}{
		{"[core]\n\tbare = false\n[remote \"origin\"]\n\turl = git@git.example.com:team/infra.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n", "git@git.example.com:team/infra.git", true},
		{"[remote \"upstream\"]\n\turl = https://a/b\n[remote \"origin\"]\n\turl=https://git.example.com/x.git\n", "https://git.example.com/x.git", true},
		{"[core]\n\tbare = false\n", "", true}, // no origin
		{"[url \"git@host:\"]\n\tinsteadOf = https://host/\n[remote \"origin\"]\n\turl = https://host/x\n", "", false},
	} {
		url, ok := originURL(write(c.config))
		if url != c.url || ok != c.ok {
			t.Errorf("%q: %q %v, want %q %v", c.config, url, ok, c.url, c.ok)
		}
	}
	if _, ok := originURL(filepath.Join(dir, "missing")); ok {
		t.Error("a missing config should leave it to git")
	}
}

func TestEnvFilesSkipPackages(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"app/main.go":                    "package main\n",
		"vendor/lib/x.go":                "package lib\n",
		"web/node_modules/pkg/index.js":  "x\n",
		"secrets/sops.yaml":              sopsYAML,
		"web/node_modules/pkg/sops.yaml": sopsYAML,
	})
	// Untracked, as .env files usually are: next to tracked code, in a
	// package folder, and in a folder with nothing tracked.
	for f, body := range map[string]string{
		"app/.env":                  "A=1\n",
		"web/node_modules/pkg/.env": "B=2\n",
		"vendor/lib/.env":           "C=3\n",
		"scratch/.env":              "D=4\n",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755)
		os.WriteFile(filepath.Join(root, f), []byte(body), 0o644)
	}
	p := New(Settings{Dir: root})
	srcs, _ := p.Sources(context.Background())
	files, err := p.Containers(context.Background(), srcs[0])
	if err != nil {
		t.Fatal(err)
	}
	got := names(files, func(c provider.Container) string { return c.Name })
	slices.Sort(got)
	if want := []string{"app/.env", "secrets/sops.yaml"}; !slices.Equal(got, want) {
		t.Errorf("files %v, want %v (nothing from node_modules or vendor)", got, want)
	}
}

func TestFile(t *testing.T) {
	root := gitRepo(t, map[string]string{
		".gitignore":       ".env\n",
		"secrets/app.yaml": sopsYAML,
	})
	os.WriteFile(filepath.Join(root, ".env"), []byte("# local\nTOKEN=x\n\nURL=y\n"), 0o600)
	p := New(Settings{Dir: root, Sops: "/opt/sops"})
	ctx := context.Background()
	srcs, _ := p.Sources(ctx)
	files, err := p.Containers(ctx, srcs[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]int{}
	for _, c := range files {
		f, err := p.File(c, nil)
		if err != nil || f.Path != filepath.Join(root, c.Name) || f.Line != 0 {
			t.Errorf("%s: %+v, %v", c.Name, f, err)
		}
		if sops := f.Sops != ""; sops != (c.Name == "secrets/app.yaml") || (sops && f.Sops != "/opt/sops") {
			t.Errorf("%s: sops %q", c.Name, f.Sops)
		}
		keys, _ := p.Objects(ctx, c, provider.TypeSecret)
		for _, k := range keys {
			f, err := p.File(c, &k)
			if err != nil {
				t.Fatal(err)
			}
			lines[c.Name+" "+k.Name] = f.Line
		}
	}
	want := map[string]int{
		".env TOKEN": 2, ".env URL": 4,
		"secrets/app.yaml db.password": 2, "secrets/app.yaml db.port": 3,
		"secrets/app.yaml list[0]": 5, "secrets/app.yaml public_unencrypted": 6,
	}
	if !maps.Equal(lines, want) {
		t.Errorf("lines %v\nwant  %v", lines, want)
	}
}

func TestFileDetails(t *testing.T) {
	root := gitRepo(t, map[string]string{
		".gitignore":       ".env\n",
		"secrets/app.yaml": sopsYAML,
		"prod.env":         sopsEnv,
		"tracked.env":      "TOKEN=x\n", // a plain .env in git: worth a look
	})
	os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=x\n"), 0o600)
	os.WriteFile(filepath.Join(root, "local.env"), []byte("TOKEN=x\n"), 0o600)
	p := New(Settings{Dir: root})
	ctx := context.Background()
	srcs, _ := p.Sources(ctx)
	details := func(name string) map[string]provider.Attr {
		t.Helper()
		attrs, err := p.Details(ctx, provider.Container{Source: srcs[0], ID: filepath.Join(root, name), Name: name})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]provider.Attr{}
		for _, a := range attrs {
			out[a.Label] = a
		}
		return out
	}

	d := details("secrets/app.yaml")
	if d["encryption"].Value != "sops · age" || d["keys"].Value != "age age1xxxx" || d["git"].Value != "tracked" ||
		!strings.HasSuffix(d["last commit"].Value, " ago") || strings.Contains(d["last commit"].Value, ":") || d["encrypted"].Value == "" {
		t.Errorf("sops yaml: %+v", d)
	}
	if d := details("prod.env"); d["keys"].Value != "age age1xxxx" {
		t.Errorf("sops env: %+v", d)
	}
	if d := details("tracked.env"); d["encryption"].Value != "none: plain text" || !d["git"].Warn {
		t.Errorf("tracked plain env: %+v", d)
	}
	if d := details(".env"); d["git"].Value != "ignored" || d["git"].Warn {
		t.Errorf("ignored env: %+v", d)
	}
	if d := details("local.env"); !d["git"].Warn || !strings.Contains(d["git"].Value, "not ignored") {
		t.Errorf("untracked env: %+v", d)
	}
}
