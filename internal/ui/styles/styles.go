// Package styles holds the lipgloss theme. It sticks to the 16 ANSI colours
// so it follows the user's terminal palette in both light and dark themes.
package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

type Styles struct {
	BorderFocused lipgloss.Style
	BorderBlurred lipgloss.Style
	TitleFocused  lipgloss.Style
	TitleBlurred  lipgloss.Style
	Footer        lipgloss.Style

	Selected        lipgloss.Style // cursor row in the focused pane
	SelectedBlurred lipgloss.Style // cursor row in other panes
	Muted           lipgloss.Style
	Error           lipgloss.Style
	Notice          lipgloss.Style
	Success         lipgloss.Style // the ✓ before confirmations
	Warning         lipgloss.Style // "can't do that here" messages
	Match           lipgloss.Style // filter matches
	Favourite       lipgloss.Style
	Prompt          lipgloss.Style // questions in the footer
	Label           lipgloss.Style
	Masked          lipgloss.Style
	Value           lipgloss.Style

	KindAzure      lipgloss.Style
	KindDatabricks lipgloss.Style
	KindRepo       lipgloss.Style
	Spring         lipgloss.Style // Spring Boot config files
	Compose        lipgloss.Style // Docker Compose files
	Dotnet         lipgloss.Style // .NET appsettings files

	// themed marks the selection colours as set by the theme, so
	// OnBackground leaves them.
	themed, themedBlurred bool
}

func Default() Styles {
	green := lipgloss.Green
	return Styles{
		BorderFocused: lipgloss.NewStyle().Foreground(green),
		BorderBlurred: lipgloss.NewStyle().Faint(true),
		TitleFocused:  lipgloss.NewStyle().Foreground(green).Bold(true),
		TitleBlurred:  lipgloss.NewStyle(),
		Footer:        lipgloss.NewStyle().Faint(true),

		// A background rather than reverse, so the row keeps its colours;
		// in every pane, so each shows what's selected in it. The focused
		// pane's is bold too. Palette 8 until the terminal says what its
		// background is (OnBackground).
		Selected:        lipgloss.NewStyle().Background(lipgloss.BrightBlack).Bold(true),
		SelectedBlurred: lipgloss.NewStyle().Background(lipgloss.BrightBlack),
		Muted:           lipgloss.NewStyle().Faint(true),
		Error:           lipgloss.NewStyle().Foreground(lipgloss.Red),
		Notice:          lipgloss.NewStyle().Foreground(lipgloss.Green),
		Success:         lipgloss.NewStyle().Foreground(lipgloss.Green).Bold(true),
		Warning:         lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		Match:           lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true),
		Favourite:       lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		Prompt:          lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true),
		Label:           lipgloss.NewStyle().Foreground(lipgloss.Blue).Bold(true),
		Masked:          lipgloss.NewStyle().Faint(true),
		Value:           lipgloss.NewStyle().Foreground(lipgloss.Yellow),

		KindAzure:      lipgloss.NewStyle().Foreground(lipgloss.Blue),
		KindDatabricks: lipgloss.NewStyle().Foreground(lipgloss.Red),
		KindRepo:       lipgloss.NewStyle().Foreground(lipgloss.Magenta),
		Spring:         lipgloss.NewStyle().Foreground(lipgloss.Green),
		Compose:        lipgloss.NewStyle().Foreground(lipgloss.Blue),
		Dotnet:         lipgloss.NewStyle().Foreground(lipgloss.BrightMagenta),
	}
}

// Kind is the colour of a provider's sources, vaults and paths.
func (s Styles) Kind(k provider.Kind) lipgloss.Style {
	switch k {
	case provider.KindDatabricks:
		return s.KindDatabricks
	case provider.KindRepo:
		return s.KindRepo
	}
	return s.KindAzure
}

// OnBackground shades the selection colours from the terminal's
// background, a little lighter on a dark one and darker on a light one:
// a quiet band that keeps the row's colours readable, where palette 8 is
// often too bright. Colours the theme sets are kept.
func (s Styles) OnBackground(bg color.Color, dark bool) Styles {
	shade := func(p float64) color.Color {
		if dark {
			return lipgloss.Lighten(bg, p)
		}
		return lipgloss.Darken(bg, p)
	}
	if !s.themed {
		s.Selected = s.Selected.Background(shade(0.09))
	}
	if !s.themedBlurred {
		s.SelectedBlurred = s.SelectedBlurred.Background(shade(0.05))
	}
	return s
}
