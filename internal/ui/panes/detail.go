package panes

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/icons"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

type ValueState int

const (
	ValueHidden ValueState = iota
	ValueLoading
	ValueShown
	ValueFailed
)

// Detail shows metadata for the selected secret (or container) and, on
// request, its value.
type Detail struct {
	Now   func() time.Time
	Icons icons.Set
	// RevealKey and VersionsKey are the keys hints mention ("space to
	// reveal"); a hint is left out when its key is "" (unbound).
	RevealKey, VersionsKey, CopyKey string

	secret    *provider.Object
	container *provider.Container

	state   ValueState
	value   provider.SecretValue
	err     error
	hidesIn string // when a shown value hides by itself, if it does

	describing  bool
	describeErr error

	versions []provider.Object // newest first; nil while unknown
	note     string            // the user's note on what's shown

	offset int
	w, h   int
}

func NewDetail(iconSet icons.Set) Detail { return Detail{Now: time.Now, Icons: iconSet} }

// ShowSecret selects a secret and forgets any previously fetched value.
func (d *Detail) ShowSecret(s provider.Object) {
	d.Clear()
	d.secret = &s
	d.container = &s.Container
}

// ShowContainer shows a container summary when no secret is selected.
func (d *Detail) ShowContainer(c provider.Container) {
	d.Clear()
	d.container = &c
}

// Clear drops the selection and any fetched value.
func (d *Detail) Clear() {
	*d = Detail{Now: d.Now, Icons: d.Icons, RevealKey: d.RevealKey, VersionsKey: d.VersionsKey, CopyKey: d.CopyKey, w: d.w, h: d.h} // drops the note too
}

// SetDescribing marks extra metadata as being fetched.
func (d *Detail) SetDescribing() { d.describing, d.describeErr = true, nil }

// SetDescribed replaces the shown object's metadata, keeping the value
// state. It ignores objects other than the one shown.
func (d *Detail) SetDescribed(o provider.Object) {
	if d.secret == nil || d.secret.ID != o.ID {
		return
	}
	// Keep the container (and so the account) the object was shown with;
	// value requests are routed by it.
	o.Container = d.secret.Container
	d.secret = &o
	d.describing = false
}

// SetNote shows the user's note on the shown object or container.
func (d *Detail) SetNote(note string) { d.note = note }

// SetVersions records the shown object's versions, newest first. It
// ignores objects other than the one shown.
func (d *Detail) SetVersions(id string, versions []provider.Object) {
	if d.secret != nil && d.secret.ID == id {
		d.versions = versions
	}
}

func (d *Detail) SetDescribeError(err error) { d.describing, d.describeErr = false, err }

func (d Detail) Secret() (provider.Object, bool) {
	if d.secret == nil {
		return provider.Object{}, false
	}
	return *d.secret, true
}

// Container is the container shown, if any.
func (d Detail) Container() (provider.Container, bool) {
	if d.container == nil {
		return provider.Container{}, false
	}
	return *d.container, true
}

// Value returns the value if it is shown.
func (d Detail) Value() (provider.SecretValue, bool) {
	return d.value, d.state == ValueShown
}

func (d Detail) State() ValueState { return d.state }

func (d *Detail) SetLoading() { d.state, d.err = ValueLoading, nil }

func (d *Detail) SetValue(v provider.SecretValue) { d.state, d.value, d.err = ValueShown, v, nil }

func (d *Detail) SetValueError(err error) { d.state, d.err = ValueFailed, err }

// Hide masks the value again and drops it from memory.
func (d *Detail) Hide() {
	d.state, d.value, d.err, d.hidesIn = ValueHidden, provider.SecretValue{}, nil, ""
}

// SetHidesIn shows when a revealed value hides by itself, e.g. "42s".
func (d *Detail) SetHidesIn(s string) { d.hidesIn = s }

func (d *Detail) SetSize(w, h int) {
	d.w, d.h = w, h
	d.Scroll(0)
}

func (d *Detail) Scroll(delta int) {
	total := len(d.lines(styles.Styles{}))
	d.offset = max(0, min(d.offset+delta, total-(d.h-2)))
}

func (d *Detail) ScrollTop()    { d.offset = 0 }
func (d *Detail) ScrollBottom() { d.Scroll(1 << 30) }

func (d Detail) View(st styles.Styles, focused bool, spin string) string {
	lines := d.lines(st)
	if spin != "" && d.state == ValueLoading {
		for i, l := range lines {
			lines[i] = strings.Replace(l, spinnerPlaceholder, spin, 1)
		}
	}
	rows := max(0, d.h-2)
	footer := ""
	if len(lines) > rows {
		footer = fmt.Sprintf("%d-%d of %d", d.offset+1, min(d.offset+rows, len(lines)), len(lines))
	}
	title := "Details"
	if d.secret != nil {
		title = string(d.secret.Type)
	}
	return Box(st, TitleStyle(st, focused).Render(title), footer, lines[min(d.offset, len(lines)):], d.w, d.h, focused)
}

const spinnerPlaceholder = "\x00spin\x00"

// carryStyle starts each wrapped line with the styles still on at the end
// of the line before: a styled value wrapped in the middle (a long ref)
// would go on unstyled, as each line is drawn on its own.
func carryStyle(lines []string) []string {
	var on string
	for i, line := range lines {
		lines[i] = on + line
		for _, code := range sgr.FindAllString(line, -1) {
			if code == "\x1b[m" || code == "\x1b[0m" {
				on = ""
			} else {
				on += code
			}
		}
	}
	return lines
}

var valueIndent = strings.Repeat(" ", 12)

// fieldLabel starts a detail line: the label, padded so values line up
// at valueIndent.
func fieldLabel(st styles.Styles, label string) string {
	return " " + st.Label.Render(fmt.Sprintf("%-10s", label)) + " "
}

// lines renders the body, wrapped to the pane width.
func (d Detail) lines(st styles.Styles) []string {
	inner := max(1, d.w-3)
	var out []string
	field := func(label, value string) {
		wrapped := carryStyle(strings.Split(ansi.Hardwrap(value, max(1, inner-11), true), "\n"))
		out = append(out, fieldLabel(st, label)+wrapped[0])
		for _, w := range wrapped[1:] {
			out = append(out, valueIndent+w)
		}
	}
	muted := st.Muted.Render
	note := func() {
		if d.note != "" {
			field("note", st.Favourite.Render(d.note))
		}
	}

	if d.container == nil {
		return []string{muted(" Select a vault or scope to browse its secrets.")}
	}
	c := *d.container
	containerLabel := map[provider.Kind]string{provider.KindDatabricks: "scope", provider.KindRepo: "file"}[c.Source.Kind]
	if containerLabel == "" {
		containerLabel = "vault"
	}
	source := d.Icons.Prefix(c.Source.Kind) + cmp.Or(c.Source.ProviderLabel, string(c.Source.Kind)) + " / " + c.Source.Name

	if d.secret == nil {
		field(containerLabel, c.Name)
		note()
		if c.Detail != "" {
			field("", muted(c.Detail))
		}
		field("source", source)
		out = append(out, "", muted(" Select a secret to see its details."))
		return out
	}

	s := *d.secret
	field("name", s.Name)
	note()
	field(containerLabel, c.Name)
	if c.Source.Kind != provider.KindAzure && c.Detail != "" {
		field("", muted(c.Detail))
	}
	field("source", source)
	if s.Version != "" {
		field("version", s.Version)
	}

	if s.Enabled != nil {
		if *s.Enabled {
			field("enabled", "yes")
		} else {
			field("enabled", st.Error.Render("no"))
		}
	}
	if !s.Updated.IsZero() {
		field("updated", formatTime(s.Updated))
	}
	if !s.Expires.IsZero() {
		exp := formatTime(s.Expires)
		if s.Expires.Before(d.Now()) {
			exp = st.Error.Render(exp + " (expired)")
		}
		field("expires", exp)
	}
	if s.ContentType != "" {
		field("content", s.ContentType)
	}
	for _, a := range s.Attrs {
		field(a.Label, a.Value)
	}
	if len(s.Tags) > 0 {
		var tags []string
		for _, k := range slices.Sorted(maps.Keys(s.Tags)) {
			tags = append(tags, k+"="+s.Tags[k])
		}
		field("tags", strings.Join(tags, ", "))
	}
	if s.Reference != "" {
		field("ref", muted(s.Reference))
	}
	switch {
	case d.describing:
		out = append(out, muted(" loading details…"))
	case d.describeErr != nil:
		field("details", st.Error.Render(d.describeErr.Error()))
	}

	// Secrets are masked; keys and certificates only expose public material.
	// What the keys do with the value, on the line under it.
	hints := func(reveal string, more ...string) string {
		var h []string
		if d.RevealKey != "" {
			h = append(h, d.RevealKey+" to "+reveal)
		}
		if d.CopyKey != "" {
			h = append(h, d.CopyKey+" to copy")
		}
		return muted(strings.Join(append(h, more...), " · "))
	}
	label, hidden, reveal := "value", st.Masked.Render("••••••••"), "reveal"
	switch s.Type {
	case provider.TypeKey:
		label, hidden, reveal = "public", "", "show the public key"
	case provider.TypeCertificate:
		label, hidden, reveal = "cert", "", "show the certificate"
	}
	out = append(out, "")
	switch d.state {
	case ValueHidden:
		if hidden != "" {
			field(label, hidden)
			out = append(out, valueIndent+hints(reveal))
		} else {
			field(label, hints(reveal))
		}
	case ValueLoading:
		field(label, spinnerPlaceholder+" fetching…")
	case ValueFailed:
		// Errors can run over several lines (sops lists every key it
		// tried): all of them, each wrapped at spaces and styled alone,
		// so lines aren't padded to the longest.
		for i, line := range strings.Split(strings.TrimRight(d.err.Error(), "\n"), "\n") {
			// Keep a line's own indent, on each part it wraps into.
			text := strings.TrimLeft(line, " ")
			lead := strings.Repeat(" ", len(line)-len(text))
			for j, w := range strings.Split(ansi.Wrap(text, max(1, inner-11-len(lead)), ""), "\n") {
				w = lead + w
				if i == 0 && j == 0 {
					out = append(out, fieldLabel(st, label)+st.Error.Render(w))
				} else {
					out = append(out, valueIndent+st.Error.Render(w))
				}
			}
		}
	case ValueShown:
		if d.value.Reveal() == "" {
			// Not in the value's colour, nor a word a value could be.
			field(label, st.Muted.Italic(true).Render("∅ no value (an empty string)"))
			out = append(out, valueIndent+hints("hide"))
			break
		}
		// Where the dots were, continuing under itself; the keys below.
		first := true
		lines, note := valueView(d.value.Reveal())
		for _, line := range lines {
			for _, w := range strings.Split(ansi.Hardwrap(line, max(1, inner-11), true), "\n") {
				if first {
					out = append(out, fieldLabel(st, label)+st.Value.Render(w))
					first = false
				} else {
					out = append(out, valueIndent+st.Value.Render(w))
				}
			}
		}
		var more []string
		if note != "" && d.CopyKey != "" {
			note += ": " + d.CopyKey + " copies it as stored"
		}
		if note != "" {
			more = append(more, note)
		}
		if d.hidesIn != "" {
			more = append(more, "hides in "+d.hidesIn)
		}
		out = append(out, valueIndent+hints("hide", more...))
	}
	return append(out, d.versionLines(st)...)
}

// maxVersionLines caps the version history shown in the detail pane; the
// versions view (v) shows them all.
const maxVersionLines = 10

func (d Detail) versionLines(st styles.Styles) []string {
	if len(d.versions) == 0 {
		return nil
	}
	muted := st.Muted.Render
	head := fmt.Sprintf("%d", len(d.versions))
	if len(d.versions) > 1 {
		head = st.Notice.Render(head)
		if d.VersionsKey != "" {
			head += muted(" · " + d.VersionsKey + " to browse")
		}
	}
	out := []string{"", fieldLabel(st, "versions") + head}
	current := d.versions[0].Version
	for i, v := range d.versions {
		if i == maxVersionLines {
			out = append(out, muted(fmt.Sprintf("   … %d more", len(d.versions)-i)))
			break
		}
		// A list to read, not to move in (v browses them): no cursor. The
		// version shown above, when it isn't the current one, is named.
		line := "   " + VersionLine(st, v, d.Now(), current)
		if d.secret.Version != "" && v.Version == d.secret.Version && v.Version != current {
			line += muted(" · shown above")
		}
		out = append(out, line)
	}
	return out
}

// VersionLine draws a version as "id  date  state".
func VersionLine(st styles.Styles, v provider.Object, now time.Time, current string) string {
	id := v.Version
	if len(id) > 8 {
		id = id[:8]
	}
	line := fmt.Sprintf("%-8s %s", id, v.Updated.Local().Format("2006-01-02"))
	switch {
	case v.Enabled != nil && !*v.Enabled:
		return st.Muted.Render(line + " disabled")
	case !v.Expires.IsZero() && v.Expires.Before(now):
		return st.Muted.Render(line + " expired")
	case v.Version == current:
		return line + st.Notice.Render(" current")
	}
	return line
}

func formatTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }
