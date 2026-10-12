// Package panes contains the lazygit-style bordered panes.
package panes

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// Box draws a rounded border of exactly w×h cells with the title embedded in
// the top edge and an optional footer in the bottom edge. The title is
// rendered as given, so callers style it (see TitleStyle). Body lines are
// truncated or padded to fit; extra lines are dropped.
func Box(st styles.Styles, title, footer string, body []string, w, h int, focused bool) string {
	return BoxCounted(st, title, footer, "", body, w, h, focused)
}

// BoxCounted is Box with a counter like "3 of 12" at the right end of the
// bottom edge, where lazygit puts it.
func BoxCounted(st styles.Styles, title, footer, counter string, body []string, w, h int, focused bool) string {
	if w < 4 || h < 2 {
		return ""
	}
	border := st.BorderBlurred
	if focused {
		border = st.BorderFocused
	}
	plain := func(s ...string) string { return strings.Join(s, "") }
	inner := w - 2

	edge := func(left, right, label string, styled func(...string) string) string {
		if label == "" {
			return border.Render(left + strings.Repeat("─", inner) + right)
		}
		label = ansi.Truncate(" "+label+" ", inner-1, "…")
		fill := inner - 1 - ansi.StringWidth(label)
		return border.Render(left+"─") + styled(label) + border.Render(strings.Repeat("─", fill)+right)
	}

	var b strings.Builder
	b.WriteString(edge("╭", "╮", title, plain))
	side := border.Render("│")
	for i := range h - 2 {
		line := ""
		if i < len(body) {
			line = ansi.Truncate(body[i], inner, "…")
		}
		b.WriteString("\n" + side + line + strings.Repeat(" ", inner-ansi.StringWidth(line)) + side)
	}
	bottom := edge("╰", "╯", footer, border.Render)
	if counter != "" {
		// Overwrite the end of the bottom edge: "… 3 of 12 ─╯".
		label := " " + counter + " "
		if keep := w - 2 - ansi.StringWidth(label); keep > 2 {
			bottom = ansi.Truncate(bottom, keep, "") + border.Render(label+"─╯")
		}
	}
	b.WriteString("\n" + bottom)
	return b.String()
}

// TitleStyle returns the style for a pane title.
func TitleStyle(st styles.Styles, focused bool) lipgloss.Style {
	if focused {
		return st.TitleFocused
	}
	return st.TitleBlurred
}
