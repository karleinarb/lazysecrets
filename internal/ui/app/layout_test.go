package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// paneHeights reads the left panes' heights back from the screen: each
// starts with a "╭─ [n]" line.
func (d *driver) paneHeights() [3]int {
	lines := strings.Split(d.screen(), "\n")
	var starts []int
	for i, l := range lines {
		for n := 1; n <= 3; n++ {
			if strings.HasPrefix(l, "╭─ ["+string(rune('0'+n))+"]") {
				starts = append(starts, i)
			}
		}
	}
	if len(starts) != 3 {
		d.t.Fatalf("found %d left panes:\n%s", len(starts), d.screen())
	}
	bottom := len(lines) - 1 - strings.Count(d.m.footerView(), "\n") // first footer line
	return [3]int{starts[1] - starts[0], starts[2] - starts[1], bottom - starts[2]}
}

func TestPaneHeights(t *testing.T) {
	t.Parallel()
	d := newDriver(t) // 30 rows, one footer line: 29 for panes
	start := d.paneHeights()
	if sum(start) != 29 {
		t.Fatalf("heights %v don't fill the column", start)
	}

	d.send(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl}) // Sources taller, from Vaults
	got := d.paneHeights()
	if got[0] != start[0]+heightStep || got[1] != start[1]-heightStep || got[2] != start[2] {
		t.Errorf("ctrl+↓ on Sources: %v → %v", start, got)
	}

	d.keys("3", "}") // Secrets is the bottom pane: it takes from Vaults above
	got2 := d.paneHeights()
	if got2[2] != got[2]+heightStep || got2[1] != got[1]-heightStep {
		t.Errorf("} on Secrets: %v → %v", got, got2)
	}

	for range 20 {
		d.keys("{") // shrink Secrets as far as it goes
	}
	if h := d.paneHeights(); h[2] != minPaneH || sum(h) != 29 {
		t.Errorf("minimum height: %v", h)
	}

	d.keys("4", "}")
	if !strings.Contains(d.screen(), "focus a left pane") {
		t.Error("no hint when resizing from the detail pane")
	}

	d.keys("=")
	if h := d.paneHeights(); h != start {
		t.Errorf("= didn't reset heights: %v", h)
	}
}

func TestZoom(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "+") // half: Secrets gets most of the column and half the width
	h := d.paneHeights()
	if h[0] != tinyPaneH || h[1] != tinyPaneH || h[2] != 29-2*tinyPaneH {
		t.Errorf("half zoom heights = %v", h)
	}
	if got := d.leftEdge(); got != 50 {
		t.Errorf("half zoom width = %d, want half of 100", got)
	}

	d.keys("+") // full: only Secrets
	screen := d.screen()
	if !strings.Contains(screen, "[3] Secrets") || strings.Contains(screen, "[1] Sources") || strings.Contains(screen, "Secret ─") {
		t.Errorf("full zoom should show only the Secrets pane:\n%s", screen)
	}
	if w := len([]rune(strings.Split(screen, "\n")[0])); w != 100 {
		t.Errorf("full zoom width = %d", w)
	}

	d.keys("4") // the zoom follows focus
	if !strings.Contains(d.screen(), "╭─ Secret ") || strings.Contains(d.screen(), "[3] Secrets") {
		t.Errorf("full zoom on the detail pane:\n%s", d.screen())
	}

	d.keys("+") // wraps to normal
	if d.m.zoom != zoomNormal {
		t.Errorf("zoom = %d after three +", d.m.zoom)
	}
	d.keys("_")
	if d.m.zoom != zoomFull {
		t.Errorf("_ from normal should go to full, got %d", d.m.zoom)
	}
	d.keys("=")
	if d.m.zoom != zoomNormal {
		t.Error("= didn't reset zoom")
	}
}

func TestLayoutIsSaved(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	load := func() *store.Store {
		s, err := store.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	d := newDriverOpts(t, Options{Store: load()})
	d.keys(">", ">", "}")
	edge, heights := d.leftEdge(), d.paneHeights()

	d2 := newDriverOpts(t, Options{Store: load()})
	if d2.leftEdge() != edge || d2.paneHeights() != heights {
		t.Errorf("restored %d %v, want %d %v", d2.leftEdge(), d2.paneHeights(), edge, heights)
	}
	d2.keys("=")
	if l := load().Layout(); l.Split != 0 || l.Heights != [3]int{} {
		t.Errorf("reset not saved: %+v", l)
	}
}

func TestContextHelpBar(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	if bar := lastLine(d.screen()); !strings.Contains(bar, "S share") || !strings.HasSuffix(strings.TrimSpace(bar), "? help • q quit") || strings.Contains(bar, "space show") {
		t.Errorf("Sources help bar: %q", bar)
	}
	d.keys("3")
	if bar := lastLine(d.screen()); !strings.Contains(bar, "s copy link") || !strings.Contains(bar, "S share") {
		t.Errorf("Secrets help bar: %q", bar)
	}
}

func TestKeybindingsMenu(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "?")
	if d.m.prompt == nil || d.m.prompt.title != "Keybindings" {
		t.Fatalf("? didn't open the menu:\n%s", d.screen())
	}
	// Sections: the focused pane's keys, then global ones; the cursor
	// starts on the first key, and a counter shows where it is.
	p := d.m.prompt
	if !p.options[0].header || p.options[0].label != "Secrets pane" {
		t.Errorf("first section: %+v", p.options[0])
	}
	if sel := ansi.Strip(p.options[p.cursor].label); !strings.HasPrefix(sel, "space") {
		t.Errorf("cursor starts on %q", sel)
	}
	screen := d.screen()
	if !strings.Contains(screen, "Global") || !strings.Contains(screen, "1 of ") {
		t.Errorf("no Global section or counter:\n%s", screen)
	}
	d.send(tea.KeyPressMsg{Code: tea.KeyUp}) // can't go up onto the header
	if p.options[p.cursor].header {
		t.Error("cursor landed on a header")
	}
	if !strings.Contains(d.screen(), "Show or hide the selected value") {
		t.Errorf("no tip for the selected row:\n%s", d.screen())
	}

	// The filter matches descriptions and groups too; matches stay under
	// their section headers, and empty sections drop out.
	for _, r := range "copy" {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	var headers []string
	for _, o := range d.m.prompt.options {
		if o.header {
			headers = append(headers, o.label)
		}
	}
	if strings.Join(headers, ",") != "Secrets pane,Global" {
		t.Errorf("sections while filtering: %v", headers)
	}
	if d.m.prompt.options[d.m.prompt.cursor].header {
		t.Error("cursor on a header after filtering")
	}
	var keys []string
	for _, o := range d.m.prompt.options {
		keys = append(keys, strings.Fields(ansi.Strip(o.label))[0])
	}
	for _, want := range []string{"y", "Y", "s", "ctrl+y"} {
		if !slices.Contains(keys, want) {
			t.Errorf("filter copy: %v lacks %s", keys, want)
		}
	}

	// Enter runs the selected binding.
	d.keys("esc", "?")
	for _, r := range "space" {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if sel := ansi.Strip(d.m.prompt.options[d.m.prompt.cursor].label); !strings.HasPrefix(sel, "space") {
		t.Fatalf("filter space: selected %q", sel)
	}
	d.keys("enter")
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") {
		t.Errorf("running reveal from the menu didn't reveal:\n%s", d.screen())
	}
}

func TestKeyPress(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"space": "space", "ctrl+y": "ctrl+y", "shift+tab": "shift+tab", "S": "S", "`": "`", "ctrl+left": "ctrl+left",
	} {
		if got := keyPress(name).String(); got != want {
			t.Errorf("keyPress(%q).String() = %q", name, got)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestNoHighlightWhileTerminalBlurred(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	green := "\x1b[32m╭" // a focused border; "current" versions are green too
	if !strings.Contains(d.m.View().Content, green) {
		t.Fatal("focused pane not highlighted")
	}
	d.send(tea.BlurMsg{})
	if strings.Contains(d.m.View().Content, green) {
		t.Error("still highlighted while the terminal is blurred")
	}
	d.send(tea.FocusMsg{})
	if !strings.Contains(d.m.View().Content, green) {
		t.Error("highlight not back after focus")
	}

	// Popups too.
	d.keys("?")
	d.send(tea.BlurMsg{})
	if strings.Contains(d.m.modalView(), "\x1b[32m") || strings.Contains(d.m.modalView(), "\x1b[7m") {
		t.Error("popup still highlighted while the terminal is blurred")
	}
	d.send(tea.FocusMsg{})
	if !strings.Contains(d.m.modalView(), "\x1b[32m╭") {
		t.Error("popup highlight not back after focus")
	}
}

func TestVersionInStatus(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{Version: "0.1.0"})
	if !strings.Contains(d.screen(), "[0] lazysecrets v0.1.0") {
		t.Errorf("no version in the status pane:\n%s", d.screen())
	}
	if bar := lastLine(d.screen()); strings.Contains(bar, "v0.1.0") || !strings.Contains(bar, "? help • q quit") {
		t.Errorf("help line %q", bar)
	}
}
