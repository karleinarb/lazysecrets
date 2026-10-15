package app

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// The Favourites and Recent scopes of the Go to popup list what's
// remembered in the store, so they work before anything has loaded.

// storeEntries lists starred items (sources first, then vaults, then
// secrets) and recently used ones (newest first), as Go to entries.
// Entries read as paths: "󰠅 platform-prod / kv-app-prod / api-key · dev",
// the account last, and only when there are several.
func (m *Model) storeEntries() (favs, recent []option) {
	type entry struct {
		depth int // 0 source, 1 vault, 2 secret: sources list first
		sort  string
		opt   option
	}
	nodes := m.store.Nodes()
	keys := slices.Sorted(maps.Keys(nodes))

	// entries lists jumps to the item through each of accounts; nil
	// means through whichever accounts list a source, else the account
	// that last read the item.
	entries := func(prefix, key string, n store.Node, accounts []string) []entry {
		r, err := ref.Parse(key)
		if err != nil {
			return nil
		}
		var out []entry
		detail := m.objectDetail(key)
		add := func(path []string, acct string, depth int, run func(*Model) tea.Cmd) {
			account := ""
			if p, ok := m.byID[acct]; ok {
				account = accountOf(provider.Source{Kind: p.Kind(), ProviderLabel: p.Label()})
			}
			account = strings.Trim(detail+" · "+account, " ·")
			sort := strings.ToLower(strings.Join(path, "/")) + " " + account
			out = append(out, entry{depth, sort, option{
				label: m.pathLabel(r.Kind, path, account, m.refFileIcon(r)), note: n.Note, mark: " ",
				id: prefix + key + "|" + acct, star: key, starAccount: acct, starred: n.StarredFor(acct),
				plain: m.inPlainEnv(r),
				run: func(m *Model) tea.Cmd {
					m.used(key)
					return run(m)
				}}})
		}
		if r.IsSource() {
			for _, p := range m.providers {
				if p.Kind() != r.Kind || (accounts != nil && !slices.Contains(accounts, p.ID())) {
					continue
				}
				for _, src := range m.sourceResults[p.ID()].sources {
					if !sourceMatches(r, src) {
						continue
					}
					row := sourceRowKey(p.ID(), src.ID)
					add([]string{src.Name}, p.ID(), 0, func(m *Model) tea.Cmd {
						m.nav = &nav{sourceRow: row, sourceOnly: true}
						return nil
					})
				}
			}
			return out
		}
		if accounts == nil {
			accounts = []string{n.Account}
		}
		depth := 1
		if !r.IsContainer() {
			depth = 2
		}
		path := slices.Clone(n.Path)
		if len(path) == 0 && !r.IsContainer() {
			path = []string{r.ContainerName()} // no context recorded
		}
		for _, acct := range accounts {
			via := n
			via.Account = acct
			add(append(slices.Clone(path), cmp.Or(n.Name, r.Name, r.ContainerName())), acct, depth,
				func(m *Model) tea.Cmd { return m.jumpTo(r, via) })
		}
		return out
	}

	var starred []entry
	for _, k := range keys {
		if n := nodes[k]; len(n.StarredBy) > 0 {
			starred = append(starred, entries("fav|", k, n, n.StarredBy)...)
		}
	}
	slices.SortFunc(starred, func(a, b entry) int {
		return cmp.Or(a.depth-b.depth, strings.Compare(a.sort, b.sort))
	})
	for _, e := range starred {
		favs = append(favs, e.opt)
	}

	if m.history {
		var used []string
		for _, k := range keys {
			if nodes[k].UsedLately(m.now()) {
				used = append(used, k)
			}
		}
		slices.SortStableFunc(used, func(a, b string) int { return nodes[b].LastUsed.Compare(nodes[a].LastUsed) })
		for _, k := range used[:min(len(used), recentMax)] {
			for _, e := range entries("recent|", k, nodes[k], nil) {
				recent = append(recent, e.opt)
			}
		}
	}
	return favs, recent
}

// recentMax is how many recently used items Recent lists.
const recentMax = 20

// pathLabel draws a jump list entry: the provider's icon and the path in
// its colour, then the account.
func (m Model) pathLabel(kind provider.Kind, path []string, account, fileIcon string) string {
	style := m.st.Kind(kind)
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = style.Render(p)
	}
	if fileIcon != "" && len(parts) > 1 {
		parts[1] = fileIcon + " " + parts[1]
	}
	label := style.Render(m.icons.Prefix(kind)) + strings.Join(parts, m.st.Muted.Render(" / "))
	if account != "" {
		label += m.st.Muted.Render(" · " + account)
	}
	return label
}

// inPlainEnv reports whether r is a key in a plain env file, as far as
// the files loaded tell.
func (m Model) inPlainEnv(r ref.Ref) bool {
	c, ok := m.repoFile(r)
	return ok && r.Name != "" && c.Detail == provider.DetailPlainEnv
}

// repoFile is the repo file r is or is in, as far as the files loaded
// tell.
func (m Model) repoFile(r ref.Ref) (provider.Container, bool) {
	if r.Kind != provider.KindRepo || r.IsSource() {
		return provider.Container{}, false
	}
	file := r
	file.Name, file.Type = "", ""
	for _, e := range m.cache.containers {
		for _, c := range e.value {
			if storeKey(c) == file.String() {
				return c, true
			}
		}
	}
	return provider.Container{}, false
}

// refFileIcon is the icon of the repo file r is or is in, if it's loaded.
func (m Model) refFileIcon(r ref.Ref) string {
	if c, ok := m.repoFile(r); ok {
		return m.fileIcon(m.st, c)
	}
	return ""
}

// objectDetail is the Detail of the secret with the store key key, as far
// as the lists loaded tell.
func (m Model) objectDetail(key string) string {
	for _, e := range m.cache.objects {
		for _, o := range e.value {
			if o.Detail != "" && objectKey(o) == key {
				return o.Detail
			}
		}
	}
	return ""
}

// repoLabel draws a repo, or a file in it, as everywhere else: the repo
// icon and colour, "app / apps/api/.env".
func (m Model) repoLabel(repo string, file ...string) string {
	return m.pathLabel(provider.KindRepo, append([]string{repo}, file...), "", "")
}

// sourceMatches reports whether src is the subscription or workspace r
// links to.
func sourceMatches(r ref.Ref, src provider.Source) bool {
	if r.Kind != provider.KindAzure { // by host: workspaces, repos
		return strings.EqualFold(strings.TrimSuffix(src.Detail, "/"), r.Host)
	}
	return src.ID == r.Subscription
}

// accountOf is the account part of a source's provider label ("alice" in
// "Azure · alice"), or "" when there's a single account.
func accountOf(src provider.Source) string {
	account, ok := strings.CutPrefix(src.ProviderLabel, string(src.Kind)+" · ")
	if !ok {
		return ""
	}
	return account
}

// jumpTo goes to a remembered vault or secret. With the account that last
// read it signed in, it goes straight there; otherwise it uses the
// accounts that list its subscription, asking if there are several, or
// finds it like a link. Reaching it updates the remembered account.
func (m *Model) jumpTo(r ref.Ref, n store.Node) tea.Cmd {
	if n.Source == "" && !r.IsContainer() {
		// A secret without context: its vault's record may have it.
		parent := r
		parent.Type, parent.Name, parent.Version = "", "", ""
		if p := m.store.Node(parent.String()); p.Source != "" {
			n.Source, n.Account = p.Source, cmp.Or(n.Account, p.Account)
		}
	}
	if _, ok := m.byID[n.Account]; ok && n.Source != "" {
		m.startNav(r, actGo, sourceRowKey(n.Account, n.Source))
		return nil
	}
	choices := m.openWith(r.Kind, func(src provider.Source) bool { return src.ID == n.Source && n.Source != "" })
	switch len(choices) {
	case 0:
		return m.openRef(r, actGo)
	case 1:
		m.startNav(r, actGo, choices[0].sourceRow)
	default:
		m.chooseSource("Several accounts can see "+r.ContainerName(), r, actGo, choices, false)
	}
	return nil
}
