package app

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/expiry"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// What lazysecrets remembers about subscriptions, vaults and secrets —
// stars, notes — lives in the store, one record per item keyed by its
// lazysecrets:// link. Keys leave out the account, so a star or note on a
// subscription shows under every account that sees it.

const star = "★ "

// recentMark marks items used lately (Recent), with ui.recentIcon.
const recentMark = "◷ "

// marks is what goes before a list row's name: ★ when it's starred for
// account and, with recent, ◷ when it was used lately.
func marks(st styles.Styles, n store.Node, account string, recent bool, now time.Time) string {
	var s string
	if n.StarredFor(account) {
		s = st.Favourite.Render(star)
	}
	if recent && n.UsedLately(now) {
		s += st.Muted.Render(recentMark)
	}
	return s
}

func sourceKey(s provider.Source) string {
	if r, err := ref.ForSource(s); err == nil {
		return r.String()
	}
	return "source:" + string(s.Kind) + "/" + s.ID
}

func storeKey(c provider.Container) string {
	if r, err := ref.ForContainer(c, provider.TypeSecret); err == nil {
		return r.String()
	}
	return "container:" + c.ID
}

func objectKey(o provider.Object) string {
	if r, err := ref.ForObject(o, ""); err == nil {
		return r.String()
	}
	return "object:" + o.ID
}

// remember records context with a change, so lists of remembered items
// can show and jump to them later.
func (m *Model) remember(key, name string, path []string, src provider.Source, change func(*store.Node)) error {
	ctx := withContext(name, path, src)
	return m.store.Update(key, func(n *store.Node) {
		ctx(n)
		change(n)
	})
}

// withContext sets a record's context: where the item is, and the account
// that read it. A vault opened from a link has no known subscription;
// that keeps the one already recorded.
func withContext(name string, path []string, src provider.Source) func(*store.Node) {
	return func(n *store.Node) {
		n.Name = name
		if src.ID != "" || n.Source == "" {
			n.Path, n.Source = path, src.ID
		}
		if src.Provider != "" {
			n.Account = src.Provider
		}
	}
}

// refreshContext updates a remembered item's context after reaching it,
// e.g. the account that could read it. Items without a record are left
// alone, so browsing doesn't write the file.
func (m *Model) refreshContext(key, name string, path []string, src provider.Source) {
	if m.store.Has(key) && src.Provider != "" {
		_ = m.remember(key, name, path, src, func(*store.Node) {})
	}
}

// containerRender draws a vault or scope, starred or not. In the
// Opened links list it also names the source, since they come from many.
func (m Model) containerRender(withSource bool) func(styles.Styles, provider.Container, int) string {
	st, iconSet := m.store, m.icons
	return func(sty styles.Styles, c provider.Container, width int) string {
		n := st.Node(storeKey(c))
		line := c.Name
		if c.Source.Kind == provider.KindRepo {
			// A secret file: a lock for an encrypted one, a file in the
			// warning colour for plain text, and its type after it
			// ("sops · age", "env · plain text").
			icon, style := iconSet.Encrypted, sty.KindRepo
			if !strings.HasPrefix(c.Detail, "sops") {
				icon, style = iconSet.PlainFile, sty.Warning
			}
			if icon != "" {
				line = style.Render(icon) + " " + line
			}
			line += sty.Muted.Render("  " + c.Detail)
		}
		line = marks(sty, n, c.Source.Provider, m.recentIcon, m.now()) + line
		if withSource {
			src := c.Source.Name
			if account, ok := strings.CutPrefix(c.Source.ProviderLabel, string(c.Source.Kind)+" · "); ok {
				src = account + " / " + src
			}
			line += sty.Muted.Render(" · " + src)
		}
		return withNote(sty, line, n.Note, width)
	}
}

func (m Model) objectRender() func(styles.Styles, provider.Object, int) string {
	st := m.store
	return func(sty styles.Styles, o provider.Object, width int) string {
		n := st.Node(objectKey(o))
		line := o.Name
		if o.Enabled != nil && !*o.Enabled {
			line = sty.Muted.Render(o.Name + " (disabled)")
		}
		line = marks(sty, n, o.Container.Source.Provider, m.recentIcon, m.now()) + line
		switch state, left := m.expiry(o); state {
		case expiry.Expired:
			line += " " + sty.Error.Render("✗ expired")
		case expiry.Soon:
			line += " " + sty.Warning.Render("⚠ "+expiry.Left(left))
		}
		return withNote(sty, line, n.Note, width)
	}
}

// starredContainersFirst moves starred vaults and scopes to the front,
// keeping the order otherwise. It doesn't modify items, which may be cached.
func (m Model) starredContainersFirst(items []provider.Container) []provider.Container {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b provider.Container) int {
		return boolRank(m.store.Node(storeKey(a)).StarredFor(a.Source.Provider)) - boolRank(m.store.Node(storeKey(b)).StarredFor(b.Source.Provider))
	})
	return out
}

// starredFirst moves starred objects to the front, keeping the order
// otherwise. It doesn't modify items, which may be cached.
func (m Model) starredFirst(items []provider.Object) []provider.Object {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b provider.Object) int {
		return boolRank(m.store.Node(objectKey(a)).StarredFor(a.Container.Source.Provider)) - boolRank(m.store.Node(objectKey(b)).StarredFor(b.Container.Source.Provider))
	})
	return out
}

// showLinkedContainers lists the vaults opened from links.
func (m *Model) showLinkedContainers() {
	m.containers.Title, m.containers.Empty = "[2] Opened links", "no links opened"
	m.containers.SetRender(m.containerRender(true))
	m.containers.SetItems(m.linked)
	m.containers.Select(m.cache.cursor[linksRowID])
}

// rebind records the account that served a vault opened from a link. The
// container's key includes the account, so the selection moves to the
// new key without reloading. It returns the new key for the object list.
func (m *Model) rebind(c provider.Container) string {
	m.refreshContext(storeKey(c), c.Name, []string{c.Source.Name}, c.Source)
	for i := range m.linked {
		if m.linked[i].ID == c.ID {
			m.linked[i].Source = c.Source
		}
	}
	key := cachefile.ObjectsKey(c, m.objType)
	if m.onLinks() {
		m.containers.SetItems(m.linked)
		m.containers.Select(containerKey(c))
		m.selContainer = key
	}
	return key
}

// resortStarred puts starred items first again in every pane, after
// stars changed elsewhere (the favourites list).
func (m *Model) resortStarred() {
	m.rebuildSources()
	if !m.onLinks() {
		m.containers.SetItems(m.starredContainersFirst(m.containers.Items()))
	}
	if m.versionsOf == nil {
		m.secrets.SetItems(m.starredFirst(m.secrets.Items()))
	}
}

// toggleFavourite stars or unstars the selected subscription (Sources),
// vault or scope (Vaults) or object (Secrets and detail panes).
func (m *Model) toggleFavourite() tea.Cmd {
	var name string
	var on bool
	// A star is the item through the account showing it.
	var err error
	switch m.focus {
	case paneSources:
		src, ok := m.selectedSource()
		if !ok {
			m.flash = warning("select a subscription or workspace to star")
			return nil
		}
		name = src.Name
		toggle := func(n *store.Node) { on = n.ToggleStar(src.Provider) }
		if err = m.remember(sourceKey(src), src.Name, nil, src, toggle); err == nil {
			m.rebuildSources() // re-sorts; the cursor follows the source
		}
	case paneContainers:
		c, ok := m.containers.Selected()
		if !ok {
			return nil
		}
		name = c.Name
		toggle := func(n *store.Node) { on = n.ToggleStar(c.Source.Provider) }
		if err = m.remember(storeKey(c), c.Name, []string{c.Source.Name}, c.Source, toggle); err == nil {
			m.containers.SetItems(m.starredContainersFirst(m.containers.Items()))
		}
	default:
		if m.versionsOf != nil {
			m.flash = warning("star the item from the list rather than a version")
			return nil
		}
		o, ok := m.detail.Secret()
		if !ok {
			return nil
		}
		name = o.Name
		c := o.Container
		toggle := func(n *store.Node) { on = n.ToggleStar(c.Source.Provider) }
		if err = m.remember(objectKey(o), o.Name, []string{c.Source.Name, c.Name}, c.Source, toggle); err == nil {
			m.secrets.SetItems(m.starredFirst(m.secrets.Items()))
		}
	}
	switch {
	case err != nil:
		m.flash = flash{text: "saving: " + err.Error(), isErr: true}
	case on:
		m.flash = done("starred " + name)
	default:
		m.flash = done("unstarred " + name)
	}
	return nil
}
