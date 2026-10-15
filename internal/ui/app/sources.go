package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/icons"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// The Sources pane either groups sources under a foldable header per
// provider, or lists them flat with the provider on each row.

type rowKind int

const (
	rowGroup rowKind = iota
	rowSource
	rowLinks // vaults opened from links that no account could locate
)

type sourceRow struct {
	kind      rowKind
	group     provider.Kind // colour and icon
	provider  string        // provider ID: one group per instance (account)
	label     string        // group: provider label, e.g. "Azure · keb"
	account   string        // flat source: account part of the label, if any
	status    string        // group: source count, "…" while loading, "!" on error
	collapsed bool          // group only
	flat      bool          // source only: shown without a group header
	top       bool          // source only: the repo lazysecrets started in, on top
	source    provider.Source
}

// sourceRowKey joins a provider ID and a source ID. "|" doesn't occur in
// either; provider IDs contain "/" (Azure:user/tenant).
func sourceRowKey(providerID, sourceID string) string { return cachefile.Row(providerID, sourceID) }

func splitSourceRow(row string) (providerID, sourceID string) {
	providerID, sourceID, _ = strings.Cut(row, "|")
	return providerID, sourceID
}

func sourceRowID(r sourceRow) string {
	switch r.kind {
	case rowGroup:
		return "group:" + r.provider
	case rowLinks:
		return linksRowID
	}
	return sourceRowKey(r.provider, r.source.ID)
}

func newSourcesList(iconSet icons.Set, mem *store.Store, recent bool) panes.List[sourceRow] {
	return panes.NewList("[1] Sources", panes.Item[sourceRow]{
		ID: sourceRowID,
		// Headers have no filter text, so filtering shows just sources.
		Filter: func(r sourceRow) string {
			if r.kind != rowSource {
				return ""
			}
			return r.source.Name
		},
		// So "data" finds Databricks workspaces and an account name finds
		// its subscriptions.
		FilterExtra: func(r sourceRow) string {
			if r.kind != rowSource {
				return ""
			}
			return cmp.Or(r.source.ProviderLabel, string(r.group)) + " " + mem.Node(sourceKey(r.source)).Note
		},
		Render: func(st styles.Styles, r sourceRow, width int) string {
			node := mem.Node(sourceKey(r.source))
			kind := st.Kind(r.group)
			starred := ""
			if r.kind == rowSource {
				starred = marks(st, node, r.source.Provider, recent, time.Now())
			}
			switch {
			case r.kind == rowLinks:
				return st.Notice.Render("↗ Opened links") + " " + st.Muted.Render(fmt.Sprintf("(%s)", r.status))
			case r.kind == rowSource && r.top:
				return withNote(st, kind.Render(iconSet.Prefix(r.group)+"Repo · ")+starred+kind.Render(r.source.Name), node.Note, width)
			case r.kind == rowSource && r.flat:
				line := kind.Render(iconSet.Prefix(r.group)+fmt.Sprintf("%-10s", r.group)) + " " + starred + r.source.Name
				if r.account != "" {
					line += st.Muted.Render(" · " + r.account)
				}
				return withNote(st, line+here(st, r.source), node.Note, width)
			case r.kind == rowSource:
				// The provider's colour, so long groups stay recognisable.
				return withNote(st, "  "+starred+kind.Render(r.source.Name), node.Note, width)
			}
			arrow := "▾"
			if r.collapsed {
				arrow = "▸"
			}
			status := st.Muted.Render(fmt.Sprintf("(%s)", r.status))
			if r.status == "!" {
				status = st.Error.Render("(!)")
			}
			return kind.Render(arrow+" "+iconSet.Prefix(r.group)+r.label) + " " + status
		},
	})
}

// rebuildSources lays out the Sources pane from the per-provider results.
func (m *Model) rebuildSources() {
	// While filtering, headers drop out, so rows show their provider and
	// account like the flat view (and folded groups are searched too).
	flat := m.flatSources || m.sources.HasFilter() || m.sourcesFavs
	m.sourcesFiltered = m.sources.HasFilter()
	// Tabs: everything, or just the starred ones ([ and ]).
	m.sources.Title, m.sources.Tabs = "[1]", []string{"Sources", "Favourites"}
	m.sources.Tab, m.sources.Empty = 0, "no sources found"
	if m.sourcesFavs {
		m.sources.Tab, m.sources.Empty = 1, "nothing starred"+withKey(m.keys.Favourite, ": %s on a subscription or workspace stars it")
	}
	var rows []sourceRow
	if n := len(m.linked); n > 0 && !m.sourcesFavs {
		rows = append(rows, sourceRow{kind: rowLinks, status: fmt.Sprint(n)})
	}
	// The repo lazysecrets started in gets a row of its own on top, so it
	// opens on it; flat, it sorts first instead.
	if src, ok := m.currentSource(); ok && !flat {
		rows = append(rows, sourceRow{kind: rowSource, group: src.Kind, provider: src.Provider, top: true, source: src})
	}
	for _, p := range m.providers {
		kind, id := p.Kind(), p.ID()
		account := accountOf(provider.Source{Kind: kind, ProviderLabel: p.Label()})
		r, done := m.sourceResults[id]
		sources := r.sources
		if !flat {
			sources = slices.DeleteFunc(slices.Clone(sources), func(s provider.Source) bool { return s.Here })
		}
		if kind == provider.KindRepo && !(done && (r.err != nil || len(sources) > 0)) {
			continue // repos show once some are found to have secret files
		}
		status := "…"
		switch {
		case done && r.err != nil:
			status = "!"
		case done:
			status = fmt.Sprint(len(sources))
		}
		if !flat {
			rows = append(rows, sourceRow{
				kind: rowGroup, group: kind, provider: id, label: p.Label(),
				status: status, collapsed: m.collapsed[id],
			})
			if m.collapsed[id] {
				continue
			}
		}
		for _, s := range m.starredSourcesFirst(sources) {
			if m.sourcesFavs && !m.store.Node(sourceKey(s)).StarredFor(s.Provider) {
				continue
			}
			rows = append(rows, sourceRow{
				kind: rowSource, group: kind, provider: id, account: account,
				flat: flat, source: s,
			})
		}
	}
	if flat {
		// Flat: starred sources go to the very top, across providers.
		slices.SortStableFunc(rows, func(a, b sourceRow) int {
			return starRank(m.store, a) - starRank(m.store, b)
		})
	}
	m.sources.SetItems(rows)

	// Until the user moves, keep the cursor on the first source in
	// provider order rather than a header, so the other panes fill in on
	// their own. Providers answer in any order, so this can change as
	// results arrive.
	if !m.sourcesTouched {
		m.sources.Top() // scroll back up so the first header shows
		for _, r := range rows {
			if r.kind == rowSource {
				m.sources.Select(sourceRowID(r))
				break
			}
		}
	}
}

// currentSource is the source lazysecrets started in: from its
// provider's list once that's in, else as found on its own beforehand.
func (m Model) currentSource() (provider.Source, bool) {
	for _, p := range m.providers {
		r, done := m.sourceResults[p.ID()]
		if !done {
			if m.current != nil && m.current.Provider == p.ID() {
				return *m.current, true
			}
			continue
		}
		for _, src := range r.sources {
			if src.Here {
				return src, true
			}
		}
	}
	return provider.Source{}, false
}

// starRank orders the Favourites row, then starred sources, then the rest.
func starRank(mem *store.Store, r sourceRow) int {
	switch {
	case r.kind == rowLinks:
		return 0
	case r.kind == rowSource && r.source.Here:
		return 1
	case r.kind == rowSource && mem.Node(sourceKey(r.source)).StarredFor(r.source.Provider):
		return 2
	}
	return 3
}

// selectedSource is the source under the cursor; false on a header.
func (m Model) selectedSource() (provider.Source, bool) {
	r, ok := m.sources.Selected()
	if !ok || r.kind != rowSource {
		return provider.Source{}, false
	}
	return r.source, true
}

// onLinks reports whether the cursor is on the Opened links row.
func (m Model) onLinks() bool {
	return m.sources.SelectedID() == linksRowID
}

// toggleSourcesView switches between grouped and flat, keeping the cursor
// on the same source.
func (m *Model) toggleSourcesView() {
	m.flatSources = !m.flatSources
	m.rebuildSources()
}

// toggleGroup folds or unfolds the group under the cursor. It reports
// whether the cursor was on a group header.
func (m *Model) toggleGroup() bool {
	r, ok := m.sources.Selected()
	if !ok || r.kind != rowGroup {
		return false
	}
	m.collapsed[r.provider] = !m.collapsed[r.provider]
	m.rebuildSources()
	m.sources.Select(sourceRowID(r))
	return true
}

// here labels the source lazysecrets was started in, in the flat list
// (grouped, it has the "Repo · name" row on top).
func here(st styles.Styles, s provider.Source) string {
	if s.Here {
		return st.Muted.Render(" · here")
	}
	return ""
}
