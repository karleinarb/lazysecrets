package app

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// The fourth pane shows Fields for anything but a secret. On a provider's
// row: its account, from what the provider knows locally.

// A file's details beyond its listing load once a
// session, when the cursor rests on it, and again with refresh. They're
// extra: moving on cancels the load, and search never waits for them.
type detailsState struct {
	attrs   []provider.Attr
	err     error
	loading bool
}

type detailsMsg struct {
	req   int
	key   string
	attrs []provider.Attr
	err   error
}

// detailOf is the pane the fourth pane describes: the one in focus, or,
// with the fourth in focus, the one it was entered from.
func (m Model) detailOf() pane {
	if m.focus != paneDetail {
		return m.focus
	}
	return m.lastLeft
}

// syncFields shows the Fields pane for what the focused pane has selected,
// unless that's a secret (or a vault with the Secrets pane in focus), which
// the secret pane shows, as it does a revealed value whatever the focus.
func (m *Model) syncFields() tea.Cmd {
	if m.detail.State() != panes.ValueHidden {
		// A value revealed (or being fetched) from any pane stays in
		// view until it's hidden.
		m.fields.Hide()
		return nil
	}
	m.fields.Header, m.fields.HeaderShort = nil, ""
	switch m.detailOf() {
	case paneSources:
		r, ok := m.sources.Selected()
		switch {
		case m.nothingFound():
			m.fields.Show("get started", m.getStartedFields())
		case !ok:
			m.fields.Show("", nil)
		case m.selectedProblem() != "":
			m.fields.Show(cmpOr(r.label, r.source.Name), m.problemFields(m.selectedProblem()))
		case r.kind == rowGroup:
			m.showAccount(r)
		case r.kind == rowSource:
			m.fields.Show(r.source.Name, m.sourceFields(r.source))
		default:
			m.fields.Show("", nil)
		}
	case paneStatus:
		m.fields.Header, m.fields.HeaderShort = logo, strings.TrimSpace("lazysecrets "+m.versionLabel())
		m.fields.Show("lazysecrets", m.statusFields())
	case paneContainers:
		if c, ok := m.containers.Selected(); ok {
			cmd := m.loadDetails(c)
			m.fields.Show(c.Name, m.containerFields(c))
			return cmd
		} else {
			m.fields.Show("", nil)
		}
	default:
		m.fields.Hide()
	}
	return nil
}

// showAccount shows a provider's account.
func (m *Model) showAccount(r sourceRow) {
	p := m.byID[r.provider]
	var fields []panes.Field
	if a, ok := p.(provider.Accounts); ok {
		if acct, ok := a.Account(); ok {
			for _, f := range acct.Fields {
				fields = append(fields, panes.Field{Label: f.Label, Value: f.Value})
			}
		}
	}
	if res, done := m.sourceResults[r.provider]; done && res.err == nil && len(res.sources) == 0 {
		hint := emptyHint(p.Kind())
		fields = append(fields, panes.Field{Label: "nothing", Value: "found: try", Info: true}, panes.Field{Value: hint.Value, URL: hint.URL})
	}
	m.fields.Show(p.Label(), fields)
}

// sourceFields describe a subscription, workspace or repo.
func (m Model) sourceFields(src provider.Source) []panes.Field {
	var out []panes.Field
	add := func(label, value string) {
		if value != "" {
			out = append(out, panes.Field{Label: label, Value: value})
		}
	}
	// Azure's detail is "subscription <ID>": the ID row says it.
	id, detail := "ID", ""
	switch src.Kind {
	case provider.KindDatabricks:
		id, detail = "profile", "host"
	case provider.KindRepo:
		id, detail = "path", "remote"
	}
	add("name", src.Name)
	add("note", m.store.Node(sourceKey(src)).Note)
	if src.ID != src.Name {
		add(id, src.ID)
	}
	if detail != "" {
		add(detail, src.Detail)
	}
	if src.ProviderLabel != string(src.Kind) {
		add("account", src.ProviderLabel)
	}
	add("web", src.URL)
	if e, ok := m.cache.containers[sourceRowKey(src.Provider, src.ID)]; ok {
		label := map[provider.Kind]string{provider.KindDatabricks: "scopes", provider.KindRepo: "files"}[src.Kind]
		add(cmpOr(label, "vaults"), fmt.Sprint(len(e.value)))
	}
	return out
}

// containerFields describe a vault, scope or file.
func (m Model) containerFields(c provider.Container) []panes.Field {
	var out []panes.Field
	add := func(label, value string) {
		if value != "" {
			out = append(out, panes.Field{Label: label, Value: value})
		}
	}
	add("name", c.Name)
	add("note", m.store.Node(storeKey(c)).Note)
	// The vault's URI, the scope's backend, the file's format.
	switch c.Source.Kind {
	case provider.KindAzure:
		add("URI", c.Detail)
	case provider.KindDatabricks:
		add("backend", c.Detail)
	case provider.KindRepo:
		add("format", c.Detail)
	}
	add("source", cmpOr(c.Source.ProviderLabel, string(c.Source.Kind))+" / "+c.Source.Name)
	add("web", c.URL)
	for _, t := range c.ObjectTypes() {
		if e, ok := m.cache.objects[containerKey(c)+"|"+string(t)]; ok {
			add(strings.ToLower(string(t))+"s", fmt.Sprint(len(e.value)))
		}
	}
	for _, a := range c.Attrs {
		out = append(out, panes.Field{Label: a.Label, Value: a.Value, Err: a.Warn})
	}
	switch st := m.details[containerKey(c)]; {
	case st.loading:
		out = append(out, panes.Field{Label: "details", Value: "loading…", Info: true})
	case errors.Is(st.err, provider.ErrUnsupported):
	case st.err != nil:
		out = append(out, panes.Field{Label: "details", Value: "couldn't load them: " + st.err.Error(), Info: true, Err: true})
	}
	for _, a := range m.details[containerKey(c)].attrs {
		out = append(out, panes.Field{Label: a.Label, Value: a.Value, Err: a.Warn})
	}
	return out
}

// loadDetails loads c's details, if its provider has them and they
// aren't loaded yet.
func (m *Model) loadDetails(c provider.Container) tea.Cmd {
	d, ok := m.providerFor(c.Source).(provider.ContainerDetails)
	key := containerKey(c)
	if _, seen := m.details[key]; !ok || seen {
		return nil
	}
	// One at a time: the previous one, if it's still loading, is
	// cancelled and forgotten, to load when it's shown again.
	for k, st := range m.details {
		if st.loading {
			delete(m.details, k)
		}
	}
	m.details[key] = detailsState{loading: true}
	ctx, req := m.begin(slotDetails)
	return m.settled(ctx, func() tea.Msg {
		attrs, err := d.Details(ctx, c)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return detailsMsg{req: req, key: key, attrs: attrs, err: err}
	})
}

// problemFields show why an account has nothing to show, wrapped to the
// pane, in red.
func (m Model) problemFields(problem string) []panes.Field {
	var out []panes.Field
	for _, l := range strings.Split(ansi.Wrap(problem, max(20, m.fields.Width()-16), " "), "\n") {
		out = append(out, panes.Field{Value: l, Info: true, Err: true})
	}
	out[0].Label = "problem"
	return out
}

// fieldName names a copied field: its label, or a row of several under
// one label (a tag), its value.
func fieldName(f panes.Field) string {
	return cmpOr(f.Label, f.Value)
}
