package styles

import (
	"fmt"
	"image/color"
	"maps"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// A theme maps roles to a colour and attributes, like lazygit's gui.theme:
//
//	activeBorderColor:   ["#e0af68", bold]
//	selectedLineBgColor: ["#2d3f76"]
//
// Role names follow lazygit where the meaning is the same, so an editor
// plugin can map them from highlight groups the way snacks.nvim does for
// lazygit. Colours are hex (#rrggbb), a palette number (0-255) or a name
// (black, red, green, yellow, blue, magenta, cyan, white, their bright
// variants like brightred, or default). Attributes: bold, italic,
// underline, reverse, faint.

// Roles lists the theme roles with what they colour.
var Roles = map[string]string{
	"activeBorderColor":               "border and title of the focused pane",
	"inactiveBorderColor":             "borders of other panes",
	"selectedLineBgColor":             "background of the cursor row in the focused pane",
	"inactiveViewSelectedLineBgColor": "background of the cursor row in other panes",
	"defaultFgColor":                  "default text colour",
	"optionsTextColor":                "keys in the help bar and menus, and labels in the detail pane",
	"searchMatchColor":                "matched characters when filtering",
	"errorColor":                      "✗ errors",
	"noticeColor":                     "notices, the clipboard countdown and current versions",
	"successColor":                    "the ✓ before confirmations (copied, opened…)",
	"warningColor":                    "⚠ messages: what can't be done here, and why",
	"mutedColor":                      "secondary text",
	"favouriteColor":                  "stars and questions",
	"valueColor":                      "revealed values",
	"azureColor":                      "Azure sources and vaults",
	"databricksColor":                 "Databricks workspaces and scopes",
	"repoColor":                       "repos and their secret files (SOPS)",
	"springColor":                     "Spring Boot config files",
	"composeColor":                    "Docker Compose files",
	"dotnetColor":                     ".NET appsettings files",
}

// FromTheme returns the default styles with the theme's roles applied, and
// the default text colour if the theme sets one. Roles it doesn't know are
// skipped (see Unknown): a theme file made for a newer lazysecrets still
// works.
func FromTheme(theme map[string][]string) (Styles, color.Color, error) {
	st := Default()
	var fg color.Color
	for _, role := range slices.Sorted(maps.Keys(theme)) {
		if _, ok := Roles[role]; !ok {
			continue
		}
		v, err := parse(theme[role])
		if err != nil {
			return st, nil, fmt.Errorf("theme: %s: %w", role, err)
		}
		switch role {
		case "activeBorderColor":
			st.BorderFocused = v.fg(lipgloss.NewStyle())
			st.TitleFocused = v.fg(lipgloss.NewStyle()).Bold(true)
		case "inactiveBorderColor":
			st.BorderBlurred = v.fg(lipgloss.NewStyle())
		case "selectedLineBgColor":
			st.Selected = v.bg(lipgloss.NewStyle()).Bold(true)
			st.themed = true
		case "inactiveViewSelectedLineBgColor":
			st.SelectedBlurred = v.bg(lipgloss.NewStyle()).Bold(true)
			st.themedBlurred = true
		case "defaultFgColor":
			fg = v.color
		case "optionsTextColor":
			st.Label = v.fg(lipgloss.NewStyle())
		case "searchMatchColor":
			st.Match = v.fg(lipgloss.NewStyle())
		case "errorColor":
			st.Error = v.fg(lipgloss.NewStyle())
		case "noticeColor":
			st.Notice = v.fg(lipgloss.NewStyle())
		case "warningColor":
			st.Warning = v.fg(lipgloss.NewStyle())
		case "successColor":
			st.Success = v.fg(lipgloss.NewStyle()).Bold(true)
		case "mutedColor":
			st.Muted = v.fg(lipgloss.NewStyle())
			st.Masked, st.Footer = st.Muted, st.Muted
		case "favouriteColor":
			st.Favourite = v.fg(lipgloss.NewStyle())
			st.Prompt = v.fg(lipgloss.NewStyle()).Bold(true)
		case "valueColor":
			st.Value = v.fg(lipgloss.NewStyle())
		case "azureColor":
			st.KindAzure = v.fg(lipgloss.NewStyle())
		case "databricksColor":
			st.KindDatabricks = v.fg(lipgloss.NewStyle())
		case "repoColor":
			st.KindRepo = v.fg(lipgloss.NewStyle())
		case "springColor":
			st.Spring = v.fg(lipgloss.NewStyle())
		case "composeColor":
			st.Compose = v.fg(lipgloss.NewStyle())
		case "dotnetColor":
			st.Dotnet = v.fg(lipgloss.NewStyle())
		}
	}
	return st, fg, nil
}

// Unknown says which of the theme's roles aren't known, for a warning, or
// is empty.
func Unknown(theme map[string][]string) string {
	var unknown []string
	for r := range theme {
		if _, ok := Roles[r]; !ok {
			unknown = append(unknown, r)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	slices.Sort(unknown)
	return "theme: skipped unknown " + strings.Join(unknown, ", ") + " (a typo, or from a newer lazysecrets)"
}

// value is a parsed role: a colour (nil for default) and attributes.
type value struct {
	color                                   color.Color
	bold, italic, underline, reverse, faint bool
}

func (v value) fg(s lipgloss.Style) lipgloss.Style {
	if v.color != nil {
		s = s.Foreground(v.color)
	}
	return v.attrs(s)
}

func (v value) bg(s lipgloss.Style) lipgloss.Style {
	if v.color != nil {
		s = s.Background(v.color)
	}
	return v.attrs(s)
}

func (v value) attrs(s lipgloss.Style) lipgloss.Style {
	return s.Bold(v.bold).Italic(v.italic).Underline(v.underline).Reverse(v.reverse).Faint(v.faint)
}

var names = map[string]color.Color{
	"black": lipgloss.Black, "red": lipgloss.Red, "green": lipgloss.Green, "yellow": lipgloss.Yellow,
	"blue": lipgloss.Blue, "magenta": lipgloss.Magenta, "cyan": lipgloss.Cyan, "white": lipgloss.White,
	"brightblack": lipgloss.BrightBlack, "brightred": lipgloss.BrightRed, "brightgreen": lipgloss.BrightGreen,
	"brightyellow": lipgloss.BrightYellow, "brightblue": lipgloss.BrightBlue,
	"brightmagenta": lipgloss.BrightMagenta, "brightcyan": lipgloss.BrightCyan, "brightwhite": lipgloss.BrightWhite,
}

func parse(items []string) (value, error) {
	var v value
	for _, it := range items {
		s := strings.ToLower(strings.TrimSpace(it))
		switch s {
		case "bold":
			v.bold = true
		case "italic":
			v.italic = true
		case "underline":
			v.underline = true
		case "reverse":
			v.reverse = true
		case "faint", "dim":
			v.faint = true
		case "default", "":
			// the terminal's own colour
		default:
			c, err := parseColor(s)
			if err != nil {
				return v, err
			}
			v.color = c
		}
	}
	return v, nil
}

func parseColor(s string) (color.Color, error) {
	if c, ok := names[s]; ok {
		return c, nil
	}
	if strings.HasPrefix(s, "#") {
		if len(s) != 7 {
			return nil, fmt.Errorf("%q: want #rrggbb", s)
		}
		if _, err := strconv.ParseUint(s[1:], 16, 32); err != nil {
			return nil, fmt.Errorf("%q: want #rrggbb", s)
		}
		return lipgloss.Color(s), nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 || n > 255 {
			return nil, fmt.Errorf("%q: palette numbers are 0-255", s)
		}
		return lipgloss.Color(s), nil
	}
	return nil, fmt.Errorf("%q isn't a colour or attribute", s)
}
