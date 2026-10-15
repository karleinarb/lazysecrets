package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// Notes: n writes a short note on the selected subscription, vault or
// secret, kept in its store record. Notes show dimmed on the right of rows
// and in the detail pane, and the filter matches them.

// withNote right-aligns a dimmed note in a row of the given width. The
// note gives way first when space is short.
func withNote(st styles.Styles, left, note string, width int) string {
	if note == "" {
		return left
	}
	room := width - ansi.StringWidth(left) - 2
	if room < 4 {
		return left
	}
	note = ansi.Truncate(note, room, "…")
	pad := width - ansi.StringWidth(left) - ansi.StringWidth(note)
	return left + strings.Repeat(" ", pad) + st.Muted.Render(note)
}

// editNote opens a popup to write the focused item's note.
func (m *Model) editNote() tea.Cmd {
	var key, name string
	var path []string
	var src provider.Source
	switch m.focus {
	case paneSources:
		s, ok := m.selectedSource()
		if !ok {
			m.flash = warning("select a subscription or workspace to add a note")
			return nil
		}
		key, name, src = sourceKey(s), s.Name, s
	case paneContainers:
		c, ok := m.containers.Selected()
		if !ok {
			return nil
		}
		key, name, path, src = storeKey(c), c.Name, []string{c.Source.Name}, c.Source
	default:
		o, ok := m.detail.Secret()
		if !ok {
			return nil
		}
		c := o.Container
		key, name, path, src = objectKey(o), o.Name, []string{c.Source.Name, c.Name}, c.Source
	}
	in := newTextInput("note: ", 48)
	in.Placeholder = "e.g. prod SQL admin"
	in.CharLimit = 80
	in.SetValue(m.store.Node(key).Note)
	in.CursorEnd()
	m.prompt = &modal{
		title: "Note on " + name, input: &in,
		lines: []string{m.st.Muted.Render("Shown on the right of the row. Empty removes it.")},
		submit: func(m *Model, text string) tea.Cmd {
			note := strings.Join(strings.Fields(text), " ") // one line
			if err := m.remember(key, name, path, src, func(n *store.Node) { n.Note = note }); err != nil {
				m.flash = flash{text: "saving: " + err.Error(), isErr: true}
				return nil
			}
			m.refreshDetailNote()
			return nil
		},
		footer: footer(hint(m.keys.PopupSelect, "save"), hint(m.keys.PopupClose, "cancel")),
	}
	m.flash = flash{}
	return m.prompt.input.Focus()
}

// refreshDetailNote shows the note of whatever the detail pane shows.
func (m *Model) refreshDetailNote() {
	if o, ok := m.detail.Secret(); ok {
		m.detail.SetNote(m.store.Node(objectKey(o)).Note)
	} else if c, ok := m.detail.Container(); ok {
		m.detail.SetNote(m.store.Node(storeKey(c)).Note)
	}
}
