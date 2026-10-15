package panes

import (
	"image/color"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var sgr = regexp.MustCompile("\x1b\\[([0-9;:]*)m")

// Highlight draws a cursor row: line, padded to width, on style's
// background, keeping the colours line already has (a provider's colour,
// a dimmed note), like LazyVim's and lazygit's selected lines; bold
// carries over too. A reverse style is applied to the plain text instead,
// since reversing coloured text would scramble it.
func Highlight(line string, style lipgloss.Style, width int) string {
	pad := strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	if style.GetReverse() {
		return style.Render(ansi.Strip(line) + pad)
	}
	var s ansi.Style
	if bg := style.GetBackground(); !isNoColor(bg) { // never nil: NoColor when unset
		s = s.BackgroundColor(bg)
	}
	if style.GetBold() {
		s = s.Bold()
	}
	if len(s) == 0 {
		return line + pad
	}
	on := s.String()
	// Every reset in line (a bare reset, one starting with 0, or a
	// background reset) would end the highlight: put it back after each.
	line = sgr.ReplaceAllStringFunc(line, func(seq string) string {
		params := sgr.FindStringSubmatch(seq)[1]
		for _, p := range strings.Split(params, ";") {
			if p == "" || p == "0" || p == "49" {
				return seq + on
			}
		}
		return seq
	})
	return on + line + pad + ansi.ResetStyle
}

func isNoColor(c color.Color) bool {
	_, ok := c.(lipgloss.NoColor)
	return ok
}
