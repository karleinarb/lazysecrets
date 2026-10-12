package styles

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestFromTheme(t *testing.T) {
	st, fg, err := FromTheme(map[string][]string{
		"activeBorderColor":   {"#e0af68", "bold"},
		"selectedLineBgColor": {"#2d3f76"},
		"defaultFgColor":      {"#c8d3f5"},
		"azureColor":          {"75"},
		"databricksColor":     {"brightred"},
		"searchMatchColor":    {"default", "underline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fg == nil {
		t.Error("defaultFgColor not returned")
	}
	if got := st.BorderFocused.GetForeground(); got != lipgloss.Color("#e0af68") || !st.BorderFocused.GetBold() {
		t.Errorf("active border = %v bold=%v", got, st.BorderFocused.GetBold())
	}
	// The theme's selected-line background replaces the default one.
	if st.Selected.GetReverse() || st.Selected.GetBackground() != lipgloss.Color("#2d3f76") {
		t.Errorf("selected = reverse %v bg %v", st.Selected.GetReverse(), st.Selected.GetBackground())
	}
	if st.KindAzure.GetForeground() != lipgloss.Color("75") || st.KindDatabricks.GetForeground() != lipgloss.BrightRed {
		t.Error("provider colours not applied")
	}
	if !st.Match.GetUnderline() {
		t.Error("attribute without colour not applied")
	}
	// Roles the theme leaves out keep their defaults.
	if st.Error.GetForeground() != lipgloss.Red {
		t.Error("default error colour changed")
	}
}

func TestFromThemeRejectsMistakes(t *testing.T) {
	for name, theme := range map[string]map[string][]string{
		"role":    {"activeBorder": {"red"}},
		"colour":  {"errorColor": {"redish"}},
		"hex":     {"errorColor": {"#fff"}},
		"palette": {"errorColor": {"300"}},
	} {
		if _, _, err := FromTheme(theme); err == nil {
			t.Errorf("%s: no error", name)
		} else if name == "role" && !strings.Contains(err.Error(), "activeBorderColor") {
			t.Errorf("unknown role error should list the known ones: %v", err)
		}
	}
}

func TestEmptyThemeIsDefault(t *testing.T) {
	st, fg, err := FromTheme(nil)
	if err != nil || fg != nil || st.Selected.GetReverse() || st.Selected.GetBackground() != lipgloss.BrightBlack {
		t.Errorf("empty theme: %v %v reverse=%v bg=%v", err, fg, st.Selected.GetReverse(), st.Selected.GetBackground())
	}
}

func TestOnBackground(t *testing.T) {
	hex := func(c any) string {
		r, g, b, _ := c.(interface{ RGBA() (r, g, b, a uint32) }).RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
	st := Default().OnBackground(lipgloss.Color("#282c34"), true)
	if got := hex(st.Selected.GetBackground()); got != "#3e424a" {
		t.Errorf("dark: selected %s", got)
	}
	if got := hex(st.SelectedBlurred.GetBackground()); got != "#343840" {
		t.Errorf("dark: blurred %s", got)
	}
	if got := hex(Default().OnBackground(lipgloss.Color("#ffffff"), false).Selected.GetBackground()); got != "#e8e8e8" {
		t.Errorf("light: selected %s", got)
	}

	themed, _, err := FromTheme(map[string][]string{"selectedLineBgColor": {"#2d3f76"}})
	if err != nil {
		t.Fatal(err)
	}
	st = themed.OnBackground(lipgloss.Color("#282c34"), true)
	if got := hex(st.Selected.GetBackground()); got != "#2d3f76" {
		t.Errorf("themed: selected %s (want the theme's)", got)
	}
	if got := hex(st.SelectedBlurred.GetBackground()); got != "#343840" {
		t.Errorf("themed: blurred %s (not themed: shaded)", got)
	}
}
