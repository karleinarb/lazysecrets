package panes

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// Fields is the fourth pane for anything but a secret (an account, and
// what describes vaults, scopes and files): labelled values, one per row,
// a label on the first of several values, to move through, filter and
// copy from. It's a List, so / filters it like the other panes. Secrets
// have Detail, for revealing and versions.
type Fields struct {
	List[Field]
	shown bool
}

// Field is a row: a value under a label. Info rows (loading, errors)
// can't be copied; Err ones are errors. A row can also be opened: URL in
// the browser (o), Path in the editor (e), Provider signed in to (L), or
// Link gone to (enter).
type Field struct {
	Label, Value string
	Info, Err    bool

	URL, Path, Provider string
	// Link is a lazysecrets link enter goes to, in the source of row Row.
	Link, Row string
}

func NewFields() Fields {
	l := NewList("", Item[Field]{
		ID:     func(f Field) string { return f.Label + "\x00" + f.Value },
		Filter: func(f Field) string { return f.Value },
		// "/tenant" finds the tenant's rows.
		FilterExtra: func(f Field) string { return f.Label },
		Render: func(st styles.Styles, f Field, width int) string {
			value := f.Value
			switch {
			case f.Err:
				value = st.Error.Render(value)
			case f.Info:
				value = st.Muted.Render(value)
			}
			return ansi.Truncate(st.Label.Render(fmt.Sprintf("%-10s", f.Label))+" "+value, width, "…")
		},
	})
	l.Empty = "nothing to show"
	return Fields{List: l}
}

// Show shows fields under title, instead of the secret pane. Another
// title (another account or vault) starts over, without a filter.
func (s *Fields) Show(title string, fields []Field) {
	if title != s.Title {
		s.Reset()
		s.Title = title
	}
	s.shown = true
	s.SetItems(fields)
}

// Hide gives the fourth pane back to the secret pane.
func (s *Fields) Hide() { s.shown = false }

// Shown reports whether the fourth pane shows Fields.
func (s Fields) Shown() bool { return s.shown }

// Selected is the field under the cursor, unless it's an info row.
func (s Fields) Selected() (Field, bool) {
	f, ok := s.List.Selected()
	if !ok || f.Info {
		return Field{}, false
	}
	return f, true
}
