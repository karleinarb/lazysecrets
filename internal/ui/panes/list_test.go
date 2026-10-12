package panes

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

func newStringList(n int) List[string] {
	l := NewList("Items", Item[string]{
		ID:     func(s string) string { return s },
		Filter: func(s string) string { return s },
		Render: func(_ styles.Styles, s string, _ int) string { return s },
	})
	var items []string
	for i := range n {
		items = append(items, fmt.Sprintf("item-%02d", i))
	}
	l.SetItems(items)
	l.SetSize(20, 7) // 5 rows
	return l
}

func TestListScrollKeepsCursorVisible(t *testing.T) {
	l := newStringList(20)
	l.Move(9)
	view := ansi.Strip(l.View(styles.Default(), true, ""))
	if !strings.Contains(view, "item-09") || strings.Contains(view, "item-04") {
		t.Fatalf("cursor row not scrolled into view:\n%s", view)
	}
	// Moving up within the window must not scroll.
	l.Move(-2)
	view = ansi.Strip(l.View(styles.Default(), true, ""))
	if !strings.Contains(view, "item-05") {
		t.Fatalf("window jumped when moving up inside it:\n%s", view)
	}
	l.Top()
	if l.SelectedID() != "item-00" || l.offset != 0 {
		t.Fatalf("Top: selected %q offset %d", l.SelectedID(), l.offset)
	}
	l.Bottom()
	if l.SelectedID() != "item-19" || l.offset != 15 {
		t.Fatalf("Bottom: selected %q offset %d", l.SelectedID(), l.offset)
	}
}

func TestListSetItemsKeepsSelection(t *testing.T) {
	l := newStringList(5)
	l.Move(3)
	l.SetItems([]string{"new", "item-03", "item-04"})
	if l.SelectedID() != "item-03" {
		t.Fatalf("selected = %q", l.SelectedID())
	}
	l.SetItems([]string{"other"})
	if l.SelectedID() != "other" {
		t.Fatalf("selected = %q after item vanished", l.SelectedID())
	}
}

func TestBoxExactSize(t *testing.T) {
	out := Box(styles.Default(), "a very long title that will not fit", "footer", []string{"x", strings.Repeat("y", 50)}, 12, 5, true)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("height = %d", len(lines))
	}
	for _, line := range lines {
		if w := ansi.StringWidth(line); w != 12 {
			t.Errorf("width %d: %q", w, ansi.Strip(line))
		}
	}
}

func filterList(t *testing.T, items []string, query string) List[string] {
	t.Helper()
	l := NewList("Items", Item[string]{
		ID:     func(s string) string { return s },
		Filter: func(s string) string { return s },
		Render: func(_ styles.Styles, s string, _ int) string { return s },
	})
	l.SetItems(items)
	l.SetSize(40, 10)
	l.StartFilter()
	for _, r := range query {
		l.UpdateFilter(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return l
}

func visibleNames(l List[string]) []string {
	var out []string
	for _, i := range l.visible {
		out = append(out, l.items[i])
	}
	return out
}

func TestFuzzyFilterRanksAndSkips(t *testing.T) {
	l := filterList(t, []string{"api-key", "db-password", "storage-key", "dbx-pat"}, "dbpw")
	if got := visibleNames(l); !reflect.DeepEqual(got, []string{"db-password"}) {
		t.Errorf("dbpw matched %v", got)
	}
	l = filterList(t, []string{"monkey", "key-vault", "api-key"}, "key")
	if got := visibleNames(l); got[0] != "key-vault" || len(got) != 3 {
		t.Errorf("prefix match not ranked first: %v", got)
	}
}

func TestFilterHighlightsMatches(t *testing.T) {
	l := filterList(t, []string{"db-password", "api-key"}, "dpw")
	l.AcceptFilter()
	view := l.View(styles.Default(), false, "")
	// d, p and w are styled; with ANSI stripped the text is unchanged.
	if !strings.Contains(ansi.Strip(view), "db-password") {
		t.Fatalf("row missing:\n%s", ansi.Strip(view))
	}
	if hits := l.highlight(0, "db-password"); !reflect.DeepEqual(hits, []int{0, 3, 7}) {
		t.Errorf("hits = %v", hits)
	}
	// Rows that show the Filter text at an offset get shifted hits.
	if hits := l.highlight(0, "  db-password (disabled)"); !reflect.DeepEqual(hits, []int{2, 5, 9}) {
		t.Errorf("shifted hits = %v", hits)
	}
}

func TestFilterKeepsRowColours(t *testing.T) {
	blue := lipgloss.NewStyle().Foreground(lipgloss.Blue)
	match := styles.Default().Match

	if got := matchRanges("platform-dev", []int{9, 10, 11}, match); len(got) != 1 || got[0].Start != 9 || got[0].End != 12 {
		t.Errorf("ranges = %+v, want one range 9-12", got)
	}

	// Only the matches change style; the rest keeps the row's colour.
	styled := "  " + blue.Render("platform-dev")
	out := lipgloss.StyleRanges(styled, matchRanges("  platform-dev", []int{11, 12, 13}, match)...)
	if ansi.Strip(out) != "  platform-dev" {
		t.Fatalf("text changed: %q", ansi.Strip(out))
	}
	if !strings.Contains(out, "\x1b[34m") {
		t.Errorf("unmatched text lost its colour: %q", out)
	}
}

func TestCounterInBottomRight(t *testing.T) {
	out := BoxCounted(styles.Default(), "T", "footer", "3 of 12", []string{"x"}, 30, 3, true)
	bottom := ansi.Strip(strings.Split(out, "\n")[2])
	if !strings.HasSuffix(bottom, " 3 of 12 ─╯") || !strings.HasPrefix(bottom, "╰─ footer ") || ansi.StringWidth(bottom) != 30 {
		t.Errorf("bottom edge = %q", bottom)
	}
}

func TestHighlightKeepsColours(t *testing.T) {
	blue := lipgloss.NewStyle().Foreground(lipgloss.Blue)
	sel := lipgloss.NewStyle().Background(lipgloss.BrightBlack).Bold(true)
	row := "  " + blue.Render("platform-prod") + lipgloss.NewStyle().Faint(true).Render(" note")
	out := Highlight(row, sel, 30)
	if ansi.StringWidth(out) != 30 || ansi.Strip(out) != ansi.Strip(row)+strings.Repeat(" ", 30-ansi.StringWidth(row)) {
		t.Fatalf("text or width changed: %q", ansi.Strip(out))
	}
	if !strings.Contains(out, "\x1b[34m") {
		t.Error("row lost its blue")
	}
	// The background is re-applied after every reset, so it spans the row.
	resets := strings.Count(out, "\x1b[m") + strings.Count(out, "\x1b[0m")
	if bgs := strings.Count(out, "\x1b[100;1m"); bgs < resets {
		t.Errorf("background not re-applied after each reset: %d resets, %d backgrounds\n%q", resets, bgs, out)
	}
	// Reverse styles fall back to plain text.
	if got := Highlight(row, lipgloss.NewStyle().Reverse(true), 30); strings.Contains(got, "34m") {
		t.Error("reverse kept the colours")
	}
}

func TestListHeader(t *testing.T) {
	l := NewList("t", Item[string]{
		ID:     func(s string) string { return s },
		Filter: func(s string) string { return s },
		Render: func(_ styles.Styles, s string, _ int) string { return s },
	})
	l.Header, l.HeaderShort = []string{"ART1", "ART2"}, "short"
	l.SetItems([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"})
	l.SetSize(30, 2+2+1+6) // the art, a blank line and 6 rows
	st := styles.Default()
	if v := l.View(st, true, ""); !strings.Contains(v, "ART1") || strings.Contains(v, "short") {
		t.Errorf("at the top, the art:\n%s", v)
	}
	l.Move(6) // below the rows under the art
	if v := l.View(st, true, ""); strings.Contains(v, "ART1") || !strings.Contains(v, "short") || !strings.Contains(v, " g") {
		t.Errorf("further down, the text:\n%s", v)
	}
	l.Top()
	l.SetSize(30, 8) // too short for the art and 5 rows
	if v := l.View(st, true, ""); strings.Contains(v, "ART1") || !strings.Contains(v, "short") {
		t.Errorf("no room, the text:\n%s", v)
	}
}
