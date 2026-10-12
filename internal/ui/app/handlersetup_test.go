package app

import (
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

func setupOpts(mem *store.Store, installs *int) Options {
	return Options{Store: mem, HandlerSetup: &HandlerSetup{
		Reason:  "lazysecrets:// links don't open lazysecrets yet",
		Install: func() (string, error) { *installs++; return "kitty", nil },
	}}
}

func TestHandlerSetupPopup(t *testing.T) {
	t.Parallel()
	mem, _ := store.Load("")
	installs := 0
	d := newDriverOpts(t, setupOpts(mem, &installs))
	for _, want := range []string{"Open links in lazysecrets?", "links don't open lazysecrets yet", "i  Set it up", "s  Not now", "n  Don't ask again"} {
		if !strings.Contains(d.screen(), want) {
			t.Fatalf("missing %q:\n%s", want, d.screen())
		}
	}
	d.keys("i")
	if installs != 1 || !strings.Contains(d.screen(), "links now open here, or in a new kitty window") {
		t.Errorf("set up: %d installs\n%s", installs, d.screen())
	}

	// Not now: asked again next start.
	d = newDriverOpts(t, setupOpts(mem, &installs))
	d.keys("s")
	if d.m.prompt != nil || mem.Flag(flagNoHandlerPrompt) {
		t.Error("not now")
	}
	d = newDriverOpts(t, setupOpts(mem, &installs))
	if d.m.prompt == nil {
		t.Fatal("not asked again after not now")
	}

	// Don't ask again: remembered.
	d.keys("n")
	if !mem.Flag(flagNoHandlerPrompt) {
		t.Error("don't ask again not stored")
	}
	d = newDriverOpts(t, setupOpts(mem, &installs))
	if d.m.prompt != nil {
		t.Errorf("asked after don't ask again:\n%s", d.screen())
	}
}

// A link given at startup is offered first; the setup question waits.
func TestHandlerSetupWaitsForLink(t *testing.T) {
	t.Parallel()
	mem, _ := store.Load("")
	installs := 0
	opts := setupOpts(mem, &installs)
	r, _ := ref.Parse("lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/api-key")
	opts.Open = &r
	d := newDriverOpts(t, opts)
	if !strings.Contains(d.screen(), "Open link") {
		t.Fatalf("link not first:\n%s", d.screen())
	}
	d.keys("x")
	if !strings.Contains(d.screen(), "Open links in lazysecrets?") {
		t.Errorf("setup question not shown after the link:\n%s", d.screen())
	}
}
