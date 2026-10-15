package app

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/match"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/keys"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// Go to is one popup for finding a place and going there. Tabs at the
// top: Search has two lists side by side, the scope on the left (All, or
// one source with how much of it is loaded: ○ ◐ ● ◌ ✗) and what's in it
// on the right; Favourites and Recent (from the store, so they work
// before anything loads) are one list each.
//
// Like a pane, it's either browsing (letters: j/k, [ and ] for tabs, f
// star, / to type) or typing a filter (ctrl+ letters). esc while typing
// clears the text and goes back to browsing; esc while browsing closes.
// F opens it on Favourites and R on Recent, browsing; search on Search,
// typing, on All. search again narrows to the source you were in, and
// back to All.
//
// The results don't move by themselves: as lists load, rows already shown
// keep their order and new ones come after them, with the cursor on the
// same entry. Starring keeps an entry where it is, its ★ showing the
// change. Only typing or picking another tab or scope re-sorts them.

type searchView struct {
	input   textinput.Model
	typing  bool // the text field has the keyboard
	tab     int  // tabSearch, tabFavs or tabRecent
	results bool // in Search, the results have the cursor (else the scopes)

	scopes []option // All, then the sources (Search)
	items  []option // everything loaded, unfiltered
	favs   []option // starred, as when the popup opened
	recent []option // recently used, as when the popup opened
	shown  []option // the tab's (or scope's) entries matching the input
	src    int      // cursor in scopes
	res    int      // cursor in shown
	here   string   // the scope of the source Go to was opened from, if any

	// picker, if set, makes Go to pick a secret instead of going to it.
	picker *picker

	// loaded are the scopes a search has loaded since the popup opened:
	// typing loads the scope once, so lists that fail aren't asked for
	// again on every key.
	loaded map[string]bool
}

const (
	tabSearch = iota
	tabFavs
	tabRecent
	tabCount
)

var tabNames = [tabCount]string{"Search", "Favourites", "Recent"}

const (
	scopeFavs    = "#favs"
	scopeRecent  = "#recent"
	allSources   = "#all"
	accountScope = "#account:" // + a provider ID: an account's sources, or Databricks', or the repos
)

const goToTitle = "Go to"

// showSearch opens Go to on Search, typing, on All, with the selected
// vault under the cursor.
func (m *Model) showSearch() tea.Cmd {
	cmd := m.openGoTo(tabSearch, true)
	m.preselect()
	return cmd
}

// showFavourites opens Go to on Favourites; showRecent on Recent. Both
// start browsing: they're short lists.
func (m *Model) showFavourites() tea.Cmd { return m.openGoTo(tabFavs, false) }
func (m *Model) showRecent() tea.Cmd     { return m.openGoTo(tabRecent, false) }

func (m *Model) openGoTo(tab int, typing bool) tea.Cmd {
	in := textinput.New()
	in.Prompt = "/"
	in.SetWidth(searchWidth(m.w) - 6) // as wide as the popup
	v := &searchView{input: in, tab: tab, results: true}
	v.favs, v.recent = m.storeEntries()
	m.search = v
	m.fillSearch(false)
	m.flash = flash{}
	return m.setTyping(typing)
}

// setTyping gives the keyboard to the text field, or takes it back.
func (m *Model) setTyping(on bool) tea.Cmd {
	v := m.search
	v.typing = on
	if on {
		v.input.Prompt, v.input.Placeholder = "/", "type to filter"
		return v.input.Focus()
	}
	v.input.Blur()
	v.input.Prompt = "" // the placeholder names the key
	if v.input.Value() != "" {
		v.input.Prompt = "/"
	}
	v.input.Placeholder = withKey(m.keys.GoFilter, "%s to filter")
	return nil
}

// scope is what the results come from: the tab, or in Search the scope
// picked on the left.
func (v *searchView) scope() option {
	switch v.tab {
	case tabFavs:
		return option{id: scopeFavs}
	case tabRecent:
		return option{id: scopeRecent}
	}
	return v.scopes[v.src]
}

// onSource reports whether the results come from one source.
func (v *searchView) onSource() bool { return v.tab == tabSearch && v.scope().group != "" }

// preselect starts where the user is: the selected source is the one
// search narrows to, and the selected vault or scope is under the cursor.
func (m *Model) preselect() {
	v := m.search
	row := m.sources.SelectedID()
	account := ""
	if id, ok := strings.CutPrefix(row, "group:"); ok { // a group header
		account = accountScope + id
	}
	i := slices.IndexFunc(v.scopes, func(o option) bool { return o.id == account || o.group != "" && o.group == row })
	if i < 0 {
		return
	}
	v.here = v.scopes[i].id
	if m.focus == paneSources {
		return
	}
	if c, ok := m.containers.Selected(); ok {
		key := storeKey(c)
		if j := slices.IndexFunc(v.shown, func(o option) bool { return o.star == key }); j >= 0 {
			v.res = j
		}
	}
}

// fillSearch (re)builds the lists from what's loaded. keep keeps the
// order of rows already shown (lists arrived); otherwise the results are
// ranked afresh (the query, tab or scope changed).
func (m *Model) fillSearch(keep bool) {
	v := m.search
	atSrc, atRes := "", ""
	if v.src < len(v.scopes) {
		atSrc = v.scopes[v.src].id
	}
	if v.res < len(v.shown) {
		atRes = v.shown[v.res].id
	}
	prev := map[string]int{}
	for i, o := range v.shown {
		prev[o.id] = i
	}

	sources, items := m.searchEntries()
	all := option{label: "All", id: allSources, mark: m.coverageMark(nil)}
	// Grouped like the Sources pane: each account (or Databricks, or the
	// repos) is a scope too, its sources under it, so a search can stay on
	// one, like the repos (local, quick), without asking Azure for every
	// vault. With only one, it's the same as All.
	var groups []provider.Provider
	for _, p := range m.providers {
		if len(m.sourceResults[p.ID()].sources) > 0 {
			groups = append(groups, p)
		}
	}
	scopes := []option{all}
	if len(groups) < 2 {
		scopes = append(scopes, sources...)
	} else {
		for _, p := range groups {
			k := p.Kind()
			scopes = append(scopes, option{
				label: m.st.Kind(k).Render(m.icons.Prefix(k) + p.Label()),
				id:    accountScope + p.ID(), kind: k, mark: m.coverageMark(ofProvider(p.ID())),
			})
			for _, o := range sources {
				if cachefile.ProviderOf(o.group) == p.ID() {
					o.label = "  " + o.label
					scopes = append(scopes, o)
				}
			}
		}
	}
	v.scopes, v.items = scopes, items
	v.src = max(0, slices.IndexFunc(v.scopes, func(o option) bool { return o.id == atSrc }))

	v.shown = v.match()
	if keep {
		v.shown = stableOrder(v.shown, prev)
		if i := slices.IndexFunc(v.shown, func(o option) bool { return o.id == atRes }); i >= 0 {
			v.res = i
		}
	}
	v.res = max(0, min(v.res, len(v.shown)-1))
}

// coverageMark is ○ ◐ ● for how much of the providers in is loaded.
func (m *Model) coverageMark(in providerFilter) string {
	switch c := m.coverageOf(in); {
	case c.complete():
		return m.st.Success.Render("●")
	case c.vaultsLoaded > 0:
		return m.st.Warning.Render("◐")
	}
	return m.st.Muted.Render("○")
}

// scopeAccount is the provider whose sources the scope covers, if it's
// an account's.
func (v *searchView) scopeAccount() (string, bool) {
	id, ok := strings.CutPrefix(v.scope().id, accountScope)
	if !ok || v.tab != tabSearch {
		return "", false
	}
	return id, true
}

// scopeFilter is what loading everything in the scope covers: an
// account's, else all of them.
func (v *searchView) scopeFilter() providerFilter {
	if id, ok := v.scopeAccount(); ok {
		return ofProvider(id)
	}
	return nil
}

// scopeName is the scope's label without colours or indent.
func scopeName(o option) string { return strings.TrimSpace(ansi.Strip(o.label)) }

// match lists the scope's entries matching the input, best first.
func (v *searchView) match() []option {
	var cand []option
	switch sc := v.scope(); sc.id {
	case scopeFavs:
		cand = v.favs
	case scopeRecent:
		cand = v.recent
	default:
		account, ofAccount := v.scopeAccount()
		for _, o := range v.items {
			if sc.id == allSources || ofAccount && cachefile.ProviderOf(o.group) == account || sc.group != "" && o.group == sc.group {
				cand = append(cand, o)
			}
		}
	}
	if v.picker != nil {
		cand = slices.DeleteFunc(slices.Clone(cand), func(o option) bool { _, ok := pickable(o); return !ok })
	}
	q := v.input.Value()
	if q == "" {
		return slices.Clone(cand)
	}
	items := make([]match.Item, len(cand))
	for i, o := range cand {
		label := ansi.Strip(o.label)
		start, end := nameRange(label)
		items[i] = match.Item{Text: strings.TrimSpace(label + " " + o.note), NameStart: start, NameEnd: end}
	}
	var out []option
	for _, h := range match.Rank(match.Parse(q), items) {
		o := cand[h.Index]
		// Hits in the label are highlighted; ones in the note just count.
		n := utf8.RuneCountInString(ansi.Strip(o.label))
		o.hits = nil
		for _, p := range h.Pos {
			if p < n {
				o.hits = append(o.hits, p)
			}
		}
		out = append(out, o)
	}
	return out
}

// nameRange is where the item's own name is in a Go to label, in runes:
// the last part of "sub / vault / secret · account", which matches count
// most for.
func nameRange(label string) (int, int) {
	end := len(label)
	if i := strings.LastIndex(label, " · "); i >= 0 {
		end = i
	}
	start := 0 // a source: all of it (an icon doesn't match letters)
	if i := strings.LastIndex(label[:end], " / "); i >= 0 {
		start = i + len(" / ")
	}
	return utf8.RuneCountInString(label[:start]), utf8.RuneCountInString(label[:end])
}

// refreshSearch updates an open search as lists arrive.
func (m *Model) refreshSearch() {
	if m.search != nil {
		m.fillSearch(true)
	}
}

// stableOrder puts entries shown before (prev) in their earlier order,
// new ones after them in the order given.
func stableOrder(opts []option, prev map[string]int) []option {
	out := slices.Clone(opts)
	slices.SortStableFunc(out, func(a, b option) int {
		ia, okA := prev[a.id]
		ib, okB := prev[b.id]
		switch {
		case okA && okB:
			return ia - ib
		case okA:
			return -1
		case okB:
			return 1
		}
		return 0
	})
	return out
}

func (m *Model) handleSearch(msg tea.KeyPressMsg) tea.Cmd {
	v := m.search
	km := m.keys
	// The actions shared by both modes, by their keys in the mode.
	type modeKeys struct{ down, up, sources, results, switchList, star, load, loadAll, sel key.Binding }
	k := modeKeys{km.GoDown, km.GoUp, km.GoSources, km.GoResults, km.GoSwitch, km.GoStar, km.GoLoad, km.GoLoadAll, km.GoSelect}
	if key.Matches(msg, km.Search) {
		return m.narrow()
	}
	if v.typing {
		k = modeKeys{km.PopupDown, km.PopupUp, km.SearchSources, km.SearchResults, km.SearchSwitch, km.PopupStar, km.SearchLoad, km.SearchLoadAll, km.PopupSelect}
		if key.Matches(msg, km.PopupClose) {
			// Back to browsing, the filter cleared.
			if v.input.Value() != "" {
				v.input.SetValue("")
				v.res = 0
				m.fillSearch(false)
			}
			return m.setTyping(false)
		}
	} else {
		switch {
		case key.Matches(msg, km.GoClose):
			m.search = nil
			return nil
		case key.Matches(msg, km.GoFilter):
			return m.setTyping(true)
		case key.Matches(msg, km.GoTop), key.Matches(msg, km.GoBottom):
			n := len(v.shown)
			if !v.results {
				n = len(v.scopes)
			}
			i := 0
			if key.Matches(msg, km.GoBottom) {
				i = max(0, n-1)
			}
			if v.results {
				v.res = i
			} else if v.src != i {
				v.src, v.res = i, 0
				m.fillSearch(false)
			}
			return nil
		case key.Matches(msg, km.Help):
			return m.showKeybindingsFor(keys.GoTo)
		case key.Matches(msg, km.GoNextTab), key.Matches(msg, km.GoPrevTab):
			d := 1
			if key.Matches(msg, km.GoPrevTab) {
				d = tabCount - 1
			}
			v.tab = (v.tab + d) % tabCount
			v.res, v.results = 0, true
			m.fillSearch(false)
			return nil
		}
	}
	inSearch := v.tab == tabSearch
	moveSrc := func(d int) {
		v.src = max(0, min(v.src+d, len(v.scopes)-1))
		v.res = 0
		m.fillSearch(false)
	}
	switch {
	case inSearch && key.Matches(msg, k.switchList):
		v.results = !v.results
		return nil
	case inSearch && key.Matches(msg, k.sources):
		v.results = false
		return nil
	case inSearch && key.Matches(msg, k.results):
		v.results = true
		return nil
	case key.Matches(msg, k.up):
		if v.results {
			v.res = max(0, v.res-1)
		} else {
			moveSrc(-1)
		}
		return nil
	case key.Matches(msg, k.down):
		if v.results {
			v.res = max(0, min(v.res+1, len(v.shown)-1))
		} else {
			moveSrc(1)
		}
		return nil
	case key.Matches(msg, k.sel):
		switch {
		case v.results && v.res < len(v.shown) && v.picker != nil:
			r, _ := pickable(v.shown[v.res])
			return v.picker.choose(m, r)
		case v.results && v.res < len(v.shown):
			m.search = nil
			return v.shown[v.res].run(m)
		case !v.results && v.picker != nil:
			v.results = true // a picker only narrows to the scope
		case !v.results && v.onSource():
			m.search = nil
			return v.scope().run(m)
		case !v.results:
			v.results = true // All: on to the results
		}
		return nil
	case inSearch && key.Matches(msg, k.load):
		if o, ok := v.selected(); ok && o.reload != nil {
			return o.reload(m) // the selected source, or a result's vault
		}
		// Nothing selected (no results yet, or All): the scope.
		if v.onSource() {
			return v.scope().reload(m)
		}
		return m.startCrawlOf(v.scopeFilter())
	case inSearch && key.Matches(msg, k.loadAll):
		return m.startCrawlOf(v.scopeFilter())
	case key.Matches(msg, k.star):
		if o, ok := v.selected(); ok && o.star != "" {
			m.toggleStarKey(o.star, o.starAccount)
			// The entry stays where it is, its ★ showing the change.
			on := m.store.Node(o.star).StarredFor(o.starAccount)
			for _, list := range [][]option{v.favs, v.recent} {
				for i := range list {
					if list[i].star == o.star && list[i].starAccount == o.starAccount {
						list[i].starred = on
					}
				}
			}
			m.fillSearch(true)
		}
		return nil
	}
	if !v.typing {
		return nil
	}
	before := v.input.Value()
	in, cmd := v.input.Update(msg)
	v.input = in
	if v.input.Value() != before {
		v.res = 0
		m.fillSearch(false)
		return tea.Batch(cmd, m.searchLoads())
	}
	return cmd
}

// narrow switches Search between All and the source Go to was opened
// from; from another tab, or another scope, it goes to Search on All.
func (m *Model) narrow() tea.Cmd {
	v := m.search
	src := 0
	if v.tab == tabSearch && v.scope().id == allSources {
		src = slices.IndexFunc(v.scopes, func(o option) bool { return o.id == v.here })
		if src < 0 {
			return nil
		}
	}
	v.tab, v.src, v.res, v.results = tabSearch, src, 0, true
	m.fillSearch(false)
	return m.searchLoads()
}

// searchLoads loads what a search in the Search tab covers, like the
// load-all key: everything on All, else the source picked. Only what
// isn't loaded yet, once a popup; repos load by themselves anyway.
func (m *Model) searchLoads() tea.Cmd {
	v := m.search
	if v.tab != tabSearch || strings.TrimSpace(v.input.Value()) == "" {
		return nil
	}
	sc := v.scope()
	if v.loaded[sc.id] {
		return nil
	}
	if v.loaded == nil {
		v.loaded = map[string]bool{}
	}
	v.loaded[sc.id] = true
	if _, ok := v.scopeAccount(); ok {
		return m.startCrawlOf(v.scopeFilter())
	}
	if !v.onSource() {
		return m.startCrawlOf(nil)
	}
	providerID, sourceID := splitSourceRow(sc.group)
	for _, src := range m.sourceResults[providerID].sources {
		if src.ID == sourceID {
			src.Provider, src.ProviderLabel = providerID, m.byID[providerID].Label()
			m.queueSource(src, false)
			return m.crawlStart()
		}
	}
	return nil
}

// selected is the entry under the cursor in the focused list (a scope
// that isn't one source is none).
func (v *searchView) selected() (option, bool) {
	if v.results {
		if v.res < len(v.shown) {
			return v.shown[v.res], true
		}
		return option{}, false
	}
	if v.onSource() {
		return v.scope(), true
	}
	return option{}, false
}

// toggleStarKey stars or unstars a store record for an account and
// re-sorts the panes.
func (m *Model) toggleStarKey(key, account string) {
	if err := m.store.Update(key, func(n *store.Node) { n.ToggleStar(account) }); err != nil {
		m.flash = flash{text: "saving: " + err.Error(), isErr: true}
		return
	}
	m.resortStarred()
}

// searchHint tops the results when what they come from isn't all
// loaded, saying how to load it.
func (m Model) searchHint() string {
	load, narrow := m.loadHint(), m.narrowHint()
	switch {
	case narrow == "":
		return load
	case load != "":
		narrow = " · " + narrow
	}
	return load + m.st.Muted.Render(narrow)
}

// narrowHint names the key for the other of All and the source Go to was
// opened from.
func (m Model) narrowHint() string {
	v := m.search
	if v.tab != tabSearch || m.crawl.running {
		return ""
	}
	if v.scope().id == v.here && v.here != "" {
		return withKey(m.keys.Search, "%s searches all")
	}
	i := slices.IndexFunc(v.scopes, func(o option) bool { return o.id == v.here })
	if i < 0 || v.scope().id != allSources {
		return ""
	}
	return withKey(m.keys.Search, "%s only "+scopeName(v.scopes[i]))
}

// loadHint says how much of the scope is loaded, and the key to load it.
func (m Model) loadHint() string {
	v := m.search
	if m.crawl.running {
		return m.st.Muted.Render(fmt.Sprintf("◌ loading… %d lists left", len(m.crawl.queue)+m.crawl.active))
	}
	if v.tab != tabSearch {
		return "" // from the store: nothing to load
	}
	if _, ok := v.scopeAccount(); ok || v.scope().id == allSources {
		c := m.coverageOf(v.scopeFilter())
		if c.complete() {
			return ""
		}
		return m.st.Warning.Render(fmt.Sprintf("⚠ %d/%d vaults loaded", c.vaultsLoaded, c.vaults) + withKey(m.loadAllKey(), " · %s loads everything"))
	}
	src := v.scopes[v.src]
	e, listed := m.cache.containers[src.group]
	name := ansi.Strip(src.label)
	if !listed {
		if m.crawl.failed[src.group] {
			return ""
		}
		return m.st.Warning.Render("⚠ not loaded" + withKey(m.loadKey(), " · %s loads "+name))
	}
	loaded := 0
	for _, c := range e.value {
		key := cachefile.ObjectsKey(c, provider.TypeSecret)
		if _, ok := m.cache.objects[key]; ok || m.crawl.failed[key] {
			loaded++
		}
	}
	if loaded == len(e.value) {
		return ""
	}
	return m.st.Warning.Render(fmt.Sprintf("⚠ %d/%d vaults loaded", loaded, len(e.value)) + withKey(m.loadKey(), " · %s loads "+name))
}

// loadKey and loadAllKey are the keys for loading in the current mode.
func (m Model) loadKey() key.Binding {
	if m.search.typing {
		return m.keys.SearchLoad
	}
	return m.keys.GoLoad
}

func (m Model) loadAllKey() key.Binding {
	if m.search.typing {
		return m.keys.SearchLoadAll
	}
	return m.keys.GoLoadAll
}

// searchWidth is most of the screen: paths are long.
func searchWidth(screen int) int {
	return max(40, min(screen-4, screen*9/10))
}

// searchViewBox draws the popup.
func (m Model) searchViewBox() string {
	v := m.search
	focused := !m.termBlurred
	w := searchWidth(m.w)
	popupW := w
	if v.picker != nil {
		w -= 4 // inside the picker's border, in the popup
	}
	listH := max(5, min(m.h-10, 14))
	if v.picker != nil {
		// What it's for, the help, a line or two on the key picked, the
		// tabs and a border more.
		listH = max(5, min(m.h-10-len(v.picker.lines)-len(v.picker.help)-8, 14))
	}

	srcW := 10
	for _, o := range v.scopes {
		srcW = max(srcW, ansi.StringWidth(ansi.Strip(o.label))+ansi.StringWidth(o.note)+12) // marks, gap, borders
	}
	srcW = min(srcW, (w-4)*2/5)
	resW := w - 4 - srcW
	if v.tab != tabSearch {
		resW = w - 4 // one list
	}

	row := func(o option, width int, cursor, active bool) string {
		mark := o.mark
		if mark == "" {
			mark = " "
		}
		star := "  "
		if o.starred {
			star = m.st.Favourite.Render("★ ")
		}
		label := panes.MarkMatches(o.label, o.hits, m.st.Match)
		text := withNote(m.st, mark+" "+star+label, o.note, width-4) // borders, cursor column
		if !cursor {
			return "  " + text
		}
		sel := m.st.Selected
		if !active || !focused {
			sel = m.st.SelectedBlurred
		}
		return " " + panes.Highlight("▸"+text, sel, width-3) // inside the borders
	}
	window := func(n, cursor, h int) (int, int) {
		first := max(0, min(cursor-h/2, n-h))
		return first, min(n, first+h)
	}
	inner := listH - 2

	// The picked source is a choice that stays, like a tab: it keeps the
	// full highlight while the results have the cursor.
	var left []string
	first, last := window(len(v.scopes), v.src, inner)
	for i := first; i < last; i++ {
		left = append(left, row(v.scopes[i], srcW, i == v.src, true))
	}
	var right []string
	resRows := inner
	if hint := m.searchHint(); hint != "" {
		right = append(right, " "+hint)
		resRows--
	}
	first, last = window(len(v.shown), v.res, resRows)
	for i := first; i < last; i++ {
		right = append(right, row(v.shown[i], resW, i == v.res, v.results))
	}
	if len(v.shown) == 0 {
		msg := "no matches"
		switch {
		case v.input.Value() == "" && v.scope().id == scopeFavs:
			msg = "nothing starred yet" + withKey(m.keys.Favourite, ": %s in the panes stars the selected item")
		case v.input.Value() == "" && v.scope().id == scopeRecent:
			msg = "nothing used yet"
		case v.input.Value() == "":
			msg = "nothing loaded here yet"
		case v.tab == tabSearch && v.scope().id != allSources:
			msg += " here · All searches everything"
		case v.tab != tabSearch:
			msg += withKey(m.keys.GoNextTab, " here · Search (%s) looks in everything loaded")
		}
		right = append(right, "  "+m.st.Muted.Render(msg))
	}
	counter := func(i, n int) string {
		if n == 0 {
			return ""
		}
		return fmt.Sprintf("%d of %d", i+1, n)
	}
	title := func(s string, active bool) string { return panes.TitleStyle(m.st, active && focused).Render(s) }
	resultsTitle := map[string]string{scopeFavs: "Favourites", scopeRecent: "Recent", allSources: "Results in all sources"}[v.scope().id]
	if _, ok := v.scopeAccount(); ok || v.onSource() {
		resultsTitle = "Results in " + scopeName(v.scope())
	}
	var lists string
	if v.tab == tabSearch {
		lists = lipgloss.JoinHorizontal(lipgloss.Top,
			panes.BoxCounted(m.st, title("Scope", !v.results), "", counter(v.src, len(v.scopes)), left, srcW, listH, !v.results && focused),
			panes.BoxCounted(m.st, title(resultsTitle, v.results), "", counter(v.res, len(v.shown)), right, resW, listH, v.results && focused),
		)
	} else {
		lists = panes.BoxCounted(m.st, title(resultsTitle, true), "", counter(v.res, len(v.shown)), right, resW, listH, focused)
	}

	ts := panes.TitleStyle(m.st, focused)
	var body []string
	if p := v.picker; p != nil {
		for _, l := range p.help {
			body = append(body, " "+l)
		}
		switch {
		case p.busy != "":
			body = append(body, "", " "+m.st.Notice.Render(p.busy))
		case p.err != "":
			body = append(body, "")
			for _, l := range strings.Split(ansi.Wordwrap(p.err, w-4, " "), "\n") {
				body = append(body, " "+m.st.Error.Render(l))
			}
		}
		body = append(body, "", " "+m.goToTabs(ts))
	}
	body = append(body, " "+v.input.View(), "")
	for _, l := range strings.Split(lists, "\n") {
		body = append(body, " "+l)
	}
	body = append(body, "")
	if v.tab == tabSearch {
		for _, l := range m.searchLines() {
			body = append(body, " "+l)
		}
	} else {
		body = append(body, "") // the same height on every tab
	}
	km := m.keys
	var foot string
	switch {
	case v.typing:
		hints := []keyHint{move(km.PopupUp, km.PopupDown)}
		if v.tab == tabSearch {
			hints = append(hints, keyHint{joinKeys(km.SearchSources, km.SearchResults), "switch list"}, hint(km.SearchLoad, "load"))
		}
		foot = footer(append(hints, hint(km.PopupStar, "star"), hint(km.PopupSelect, v.selectLabel()), hint(km.PopupClose, "stop typing"))...)
	default:
		hints := []keyHint{move(km.GoUp, km.GoDown), {joinKeys(km.GoPrevTab, km.GoNextTab), "tab"}}
		if v.tab == tabSearch {
			hints = append(hints, keyHint{joinKeys(km.GoSources, km.GoResults), "list"}, hint(km.GoLoad, "load"), hint(km.GoLoadAll, "load all"))
		}
		foot = footer(append(hints, hint(km.GoFilter, "filter"), hint(km.GoStar, "star"), hint(km.GoSelect, v.selectLabel()), hint(km.GoClose, "close"))...)
	}
	// Tabs in the title, the current one underlined, like the panes; a
	// picker has its own title, and the tabs over the input.
	if v.picker == nil {
		head := ts.Render(goToTitle+" ") + m.goToTabs(ts)
		return panes.Box(m.st, head, foot, body, w, len(body)+2, focused)
	}
	// A picker: in its own border, under what it's for.
	var outer []string
	for _, l := range v.picker.lines {
		outer = append(outer, " "+l)
	}
	outer = append(outer, "")
	for _, l := range strings.Split(panes.Box(m.st, ts.Render(v.picker.heading), "", body, w, len(body)+2, focused), "\n") {
		outer = append(outer, " "+l)
	}
	return panes.Box(m.st, ts.Render(v.picker.title), foot, outer, popupW, len(outer)+2, focused)
}

// picker is Go to picking a secret for something (the key to share
// with): it lists secrets only, and enter hands the selected one's link
// to pick, which closes Go to when it's done with it.
type picker struct {
	pick    func(m *Model, r ref.Ref) tea.Cmd
	title   string   // the popup's
	lines   []string // what picking is for, above the picker
	heading string   // the picker's title, on its border
	help    []string // inside it, above the tabs
	busy    string   // what the pick is doing (picks wait meanwhile)
	err     string   // why the secret picked can't be used
}

// choose hands r to the picker, unless it's still busy with one.
func (p *picker) choose(m *Model, r ref.Ref) tea.Cmd {
	if p.busy != "" {
		return nil
	}
	return p.pick(m, r)
}

// selectLabel names what enter does with the selected entry.
func (v *searchView) selectLabel() string {
	if v.picker != nil {
		return "pick"
	}
	return "go"
}

// pickable is the link of a Go to entry that a picker can pick: a
// secret (not a key or certificate, nor a key in a plain env file).
func pickable(o option) (ref.Ref, bool) {
	r, err := ref.Parse(o.star)
	if err != nil || o.plain || r.IsShare() || r.IsSource() || r.IsContainer() || (r.Type != "" && r.Type != provider.TypeSecret) {
		return ref.Ref{}, false
	}
	return r, true
}

// goToTabs draws Go to's tabs, the current one underlined.
func (m Model) goToTabs(ts lipgloss.Style) string {
	var out string
	for i, name := range tabNames {
		if i > 0 {
			out += ts.Render(" - ")
		}
		if i == m.search.tab {
			out += ts.Underline(true).Render(name)
		} else {
			out += m.st.Muted.Render(name)
		}
	}
	return out
}
