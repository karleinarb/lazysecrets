package app

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/repo"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

const sopsFile = `db:
    password: ENC[AES256_GCM,data:x,iv:x,tag:x,type:str]
sops:
    age:
        - recipient: age1xxxx
    lastmodified: "2026-10-09T10:05:29Z"
    mac: ENC[AES256_GCM,data:x,iv:x,tag:x,type:str]
`

// gitRepoWith makes a git repository holding a SOPS file, with a remote.
func gitRepoWith(t *testing.T, remote string) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "app.yaml"), []byte(sopsFile), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"},
		{"remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	return root
}

func TestCurrentRepoFirst(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	here := gitRepoWith(t, "https://git.example.com/team/here.git")
	other := gitRepoWith(t, "https://git.example.com/team/aaa-other.git")
	mem, _ := store.Load("")
	mem.Update("lazysecrets://repo/git.example.com%2Fteam%2Faaa-other", func(n *store.Node) {
		n.Source, n.StarredBy = other, []string{"Repos"}
	})
	d := newDriverOpts(t, Options{Store: mem}, repo.New(repo.Settings{Dir: here, Starred: []string{other}}))
	// Grouped: the repo it started in on a row of its own on top,
	// selected, then the other repos under Repos, without it.
	var rows []string
	for _, r := range d.m.sources.Items() {
		switch r.kind {
		case rowGroup:
			rows = append(rows, "group "+r.label)
		case rowSource:
			rows = append(rows, r.source.ID)
		}
	}
	if want := []string{here, "group Repos", other}; !slices.Equal(rows, want) {
		t.Errorf("rows %v, want %v", rows, want)
	}
	if !strings.Contains(d.screen(), "Repo · "+filepath.Base(here)) {
		t.Errorf("no Repo · name row:\n%s", d.screen())
	}
	if src, ok := d.m.selectedSource(); !ok || src.ID != here {
		t.Errorf("selected %v, want the repo it started in", src.ID)
	}

	// Flat: it sorts first, labelled.
	d.keys("`")
	if !strings.Contains(d.screen(), filepath.Base(here)+" · here") {
		t.Errorf("no here label in the flat list:\n%s", d.screen())
	}
}

func TestRepoSource(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "app.yaml"), []byte(sopsFile), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"},
		{"remote", "add", "origin", "https://git.example.com/team/infra.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}

	d := newDriverOpts(t, Options{}, []provider.Provider{repo.New(repo.Settings{Dir: root})}...)
	scr := d.screen()
	for _, want := range []string{"Repos", filepath.Base(root), "[2] Files", "secrets/app.yaml  sops · age", "db.password"} {
		if !strings.Contains(scr, want) {
			t.Fatalf("missing %q:\n%s", want, scr)
		}
	}
	d.keys("3", "f")
	key := "lazysecrets://repo/git.example.com%2Fteam%2Finfra/secrets%2Fapp.yaml/db.password"
	if !d.m.store.Node(key).StarredFor("Repos") {
		t.Errorf("star not stored under the repo link; nodes: %v", d.m.store.Nodes())
	}
	d.keys("s")
	if got := d.clip().content; got != key {
		t.Errorf("shared link %q", got)
	}

	// Opening the link finds the repo by its remote and goes there.
	d.keys("1")
	r, _ := ref.Parse(key)
	d.send(OpenLink(r))
	d.keys("o")
	if src, c, s := d.selected(); src != filepath.Base(root) || c != "secrets/app.yaml" || s != "db.password" {
		t.Errorf("opened %s/%s/%s\n%s", src, c, s, d.screen())
	}
}

func TestReposLoadThemselves(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := gitRepoWith(t, "https://git.example.com/team/x.git")
	// Started elsewhere, so the repo isn't the selected source.
	d := newDriverOpts(t, Options{}, fake.Azure(0), repo.New(repo.Settings{Dir: t.TempDir(), Paths: []string{root}}))
	d.openSearch()
	d.searchAll()
	d.typeText("db.password")
	if !strings.Contains(d.screen(), "secrets/app.yaml / db.password") {
		t.Errorf("repo keys not searchable without loading them:\n%s", d.screen())
	}
}

func TestGroupsStartCollapsed(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	here := gitRepoWith(t, "https://git.example.com/team/here.git")
	d := newDriverOpts(t, Options{CollapseGroups: true},
		fake.Azure(0), fake.Databricks(0), repo.New(repo.Settings{Dir: here}))
	var rows []string
	for _, r := range d.m.sources.Items() {
		switch r.kind {
		case rowGroup:
			rows = append(rows, "group "+r.label)
		case rowSource:
			rows = append(rows, r.source.Name)
		}
	}
	// Folded groups, and the repo it started in on top, selected, so its
	// files show.
	if want := []string{filepath.Base(here), "group Azure", "group Databricks"}; !slices.Equal(rows, want) {
		t.Errorf("rows %v, want %v", rows, want)
	}
	if src, ok := d.m.selectedSource(); !ok || src.ID != here {
		t.Errorf("selected %v, want the repo it started in", src.ID)
	}
	d.keys("j", "enter") // unfold Azure
	if d.sourceCount() < 2 {
		t.Errorf("enter on a folded group didn't unfold it:\n%s", d.screen())
	}
}

func TestCurrentRepoBeforeTheRest(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	here := gitRepoWith(t, "https://git.example.com/team/here.git")
	p := repo.New(repo.Settings{Dir: here})
	d := newDriverOpts(t, Options{}, fake.Azure(0), p)
	// Sources asked again, nothing answered yet but the started-in repo
	// on its own (provider.Current): it shows on top already.
	d.m.loadSources()
	src, ok := p.Current(context.Background())
	if !ok {
		t.Fatal("no current repo")
	}
	src.Provider, src.ProviderLabel = p.ID(), p.Label()
	d.send(currentMsg{gen: d.m.sourcesGen, source: src})
	if !strings.Contains(d.screen(), "Repo · "+filepath.Base(here)) {
		t.Errorf("the started-in repo waits for the others:\n%s", d.screen())
	}
}

func TestSearchAllRepos(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	a := gitRepoWith(t, "https://git.example.com/team/alpha.git")
	b := gitRepoWith(t, "https://git.example.com/team/beta.git")
	d := newDriverOpts(t, Options{}, fake.Azure(0), repo.New(repo.Settings{Paths: []string{a, b}}))
	d.openSearch()
	d.send(ctrl('h'))
	for d.m.search.src > 0 {
		d.send(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for d.m.search.scope().id != accountScope+"Repos" {
		if d.m.search.src == len(d.m.search.scopes)-1 {
			t.Fatalf("no Repos scope: %v", d.m.search.scopes)
		}
		d.send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	azure := func() (n int) {
		for row := range d.m.cache.containers {
			if p, _ := splitSourceRow(row); p != "Repos" {
				n++
			}
		}
		return n
	}
	azureBefore := azure()
	d.typeText("password")
	var got []string
	for _, o := range d.m.search.shown {
		if o.kind != provider.KindRepo {
			t.Errorf("not a repo: %s", ansiStrip(o.label))
		}
		got = append(got, ansiStrip(o.label))
	}
	if len(got) != 2 {
		t.Errorf("want db.password in both repos: %v", got)
	}
	if azure() != azureBefore || d.m.crawl.running {
		t.Errorf("searching all repos loaded Azure: %d → %d lists", azureBefore, azure())
	}
	if !strings.Contains(d.screen(), "Results in Repos") {
		t.Errorf("title:\n%s", d.screen())
	}
}

func TestEditFile(t *testing.T) {
	// Not parallel: git, run by the repo provider, is slow under load.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := gitRepoWith(t, "https://git.example.com/team/x.git")
	envPath := filepath.Join(root, "secrets", ".env")
	os.WriteFile(envPath, []byte("# local\nTOKEN=x\n"), 0o600)
	var opened []string
	d := newDriverOpts(t, Options{OpenURL: func(u string) error { opened = append(opened, u); return nil }},
		fake.Azure(0), repo.New(repo.Settings{Dir: root}))
	env := map[string]string{"NVIM": "/tmp/nvim.sock"}
	d.m.getenv = func(k string) string { return env[k] }
	var nvim []string
	d.m.nvimOpen = func(server, path string, line int) error {
		nvim = append(nvim, fmt.Sprintf("%s %s:%d", server, path, line))
		return nil
	}

	// Inside nvim: the file, then a secret at its line, and lazysecrets quits.
	d.keys("2")
	if _, c, _ := d.selected(); c != "secrets/.env" {
		t.Fatalf("selected %q\n%s", c, d.screen())
	}
	d.keys("e")
	d.keys("3", "e")
	want := []string{"/tmp/nvim.sock " + envPath + ":0", "/tmp/nvim.sock " + envPath + ":2"}
	if !slices.Equal(nvim, want) {
		t.Errorf("nvim got %q\nwant %q", nvim, want)
	}
	if msg := d.m.edit()(); msg != (tea.QuitMsg{}) {
		t.Errorf("after opening in nvim: %#v, want quit", msg)
	}

	// SOPS files go to sops edit, in the terminal, even inside nvim.
	nvim = nil
	d.keys("2", "j")
	if _, c, _ := d.selected(); c != "secrets/app.yaml" {
		t.Fatalf("selected %q", c)
	}
	if cmd := d.m.edit(); cmd == nil || len(nvim) > 0 {
		t.Errorf("sops file: cmd %v, nvim %q", cmd, nvim)
	}

	// Neither nvim nor an editor: the system's default app.
	delete(env, "NVIM")
	d.keys("k", "e")
	if !slices.Equal(opened, []string{envPath}) || !strings.Contains(d.screen(), "opened .env") {
		t.Errorf("opened %q\n%s", opened, d.screen())
	}

	// Done editing: the file's secrets reload.
	d.send(editDoneMsg{c: provider.Container{Source: provider.Source{Provider: "Repos"}, ID: envPath}, name: ".env"})
	if !strings.Contains(d.screen(), "closed .env") {
		t.Errorf("after editing:\n%s", d.screen())
	}

	// Other providers have no files.
	az := newDriver(t, fake.Azure(0))
	az.keys("2", "e")
	if !strings.Contains(az.screen(), "only SOPS and .env files") {
		t.Errorf("azure:\n%s", az.screen())
	}
}

func TestEditorArgs(t *testing.T) {
	t.Parallel()
	f := provider.File{Path: "/r/.env", Line: 7}
	for editor, want := range map[string][]string{
		"nvim":             {"+7", "/r/.env"},
		"/usr/bin/vim -p":  {"+7", "/r/.env"},
		"hx":               {"/r/.env:7"},
		"code --wait":      {"-g", "/r/.env:7"},
		"open -a TextEdit": {"/r/.env"},
	} {
		if got := editorArgs(editor, f); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", editor, got, want)
		}
	}
	if got := editorArgs("nvim", provider.File{Path: "/r/.env"}); !slices.Equal(got, []string{"/r/.env"}) {
		t.Errorf("no line: %q", got)
	}
}

func TestEditorAtLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		editor string
		line   int
		want   string
		ok     bool
	}{
		{"nvim", 4, "nvim +4", true},
		{"/usr/bin/vim -p", 4, "/usr/bin/vim -p +4", true},
		{"code --wait", 4, "", false}, // sops adds the file last: no -g file:N
		{"nvim", 0, "", false},
	} {
		if got, ok := editorAtLine(c.editor, c.line); got != c.want || ok != c.ok {
			t.Errorf("%q at %d: %q %v", c.editor, c.line, got, ok)
		}
	}
}

func TestEditConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lazysecrets", "config.yaml")
	var opened []string
	d := newDriverOpts(t, Options{ConfigFiles: []string{path}, OpenURL: func(u string) error { opened = append(opened, u); return nil }})
	d.m.getenv = func(string) string { return "" }

	// e on the status pane. No editor: the system's default app, on a
	// config made for it.
	d.keys("0", "e")
	if !slices.Equal(opened, []string{path}) {
		t.Errorf("opened %q", opened)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(b), "# lazysecrets config") {
		t.Errorf("config made: %q, %v", b, err)
	}

	// Inside nvim: in that nvim; an existing config is left as it is.
	os.WriteFile(path, []byte("icons: false\n"), 0o644)
	d.m.getenv = func(k string) string { return map[string]string{"NVIM": "/tmp/nvim.sock"}[k] }
	var nvim []string
	d.m.nvimOpen = func(server, path string, line int) error {
		nvim = append(nvim, server+" "+path)
		return nil
	}
	if msg := d.m.editConfig()(); msg != (tea.QuitMsg{}) {
		t.Errorf("didn't quit for nvim: %v", msg)
	}
	if want := []string{"/tmp/nvim.sock " + path}; !slices.Equal(nvim, want) {
		t.Errorf("nvim got %q", nvim)
	}
	if b, _ := os.ReadFile(path); string(b) != "icons: false\n" {
		t.Errorf("config changed: %q", b)
	}

	d.send(editDoneMsg{name: "config.yaml", config: true})
	if !strings.Contains(d.m.flash.text, "restart") {
		t.Errorf("flash %q", d.m.flash.text)
	}
}

func TestStatusFooterEditsConfig(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("0")
	if !strings.Contains(d.screen(), "e edit config") {
		t.Errorf("no edit config in the footer:\n%s", d.screen())
	}
}
