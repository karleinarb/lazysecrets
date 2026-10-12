package app

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/ui/keys"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// The help bar shows the keys that matter in the focused pane; ? lists all
// of them in a menu (showKeybindings).

// newHelp styles the help bar to read clearly: keys like labels elsewhere,
// descriptions in the normal text colour, only the separators dimmed. The
// bubbles defaults are grey on grey.
func newHelp(st styles.Styles) help.Model {
	h := help.New()
	h.Styles.ShortKey = st.Label
	h.Styles.ShortDesc = lipgloss.NewStyle()
	h.Styles.ShortSeparator = st.Muted
	h.Styles.Ellipsis = st.Muted
	h.Styles.FullKey, h.Styles.FullDesc, h.Styles.FullSeparator = h.Styles.ShortKey, h.Styles.ShortDesc, h.Styles.ShortSeparator
	return h
}

// contextHelp is the help bar for the focused pane.
type contextHelp struct {
	k        keys.Map
	focus    pane
	commands bool // custom commands apply here
	account  bool // the fourth pane shows Fields (fields.go)
	of       pane // what the fourth pane describes
	// revealed, when set, says whether the selected value is shown, so
	// the reveal key says just what it'll do: show or hide, the same
	// width so the bar doesn't shift.
	revealed *bool
}

func (c contextHelp) ShortHelp() []key.Binding {
	k := c.k
	var b []key.Binding
	if k.Login.Enabled() {
		b = append(b, k.Login)
	}
	copyField := key.NewBinding(key.WithKeys(k.Copy.Keys()...), key.WithHelp(k.Copy.Help().Key, "copy"))
	switch {
	case c.focus == paneStatus:
		details := key.NewBinding(key.WithKeys(k.Enter.Keys()...), key.WithHelp(k.Enter.Help().Key, "details"))
		return append(b, details, k.Update, k.Help, k.Quit)
	case c.focus == paneDetail && c.account && c.of == paneStatus:
		open := key.NewBinding(key.WithKeys(k.Open.Keys()...), key.WithHelp(k.Open.Help().Key, "open link"))
		return append(b, k.Up, k.Down, copyField, open, k.Edit, k.Filter, k.Help, k.Quit)
	case c.focus == paneDetail && c.account:
		return append(b, k.Up, k.Down, copyField, k.Filter, k.Help, k.Quit)
	}
	switch c.focus {
	case paneSources:
		b = append(b, k.Enter, k.Search, k.Share, k.ShareMenu, k.Favourite, k.Favourites, k.Filter)
	case paneContainers:
		b = append(b, k.Enter, k.Share, k.ShareMenu, k.Favourite, k.Open, k.Filter)
	default:
		reveal := k.Reveal
		if c.revealed != nil {
			reveal = key.NewBinding(key.WithKeys(k.Reveal.Keys()...), key.WithHelp(k.Reveal.Help().Key, map[bool]string{false: "show", true: "hide"}[*c.revealed]))
		}
		b = append(b, reveal, k.Copy, k.Share, k.ShareMenu, k.Versions, k.Favourite, k.Filter)
	}
	if c.commands {
		b = append(b, k.Commands)
	}
	return append(b, k.OpenRef, k.Help, k.Quit)
}

func (c contextHelp) FullHelp() [][]key.Binding { return [][]key.Binding{c.ShortHelp()} }

var paneNames = [paneCount]string{"Sources", "Vaults", "Secrets", "Detail", "Status"}

// showKeybindings opens the ? menu, like lazygit's: every binding with
// what it does, the focused pane's first, filterable by key, description
// and group. Enter runs the selected one.
func (m *Model) showKeybindings() tea.Cmd { return m.showKeybindingsFor("") }

// showKeybindingsFor opens the ? menu, like lazygit's: every binding with
// what it does, filterable by key, description and group; enter runs the
// selected one. Opened from the panes, it lists the focused pane's keys
// ("") first, then the rest; from a popup (keys.GoTo, keys.Menu), only
// the keys that work there, and it comes back to the popup when closed.
func (m *Model) showKeybindingsFor(ctx string) tea.Cmd {
	// Where it was opened from, to come back to.
	search, prompt := m.search, m.prompt
	m.search = nil

	actions := m.keys.Actions()
	id := func(a keys.Action) string { return a.Context + "." + a.Name }
	here := map[string]bool{}
	var local []keys.Action
	switch ctx {
	case "":
		for _, b := range (contextHelp{k: m.keys, focus: m.focus, commands: len(m.commandsHere()) > 0}).ShortHelp() {
			for _, a := range actions {
				if a.Context == keys.Lists && !here[id(a)] && slices.Equal(a.Binding.Keys(), b.Keys()) && a.Desc == b.Help().Desc {
					here[id(a)] = true
					local = append(local, a)
				}
			}
		}
	default:
		for _, a := range actions {
			if a.Context == ctx {
				here[id(a)] = true
				local = append(local, a)
			}
		}
	}
	var rest []keys.Action
	for _, a := range actions {
		if !here[id(a)] && a.Binding.Enabled() && !(a.Context == keys.Lists && a.Name == "help") {
			rest = append(rest, a)
		}
	}
	var opts []option
	section := func(title string, list []keys.Action) {
		opts = append(opts, option{label: title, header: true})
		for _, a := range list {
			h := a.Binding.Help()
			if len(a.Binding.Keys()) == 0 {
				continue // unbound
			}
			msg := keyPress(a.Binding.Keys()[0])
			// Go to's typing keys need Go to typing: start it first.
			typing := ctx == keys.GoTo && (a.Context == keys.Popup || a.Context == keys.Search)
			label := m.st.Label.Render(fmt.Sprintf("%-14s", h.Key)) + fmt.Sprintf("%-26s", h.Desc) + m.st.Muted.Render(a.Group)
			opts = append(opts, option{label: label, tip: a.Tip, run: func(m *Model) tea.Cmd {
				send := func() tea.Msg { return msg }
				if typing && m.search != nil && !m.search.typing {
					// Typing starts now; its command only blinks the cursor.
					return tea.Batch(m.setTyping(true), send)
				}
				return send
			}})
		}
	}
	title := map[string]string{"": paneNames[m.focus] + " pane", keys.GoTo: "Go to", keys.Menu: "This menu"}[ctx]
	section(title, local)
	if cmds := m.commandsHere(); ctx == "" && len(cmds) > 0 {
		opts = append(opts, option{label: "Custom commands", header: true})
		for _, c := range cmds {
			label := m.st.Label.Render(fmt.Sprintf("%-14s", c.Key)) + fmt.Sprintf("%-26s", cmpOr(c.Description, "command")) + m.st.Muted.Render("custom")
			opts = append(opts, option{label: label, tip: c.Command, run: func(m *Model) tea.Cmd { return m.startCommand(c) }})
		}
	}
	switch ctx {
	case "":
		section("Global", rest)
	case keys.GoTo:
		// Only what works here: Go to's keys, browsing and typing.
		var typing []keys.Action
		for _, a := range actions {
			if a.Context == keys.Popup || a.Context == keys.Search {
				typing = append(typing, a)
			}
		}
		section("While typing, after "+keys.Name(m.keys.GoFilter), typing)
	}
	cmd := m.showFilterMenu("Keybindings", opts)
	m.prompt.footer = footer(move(m.keys.PopupUp, m.keys.PopupDown), hint(m.keys.PopupSelect, "run"), hint(m.keys.PopupClose, "close"))
	if ctx != "" {
		m.prompt.onClose = func(m *Model) { m.search, m.prompt = search, prompt }
	}
	return cmd
}

// versionLabel is the version as shown: "v0.1.0", or the build for
// development builds ("dev-c1a4f90").
func (m Model) versionLabel() string {
	if m.version == "" || strings.HasPrefix(m.version, "dev") || strings.HasPrefix(m.version, "v") {
		return m.version
	}
	return "v" + m.version
}

// helpBar draws bindings as the help bar. When they don't fit, entries
// before the last keep (like "? help • q quit") give way, marked "…",
// so those always show.
func (m Model) helpBar(bindings []key.Binding, keep int) string {
	var bs []key.Binding
	for _, b := range bindings {
		if b.Enabled() {
			bs = append(bs, b)
		}
	}
	h := m.help
	h.SetWidth(0) // measure untruncated
	limit := max(0, m.w-1)
	if v := m.versionLabel(); v != "" {
		limit -= ansi.StringWidth(v) + 2 // a gap, the version, the margin
	}
	if full := h.ShortHelpView(bs); ansi.StringWidth(full) <= limit {
		return full
	}
	keep = min(keep, len(bs))
	head, tail := bs[:len(bs)-keep], bs[len(bs)-keep:]
	sep := h.Styles.ShortSeparator.Render(h.ShortSeparator)
	more := h.Styles.Ellipsis.Render("…")
	for n := len(head); n >= 0; n-- {
		line := more + sep + h.ShortHelpView(tail)
		if n > 0 {
			line = h.ShortHelpView(head[:n]) + sep + line
		}
		if ansi.StringWidth(line) <= limit || n == 0 {
			return ansi.Truncate(line, limit, "…")
		}
	}
	return ""
}

// popupKeys are the open popup's keys for the help bar: Go to's for its
// mode and tab, a menu's, or a text field's.
func (m Model) popupKeys() []key.Binding {
	km := m.keys
	describe := func(b key.Binding, desc string) key.Binding {
		b.SetHelp(b.Help().Key, desc)
		return b
	}
	if v := m.search; v != nil {
		inSearch := v.tab == tabSearch
		if v.typing {
			b := []key.Binding{km.PopupDown, km.PopupUp}
			if inSearch {
				b = append(b, km.SearchSources, km.SearchResults, km.SearchLoad, km.SearchLoadAll)
			}
			return append(b, km.PopupStar, km.PopupSelect, describe(km.PopupClose, "stop typing"))
		}
		b := []key.Binding{km.GoDown, km.GoUp, km.GoPrevTab, km.GoNextTab}
		if inSearch {
			b = append(b, km.GoSources, km.GoResults, km.GoLoad, km.GoLoadAll)
		}
		return append(b, km.GoFilter, km.GoStar, km.GoSelect, km.GoClose, km.Help)
	}
	switch p := m.prompt; {
	case p.input != nil:
		return []key.Binding{describe(km.PopupSelect, "confirm"), describe(km.PopupClose, "cancel")}
	case p.filter != nil:
		return []key.Binding{km.PopupDown, km.PopupUp, km.PopupSelect, km.PopupClose}
	}
	return []key.Binding{km.MenuDown, km.MenuUp, km.MenuSelect, km.MenuClose, km.Help}
}

// A keyHint is a key and what it does, for footers and hints, read from
// the bindings so they show what's bound.
type keyHint struct{ key, desc string }

func hint(b key.Binding, desc string) keyHint { return keyHint{keys.Name(b), desc} }

// move is "↑/↓ move" for an up and a down binding.
func move(up, down key.Binding) keyHint {
	u, d := keys.Name(up), keys.Name(down)
	if u == "" || d == "" {
		return keyHint{u + d, "move"}
	}
	return keyHint{u + "/" + d, "move"}
}

// withKey fills a hint's format with the binding's key ("· %s loads
// everything"), or is empty when the action is unbound.
func withKey(b key.Binding, format string) string {
	if k := keys.Name(b); k != "" {
		return fmt.Sprintf(format, k)
	}
	return ""
}

// joinKeys is "ctrl+h/ctrl+l" for two bindings, or whichever is bound.
func joinKeys(a, b key.Binding) string {
	x, y := keys.Name(a), keys.Name(b)
	if x != "" && y != "" {
		return x + "/" + y
	}
	return x + y
}

// footer joins hints as "↑/↓ move · enter go", leaving out unbound keys.
func footer(hints ...keyHint) string {
	var parts []string
	for _, h := range hints {
		if h.key != "" {
			parts = append(parts, h.key+" "+h.desc)
		}
	}
	return strings.Join(parts, " · ")
}

// keyPress builds the key press a binding's key name stands for, so the ?
// menu can run it.
func keyPress(name string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for {
		switch {
		case strings.HasPrefix(name, "ctrl+") && len(name) > 5:
			mod |= tea.ModCtrl
			name = name[5:]
			continue
		case strings.HasPrefix(name, "shift+") && len(name) > 6:
			mod |= tea.ModShift
			name = name[6:]
			continue
		}
		break
	}
	named := map[string]rune{
		"space": tea.KeySpace, "enter": tea.KeyEnter, "tab": tea.KeyTab, "esc": tea.KeyEscape,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "backspace": tea.KeyBackspace,
	}
	if code, ok := named[name]; ok {
		k := tea.KeyPressMsg{Code: code, Mod: mod}
		if code == tea.KeySpace && mod == 0 {
			k.Text = " "
		}
		return k
	}
	r := []rune(name)[0]
	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Code: r, Text: name}
}
