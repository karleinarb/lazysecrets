package keys

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }

func TestDefaultsHaveNoConflicts(t *testing.T) {
	m := Default()
	if err := m.check(); err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Actions() {
		if len(a.Binding.Keys()) == 0 {
			t.Errorf("%s.%s has no default keys", a.Context, a.Name)
		}
	}
}

func TestApply(t *testing.T) {
	m := Default()
	err := m.Apply(map[string]map[string][]string{
		"lists":  {"star": {"z"}, "versions": {}},
		"filter": {"star": {"ctrl+x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !key.Matches(press("z"), m.Favourite) || key.Matches(press("f"), m.Favourite) {
		t.Error("star not rebound from f to z")
	}
	if m.Favourite.Help().Key != "z" {
		t.Errorf("help shows %q", m.Favourite.Help().Key)
	}
	if m.Versions.Enabled() || Name(m.Versions) != "" {
		t.Error("versions not unbound")
	}
	if Name(m.FilterStar) != "ctrl+x" || Name(m.PopupStar) != "ctrl+f" {
		t.Errorf("filter star %q, popup star %q", Name(m.FilterStar), Name(m.PopupStar))
	}
}

func TestApplyErrors(t *testing.T) {
	for _, c := range []struct {
		cfg  map[string]map[string][]string
		want string
	}{
		{map[string]map[string][]string{"panes": {"star": {"x"}}}, `unknown context "panes"`},
		{map[string]map[string][]string{"lists": {"starr": {"x"}}}, `unknown action "starr"`},
		{map[string]map[string][]string{"lists": {"star": {"s"}}}, `"s" is bound to both`},            // share
		{map[string]map[string][]string{"search": {"load": {"ctrl+f"}}}, `"ctrl+f" is bound to both`}, // popup star
	} {
		m := Default()
		if err := m.Apply(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.cfg, err, c.want)
		}
	}
	// The same key in contexts that never apply together is fine.
	m := Default()
	if err := m.Apply(map[string]map[string][]string{"filter": {"star": {"ctrl+r"}, "reveal": {"ctrl+e"}}}); err != nil {
		t.Errorf("filter ctrl+r and search ctrl+r clash: %v", err)
	}
}

func TestLabel(t *testing.T) {
	for keys, want := range map[string]string{
		"k up":               "k/↑",
		"ctrl+j ctrl+n down": "ctrl+j/n",
		"space":              "space",
		"ctrl+left <":        "ctrl+←/<",
	} {
		if got := Label(strings.Fields(keys)); got != want {
			t.Errorf("Label(%s) = %q, want %q", keys, got, want)
		}
	}
}
