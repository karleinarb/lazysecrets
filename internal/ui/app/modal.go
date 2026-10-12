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

	"git.kebcloud.no/keb/lazysecrets/internal/match"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/keys"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// A modal is a centred popup that takes the keyboard, like lazygit's menus:
// a list of options, each with a shortcut key, or a text input.

type option struct {
	key   string
	label string
	tip   string // shown under the list while the option is selected
	note  string // dimmed on the right of the row; the filter matches it
	// star is the store key ctrl+f stars or unstars (favourites, search);
	// starred shows a ★ before the label.
	star        string
	starAccount string // the account the star is for
	starred     bool
	// mark, if set, goes first: how much is loaded (search). reload,
	// if set, runs on ctrl+r.
	mark   string
	reload func(m *Model) tea.Cmd
	id     string        // identifies the entry across rebuilds (search)
	group  string        // the source row it's in (search)
	kind   provider.Kind // the kind of provider it's from (search)
	hits   []int         // characters of the label the filter matched (Go to)
	run    func(m *Model) tea.Cmd
	// header makes this a section title: not selectable, and hidden
	// while a filter narrows the list.
	header bool
}

type modal struct {
	title   string
	lines   []string // explanation above the options
	about   *ref.Ref // drawn above lines, redrawn as lists load (refLabel)
	options []option
	cursor  int

	// A text input instead of options; submit runs on enter.
	input  *textinput.Model
	submit func(m *Model, text string) tea.Cmd

	// A filter over the options: typing narrows them (all holds them
	// all), so moving uses arrows or ctrl+j/k.
	filter *textinput.Model
	all    []option

	footer  string         // overrides the default footer
	onClose func(m *Model) // runs when it closes, picked or not
}

// showFilterMenu opens a menu with a filter input above the options.
func (m *Model) showFilterMenu(title string, options []option) tea.Cmd {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "type to filter"
	in.SetWidth(40)
	m.prompt = &modal{title: title, options: options, all: options, filter: &in}
	m.prompt.move(0)
	m.flash = flash{}
	return m.prompt.filter.Focus()
}

// move moves the cursor by delta, skipping section headers; with nothing
// selectable that way it stays put. move(0) settles on a selectable option.
func (p *modal) move(delta int) {
	ok := func(i int) bool { return i >= 0 && i < len(p.options) && !p.options[i].header }
	step := 1
	if delta < 0 {
		step = -1
	}
	i := max(0, min(p.cursor+delta, len(p.options)-1))
	for i >= 0 && i < len(p.options) && !ok(i) {
		i += step
	}
	switch {
	case ok(i):
		p.cursor = i
	case !ok(p.cursor): // e.g. after filtering: first selectable
		for p.cursor = 0; p.cursor < len(p.options) && !ok(p.cursor); p.cursor++ {
		}
	}
}

// counter is "3 of 12" over the selectable options.
func (p *modal) counter() string {
	n, at := 0, 0
	for i, o := range p.options {
		if o.header {
			continue
		}
		n++
		if i == p.cursor {
			at = n
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d", at, n)
}

// refilter narrows the options to fuzzy matches of the filter. Matches
// stay under their section headers, best first within each section, and
// sections without matches drop out.
func (p *modal) refilter() {
	q := p.filter.Value()
	if q == "" {
		p.options = p.all
		p.cursor = max(0, min(p.cursor, len(p.options)-1))
		p.move(0)
		return
	}
	items := make([]match.Item, len(p.all))
	for i, o := range p.all {
		label := ansi.Strip(o.label)
		items[i] = match.Item{Text: strings.TrimSpace(label + " " + o.note), NameEnd: utf8.RuneCountInString(label)}
	}
	rank := map[int]int{} // matching options: their place in the ranking
	for n, h := range match.Rank(match.Parse(q), items) {
		if !p.all[h.Index].header {
			rank[h.Index] = n
		}
	}

	p.options = nil
	var section []int
	header := -1
	flush := func() {
		if len(section) == 0 {
			return
		}
		slices.SortStableFunc(section, func(a, b int) int { return rank[a] - rank[b] })
		if header >= 0 {
			p.options = append(p.options, p.all[header])
		}
		for _, i := range section {
			p.options = append(p.options, p.all[i])
		}
	}
	for i, o := range p.all {
		if o.header {
			flush()
			section, header = nil, i
			continue
		}
		if _, ok := rank[i]; ok {
			section = append(section, i)
		}
	}
	flush()
	p.cursor = max(0, min(p.cursor, len(p.options)-1))
	p.move(0)
}

// showMenu opens a menu. Every menu ends with an x Dismiss row.
func (m *Model) showMenu(title string, lines []string, options []option) {
	options = append(options, option{key: "x", label: "Dismiss", run: func(*Model) tea.Cmd { return nil }})
	m.prompt = &modal{title: title, lines: lines, options: options}
	m.flash = flash{}
}

// handleModal routes keys while a modal is shown.
func (m *Model) handleModal(msg tea.KeyPressMsg) tea.Cmd {
	p := m.prompt
	km := m.keys
	textField := p.input != nil || p.filter != nil
	closeIt := func() {
		m.prompt = nil
		if p.onClose != nil {
			p.onClose(m)
		}
	}
	if (textField && key.Matches(msg, km.PopupClose)) || (!textField && key.Matches(msg, km.MenuClose)) {
		closeIt()
		return nil
	}
	if p.input != nil {
		if key.Matches(msg, km.PopupSelect) {
			m.prompt = nil
			return p.submit(m, p.input.Value())
		}
		in, cmd := p.input.Update(msg)
		p.input = &in
		return cmd
	}
	if p.filter != nil {
		switch {
		case key.Matches(msg, km.PopupDown):
			p.move(1)
		case key.Matches(msg, km.PopupUp):
			p.move(-1)
		case key.Matches(msg, km.PopupSelect):
			if len(p.options) > 0 && !p.options[p.cursor].header {
				closeIt()
				return p.options[p.cursor].run(m)
			}
		default:
			in, cmd := p.filter.Update(msg)
			p.filter = &in
			p.refilter()
			return cmd
		}
		return nil
	}
	// A menu: each entry's letter picks it (fixed, part of its label),
	// before the moving keys.
	for _, o := range p.options {
		if o.key != "" && o.key == msg.String() {
			m.prompt = nil
			return o.run(m)
		}
	}
	switch {
	case key.Matches(msg, km.Help):
		return m.showKeybindingsFor(keys.Menu)
	case key.Matches(msg, km.MenuDown):
		p.cursor = min(p.cursor+1, len(p.options)-1)
	case key.Matches(msg, km.MenuUp):
		p.cursor = max(p.cursor-1, 0)
	case key.Matches(msg, km.MenuSelect):
		m.prompt = nil
		return p.options[p.cursor].run(m)
	}
	return nil
}

// modalView draws the modal as a box sized to its content.
func (m Model) modalView() string {
	p := m.prompt
	var body []string
	if p.about != nil {
		body = append(body, " "+m.refLabel(*p.about))
	}
	for _, l := range p.lines {
		body = append(body, " "+l)
	}
	if (len(p.lines) > 0 || p.about != nil) && (len(p.options) > 0 || p.input != nil) {
		body = append(body, "")
	}
	km := m.keys
	foot := footer(move(km.MenuUp, km.MenuDown), hint(km.MenuSelect, "select"), hint(km.MenuClose, "dismiss"))
	if p.input != nil {
		body = append(body, " "+p.input.View())
		foot = footer(hint(km.PopupSelect, "open"), hint(km.PopupClose, "cancel"))
	}

	// Size from all options, not the ones shown or the cursor's, so the
	// popup doesn't change shape while moving or filtering.
	all := p.all
	if all == nil {
		all = p.options
	}
	optText := func(o option) string {
		pre := ""
		if o.mark != "" {
			pre = o.mark + " "
		}
		if o.star != "" {
			mark := "  "
			if o.starred {
				mark = m.st.Favourite.Render(star)
			}
			return pre + mark + o.label
		}
		if o.key == "" {
			return o.label
		}
		return m.st.Label.Render(o.key) + "  " + o.label
	}
	optW := 0
	for _, o := range all {
		if !o.header {
			w := ansi.StringWidth(optText(o)) + 2 // "▸ " or "  "
			if o.note != "" {
				w += 2 + ansi.StringWidth(o.note)
			}
			optW = max(optW, w)
		}
	}
	optW = min(optW, max(0, m.w-8)) // notes give way to the screen
	row := func(o option) string {
		return withNote(m.st, optText(o), o.note, optW-2)
	}

	const window = 15 // rows of a filtered list
	options, first, rows := p.options, 0, len(p.options)
	if p.filter != nil {
		body = append(body, " "+p.filter.View(), "")
		foot = footer(move(km.PopupUp, km.PopupDown), hint(km.PopupSelect, "jump"), hint(km.PopupClose, "close"))
		rows = min(len(all), window)
		first = max(0, min(p.cursor-window/2, len(options)-window))
		options = options[first:min(len(options), first+window)]
		if len(options) == 0 {
			body = append(body, " "+m.st.Muted.Render("no matches"))
			rows--
		}
	}
	var headers []int // body lines that are section headers, drawn once w is known
	for j, o := range options {
		if o.header {
			if j > 0 {
				body = append(body, "")
			}
			headers = append(headers, len(body))
			body = append(body, o.label)
			continue
		}
		if i := first + j; i == p.cursor {
			sel := m.st.Selected
			if m.termBlurred {
				sel = m.st.SelectedBlurred
			}
			body = append(body, " "+panes.Highlight("▸ "+row(o), sel, optW))
		} else {
			body = append(body, " "+"  "+row(o))
		}
	}
	for range rows - len(options) {
		body = append(body, "") // keep the height while filtering
	}

	if p.footer != "" {
		foot = p.footer
	}
	w := max(ansi.StringWidth(p.title)+6, optW+4, ansi.StringWidth(foot)+6)
	for _, l := range body {
		w = max(w, ansi.StringWidth(l)+3)
	}
	if p.input != nil || p.filter != nil {
		w = max(w, min(m.w-4, 72))
	}
	w = min(w, m.w-2)

	// Section headers as rules: "─── Secrets pane ─────────".
	for _, i := range headers {
		title := body[i]
		fill := max(1, w-2-2-3-2-ansi.StringWidth(title)) // borders, margins, "───", spaces
		body[i] = " " + m.st.Muted.Render("─── ") + panes.TitleStyle(m.st, !m.termBlurred).Render(title) +
			m.st.Muted.Render(" "+strings.Repeat("─", fill))
	}

	// The selected option's tip, wrapped; room for the longest one is kept
	// so the popup doesn't change height while moving.
	wrapTip := func(tip string) []string {
		if tip == "" {
			return nil
		}
		return strings.Split(ansi.Wordwrap(tip, w-4, " "), "\n")
	}
	tipRows := 0
	for _, o := range all {
		tipRows = max(tipRows, len(wrapTip(o.tip)))
	}
	if tipRows > 0 {
		var tip []string
		if p.cursor < len(p.options) {
			tip = wrapTip(p.options[p.cursor].tip)
		}
		body = append(body, "")
		for i := range tipRows {
			line := ""
			if i < len(tip) {
				line = " " + m.st.Muted.Render(tip[i])
			}
			body = append(body, line)
		}
	}
	counter := ""
	if p.filter != nil {
		counter = p.counter()
	}
	focused := !m.termBlurred // like the panes, no highlight in an unfocused window
	return panes.BoxCounted(m.st, panes.TitleStyle(m.st, focused).Render(p.title), foot, counter, body, w, len(body)+2, focused)
}

// overlay draws the modal centred over the screen.
func (m Model) overlay(base string) string {
	var box string
	if m.search != nil {
		box = m.searchViewBox()
	} else {
		box = m.modalView()
	}
	x := max(0, (m.w-lipgloss.Width(box))/2)
	y := max(0, (strings.Count(base, "\n")+1-lipgloss.Height(box))/3)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}
