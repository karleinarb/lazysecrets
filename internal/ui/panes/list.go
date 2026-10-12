package panes

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/match"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// Item adapts a value for display in a List.
type Item[T any] struct {
	ID     func(T) string // stable identity, used to detect selection changes
	Filter func(T) string // text matched by the filter, and highlighted
	// FilterExtra, if set, is matched too but not shown highlighted, for
	// text the row doesn't show (e.g. the provider of a grouped source).
	FilterExtra func(T) string
	Render      func(st styles.Styles, v T, width int) string
}

// List is a scrollable, filterable list pane with loading and error states.
type List[T any] struct {
	Title string
	// Tabs, when there are several, are shown after the title; Tab is the
	// active one.
	Tabs    []string
	Tab     int
	Empty   string // shown when loaded with no items
	Loading bool
	Err     error
	// Age says how old the items are ("2h ago") while they reload, by
	// the counter.
	Age string
	// Header lines (a logo) go above the items while the cursor is in
	// the rows under it, the pane has room and there's no filter; else
	// HeaderShort, a line of plain text, does.
	Header      []string
	HeaderShort string

	item    Item[T]
	items   []T
	visible []int   // indices into items that match the filter, best first
	hits    [][]int // per visible item: matched rune indices in its Filter text
	cursor  int     // index into visible
	offset  int     // first visible row
	w, h    int     // outer size including border

	input     textinput.Model
	filtering bool
}

func NewList[T any](title string, item Item[T]) List[T] {
	in := textinput.New()
	in.Prompt = "/"
	return List[T]{Title: title, Empty: "nothing here", item: item, input: in}
}

// SetRender changes how items are drawn, e.g. when the pane switches to
// showing versions.
func (l *List[T]) SetRender(render func(st styles.Styles, v T, width int) string) {
	l.item.Render = render
}

// SetItems replaces the contents, keeping the cursor on the same item if it
// is still present.
func (l *List[T]) SetItems(items []T) {
	prev := l.SelectedID()
	l.items = items
	l.Loading = false
	l.Err = nil
	l.applyFilter(prev)
}

// Reset clears items, error, filter and cursor.
func (l *List[T]) Reset() {
	l.items, l.visible = nil, nil
	l.cursor, l.offset = 0, 0
	l.Loading, l.Err = false, nil
	l.filtering = false
	l.input.Reset()
}

func (l *List[T]) SetError(err error) {
	l.Reset()
	l.Err = err
}

// Refresh marks the pane as loading while keeping the current items and
// selection on screen until SetItems replaces them.
func (l *List[T]) Refresh() {
	l.Loading = true
	l.Err = nil
	l.Age = ""
}

func (l *List[T]) SetLoading() {
	l.Reset()
	l.Loading = true
}

func (l List[T]) Selected() (T, bool) {
	if l.cursor < 0 || l.cursor >= len(l.visible) {
		var zero T
		return zero, false
	}
	return l.items[l.visible[l.cursor]], true
}

// SelectedID is "" when nothing is selected.
func (l List[T]) SelectedID() string {
	v, ok := l.Selected()
	if !ok {
		return ""
	}
	return l.item.ID(v)
}

func (l List[T]) Len() int { return len(l.visible) }

// Width is the pane's outer width.
func (l List[T]) Width() int { return l.w }

// Items returns all items, ignoring the filter.
func (l List[T]) Items() []T { return l.items }

// Select moves the cursor to the item with the given ID, if visible, and
// reports whether it was.
func (l *List[T]) Select(id string) bool {
	return l.SelectWhere(func(v T) bool { return l.item.ID(v) == id })
}

// SelectWhere moves the cursor to the first visible item matching f.
func (l *List[T]) SelectWhere(f func(T) bool) bool {
	for i, idx := range l.visible {
		if f(l.items[idx]) {
			l.cursor = i
			l.scroll()
			return true
		}
	}
	return false
}

func (l *List[T]) Move(delta int) {
	l.cursor = max(0, min(l.cursor+delta, len(l.visible)-1))
	l.scroll()
}

func (l *List[T]) Top()    { l.Move(-len(l.visible)) }
func (l *List[T]) Bottom() { l.Move(len(l.visible)) }

// SetSize sets the outer size of the pane, including its border.
func (l *List[T]) SetSize(w, h int) {
	l.w, l.h = w, h
	l.input.SetWidth(max(1, w-4))
	l.scroll()
}

// rows is the number of item rows that fit in the pane.
func (l List[T]) rows() int {
	r := l.h - 2
	if l.filtering {
		r--
	}
	r -= len(l.header())
	return max(0, r)
}

// headerMinRows is how many item rows Header leaves at least.
const headerMinRows = 5

// header is the lines above the items: Header and a blank line, if it
// fits, leaves headerMinRows and the cursor is in the rows under it;
// else HeaderShort.
func (l List[T]) header() []string {
	short := []string{}
	if l.HeaderShort != "" {
		short = []string{l.HeaderShort}
	}
	if len(l.Header) == 0 || l.filtering || l.input.Value() != "" {
		return short
	}
	for _, line := range l.Header {
		if ansi.StringWidth(line) > l.w-3 {
			return short
		}
	}
	below := l.h - 2 - len(l.Header) - 1
	if below < headerMinRows || l.cursor >= below {
		return short
	}
	return append(l.Header[:len(l.Header):len(l.Header)], "")
}

// Filtering reports whether the filter input has focus.
func (l List[T]) Filtering() bool { return l.filtering }

// HasFilter reports whether a filter query is applied.
func (l List[T]) HasFilter() bool { return l.input.Value() != "" }

func (l *List[T]) StartFilter() tea.Cmd {
	l.filtering = true
	l.scroll()
	return l.input.Focus()
}

// AcceptFilter leaves filter mode, keeping the query applied.
func (l *List[T]) AcceptFilter() {
	l.filtering = false
	l.input.Blur()
}

// ClearFilter leaves filter mode and removes the query.
func (l *List[T]) ClearFilter() {
	l.filtering = false
	l.input.Blur()
	prev := l.SelectedID()
	l.input.Reset()
	l.applyFilter(prev)
}

// UpdateFilter forwards a message to the filter input and re-filters.
func (l *List[T]) UpdateFilter(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	prev := l.SelectedID()
	l.input, cmd = l.input.Update(msg)
	l.applyFilter(prev)
	return cmd
}

// applyFilter recomputes the visible items and puts the cursor back on the
// item with ID keep if it is still visible.
func (l *List[T]) applyFilter(keep string) {
	l.visible, l.hits = l.visible[:0], nil
	if q := l.input.Value(); q == "" {
		for i := range l.items {
			l.visible = append(l.visible, i)
		}
	} else {
		// The row's own text is the name; FilterExtra (a note, a provider)
		// is context, matched too but ranked lower.
		items := make([]match.Item, len(l.items))
		for i, v := range l.items {
			text := l.item.Filter(v)
			items[i] = match.Item{Text: text, NameEnd: utf8.RuneCountInString(text)}
			if l.item.FilterExtra != nil {
				if extra := l.item.FilterExtra(v); extra != "" {
					items[i].Text += " " + extra
				}
			}
		}
		for _, h := range match.Rank(match.Parse(q), items) {
			var hits []int // the ones in the text the row shows
			for _, p := range h.Pos {
				if p < items[h.Index].NameEnd {
					hits = append(hits, p)
				}
			}
			l.visible = append(l.visible, h.Index)
			l.hits = append(l.hits, hits)
		}
	}
	l.cursor = max(0, min(l.cursor, len(l.visible)-1))
	if keep != "" {
		l.Select(keep)
	}
	l.scroll()
}

// MarkMatches styles the characters at hits (rune indexes in the text
// shown, without styling) in a styled string, keeping its own colours.
func MarkMatches(styled string, hits []int, style lipgloss.Style) string {
	if len(hits) == 0 {
		return styled
	}
	return lipgloss.StyleRanges(styled, matchRanges(ansi.Strip(styled), hits, style)...)
}

// matchRanges turns matched rune indexes in plain into ranges of screen
// cells, merging neighbours, for lipgloss.StyleRanges.
func matchRanges(plain string, hits []int, style lipgloss.Style) []lipgloss.Range {
	runes := []rune(plain)
	cell := func(i int) int { return ansi.StringWidth(string(runes[:min(i, len(runes))])) }
	var out []lipgloss.Range
	for i := 0; i < len(hits); {
		j := i
		for j+1 < len(hits) && hits[j+1] == hits[j]+1 {
			j++
		}
		if hits[i] < len(runes) {
			out = append(out, lipgloss.NewRange(cell(hits[i]), cell(hits[j]+1), style))
		}
		i = j + 1
	}
	return out
}

// RuneIndexes converts byte offsets in s to rune offsets.
func RuneIndexes(s string, bytes []int) []int {
	out := make([]int, 0, len(bytes))
	for _, b := range bytes {
		out = append(out, utf8.RuneCountInString(s[:b]))
	}
	return out
}

// highlight returns the rune indexes to highlight in a row's plain text:
// the filter hits, shifted to where the Filter text appears in the row.
// It returns nil if the row doesn't contain the Filter text verbatim.
func (l List[T]) highlight(i int, plain string) []int {
	if i >= len(l.hits) {
		return nil
	}
	off := strings.Index(plain, l.item.Filter(l.items[l.visible[i]]))
	if off < 0 {
		return nil
	}
	shift := utf8.RuneCountInString(plain[:off])
	out := make([]int, len(l.hits[i]))
	for j, h := range l.hits[i] {
		out[j] = h + shift
	}
	return out
}

// View renders the pane at the size given to SetSize. spin is the current
// spinner frame.
func (l List[T]) View(st styles.Styles, focused bool, spin string) string {
	rows := l.rows()
	inner := max(0, l.w-2)

	ts := TitleStyle(st, focused)
	title := ts.Render(l.Title)
	if len(l.Tabs) > 1 {
		for i, tab := range l.Tabs {
			sep := " - "
			if i == 0 {
				sep = " "
			}
			if i == l.Tab {
				title += ts.Render(sep) + ts.Underline(true).Render(tab)
			} else {
				title += ts.Render(sep) + st.Muted.Render(tab)
			}
		}
	}
	if q := l.input.Value(); q != "" && !l.filtering {
		title += ts.Render(" /" + q)
	}
	if l.Loading && len(l.items) > 0 {
		title += ts.Render(" " + spin)
	}

	var body []string
	if l.filtering {
		body = append(body, l.input.View())
	}
	for _, line := range l.header() {
		body = append(body, st.Notice.Render(" "+line))
	}

	switch {
	case l.Loading && len(l.items) == 0:
		body = append(body, " "+spin+" loading…")
	case l.Err != nil:
		for _, line := range wrapLines(l.Err.Error(), inner-1) {
			body = append(body, st.Error.Render(" "+line))
		}
	case len(l.visible) == 0 && len(l.items) > 0:
		body = append(body, st.Muted.Render(" no matches"))
	case len(l.visible) == 0:
		body = append(body, st.Muted.Render(" "+l.Empty))
	default:
		for i := l.offset; i < len(l.visible) && i < l.offset+rows; i++ {
			line := l.item.Render(st, l.items[l.visible[i]], inner-1)
			plain := ansi.Strip(line)
			hits := l.highlight(i, plain)
			if i == l.cursor {
				base := st.SelectedBlurred
				if focused {
					base = st.Selected
				}
				text := " " + line
				if len(hits) > 0 {
					for j := range hits {
						hits[j]++ // leading space
					}
					text = lipgloss.StyleRanges(text, matchRanges(" "+plain, hits, st.Match)...)
				}
				line = Highlight(ansi.Truncate(text, inner, "…"), base, inner)
			} else if len(hits) > 0 {
				// Style just the matches, keeping the row's own colours.
				line = " " + lipgloss.StyleRanges(line, matchRanges(plain, hits, st.Match)...)
			} else {
				line = " " + line
			}
			body = append(body, line)
		}
	}

	counter := ""
	if len(l.visible) > 0 {
		counter = fmt.Sprintf("%d of %d", l.cursor+1, len(l.visible))
	}
	if l.Loading && l.Age != "" && len(l.items) > 0 {
		counter = "cached " + l.Age + " · " + counter
	}
	return BoxCounted(st, title, "", counter, body, l.w, l.h, focused)
}

// scroll keeps the cursor within the visible window.
func (l *List[T]) scroll() {
	rows := l.rows()
	if rows <= 0 {
		return
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+rows {
		l.offset = l.cursor - rows + 1
	}
	l.offset = max(0, min(l.offset, len(l.visible)-rows))
}

// wrapLines wraps text to width, line by line; a "• " item's further
// lines line up under its text.
func wrapLines(text string, width int) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		indent := ""
		if strings.HasPrefix(line, "• ") {
			indent = "  "
		}
		for i, l := range strings.Split(ansi.Wrap(line, max(1, width-len(indent)), " -/"), "\n") {
			if i > 0 {
				l = indent + l
			}
			out = append(out, l)
		}
	}
	return out
}
