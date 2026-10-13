package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/repo"
	"git.kebcloud.no/keb/lazysecrets/internal/share"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// scryptWait is how long the driver waits for encrypting and decrypting:
// tens of milliseconds, more than its usual wait, and more under load.
const scryptWait = 20 * time.Second

// demoRules take the demo key, made up to be remembered.
var demoRules = share.Rules{MinLength: 16, MinClasses: 1}

// cloneWith makes a clone of git.example.com/team/app holding files.
func cloneWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := gitRepoWith(t, "https://git.example.com/team/app.git")
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0o644)
	for name, data := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		os.WriteFile(filepath.Join(root, name), []byte(data), 0o600)
	}
	return root
}

// shareFrom shares a repo's env files from lazysecrets started in it,
// returning the link copied.
func shareFrom(t *testing.T, root string, paste bool) string {
	t.Helper()
	clip := &fakeClipboard{}
	d := newDriverOpts(t, Options{Clipboard: clip, ShareRules: demoRules}, fake.Azure(0), repo.New(repo.Settings{Dir: root}))
	d.wait = scryptWait
	if src, ok := d.m.selectedSource(); !ok || src.ID != root {
		t.Fatalf("selected %v, want the repo", src.ID)
	}
	d.keys("S", "f")
	// No sharing.key set: it asks which secret, in Go to, which lists
	// only secrets.
	v := d.m.search
	if v == nil || v.picker == nil {
		t.Fatalf("no secret picker: %s", d.m.flash.text)
	}
	for _, o := range v.shown {
		if o.plain || strings.Contains(o.label, ".env") {
			t.Errorf("a plain env value offered as the key: %s", o.label)
		}
	}
	if !strings.Contains(d.screen(), "as an encrypted link") {
		t.Errorf("doesn't say what picking does:\n%s", d.screen())
	}
	if paste {
		// A reference pasted is picked as it is.
		d.send(tea.PasteMsg{Content: "https://" + kv + "/secrets/db-password"})
	} else {
		for _, r := range "app-prod db-password" {
			d.keys(string(r))
		}
		if len(v.shown) == 0 {
			t.Fatalf("no secrets to pick:\n%s", d.screen())
		}
		for _, o := range v.shown {
			if _, ok := pickable(o); !ok {
				t.Errorf("not a secret: %s", o.label)
			}
		}
		if !strings.Contains(d.screen(), "Share 2 env files") && !strings.Contains(d.screen(), "Share .env") {
			t.Errorf("not titled as a picker:\n%s", d.screen())
		}
		d.keys("enter")
	}
	// Picking it shares: the picker said what.
	if !strings.HasPrefix(clip.content, "lazysecrets://share/") {
		t.Fatalf("copied %q (%s)", clip.content, d.m.flash.text)
	}
	return clip.content
}

func TestShareFilesRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	link := shareFrom(t, cloneWith(t, map[string]string{
		".env":     "DB_PASSWORD=hunter2\n",
		"prod.env": "TOKEN=abc\n",
	}), false)

	// A colleague's clone: .env is there already, prod.env isn't.
	theirs := cloneWith(t, map[string]string{".env": "DB_PASSWORD=old\n"})
	d := newDriverOpts(t, Options{}, fake.Azure(0), repo.New(repo.Settings{Dir: theirs}))
	d.wait = scryptWait
	d.send(tea.PasteMsg{Content: link})
	if d.m.prompt == nil || d.m.prompt.title != "Shared env files" || !strings.Contains(d.screen(), "2 env files from git.example.com/team/app") {
		t.Fatalf("not offered:\n%s", d.screen())
	}
	d.keys("o")
	if d.m.prompt == nil || d.m.prompt.title != "Save the shared files" {
		t.Fatalf("no save menu (%s)", d.m.flash.text)
	}
	if s := d.screen(); !strings.Contains(s, "replaces the one there") || !strings.Contains(s, "not ignored by git") {
		t.Errorf("save menu doesn't say what happens:\n%s", s)
	}
	d.keys("n") // only the new one
	if b, _ := os.ReadFile(filepath.Join(theirs, ".env")); string(b) != "DB_PASSWORD=old\n" {
		t.Errorf(".env replaced: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(theirs, "prod.env")); string(b) != "TOKEN=abc\n" {
		t.Errorf("prod.env: %q", b)
	}

	// Again, replacing.
	d.send(tea.PasteMsg{Content: link})
	d.keys("o", "w")
	if b, _ := os.ReadFile(filepath.Join(theirs, ".env")); string(b) != "DB_PASSWORD=hunter2\n" {
		t.Errorf(".env not replaced: %q", b)
	}
}

func TestShareWithShareKeyConfirms(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := cloneWith(t, map[string]string{
		".env":              "A=1\n",
		".lazysecrets.yaml": "sharing:\n  key: https://" + kv + "/secrets/db-password\n",
	})
	clip := &fakeClipboard{}
	d := newDriverOpts(t, Options{Clipboard: clip, ShareRules: demoRules}, fake.Azure(0), repo.New(repo.Settings{Dir: root}))
	d.wait = scryptWait
	d.keys("S", "f")
	// The key wasn't picked: it shows which, before sharing.
	if d.m.search != nil || d.m.prompt == nil || d.m.prompt.title != "Share .env" || !strings.Contains(d.screen(), "db-password") {
		t.Fatalf("no confirmation:\n%s", d.screen())
	}
	d.keys("c")
	if !strings.HasPrefix(clip.content, "lazysecrets://share/") {
		t.Errorf("copied %q (%s)", clip.content, d.m.flash.text)
	}
}

func TestShareLinkElsewhereAsksWhere(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	link := shareFrom(t, cloneWith(t, map[string]string{".env": "A=1\n"}), true)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	d := newDriverOpts(t, Options{Dir: dir}, fake.Azure(0))
	d.wait = scryptWait
	d.send(tea.PasteMsg{Content: link})
	d.keys("o")
	if d.m.prompt == nil || d.m.prompt.input == nil || d.m.prompt.input.Value() != dir {
		t.Fatalf("doesn't ask where, starting from where it runs:\n%s", d.screen())
	}
	// A folder that isn't there: the prompt stays, saying so.
	d.m.prompt.input.SetValue(filepath.Join(dir, "nope"))
	d.keys("enter")
	if d.m.prompt == nil || d.m.prompt.input == nil || !strings.Contains(d.screen(), "✗ No folder") {
		t.Fatalf("a missing folder isn't said in the prompt:\n%s", d.screen())
	}
	if !strings.Contains(d.screen(), "enter save") {
		t.Errorf("the footer doesn't say enter saves:\n%s", d.screen())
	}
	d.m.prompt.input.SetValue(dir)
	d.keys("enter") // saved at once: nothing there to replace
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != "A=1\n" {
		t.Errorf(".env: %q (%s)", b, d.m.flash.text)
	}
	if d.m.prompt != nil || !strings.Contains(d.m.flash.text, "saved 1 file in "+dir) {
		t.Errorf("not closed with where it saved: %q", d.m.flash.text)
	}
}

func TestShareLinkGarbled(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.send(tea.PasteMsg{Content: "lazysecrets://share/AQ" + strings.Repeat("A", 30)})
	if d.m.prompt == nil || !strings.Contains(d.screen(), "garbled") {
		t.Errorf("not reported:\n%s", d.screen())
	}
}

// TestShareInDemo shares a demo repo's env files with the demo key and
// opens the link, as lazysecrets --demo does: the demo repos aren't on
// disk, so it asks where to save.
func TestShareInDemo(t *testing.T) {
	clip := &fakeClipboard{}
	d := newDriverOpts(t, Options{Clipboard: clip},
		fake.Azure(0), fake.AzureOps(0).As("Azure:admin", "Azure · admin"), fake.Repos())
	d.wait = scryptWait
	var src provider.Source
	for _, s := range d.m.sourceResults["Repos"].sources {
		if s.Here {
			src = s
		}
	}
	if src.ID == "" {
		t.Fatal("no demo repo")
	}
	src.Provider = "Repos"
	d.run(d.m.shareFiles(src, nil))
	if d.m.search == nil || d.m.search.picker == nil {
		t.Fatalf("no picker: %s", d.m.flash.text)
	}
	// Too short a key: the picker stays, saying why.
	d.send(tea.PasteMsg{Content: "https://kv-app-prod.vault.azure.net/secrets/db-password"})
	if d.m.search == nil || !strings.Contains(d.m.search.picker.err, "Can't use db-password: its value has 28 characters; sharing.keyRules needs 32 (minLength)") {
		t.Fatalf("not said in the picker: %v\n%s", d.m.search != nil, d.screen())
	}
	if !strings.Contains(d.screen(), "28 characters") {
		t.Errorf("not shown:\n%s", d.screen())
	}
	d.send(tea.PasteMsg{Content: "https://kv-shared-prod.vault.azure.net/secrets/env-share"})
	if !strings.HasPrefix(clip.content, "lazysecrets://share/") || d.m.search != nil {
		t.Fatalf("copied %q (%s)", clip.content, d.m.flash.text)
	}
	link := clip.content
	// A value in a demo repo's SOPS file can be the key too, found by
	// the repo's remote.
	clip.content = ""
	d.run(d.m.shareFiles(src, nil))
	d.send(tea.PasteMsg{Content: "lazysecrets://repo/" + url.PathEscape(src.Detail) + "/config%2Foauth.enc.json/github.client_secret"})
	if !strings.HasPrefix(clip.content, "lazysecrets://share/") {
		t.Errorf("with the demo repo's key: copied %q (%s)", clip.content, d.m.flash.text)
	}

	dir, _ := filepath.EvalSymlinks(t.TempDir())
	d.m.placeDir = dir
	d.send(tea.PasteMsg{Content: link})
	d.keys("o")
	if d.m.prompt == nil || d.m.prompt.input == nil || !strings.Contains(d.screen(), "isn't among your repos") {
		t.Fatalf("doesn't ask where:\n%s", d.screen())
	}
	d.keys("enter") // saved at once: nothing there to replace
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatalf("nothing saved (%s)", d.m.flash.text)
	}
}

func TestPickerCancelAndBusy(t *testing.T) {
	clip := &fakeClipboard{}
	d := newDriverOpts(t, Options{Clipboard: clip},
		fake.Azure(0), fake.AzureOps(0).As("Azure:admin", "Azure · admin"), fake.Repos())
	var src provider.Source
	for _, s := range d.m.sourceResults["Repos"].sources {
		if s.Here {
			src = s
		}
	}
	src.Provider = "Repos"
	d.run(d.m.shareFiles(src, nil))
	p := d.m.search.picker
	key, _ := share.ParseKey("https://kv-shared-prod.vault.azure.net/secrets/env-share")

	// Picking starts sealing; another pick meanwhile is ignored.
	cmd := p.choose(&d.m, key)
	if cmd == nil || p.busy == "" {
		t.Fatal("picking didn't start")
	}
	if again := p.choose(&d.m, key); again != nil {
		t.Error("a second pick while busy wasn't ignored")
	}

	// Closed before the link is made: cancelled, nothing copied.
	d.m.search = nil
	d.wait = scryptWait
	d.run(cmd)
	if clip.content != "" {
		t.Errorf("copied after the picker was closed: %q", clip.content)
	}
}

func TestPlainEnvNotPickableFromFavourites(t *testing.T) {
	mem, _ := store.Load("")
	d := newDriverOpts(t, Options{Store: mem}, fake.Azure(0), fake.Repos())
	var src provider.Source
	for _, s := range d.m.sourceResults["Repos"].sources {
		if s.Here {
			src = s
		}
	}
	// A key in the demo repo's plain .env, starred.
	key := "lazysecrets://repo/" + url.PathEscape(src.Detail) + "/.env/DATABASE_URL"
	mem.Update(key, func(n *store.Node) {
		n.StarredBy = []string{"Repos"}
		n.Path = []string{src.Name, ".env"}
		n.Source = src.ID
	})
	src.Provider = "Repos"
	d.run(d.m.shareFiles(src, nil))
	favs, _ := d.m.storeEntries()
	for _, o := range favs {
		if o.star == key {
			if _, ok := pickable(o); ok {
				t.Error("a plain .env value is offered as a key in Favourites")
			}
			return
		}
	}
	t.Fatalf("the starred key isn't among the favourites: %d", len(favs))
}

// refusing is a provider whose values can't be read, like a disabled
// version in Key Vault.
type refusing struct{ provider.Provider }

func (refusing) Value(context.Context, provider.Object) (provider.SecretValue, error) {
	return provider.SecretValue{}, &provider.Denied{
		Err:    errors.New("Operation get is not allowed on a disabled secret."),
		Checks: []string{"the secret's version is enabled"},
	}
}

func TestOpenFailsInAPopup(t *testing.T) {
	key, _ := share.ParseKey("https://" + kv + "/secrets/db-password")
	link, err := share.Seal(share.Header{Key: key, Repo: "git.example.com/team/app"},
		[]byte("correct-horse-battery-staple"), []share.File{{Path: ".env", Data: []byte("A=1\n")}}, demoRules)
	if err != nil {
		t.Fatal(err)
	}
	d := newDriverOpts(t, Options{}, refusing{fake.Azure(0)})
	d.wait = scryptWait
	d.send(tea.PasteMsg{Content: link.String()})
	d.keys("o")
	p := d.m.prompt
	if p == nil || p.title != "Can't decrypt the shared files" || !strings.Contains(d.screen(), "Can't use db-password: Operation get is not allowed") {
		t.Fatalf("not in a popup:\n%s", d.screen())
	}
	if strings.Contains(d.screen(), "version is enabled") {
		t.Error("the whole message is shown before asking")
	}
	d.keys("r")
	if !strings.Contains(d.screen(), "version is enabled") {
		t.Errorf("r doesn't show the whole message:\n%s", d.screen())
	}
	d.keys("g")
	if _, _, sel := d.selected(); d.m.prompt != nil || (sel != "db-password" && (d.m.nav == nil || d.m.nav.ref.Name != "db-password")) {
		t.Errorf("g doesn't go to the key: %q, %q\n%s", sel, d.m.flash.text, d.screen())
	}
}
