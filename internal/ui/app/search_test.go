package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"github.com/charmbracelet/x/ansi"
)

func (d *driver) openSearch() {
	d.t.Helper()
	d.send(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if d.m.search == nil {
		d.t.Fatalf("no search popup:\n%s", d.screen())
	}
}

func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

// searchAll picks All on the left and goes back to the results.
func (d *driver) searchAll() {
	d.send(ctrl('h'))
	for d.m.search.src > 0 {
		d.send(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for d.m.search.scope().id != allSources {
		d.send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	d.send(ctrl('l'))
}

func TestSearchLoaded(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j") // platform-dev: away from what's loaded
	d.openSearch()
	for _, want := range []string{"Sources", "Results", "All", "platform-dev"} {
		if !strings.Contains(d.screen(), want) {
			t.Fatalf("missing %q:\n%s", want, d.screen())
		}
	}
	d.typeText("api-key") // on All: found at once
	if !strings.Contains(d.screen(), "ctrl+s only platform-dev") {
		t.Errorf("no hint to narrow to the source:\n%s", d.screen())
	}
	d.send(ctrl('s'))
	if !strings.Contains(d.screen(), "no matches here · All searches everything") || !strings.Contains(d.screen(), "ctrl+s searches all") {
		t.Errorf("no hint to search everything:\n%s", d.screen())
	}
	d.send(ctrl('s'))
	if !strings.Contains(d.screen(), "platform-prod / kv-app-prod / api-key") {
		t.Fatalf("loaded secret not found:\n%s", d.screen())
	}
	d.keys("enter")
	if src, c, s := d.selected(); src != "platform-prod" || c != "kv-app-prod" || s != "api-key" {
		t.Errorf("jumped to %s/%s/%s", src, c, s)
	}
	if d.m.search != nil {
		t.Error("popup still open")
	}
}

func TestSearchLoadsAsYouType(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.openSearch()
	d.send(ctrl('s')) // only the source the cursor is on: platform-prod
	// Typing loads the scope: that source, without ctrl+a.
	d.typeText("smtp")
	if !strings.Contains(d.screen(), "platform-prod / kv-shared-prod / smtp-password") {
		t.Fatalf("not found in a vault that wasn't loaded:\n%s", d.screen())
	}
	if _, ok := d.m.cache.objects["Azure|sub-prod/kv-shared-prod|Secret"]; !ok {
		t.Error("loaded list not in the panes' cache")
	}
	if _, ok := d.m.cache.containers[sourceRowKey("Azure", "sub-dev")]; ok {
		t.Error("another source loaded too")
	}

	// On All: everything.
	d.send(ctrl('s'))
	d.typeText("-")
	if _, ok := d.m.cache.containers[sourceRowKey("Azure", "sub-dev")]; !ok {
		t.Errorf("typing on All didn't load the other sources:\n%s", d.screen())
	}
	if d.m.search.input.Value() != "smtp-" {
		t.Error("query lost while loading")
	}
	if !strings.Contains(d.screen(), "unreadable") { // kv-restricted refuses
		t.Errorf("unreadable vaults not counted:\n%s", d.screen())
	}
	// Once: the next key doesn't ask for the list that failed again;
	// ctrl+a still does, on purpose.
	d.typeText("p")
	if d.m.crawl.running {
		t.Error("typing on reloads what failed")
	}
}

func TestSearchSourceNarrowsAndLoads(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.openSearch()
	d.keys("tab") // to Sources
	sel := func() string { return d.m.search.scopes[d.m.search.src].id }
	for d.m.search.src > 0 {
		d.send(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for !strings.HasPrefix(sel(), "platform-dev") {
		d.send(tea.KeyPressMsg{Code: tea.KeyDown})
		if d.m.search.src == len(d.m.search.scopes)-1 {
			t.Fatal("platform-dev not in sources")
		}
	}
	if !strings.Contains(ansi.Strip(d.m.search.scopes[d.m.search.src].mark), "○") {
		t.Errorf("unloaded source mark %q", d.m.search.scopes[d.m.search.src].mark)
	}
	if len(d.m.search.shown) != 0 {
		t.Errorf("results not narrowed to platform-dev: %d", len(d.m.search.shown))
	}
	d.send(ctrl('r'))
	if !strings.Contains(ansi.Strip(d.m.search.scopes[d.m.search.src].mark), "●") {
		t.Errorf("loaded source mark %q", d.m.search.scopes[d.m.search.src].mark)
	}
	for _, o := range d.m.search.shown {
		if !strings.Contains(ansi.Strip(o.label), "platform-dev") {
			t.Errorf("result from elsewhere: %q", ansi.Strip(o.label))
		}
	}
	if len(d.m.search.shown) == 0 {
		t.Error("nothing shown after loading platform-dev")
	}
	if _, ok := d.m.cache.objects["Azure|sub-prod/kv-shared-prod|Secret"]; ok {
		t.Error("loaded another subscription too")
	}
}

func TestSearchResultsStayPut(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.openSearch()
	d.send(tea.KeyPressMsg{Code: tea.KeyDown})
	ids := func() []string {
		var out []string
		for _, o := range d.m.search.shown {
			out = append(out, o.id)
		}
		return out
	}
	before, at := ids(), d.m.search.shown[d.m.search.res].id
	d.send(ctrl('a'))
	after := ids()
	if len(after) <= len(before) {
		t.Fatalf("nothing loaded: %d → %d", len(before), len(after))
	}
	for i, id := range before {
		if after[i] != id {
			t.Errorf("row %d changed from %q to %q", i, id, after[i])
		}
	}
	if got := d.m.search.shown[d.m.search.res].id; got != at {
		t.Errorf("cursor moved from %q to %q", at, got)
	}
}

func TestStarFromSearch(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.openSearch()
	d.typeText("api-key")
	d.send(ctrl('f'))
	if !d.m.store.Node(kvKey + "/secrets/api-key").StarredFor("Azure") {
		t.Errorf("ctrl+f in search didn't star:\n%s", d.screen())
	}
}

func TestSearchStartsWhereYouAre(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "j") // platform-prod / kv-shared-prod
	d.openSearch()
	v := d.m.search
	scope := func() string { return ansi.Strip(v.scopes[v.src].label) }
	if scope() != "All" {
		t.Errorf("opened on %q, want All", scope())
	}
	if v.res >= len(v.shown) || !strings.Contains(ansi.Strip(v.shown[v.res].label), "kv-shared-prod") {
		t.Errorf("vault not under the cursor:\n%s", d.screen())
	}
	d.send(ctrl('s')) // narrows to the source, and back
	if !strings.Contains(scope(), "platform-prod") {
		t.Errorf("ctrl+s narrowed to %q", scope())
	}
	d.send(ctrl('s'))
	if scope() != "All" {
		t.Errorf("second ctrl+s on %q, want All", scope())
	}

	// From Favourites: to Search, on All.
	d.keys("esc", "]", "]") // stop typing; Favourites
	d.send(ctrl('s'))
	if v.tab != tabSearch || scope() != "All" {
		t.Errorf("ctrl+s from another tab: tab %d, %q", v.tab, scope())
	}

	// From the Sources pane: the source.
	d.keys("esc", "1", "j") // close; platform-dev
	d.openSearch()
	v = d.m.search
	d.send(ctrl('s'))
	if !strings.Contains(scope(), "platform-dev") {
		t.Errorf("ctrl+s narrowed to %q", scope())
	}
}

func TestSearchNarrowsToAccount(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("1", "g") // the first group header
	header, _ := d.m.sources.Selected()
	d.openSearch()
	d.send(ctrl('s'))
	v := d.m.search
	if v.scope().id != accountScope+header.provider {
		t.Fatalf("ctrl+s on %q narrowed to %q", header.label, v.scope().id)
	}
	if len(v.shown) == 0 || !strings.Contains(d.screen(), "Results in "+header.label) {
		t.Errorf("no results in the account:\n%s", d.screen())
	}
	for _, o := range v.shown {
		if cachefile.ProviderOf(o.group) != header.provider {
			t.Errorf("result from another account: %q", ansi.Strip(o.label))
		}
	}
	// The account's sources are under it, indented.
	if next := v.scopes[v.src+1]; next.group == "" || cachefile.ProviderOf(next.group) != header.provider || !strings.HasPrefix(ansi.Strip(next.label), "  ") {
		t.Errorf("after the account: %q", ansi.Strip(next.label))
	}
}

func TestSearchHint(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.openSearch()
	d.searchAll()
	if !strings.Contains(d.screen(), "vaults loaded · ctrl+a loads everything") {
		t.Errorf("no hint on All:\n%s", d.screen())
	}
	d.send(ctrl('a'))
	if strings.Contains(d.screen(), "ctrl+a loads everything") {
		t.Errorf("hint after loading everything:\n%s", d.screen())
	}
}

func TestGoToTabsAndModes(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "f", "j", "f") // two favourites
	d.keys("F")
	v := d.m.search
	if v.tab != tabFavs || v.typing {
		t.Fatalf("F: tab %d, typing %v", v.tab, v.typing)
	}
	d.keys("j") // browsing: j moves, doesn't type
	if v.res != 1 || v.input.Value() != "" {
		t.Errorf("j: cursor %d, input %q", v.res, v.input.Value())
	}
	d.keys("]")
	if v.tab != tabRecent {
		t.Errorf("] → tab %d, want Recent", v.tab)
	}
	d.keys("]", "]")
	if v.tab != tabFavs {
		t.Errorf("] wraps to tab %d, want Favourites", v.tab)
	}
	d.keys("[")
	if v.tab != tabSearch || !strings.Contains(d.screen(), "Scope") {
		t.Errorf("[ → tab %d, want Search:\n%s", v.tab, d.screen())
	}

	// g and G: top and bottom of the list with the cursor.
	d.keys("G")
	if v.res != len(v.shown)-1 || v.res == 0 {
		t.Errorf("G on results: %d of %d", v.res, len(v.shown))
	}
	d.keys("g")
	if v.res != 0 {
		t.Errorf("g on results: %d", v.res)
	}
	d.keys("h", "G")
	if v.src != len(v.scopes)-1 {
		t.Errorf("G on scopes: %d of %d", v.src, len(v.scopes))
	}
	d.keys("g", "l")
	if v.src != 0 {
		t.Errorf("g on scopes: %d", v.src)
	}

	d.keys("/")
	d.typeText("shared")
	if !v.typing || len(v.shown) == 0 {
		t.Fatalf("typing: %v, %d results", v.typing, len(v.shown))
	}
	d.keys("esc") // clears, back to browsing
	if v.typing || v.input.Value() != "" || d.m.search == nil {
		t.Errorf("esc while typing: typing %v, input %q, open %v", v.typing, v.input.Value(), d.m.search != nil)
	}
	d.keys("esc") // closes
	if d.m.search != nil {
		t.Error("esc while browsing didn't close")
	}
}

func TestSearchHighlightsMatches(t *testing.T) {
	t.Parallel()
	// Icons: labels start with a multi-byte glyph, where byte offsets and
	// characters differ.
	d := newDriverOpts(t, Options{Icons: true})
	d.openSearch()
	d.typeText("dbpw")
	if len(d.m.search.shown) == 0 {
		t.Fatal("no results")
	}
	o := d.m.search.shown[0]
	plain := []rune(ansi.Strip(o.label))
	var got string
	for _, h := range o.hits {
		got += string(plain[h])
	}
	if got != "dbpw" {
		t.Errorf("hits %v spell %q in %q", o.hits, got, string(plain))
	}
	with := d.m.searchViewBox()
	for i := range d.m.search.shown {
		d.m.search.shown[i].hits = nil
	}
	if with == d.m.searchViewBox() {
		t.Error("matched characters not styled")
	}
}

func TestHelpBarFollowsPopup(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("F")
	bar := lastLines(d.screen(), 1)
	for _, want := range []string{"[ prev tab", "] next tab", "/ filter", "f star"} {
		if !strings.Contains(bar, want) {
			t.Errorf("browsing: help bar %q lacks %q", bar, want)
		}
	}
	d.keys("/")
	if bar := lastLines(d.screen(), 1); !strings.Contains(bar, "ctrl+f star") || !strings.Contains(bar, "esc stop typing") {
		t.Errorf("typing: help bar %q", bar)
	}
}

func TestHelpFromPopups(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("F", "?")
	if d.m.prompt == nil || d.m.prompt.title != "Keybindings" || d.m.prompt.options[0].label != "Go to" {
		t.Fatalf("? in Go to:\n%s", d.screen())
	}
	if !strings.Contains(d.screen(), "switch list") { // tab: not on the help bar
		t.Errorf("Go to's keys missing:\n%s", d.screen())
	}
	var sections []string
	for _, o := range d.m.prompt.options {
		if o.header {
			sections = append(sections, o.label)
		}
		if strings.Contains(ansi.Strip(o.label), "copy value") {
			t.Errorf("pane key listed in Go to's help: %q", ansi.Strip(o.label))
		}
	}
	if strings.Join(sections, ",") != "Go to,While typing, after /" {
		t.Errorf("sections %v", sections)
	}
	d.keys("esc") // back to Go to, where it was
	if d.m.search == nil || d.m.search.tab != tabFavs || d.m.prompt != nil {
		t.Errorf("not back in Go to: search %v, prompt %v", d.m.search != nil, d.m.prompt != nil)
	}

	// Picking a key in the list runs it in Go to.
	d.keys("?")
	d.typeText("next tab")
	d.keys("enter")
	if d.m.search == nil || d.m.search.tab != tabRecent {
		t.Errorf("running ] from ? didn't switch Go to's tab")
	}

	// Menus too.
	d.keys("esc", "3", "S", "?")
	if d.m.prompt == nil || d.m.prompt.options[0].label != "This menu" {
		t.Fatalf("? in a menu:\n%s", d.screen())
	}
	for _, o := range d.m.prompt.options {
		if o.header && o.label != "This menu" {
			t.Errorf("menu help has section %q", o.label)
		}
	}
	d.keys("esc")
	if d.m.prompt == nil || !strings.HasPrefix(d.m.prompt.title, "Share") {
		t.Errorf("not back in the share menu:\n%s", d.screen())
	}
}

func TestHelpRunsTypingKeysInGoTo(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("ctrl+s", "esc") // Go to on Search, browsing
	before := d.m.search.results
	d.keys("?")
	d.typeText("ctrl+h") // "sources", a typing key
	d.keys("enter")
	if d.m.search == nil || !d.m.search.typing || d.m.search.results || !before {
		t.Errorf("typing key from ? didn't run: typing %v, results %v", d.m.search != nil && d.m.search.typing, d.m.search.results)
	}
}

func TestGoToHelpBarEndsWithHelp(t *testing.T) {
	t.Parallel()
	d := newDriver(t) // 100 columns: Go to's keys don't all fit
	d.keys("ctrl+s", "esc")
	bar := strings.TrimSpace(lastLines(d.screen(), 1))
	if !strings.HasSuffix(bar, "? help") || !strings.Contains(bar, "…") {
		t.Errorf("help bar %q", bar)
	}
}

func TestLoadWithEmptyResults(t *testing.T) {
	t.Parallel()
	c := &counting{Provider: fake.Azure(0)}
	d := newDriverOpts(t, Options{}, c)
	d.openSearch()
	d.send(ctrl('h')) // pick platform-dev, never opened, in the scopes
	for d.m.search.src > 0 {
		d.send(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for i := 0; !strings.Contains(ansi.Strip(d.m.search.scope().label), "platform-dev"); i++ {
		if i > len(d.m.search.scopes) {
			var labels []string
			for _, o := range d.m.search.scopes {
				labels = append(labels, ansi.Strip(o.label))
			}
			t.Fatalf("no platform-dev scope: %q (at %d, results %v)", labels, d.m.search.src, d.m.search.results)
		}
		d.send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	d.send(ctrl('l'))
	if len(d.m.search.shown) != 0 || !d.m.search.results {
		t.Fatalf("want empty results with the cursor there: %d shown", len(d.m.search.shown))
	}
	before := c.containers
	d.send(ctrl('r')) // in the results, nothing selected: the source
	if c.containers == before {
		t.Error("ctrl+r with empty results didn't load the source")
	}
	if len(d.m.search.shown) == 0 {
		t.Errorf("nothing shown after loading:\n%s", d.screen())
	}
}
