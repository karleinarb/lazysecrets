package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

// fakeUpdater offers latest and records what it installs.
type fakeUpdater struct {
	latest    update.Release
	err       error
	asked     int
	installed string
}

func (f *fakeUpdater) updater() *Updater {
	return &Updater{
		Latest: func(context.Context) (update.Release, error) {
			f.asked++
			return f.latest, f.err
		},
		Install: func(_ context.Context, r update.Release) error {
			f.installed = r.Tag
			return nil
		},
	}
}

var v020 = update.Release{
	Tag:  "v0.2.0",
	Body: "### Added\n\n- Install with a one-liner (#7)\n\n## Install\n\ncurl …",
	URL:  "https://git.example.com/keb/lazysecrets/releases/tag/v0.2.0",
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	f := &fakeUpdater{latest: v020}
	mem, _ := store.Load(filepath.Join(t.TempDir(), "state.json"))
	d := newDriverOpts(t, Options{Version: "0.1.1", Updater: f.updater(), UpdateCheck: true, Store: mem})
	if f.asked != 1 {
		t.Fatalf("asked %d times at startup", f.asked)
	}
	if scr := d.screen(); !strings.Contains(scr, "[0] lazysecrets v0.1.1") || !strings.Contains(scr, "↑ v0.2.0 U") {
		t.Errorf("no update in the status pane:\n%s", scr)
	}

	// U: the changelog, without the install notes; u installs.
	d.keys("U")
	scr := d.screen()
	for _, want := range []string{"lazysecrets v0.2.0", "You have v0.1.1.", "Install with a one-liner (#7)", "Update now", "Open the release page"} {
		if !strings.Contains(scr, want) {
			t.Errorf("missing %q:\n%s", want, scr)
		}
	}
	if strings.Contains(scr, "curl") {
		t.Errorf("install notes shown:\n%s", scr)
	}
	d.keys("u")
	if f.installed != "v0.2.0" {
		t.Fatalf("installed %q", f.installed)
	}
	if !strings.Contains(d.screen(), "updated to lazysecrets v0.2.0 · restart lazysecrets to use it") ||
		!strings.Contains(d.screen(), "→ v0.2.0 (restart)") {
		t.Errorf("after updating:\n%s", d.screen())
	}

	// Within a day, the next start uses what it found: no request.
	f2 := &fakeUpdater{latest: v020}
	d2 := newDriverOpts(t, Options{Version: "0.1.1", Updater: f2.updater(), UpdateCheck: true, Store: mem})
	if f2.asked != 0 || !strings.Contains(d2.screen(), "↑ v0.2.0") {
		t.Errorf("asked %d times within a day:\n%s", f2.asked, d2.screen())
	}
	// A day later it asks again.
	mem.SetUpdateCheck(store.UpdateCheck{At: time.Now().Add(-25 * time.Hour), Tag: "v0.2.0"})
	newDriverOpts(t, Options{Version: "0.1.1", Updater: f2.updater(), UpdateCheck: true, Store: mem})
	if f2.asked != 1 {
		t.Errorf("asked %d times a day later", f2.asked)
	}
}

func TestUpdateAsked(t *testing.T) {
	t.Parallel()
	mem, _ := store.Load(filepath.Join(t.TempDir(), "state.json"))
	// Not looking on its own: U looks.
	f := &fakeUpdater{latest: update.Release{Tag: "v0.1.1"}}
	d := newDriverOpts(t, Options{Version: "0.1.1", Updater: f.updater(), Store: mem})
	if f.asked != 0 {
		t.Fatal("looked without updateCheck")
	}
	d.keys("U")
	if f.asked != 1 || !strings.Contains(d.screen(), "lazysecrets v0.1.1 is the latest (v0.1.1)") {
		t.Errorf("asked %d:\n%s", f.asked, d.screen())
	}

	// The daily check stays quiet when offline; U says why.
	f.err = errors.New("no network")
	d.keys("U")
	if !strings.Contains(d.screen(), "couldn't check for a newer lazysecrets: no network") {
		t.Errorf("no error:\n%s", d.screen())
	}

	// A development build isn't offered releases.
	dev := newDriverOpts(t, Options{Version: "dev-2ba762c", Updater: (&fakeUpdater{latest: v020}).updater(), UpdateCheck: true, Store: mem})
	if strings.Contains(dev.screen(), "↑") {
		t.Errorf("dev build offered an update:\n%s", dev.screen())
	}
	dev.keys("U")
	if !strings.Contains(dev.screen(), "development build") {
		t.Errorf("U on a dev build:\n%s", dev.screen())
	}
}
