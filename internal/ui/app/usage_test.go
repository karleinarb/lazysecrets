package app

import (
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"github.com/charmbracelet/x/ansi"
)

func TestRecentScope(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "j", "space") // reveal api-key
	d.keys("j", "s")          // share legacy-token: a use too
	d.keys("2", "j", "f")     // star kv-shared-prod

	key := "lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/api-key"
	if n := d.m.store.Node(key); n.Uses != 1 || n.LastUsed.IsZero() || n.Name != "api-key" {
		t.Fatalf("reveal not counted: %+v", n)
	}
	d.keys("R")
	var recent []string
	for _, o := range d.m.search.shown {
		recent = append(recent, ansi.Strip(o.label))
	}
	if len(recent) < 2 || !strings.Contains(recent[0], "legacy-token") || !strings.Contains(recent[1], "api-key") {
		t.Fatalf("want newest first: %q", recent)
	}
	d.keys("enter") // jump to legacy-token
	if _, _, s := d.selected(); s != "legacy-token" {
		t.Errorf("jumped to %q", s)
	}
	d.keys("F")
	if len(d.m.search.shown) != 1 || !strings.Contains(ansi.Strip(d.m.search.shown[0].label), "kv-shared-prod") {
		t.Errorf("favourites: %d entries\n%s", len(d.m.search.shown), d.screen())
	}
}

func TestNoHistory(t *testing.T) {
	t.Parallel()
	mem, _ := store.Load("")
	d := newDriverOpts(t, Options{Store: mem, NoHistory: true})
	d.keys("3", "j", "space", "y")
	if len(mem.Nodes()) != 0 {
		t.Errorf("recorded with history off: %v", mem.Nodes())
	}
}
