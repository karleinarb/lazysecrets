// Package keys defines the keymap: named actions in contexts, their
// default keys, and overrides from the config:
//
//	keybinding:
//	  lists:
//	    star: [f]
//	    versions: []          # unbound
//	  filter:
//	    star: [ctrl+f]
//
// An action set in the config gets exactly the keys given, replacing its
// defaults; an empty list unbinds it. Everything that shows a key (the
// help bar, ?, footers, hints) reads it from here.
package keys

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
)

// Contexts group the actions by where they apply. The search popup takes
// search's actions and popup's.
const (
	Lists  = "lists"  // the panes, when not typing
	Filter = "filter" // typing a filter in a pane
	Menu   = "menu"   // popups without a text field
	Popup  = "popup"  // popups with a text field: favourites, help, search, notes, open link
	Search = "search" // Go to's own actions while typing
	GoTo   = "goto"   // Go to while browsing (no text field active)
)

type Map struct {
	// lists
	Up, Down, Top, Bottom             key.Binding
	NextPane, PrevPane, Details       key.Binding
	Pane0, Pane1, Pane2, Pane3, Pane4 key.Binding
	NextTab, PrevTab                  key.Binding
	Enter, Back, Filter               key.Binding
	Reveal, Copy, CopyRef, Open, Edit key.Binding
	Versions, Favourite, Favourites   key.Binding
	Recent                            key.Binding
	Note, Share, ShareMenu, OpenRef   key.Binding
	Search, SourcesView, Commands     key.Binding
	SplitLeft, SplitRight, SplitReset key.Binding
	Taller, Shorter, ZoomIn, ZoomOut  key.Binding
	Login, Refresh, Help, Quit        key.Binding
	Update                            key.Binding
	// filter
	AcceptFilter, ClearFilter            key.Binding
	FilterUp, FilterDown                 key.Binding
	FilterCopy, FilterReveal, FilterStar key.Binding
	// menu
	MenuUp, MenuDown, MenuSelect, MenuClose key.Binding
	// popup
	PopupUp, PopupDown, PopupSelect, PopupClose, PopupStar key.Binding
	// search
	SearchSources, SearchResults, SearchSwitch key.Binding
	SearchLoad, SearchLoadAll                  key.Binding
	// goto
	GoDown, GoUp, GoTop, GoBottom       key.Binding
	GoPrevTab, GoNextTab                key.Binding
	GoSources, GoResults, GoSwitch      key.Binding
	GoStar, GoLoad, GoLoadAll, GoFilter key.Binding
	GoSelect, GoClose                   key.Binding
}

// Action is a binding with its name in the config and what it does.
type Action struct {
	Context, Name string
	Binding       *key.Binding
	Desc          string // short, for the help bar
	Group         string // section in the ? menu
	Tip           string // a sentence or two for the ? menu
}

// Actions lists every action, in the ? menu's order.
func (m *Map) Actions() []Action {
	a := func(ctx, name string, b *key.Binding, desc, group, tip string) Action {
		return Action{ctx, name, b, desc, group, tip}
	}
	return []Action{
		a(Lists, "up", &m.Up, "up", "move", "Move up in the focused list, or scroll the detail pane."),
		a(Lists, "down", &m.Down, "down", "move", "Move down in the focused list, or scroll the detail pane."),
		a(Lists, "top", &m.Top, "top", "move", "Jump to the top of the focused list."),
		a(Lists, "bottom", &m.Bottom, "bottom", "move", "Jump to the bottom of the focused list."),
		a(Lists, "nextPane", &m.NextPane, "next pane", "move", "Focus the next pane: Sources, Vaults, Secrets, detail."),
		a(Lists, "prevPane", &m.PrevPane, "prev pane", "move", "Focus the previous pane; from the detail pane, the one it describes."),
		a(Lists, "details", &m.Details, "details", "move", "Focus the detail pane on the right, on what's selected: a source, vault, file or secret."),
		a(Lists, "pane1", &m.Pane1, "Sources pane", "move", "Focus the Sources pane."),
		a(Lists, "pane2", &m.Pane2, "Vaults pane", "move", "Focus the Vaults (or Scopes) pane."),
		a(Lists, "pane3", &m.Pane3, "Secrets pane", "move", "Focus the Secrets pane."),
		a(Lists, "pane4", &m.Pane4, "detail pane", "move", "Focus the detail pane."),
		a(Lists, "pane0", &m.Pane0, "status", "move", "Focus the status pane: version and updates, accounts, config files, links and the changelog, on the right."),
		a(Lists, "open", &m.Enter, "open", "move", "Go into the selected item: from a subscription to its vaults, from a vault to its secrets. On a provider header, fold or unfold it."),
		a(Lists, "back", &m.Back, "back", "move", "Go back: clear an applied filter, leave the versions view, or focus the previous pane."),

		a(Lists, "filter", &m.Filter, "filter", "lists", "Filter the focused list by name, fuzzily: dbpw finds db-password. In Sources the provider and account match too."),
		a(Lists, "prevTab", &m.PrevTab, "prev tab", "lists", "Previous tab of the focused pane: Sources and Favourites in Sources; Secrets, Keys, Certs in Secrets."),
		a(Lists, "nextTab", &m.NextTab, "next tab", "lists", "Next tab of the focused pane."),
		a(Lists, "sourcesView", &m.SourcesView, "grouped/flat sources", "lists", "Switch the Sources pane between grouped by provider and a flat list."),
		a(Lists, "star", &m.Favourite, "star", "lists", "Star or unstar the selected subscription, vault or secret, for the account showing it. Starred items get a ★ and sort first."),
		a(Lists, "goTo", &m.Search, "search", "lists", "Go to anything: search every vault and secret loaded and jump to one. Again in Go to: only the source or group you're in, and back to all. On the left, the scope: All, an account (or Databricks, or Repos), or one source (○ ◐ ● for how much of it is loaded)."),
		a(Lists, "favourites", &m.Favourites, "favourites", "lists", "Go to, on Favourites: everything you've starred, to search and jump to."),
		a(Lists, "recent", &m.Recent, "recent", "lists", "Go to, on Recent: what you used last, newest first."),
		a(Lists, "commands", &m.Commands, "commands", "lists", "List the custom commands for the focused pane (customCommands in the config) and run one."),
		a(Lists, "note", &m.Note, "note", "lists", "Write a short note on the selected subscription, vault or secret, shown on the right of its row. Filtering matches notes too."),

		a(Lists, "reveal", &m.Reveal, "reveal/hide", "secrets", "Show or hide the selected value. For keys and certificates: the public key or certificate."),
		a(Lists, "copyValue", &m.Copy, "copy value", "secrets", "Copy the selected value without showing it. It is cleared from the clipboard after a countdown."),
		a(Lists, "copyReference", &m.CopyRef, "copy reference", "secrets", "Copy the reference to use in config, e.g. @Microsoft.KeyVault(SecretUri=…) or {{secrets/scope/key}}."),
		a(Lists, "versions", &m.Versions, "versions", "secrets", "List the versions of the selected item (Azure). Reveal, copy and share then act on the selected version."),
		a(Lists, "openInBrowser", &m.Open, "open in browser", "secrets", "Open the selected subscription, vault or item in the browser (Azure portal or Databricks workspace)."),
		a(Lists, "edit", &m.Edit, "open file", "secrets", "Open the selected SOPS or .env file, or the file of the selected secret at its line; on the status pane, your config (made if it isn't there yet). SOPS files open with sops edit. Others open in the nvim lazysecrets runs in (quitting lazysecrets), else in $VISUAL or $EDITOR, else in the system's default app."),

		a(Lists, "copyLink", &m.Share, "copy link", "share", "Copy a lazysecrets:// link to the selected subscription, workspace, vault, scope or secret, for sharing. From the versions view the link is pinned to that version."),
		a(Lists, "shareMenu", &m.ShareMenu, "share", "share", "Choose what to share: the lazysecrets link, the plain Key Vault URL, the config reference, the web page, or env files as an encrypted link."),
		a(Lists, "openLink", &m.OpenRef, "open link", "share", "Open a lazysecrets:// link, Key Vault URL or reference. Pasting one into lazysecrets does the same."),

		a(Lists, "dividerLeft", &m.SplitLeft, "divider left", "layout", "Move the divider between the left panes and the detail pane to the left."),
		a(Lists, "dividerRight", &m.SplitRight, "divider right", "layout", "Move the divider between the left panes and the detail pane to the right."),
		a(Lists, "taller", &m.Taller, "taller", "layout", "Make the focused left pane taller."),
		a(Lists, "shorter", &m.Shorter, "shorter", "layout", "Make the focused left pane shorter."),
		a(Lists, "zoomIn", &m.ZoomIn, "zoom", "layout", "Zoom the focused pane: normal, half screen, full screen."),
		a(Lists, "zoomOut", &m.ZoomOut, "unzoom", "layout", "Zoom back out."),
		a(Lists, "resetLayout", &m.SplitReset, "reset layout", "layout", "Reset the divider, pane heights and zoom."),

		a(Lists, "refresh", &m.Refresh, "refresh", "other", "Reload the focused pane, bypassing the cache."),
		a(Lists, "login", &m.Login, "sign in", "other", "Sign in again (az login or databricks auth login) when a login has expired."),
		a(Lists, "update", &m.Update, "update", "other", "Check for a newer lazysecrets: its changelog, and update to it."),
		a(Lists, "help", &m.Help, "help", "other", "Show this list. Also works in Go to while browsing, and in menus, showing their keys first."),
		a(Lists, "quit", &m.Quit, "quit", "other", "Quit lazysecrets. ctrl+c always quits too."),

		a(Filter, "down", &m.FilterDown, "down", "while filtering", "Move down while typing a filter."),
		a(Filter, "up", &m.FilterUp, "up", "while filtering", "Move up while typing a filter."),
		a(Filter, "copyValue", &m.FilterCopy, "copy value", "while filtering", "Copy the selected value while typing a filter."),
		a(Filter, "reveal", &m.FilterReveal, "reveal/hide", "while filtering", "Reveal or hide the selected value while typing a filter."),
		a(Filter, "star", &m.FilterStar, "star", "while filtering", "Star or unstar the selected item while typing a filter."),
		a(Filter, "accept", &m.AcceptFilter, "apply filter", "while filtering", "Stop typing and keep the filter."),
		a(Filter, "clear", &m.ClearFilter, "clear filter", "while filtering", "Clear the filter."),

		a(Menu, "down", &m.MenuDown, "down", "menus", "Move down in a menu."),
		a(Menu, "up", &m.MenuUp, "up", "menus", "Move up in a menu."),
		a(Menu, "select", &m.MenuSelect, "select", "menus", "Pick the selected entry. Each entry's letter picks it directly."),
		a(Menu, "close", &m.MenuClose, "dismiss", "menus", "Close the menu."),

		a(Popup, "down", &m.PopupDown, "down", "popups", "Move down in a popup with a text field (favourites, help, search)."),
		a(Popup, "up", &m.PopupUp, "up", "popups", "Move up in a popup with a text field."),
		a(Popup, "select", &m.PopupSelect, "go", "popups", "Go to the selected entry, or confirm what you typed."),
		a(Popup, "close", &m.PopupClose, "close", "popups", "Close the popup."),
		a(Popup, "star", &m.PopupStar, "star", "popups", "Star or unstar the selected entry in Go to. It stays where it is until Go to opens again."),

		a(Search, "sources", &m.SearchSources, "sources", "search", "Move to the sources list."),
		a(Search, "results", &m.SearchResults, "results", "search", "Move to the results list."),
		a(Search, "switchList", &m.SearchSwitch, "switch list", "search", "Switch between the sources and the results."),
		a(Search, "load", &m.SearchLoad, "load selected", "search", "Load (or reload) the selected source's vaults and secrets, or the selected vault's secrets, in the background."),
		a(Search, "loadAll", &m.SearchLoadAll, "load all", "search", "Load every source's vaults and secrets in the background, to search them all."),

		a(GoTo, "down", &m.GoDown, "down", "go to", "Move down in Go to while browsing."),
		a(GoTo, "up", &m.GoUp, "up", "go to", "Move up in Go to while browsing."),
		a(GoTo, "top", &m.GoTop, "top", "go to", "Jump to the top of the list in Go to while browsing."),
		a(GoTo, "bottom", &m.GoBottom, "bottom", "go to", "Jump to the bottom of the list in Go to while browsing."),
		a(GoTo, "prevTab", &m.GoPrevTab, "prev tab", "go to", "Previous tab of Go to: Search, Favourites, Recent."),
		a(GoTo, "nextTab", &m.GoNextTab, "next tab", "go to", "Next tab of Go to."),
		a(GoTo, "sources", &m.GoSources, "sources", "go to", "Move to the sources list (Search tab)."),
		a(GoTo, "results", &m.GoResults, "results", "go to", "Move to the results list (Search tab)."),
		a(GoTo, "switchList", &m.GoSwitch, "switch list", "go to", "Switch between the sources and the results (Search tab)."),
		a(GoTo, "star", &m.GoStar, "star", "go to", "Star or unstar the selected entry. It stays where it is until Go to opens again."),
		a(GoTo, "load", &m.GoLoad, "load selected", "go to", "Load (or reload) the selected source or vault in the background (Search tab)."),
		a(GoTo, "loadAll", &m.GoLoadAll, "load all", "go to", "Load everything in the background, to search it all (Search tab)."),
		a(GoTo, "filter", &m.GoFilter, "filter", "go to", "Start typing to filter the list."),
		a(GoTo, "select", &m.GoSelect, "go", "go to", "Go to the selected entry."),
		a(GoTo, "close", &m.GoClose, "close", "go to", "Close Go to."),
	}
}

// defaults follow one rule: a letter in the lists, ctrl+ the same letter
// where you type.
var defaults = map[string][]string{
	"lists.up": {"k", "up", "ctrl+k", "ctrl+p"}, "lists.down": {"j", "down", "ctrl+j", "ctrl+n"},
	"lists.top": {"g", "home"}, "lists.bottom": {"G", "end"},
	"lists.nextPane": {"tab"}, "lists.prevPane": {"shift+tab", "h", "left"}, "lists.details": {"l", "right"},
	"lists.pane1": {"1"}, "lists.pane2": {"2"}, "lists.pane3": {"3"}, "lists.pane4": {"4"}, "lists.pane0": {"0"},
	"lists.open": {"enter"}, "lists.back": {"esc", "backspace"},
	"lists.filter": {"/"}, "lists.prevTab": {"["}, "lists.nextTab": {"]"}, "lists.sourcesView": {"`"},
	"lists.star": {"f"}, "lists.favourites": {"F"}, "lists.recent": {"R"}, "lists.goTo": {"ctrl+s"}, "lists.note": {"n"},
	"lists.commands": {"x"},
	"lists.reveal":   {"space"}, "lists.copyValue": {"y"}, "lists.copyReference": {"Y"},
	"lists.versions": {"v"}, "lists.openInBrowser": {"o"}, "lists.edit": {"e"},
	"lists.copyLink": {"s"}, "lists.shareMenu": {"S"}, "lists.openLink": {"ctrl+o"},
	"lists.dividerLeft": {"ctrl+left", "<"}, "lists.dividerRight": {"ctrl+right", ">"},
	"lists.taller": {"ctrl+down", "}"}, "lists.shorter": {"ctrl+up", "{"},
	"lists.zoomIn": {"+"}, "lists.zoomOut": {"_"}, "lists.resetLayout": {"="},
	"lists.refresh": {"r"}, "lists.login": {"L"}, "lists.update": {"U"}, "lists.help": {"?"}, "lists.quit": {"q"},

	"filter.down": {"ctrl+j", "ctrl+n", "down"}, "filter.up": {"ctrl+k", "ctrl+p", "up"},
	"filter.copyValue": {"ctrl+y"}, "filter.reveal": {"ctrl+r"}, "filter.star": {"ctrl+f"},
	"filter.accept": {"enter"}, "filter.clear": {"esc"},

	"menu.down": {"j", "down", "ctrl+j", "ctrl+n"}, "menu.up": {"k", "up", "ctrl+k", "ctrl+p"},
	"menu.select": {"enter"}, "menu.close": {"esc"},

	"popup.down": {"ctrl+j", "ctrl+n", "down"}, "popup.up": {"ctrl+k", "ctrl+p", "up"},
	"popup.select": {"enter"}, "popup.close": {"esc"}, "popup.star": {"ctrl+f"},

	"search.sources": {"ctrl+h"}, "search.results": {"ctrl+l"}, "search.switchList": {"tab", "shift+tab"},
	"search.load": {"ctrl+r"}, "search.loadAll": {"ctrl+a"},

	"goto.down": {"j", "down"}, "goto.up": {"k", "up"}, "goto.top": {"g", "home"}, "goto.bottom": {"G", "end"}, "goto.prevTab": {"["}, "goto.nextTab": {"]"},
	"goto.sources": {"h", "left"}, "goto.results": {"l", "right"}, "goto.switchList": {"tab", "shift+tab"},
	"goto.star": {"f"}, "goto.load": {"r"}, "goto.loadAll": {"a"}, "goto.filter": {"/"},
	"goto.select": {"enter"}, "goto.close": {"esc", "q"},
}

// Default is the keymap without overrides.
func Default() Map {
	var m Map
	for _, a := range m.Actions() {
		*a.Binding = bind(defaults[a.Context+"."+a.Name], a.Desc)
	}
	m.Login.SetEnabled(false) // shown and active only once a login expires
	return m
}

func bind(keys []string, desc string) key.Binding {
	if len(keys) == 0 {
		return key.NewBinding(key.WithDisabled())
	}
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(Label(keys), desc))
}

// Label shows keys in help: the first two, arrows as arrows, a shared
// ctrl+ written once ("k/↑", "ctrl+j/n").
func Label(keys []string) string {
	pretty := strings.NewReplacer("left", "←", "right", "→", "up", "↑", "down", "↓", "backspace", "⌫")
	var out []string
	for i, k := range keys[:min(2, len(keys))] {
		k = pretty.Replace(k)
		if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && i > 0 && strings.HasPrefix(out[i-1], "ctrl+") {
			k = rest
		}
		out = append(out, k)
	}
	return strings.Join(out, "/")
}

// Name is the key to mention in a hint ("space to reveal"): the first one
// bound, or "" if the action is unbound.
func Name(b key.Binding) string {
	if ks := b.Keys(); len(ks) > 0 {
		return Label(ks[:1])
	}
	return ""
}

// Apply overrides the defaults from the config: each action given gets
// exactly the keys listed; none unbinds it. Unknown contexts or actions,
// and a key bound to two actions that apply together, are errors.
func (m *Map) Apply(cfg map[string]map[string][]string) error {
	byName := map[string]Action{}
	contexts := map[string]bool{}
	for _, a := range m.Actions() {
		byName[a.Context+"."+a.Name] = a
		contexts[a.Context] = true
	}
	for _, ctx := range slices.Sorted(maps.Keys(cfg)) {
		if !contexts[ctx] {
			return fmt.Errorf("keybinding: unknown context %q (known: %s)", ctx, strings.Join(slices.Sorted(maps.Keys(contexts)), ", "))
		}
		for _, name := range slices.Sorted(maps.Keys(cfg[ctx])) {
			a, ok := byName[ctx+"."+name]
			if !ok {
				return fmt.Errorf("keybinding.%s: unknown action %q (known: %s)", ctx, name, strings.Join(m.names(ctx), ", "))
			}
			*a.Binding = bind(cfg[ctx][name], a.Desc)
		}
	}
	m.Login.SetEnabled(false)
	return m.check()
}

func (m *Map) names(ctx string) []string {
	var out []string
	for _, a := range m.Actions() {
		if a.Context == ctx {
			out = append(out, a.Name)
		}
	}
	slices.Sort(out)
	return out
}

// check finds a key bound to two actions that apply together: within a
// context, and in the search popup (search and popup).
func (m *Map) check() error {
	for _, group := range [][]string{{Lists}, {Filter}, {Menu}, {Popup}, {Search, Popup}, {GoTo}} {
		seen := map[string]string{}
		for _, a := range m.Actions() {
			if !slices.Contains(group, a.Context) {
				continue
			}
			name := a.Context + "." + a.Name
			for _, k := range a.Binding.Keys() {
				if other, ok := seen[k]; ok && other != name {
					return fmt.Errorf("keybinding: %q is bound to both %s and %s", k, other, name)
				}
				seen[k] = name
			}
		}
	}
	return nil
}

// Section is a titled group of actions for the ? menu.
type Section struct {
	Title   string
	Entries []Action
}

// Sections groups the actions for the ? menu.
func (m *Map) Sections() []Section {
	var out []Section
	for _, a := range m.Actions() {
		if n := len(out); n == 0 || out[n-1].Title != a.Group {
			out = append(out, Section{Title: a.Group})
		}
		out[len(out)-1].Entries = append(out[len(out)-1].Entries, a)
	}
	return out
}

// FilterHelp is shown while typing a filter.
type FilterHelp struct{ Map }

func (f FilterHelp) ShortHelp() []key.Binding {
	return []key.Binding{f.FilterDown, f.FilterUp, f.FilterCopy, f.FilterReveal, f.FilterStar, f.AcceptFilter, f.ClearFilter}
}

func (f FilterHelp) FullHelp() [][]key.Binding { return [][]key.Binding{f.ShortHelp()} }

// YAML is the keymap as a config section, every action with its keys and
// what it does, to copy into the config and edit.
func (m *Map) YAML() string {
	var b strings.Builder
	b.WriteString("keybinding:\n")
	ctx := ""
	for _, a := range m.Actions() {
		if a.Context != ctx {
			ctx = a.Context
			fmt.Fprintf(&b, "  %s:\n", ctx)
		}
		quoted := make([]string, len(a.Binding.Keys()))
		for i, k := range a.Binding.Keys() {
			quoted[i] = fmt.Sprintf("%q", k)
		}
		fmt.Fprintf(&b, "    %-15s [%s]  # %s\n", a.Name+":", strings.Join(quoted, ", "), a.Desc)
	}
	return b.String()
}

// CheckExtra reports a key some other binding in the lists takes, for keys
// added outside the keymap (custom commands). name says whose key it is.
func (m *Map) CheckExtra(k, name string) error {
	for _, a := range m.Actions() {
		if a.Context == Lists && slices.Contains(a.Binding.Keys(), k) {
			return fmt.Errorf("%s: %q is bound to lists.%s", name, k, a.Name)
		}
	}
	return nil
}
