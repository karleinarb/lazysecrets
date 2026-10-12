package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

func (d *driver) note(text string) {
	d.t.Helper()
	d.keys("n")
	if d.m.prompt == nil || !strings.HasPrefix(d.m.prompt.title, "Note on ") {
		d.t.Fatalf("n didn't open the note popup:\n%s", d.screen())
	}
	for range 40 { // clear what's there
		d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range text {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	d.keys("enter")
}

func TestNotesOnRows(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	mem, _ := store.Load(path)
	d := newDriverOpts(t, Options{Store: mem})

	d.keys("3", "j")
	d.note("stripe live")
	d.keys("2")
	d.note("app secrets")
	d.keys("1")
	d.note("main prod")

	screen := d.screen()
	for _, want := range []string{"stripe live", "app secrets", "main prod", "note       main prod"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q:\n%s", want, screen)
		}
	}

	// The filter matches notes.
	d.keys("3", "/")
	d.typeText("stripe")
	if _, _, s := d.selected(); s != "api-key" || d.m.secrets.Len() != 1 {
		t.Errorf("filter by note: %q (%d)", s, d.m.secrets.Len())
	}
	d.keys("esc")

	// Saved, and an empty note removes it.
	again, _ := store.Load(path)
	notes := 0
	for _, n := range again.Nodes() {
		if n.Note != "" {
			notes++
		}
	}
	if notes != 3 {
		t.Errorf("saved notes = %v", again.Nodes())
	}
	d.keys("3")
	d.note("")
	if strings.Contains(d.screen(), "stripe live") {
		t.Error("empty note didn't remove it")
	}
}

func TestWithNoteGivesWay(t *testing.T) {
	t.Parallel()
	st := styles.Default()
	if got := withNote(st, "db-password", "prod SQL admin", 30); len([]rune(stripANSI(got))) != 30 ||
		!strings.HasSuffix(stripANSI(got), "prod SQL admin") {
		t.Errorf("fits: %q", stripANSI(got))
	}
	if got := stripANSI(withNote(st, "db-password", "prod SQL admin", 20)); !strings.HasPrefix(got, "db-password") ||
		!strings.HasSuffix(got, "…") {
		t.Errorf("tight: %q", got)
	}
	if got := stripANSI(withNote(st, "a-very-long-secret-name", "note", 20)); got != "a-very-long-secret-name" {
		t.Errorf("no room: %q", got)
	}
}

func stripANSI(s string) string { return ansi.Strip(s) }
