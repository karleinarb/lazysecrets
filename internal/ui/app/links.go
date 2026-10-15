package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// Opening references: a reference (lazysecrets:// link, Key Vault URL, …)
// is first resolved to a source, then the panes are stepped through to it
// as each list loads. Copy and reveal actions ask before reading the value.

// linksRowID is the Sources row listing vaults opened from links that no
// account could locate (e.g. no read access to the vault resource).
const linksRowID = "links"

type navStage int

const (
	navSource navStage = iota
	navContainer
	navObject
	navVersion
	navAction
)

// nav is an open in progress.
type nav struct {
	ref ref.Ref
	act linkAction
	// sourceOnly stops at the source (jumping to a starred subscription).
	sourceOnly bool

	sourceRow string // Sources row to select
	stage     navStage
	// quiet gives up without a word: going back to where lazysecrets
	// was left, which may be gone.
	quiet bool
	// link ends on the object's details, as enter on it would: opening
	// a link to it opens it, rather than just finding it in its list.
	link bool
}

type resolvedMsg struct {
	ref       ref.Ref
	act       linkAction
	sourceRow string
	others    []string // other accounts' rows that also found the vault
	// find ends in the list, like Go to, not on the details: opened by a
	// tool that found it (lazysecrets open --action, from fzf).
	find    bool
	choices []choice // several candidates: ask
	linked  *provider.Container
	err     error
}

type choice struct {
	label     string
	sourceRow string
}

// linkAction is what to do with an object once it's opened. Links don't
// carry it: whoever opens one chooses.
type linkAction int

const (
	actGo   linkAction = iota // just go to it
	actCopy                   // and copy its value
	actShow                   // and show its value
)

// offerRef asks what to do with a reference: for an object, go to it, or
// go to it and copy or show its value; for a subscription, vault or scope,
// just open it.
// With ask false, vault and scope links open straight away.
func (m *Model) offerRef(title string, r ref.Ref, ask bool) tea.Cmd {
	if r.IsShare() {
		return m.offerShare(r)
	}
	if r.IsContainer() || r.IsSource() {
		if !ask {
			return m.openRef(r, actGo)
		}
		m.showMenu(title, nil, []option{
			{key: "o", label: "Open it", run: func(m *Model) tea.Cmd { return m.openRef(r, actGo) }},
		})
		m.prompt.about = &r
		return nil
	}
	do := func(a linkAction) func(*Model) tea.Cmd {
		return func(m *Model) tea.Cmd { return m.openRef(r, a) }
	}
	opts := []option{
		{key: "o", label: "Go to it", run: do(actGo)},
		{key: "c", label: "Go to it and copy its value", run: do(actCopy)},
		{key: "r", label: "Go to it and show its value", run: do(actShow)},
	}
	if r.Kind == provider.KindAzure || r.Host != "" { // {{secrets/s/k}} names no workspace
		opts = append(opts, option{key: "y", label: "Copy its value, staying here", run: func(m *Model) tea.Cmd { return m.copyFromRef(r) }})
	}
	m.showMenu(title, nil, opts)
	m.prompt.about = &r
	return nil
}

// copyFromRef copies the value a link points at without going there. The
// link says enough to read it: the vault and name (Azure, tried with each
// account, the one that last read it first) or the workspace, scope and
// key (Databricks).
func (m *Model) copyFromRef(r ref.Ref) tea.Cmd {
	type try struct {
		p provider.Provider
		o provider.Object
	}
	var tries []try
	key := r
	key.Version = ""
	node := m.store.Node(key.String())
	for _, p := range m.providers {
		if p.Kind() != r.Kind {
			continue
		}
		o := provider.Object{Name: r.Name, Type: cmp.Or(r.Type, provider.TypeSecret), Version: r.Version}
		src := provider.Source{Kind: r.Kind, Provider: p.ID(), ID: node.Source}
		if len(node.Path) > 0 {
			src.Name = node.Path[0]
		}
		if r.Kind != provider.KindAzure { // by host: Databricks workspaces, repos
			o.Type = provider.TypeSecret
			src.ID = ""
			for _, s := range m.sourceResults[p.ID()].sources {
				if sourceMatches(r, s) {
					src = s
				}
			}
			if src.ID == "" {
				continue
			}
			o.Container = provider.Container{Source: src, Name: r.Scope}
		} else {
			o.Container = provider.Container{Source: src, Name: r.ContainerName(), Detail: r.VaultURL()}
		}
		t := try{p, o}
		if p.ID() == node.Account {
			tries = append([]try{t}, tries...)
		} else {
			tries = append(tries, t)
		}
	}
	if len(tries) == 0 {
		m.flash = flash{text: "copy: no account or profile for " + describeRef(r), isErr: true}
		return nil
	}
	ctx, req := m.begin(slotCopy, r.Kind)
	m.flash = flash{text: "copying " + r.Name + "…"}
	return func() tea.Msg {
		var errs []error
		for _, t := range tries {
			v, err := t.p.Value(ctx, t.o)
			if err == nil {
				msg := copyMsg{req: req, name: r.Name, value: v}
				if t.o.Container.Source.ID != "" {
					msg.obj = t.o // where it is is known: count the use
				}
				return msg
			}
			if ctx.Err() != nil {
				return copyMsg{req: req, name: r.Name, err: err}
			}
			errs = append(errs, fmt.Errorf("%s: %w", t.p.Label(), err))
		}
		if len(errs) == 1 {
			return copyMsg{req: req, name: r.Name, err: errors.Unwrap(errs[0])} // one account: no need to name it
		}
		return copyMsg{req: req, name: r.Name, err: errors.Join(errs...)}
	}
}

// openRef starts opening a reference, doing act once there.
func (m *Model) openRef(r ref.Ref, act linkAction) tea.Cmd {
	m.nav = nil
	if r.IsSource() {
		m.openSource(r)
		return nil
	}
	m.flash = flash{text: "opening " + describeRef(r) + "…"}
	cands := m.providers
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultResolveTimeout)
		defer cancel()
		msg := resolve(ctx, cands, r)
		msg.act = act
		return msg
	}
}

const defaultResolveTimeout = 30 * time.Second

// openSource goes to a subscription or workspace, asking which account
// when several list it. Sources are loaded already, so it needs no lookup.
func (m *Model) openSource(r ref.Ref) {
	choices := m.openWith(r.Kind, func(src provider.Source) bool { return sourceMatches(r, src) })
	if row, ok := preferredRow(r, choices); ok {
		choices = []choice{{sourceRow: row}}
	}
	switch len(choices) {
	case 0:
		m.flash = flash{text: "open: " + describeRef(r) + " isn't in your sources", isErr: true}
	case 1:
		m.nav = &nav{ref: r, sourceRow: choices[0].sourceRow, sourceOnly: true}
	default:
		var opts []option
		for i, c := range choices {
			row := c.sourceRow
			opts = append(opts, option{key: fmt.Sprint(i + 1), label: c.label, run: func(m *Model) tea.Cmd {
				m.nav = &nav{ref: r, sourceRow: row, sourceOnly: true}
				return nil
			}})
		}
		m.showMenu("Several accounts can see "+describeRef(r), nil, opts)
	}
}

// openWith offers each loaded source of kind that matches, one per
// account that lists it.
func (m Model) openWith(kind provider.Kind, matches func(provider.Source) bool) []choice {
	var choices []choice
	for _, p := range m.providers {
		if p.Kind() != kind {
			continue
		}
		for _, src := range m.sourceResults[p.ID()].sources {
			if matches(src) {
				row := sourceRowKey(p.ID(), src.ID)
				choices = append(choices, choice{label: "Open with " + m.sourceLabel(row), sourceRow: row})
			}
		}
	}
	return choices
}

// refLabel draws what a link points at like the favourites list: the
// provider's icon and the path in its colour, "󰠅 platform-prod /
// kv-app-prod / api-key", with the type and version after it. The
// subscription or workspace isn't in the link: it comes from what's
// remembered about the item, or from lists already loaded.
func (m Model) refLabel(r ref.Ref) string {
	var path []string
	if src := m.refSource(r); src != "" {
		path = append(path, src)
	}
	switch {
	case r.IsSource():
		if len(path) == 0 {
			path = append(path, cmp.Or(r.Subscription, r.Host))
		}
	case r.IsContainer():
		path = append(path, r.ContainerName())
	default:
		path = append(path, r.ContainerName(), r.Name)
	}
	label := m.pathLabel(r.Kind, path, "")
	var extra []string
	if t := strings.ToLower(pluralLabels[r.Type]); t != "" && r.Type != provider.TypeSecret {
		if r.IsContainer() {
			extra = append(extra, t+" tab")
		} else {
			extra = append(extra, strings.TrimSuffix(t, "s")) // "key", "certificate"
		}
	}
	if r.Version != "" {
		extra = append(extra, "version "+r.Version)
	}
	if len(extra) > 0 {
		label += m.st.Muted.Render(" · " + strings.Join(extra, " · "))
	}
	return label
}

// refSource names the subscription or workspace r is in, if known.
func (m Model) refSource(r ref.Ref) string {
	key := r
	key.Version = ""
	if n := m.store.Node(key.String()); len(n.Path) > 0 {
		return n.Path[0]
	}
	for _, res := range m.sourceResults {
		for _, src := range res.sources {
			if (r.Kind != provider.KindAzure || r.IsSource()) && src.Kind == r.Kind && sourceMatches(r, src) {
				return src.Name
			}
		}
	}
	if r.Kind == provider.KindAzure && !r.IsSource() {
		for _, e := range m.cache.containers {
			for _, c := range e.value {
				if matchesContainer(c, r) {
					return c.Source.Name
				}
			}
		}
	}
	return ""
}

func describeRef(r ref.Ref) string {
	if r.IsSource() {
		switch r.Kind {
		case provider.KindDatabricks:
			return "workspace " + r.Host
		case provider.KindRepo:
			return "repo " + r.Host
		}
		return "subscription " + r.Subscription
	}
	if r.IsContainer() {
		return r.ContainerName()
	}
	return r.Name + " in " + r.ContainerName()
}

// resolve finds the source holding the reference's container.
func resolve(ctx context.Context, providers []provider.Provider, r ref.Ref) resolvedMsg {
	msg := resolvedMsg{ref: r}
	var errs []error
	switch {
	case r.Kind == provider.KindDatabricks && r.Host == "":
		// {{secrets/scope/key}}: search every workspace for the scope.
		for _, p := range providers {
			if p.Kind() != provider.KindDatabricks {
				continue
			}
			srcs, err := p.Sources(ctx)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, src := range srcs {
				cs, err := p.Containers(ctx, src)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", src.Name, err))
					continue
				}
				for _, c := range cs {
					if c.Name == r.Scope {
						msg.choices = append(msg.choices, choice{label: src.Name, sourceRow: sourceRowKey(p.ID(), src.ID)})
					}
				}
			}
		}
		switch len(msg.choices) {
		case 0:
			msg.err = errors.Join(append([]error{fmt.Errorf("scope %q isn't in any workspace", r.Scope)}, errs...)...)
		case 1:
			msg.sourceRow, msg.choices = msg.choices[0].sourceRow, nil
		}
		return msg
	}

	for _, p := range providers {
		loc, ok := p.(provider.Locator)
		if !ok || p.Kind() != r.Kind {
			continue
		}
		id, found, err := loc.Locate(ctx, r.Host)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Label(), err))
			continue
		}
		if found {
			// Several accounts may see it, with different access: the
			// user picks.
			row := sourceRowKey(p.ID(), id)
			if msg.sourceRow == "" {
				msg.sourceRow = row
			} else {
				msg.others = append(msg.others, row)
			}
		}
	}
	if msg.sourceRow != "" {
		return msg
	}
	if r.Kind == provider.KindAzure {
		// No account can find the vault resource, but one may still read
		// it: open it under "Opened links", where each account is tried.
		msg.linked = &provider.Container{
			Source: provider.Source{Kind: provider.KindAzure, Name: r.Host},
			ID:     r.VaultURL(), Name: r.ContainerName(), Detail: r.VaultURL(),
			Types: []provider.ObjectType{provider.TypeSecret, provider.TypeKey, provider.TypeCertificate},
		}
		return msg
	}
	missing := fmt.Errorf("no Databricks profile for workspace %s", r.Host)
	if r.Kind == provider.KindRepo {
		missing = fmt.Errorf("repo %s isn't here: start lazysecrets in it, or list it under providers.repos.paths", r.Host)
	}
	msg.err = errors.Join(append([]error{missing}, errs...)...)
	return msg
}

func (m *Model) handleResolved(msg resolvedMsg) {
	switch {
	case msg.err != nil:
		m.flash = flash{text: "open: " + msg.err.Error(), isErr: true}
	case len(msg.choices) > 1:
		for i := range msg.choices {
			msg.choices[i].label = "Open in " + msg.choices[i].label
		}
		m.chooseSource("Scope "+msg.ref.Scope+" is in several workspaces", msg.ref, msg.act, msg.choices, !msg.find)
	case len(msg.others) > 0:
		var choices []choice
		for _, row := range append([]string{msg.sourceRow}, msg.others...) {
			choices = append(choices, choice{label: "Open with " + m.sourceLabel(row), sourceRow: row})
		}
		if row, ok := preferredRow(msg.ref, choices); ok {
			m.navResolved(msg, row)
			return
		}
		m.chooseSource("Several accounts can see "+msg.ref.ContainerName(), msg.ref, msg.act, choices, !msg.find)
	case msg.linked != nil:
		m.addLinked(*msg.linked)
		m.navResolved(msg, linksRowID)
	default:
		m.navResolved(msg, msg.sourceRow)
	}
}

// preferredRow is the choice under the account the reference asks for
// (ref.Ref.Account), if it's one of them.
func preferredRow(r ref.Ref, choices []choice) (string, bool) {
	for _, c := range choices {
		if r.Account != "" && cachefile.ProviderOf(c.sourceRow) == r.Account {
			return c.sourceRow, true
		}
	}
	return "", false
}

// navResolved goes to what a resolved link points at, under row.
func (m *Model) navResolved(msg resolvedMsg, row string) {
	if msg.find {
		m.startNav(msg.ref, msg.act, row)
		return
	}
	m.startLinkNav(msg.ref, msg.act, row)
}

// findRef is openRef ending in the list, like Go to: for a link a tool
// found (lazysecrets open --action).
func (m *Model) findRef(r ref.Ref, act linkAction) tea.Cmd {
	cmd := m.openRef(r, act)
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if rm, ok := msg.(resolvedMsg); ok {
			rm.find = true
			return rm
		}
		return msg
	}
}

// startLinkNav opens a link: like startNav, ending on its object's
// details.
func (m *Model) startLinkNav(r ref.Ref, act linkAction, sourceRow string) {
	m.startNav(r, act, sourceRow)
	m.nav.link = true
}

// chooseSource asks where to open r.
func (m *Model) chooseSource(title string, r ref.Ref, act linkAction, choices []choice, link bool) {
	var opts []option
	for i, c := range choices {
		key := ""
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		row := c.sourceRow
		opts = append(opts, option{key: key, label: c.label, run: func(m *Model) tea.Cmd {
			m.startNav(r, act, row)
			m.nav.link = link
			return nil
		}})
	}
	m.showMenu(title, nil, opts)
}

// sourceLabel names a Sources row as "Azure · admin / app-dev".
func (m Model) sourceLabel(row string) string {
	providerID, sourceID := splitSourceRow(row)
	label := providerID
	if p, ok := m.byID[providerID]; ok {
		label = p.Label()
	}
	for _, s := range m.sourceResults[providerID].sources {
		if s.ID == sourceID {
			return label + " / " + s.Name
		}
	}
	return label
}

func (m *Model) startNav(r ref.Ref, act linkAction, sourceRow string) {
	m.nav = &nav{ref: r, act: act, sourceRow: sourceRow}
	m.versionsOf = nil
	m.flash = flash{}
}

// addLinked lists a vault under "Opened links" for this session.
func (m *Model) addLinked(c provider.Container) {
	for _, l := range m.linked {
		if l.ID == c.ID {
			return
		}
	}
	m.linked = append(m.linked, c)
	m.rebuildSources()
}

func (m *Model) navFail(format string, a ...any) {
	if !m.nav.quiet {
		m.flash = flash{text: "open: " + fmt.Sprintf(format, a...), isErr: true}
	}
	m.nav = nil
}

// advanceNav moves an open along as far as the loaded lists allow. It
// reports whether it got further, so the caller can sync and try again.
func (m *Model) advanceNav() bool {
	n := m.nav
	if n == nil {
		return false
	}
	r := n.ref
	switch n.stage {
	case navSource:
		if !m.sources.Select(n.sourceRow) {
			group, _ := splitSourceRow(n.sourceRow)
			if m.collapsed[group] {
				m.collapsed[group] = false
				m.rebuildSources()
				return true
			}
			if m.sourcesPending > 0 {
				return false // still loading
			}
			m.navFail("%s isn't in the Sources list (hidden in the config?)", describeRef(r))
			return false
		}
		m.sourcesTouched = true
		m.focus = paneContainers
		if n.sourceOnly {
			m.nav = nil
			return true
		}
		n.stage = navContainer
		return true

	case navContainer:
		if m.selSource != n.sourceRow || m.containers.Loading {
			return false
		}
		if !m.containers.SelectWhere(func(c provider.Container) bool { return matchesContainer(c, r) }) {
			m.navFail("%s not found", r.ContainerName())
			return false
		}
		if r.Type != "" {
			m.wantType = r.Type
		}
		if !r.IsContainer() {
			m.focus = paneSecrets
		}
		n.stage = navObject
		return true

	case navObject:
		c, _ := m.containers.Selected()
		if (r.Type != "" && m.objType != r.Type) || m.secrets.Loading ||
			!strings.HasPrefix(m.selContainer, containerKey(c)+"|") {
			return false
		}
		if m.secrets.Err != nil {
			m.nav = nil // the pane shows the error
			return false
		}
		// Reached it, and its account can read it: remember that.
		m.refreshContext(storeKey(c), c.Name, []string{c.Source.Name}, c.Source)
		if r.IsContainer() {
			m.nav = nil
			return true
		}
		if !m.secrets.SelectWhere(func(o provider.Object) bool { return o.Name == r.Name }) {
			m.navFail("%s not found in %s", r.Name, r.ContainerName())
			return false
		}
		if o, ok := m.secrets.Selected(); ok {
			m.refreshContext(objectKey(o), o.Name, []string{c.Source.Name, c.Name}, c.Source)
		}
		if r.Version != "" {
			o, _ := m.secrets.Selected()
			m.versionsOf = &o
			n.stage = navVersion
			return true
		}
		n.stage = navAction
		return true

	case navVersion:
		if m.versionsOf == nil || m.secrets.Loading || m.secrets.Err != nil {
			if m.secrets.Err != nil {
				m.nav = nil
			}
			return false
		}
		if !m.secrets.SelectWhere(func(o provider.Object) bool { return o.Version == r.Version }) {
			m.flash = flash{text: "version " + r.Version + " not found; showing all versions", isErr: true}
		}
		n.stage = navAction
		return true

	case navAction:
		// The user chose this when opening the link; no need to ask again.
		m.nav = nil
		switch n.act {
		case actCopy:
			m.pendingCmd = m.copyValue()
		case actShow:
			if m.detail.State() != panes.ValueShown {
				m.pendingCmd = m.toggleReveal()
			}
		}
		if n.link {
			m.focus = paneDetail // after: copying acts on the focused pane
		}
		return true
	}
	return false
}

// matchesContainer compares by vault URL (Azure), or scope or file name.
func matchesContainer(c provider.Container, r ref.Ref) bool {
	if r.Kind != provider.KindAzure {
		return c.Name == r.Scope
	}
	return strings.EqualFold(strings.TrimSuffix(c.Detail, "/")+"/", r.VaultURL())
}

// share copies a link to the focused item. With menu, it offers the
// other things worth sharing about it.
func (m *Model) share(menu bool) tea.Cmd {
	switch m.focus {
	case paneSources:
		s, ok := m.selectedSource()
		if !ok {
			m.flash = warning("select a subscription, workspace, vault or secret to share")
			return nil
		}
		if !menu {
			return m.shareSource(s, "o")
		}
		share := func(k string) func(*Model) tea.Cmd {
			return func(m *Model) tea.Cmd { return m.shareSource(s, k) }
		}
		opts := []option{{key: "o", label: "Lazysecrets link", run: share("o")}}
		if s.URL != "" {
			opts = append(opts, option{key: "b", label: webLabel(s.Kind), run: share("b")})
		}
		if m.canShareFiles(s) {
			opts = append(opts, option{key: "f", label: "Encrypted link to its env files", run: func(m *Model) tea.Cmd { return m.shareFiles(s, nil) }})
		}
		m.showMenu("Share "+s.Name, nil, opts)
	case paneContainers:
		c, ok := m.containers.Selected()
		if !ok {
			return nil
		}
		if !menu {
			return m.shareContainer(c, "o")
		}
		share := func(k string) func(*Model) tea.Cmd {
			return func(m *Model) tea.Cmd { return m.shareContainer(c, k) }
		}
		opts := []option{{key: "o", label: "Lazysecrets link", run: share("o")}}
		if c.Source.Kind == provider.KindAzure && m.objType != provider.TypeSecret {
			opts = append(opts, option{key: "t", label: "Lazysecrets link to the " + tabLabels[m.objType] + " tab", run: share("t")})
		}
		if c.Source.Kind == provider.KindAzure {
			opts = append(opts, option{key: "u", label: "Vault URL", run: share("u")})
		}
		if c.URL != "" {
			opts = append(opts, option{key: "b", label: webLabel(c.Source.Kind), run: share("b")})
		}
		if c.Detail == provider.DetailPlainEnv && m.canShareFiles(c.Source) {
			opts = append(opts, option{key: "f", label: "Encrypted link to the file", run: func(m *Model) tea.Cmd {
				return m.shareFiles(c.Source, []provider.Container{c})
			}})
		}
		m.showMenu("Share "+c.Name, nil, opts)
	default:
		o, ok := m.detail.Secret()
		if !ok {
			return nil
		}
		if !menu {
			return m.shareObject(o, "o")
		}
		share := func(k string) func(*Model) tea.Cmd {
			return func(m *Model) tea.Cmd { return m.shareObject(o, k) }
		}
		opts := []option{
			{key: "o", label: "Lazysecrets link", run: share("o")},
		}
		if o.Container.Source.Kind == provider.KindAzure {
			opts = append(opts, option{key: "u", label: "Key Vault URL", run: share("u")})
		}
		if o.Reference != "" {
			opts = append(opts, option{key: "y", label: "Config reference", run: share("y")})
		}
		if o.URL != "" {
			opts = append(opts, option{key: "b", label: webLabel(o.Container.Source.Kind), run: share("b")})
		}
		if c := o.Container; c.Detail == provider.DetailPlainEnv && m.canShareFiles(c.Source) {
			opts = append(opts, option{key: "f", label: "Encrypted link to its file", run: func(m *Model) tea.Cmd {
				return m.shareFiles(c.Source, []provider.Container{c})
			}})
		}
		m.showMenu("Share "+o.Name, nil, opts)
	}
	return nil
}

// webLabel names the web page o (open in browser) opens.
func webLabel(kind provider.Kind) string {
	switch kind {
	case provider.KindDatabricks:
		return "Web page (Databricks workspace)"
	case provider.KindRepo:
		return "Web page (repository)"
	}
	return "Web page (Azure portal)"
}

// shareSource copies what the share menu's key k stands for, for a
// subscription or workspace.
func (m *Model) shareSource(s provider.Source, k string) tea.Cmd {
	m.usedSource(s)
	if k == "b" {
		if s.URL == "" {
			return nil
		}
		return m.copyText(s.Name+" web page", s.URL, false)
	}
	r, err := ref.ForSource(s)
	if err != nil {
		m.flash = flash{text: "share: " + err.Error(), isErr: true}
		return nil
	}
	return m.copyText("link to "+s.Name, r.String(), false)
}

// shareContainer copies what the share menu's key k stands for, for a
// vault or scope.
func (m *Model) shareContainer(c provider.Container, k string) tea.Cmd {
	m.usedContainer(c)
	tab := provider.TypeSecret
	switch k {
	case "b":
		if c.URL == "" {
			return nil
		}
		return m.copyText(c.Name+" web page", c.URL, false)
	case "t":
		tab = m.objType
	case "o", "u":
	default:
		return nil
	}
	r, err := ref.ForContainer(c, tab)
	if err != nil {
		m.flash = flash{text: "share: " + err.Error(), isErr: true}
		return nil
	}
	if k == "u" {
		if u := r.IdentifierURL(); u != "" {
			return m.copyText(c.Name+" URL", u, false)
		}
		return nil
	}
	return m.copyText("link to "+c.Name, r.String(), false)
}

// shareObject copies what the share menu's key k stands for.
func (m *Model) shareObject(o provider.Object, k string) tea.Cmd {
	m.usedObject(o)
	if k == "b" {
		if o.URL == "" {
			return nil
		}
		return m.copyText(o.Name+" web page", o.URL, false)
	}
	if k == "y" {
		if o.Reference == "" {
			return nil
		}
		return m.copyText(o.Name+" reference", o.Reference, false)
	}
	version := ""
	if m.versionsOf != nil {
		version = o.Version // shared from the versions view: that version
	}
	r, err := ref.ForObject(o, version)
	if err != nil {
		m.flash = flash{text: "share: " + err.Error(), isErr: true}
		return nil
	}
	switch k {
	case "u":
		if u := r.IdentifierURL(); u != "" {
			return m.copyText(o.Name+" URL", u, false)
		}
		return nil
	}
	return m.copyText("link to "+o.Name, r.String(), false)
}

func (m *Model) startOpenPrompt() tea.Cmd {
	in := newTextInput("open: ", m.pathInputWidth())
	in.Placeholder = "lazysecrets:// link, Key Vault URL or reference"
	m.prompt = &modal{
		title: "Open a link", input: &in,
		lines:  []string{m.st.Muted.Render("lazysecrets:// link, Key Vault URL or reference")},
		submit: func(m *Model, text string) tea.Cmd { return m.openText(text) },
	}
	// Pre-fill from the clipboard, read now: it may hold any reference,
	// including ones not offered in the footer (our own, dismissed ones).
	clip := m.clip
	readClip := func() tea.Msg {
		text, err := clip.Read()
		if err != nil {
			return nil
		}
		text = strings.TrimSpace(text)
		if _, err := ref.Parse(text); err != nil || strings.ContainsAny(text, "\n") {
			return nil // not a reference: don't keep it
		}
		return openPromptFillMsg(text)
	}
	return tea.Batch(m.prompt.input.Focus(), readClip)
}

type openPromptFillMsg string

// fillOpenPrompt puts a reference from the clipboard in the open prompt,
// unless the user has started typing.
func (m *Model) fillOpenPrompt(text string) {
	if p := m.prompt; p != nil && p.input != nil && p.input.Value() == "" {
		p.input.SetValue(text)
		p.input.CursorEnd()
	}
}

// openText parses a pasted or typed reference and asks what to do with it.
func (m *Model) openText(s string) tea.Cmd {
	r, err := ref.Parse(s)
	if err != nil {
		m.flash = flash{text: "open: " + err.Error(), isErr: true}
		return nil
	}
	return m.offerRef("Open link", r, false)
}

// handlePaste opens a pasted reference, unless a text input has focus.
func (m *Model) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if m.search != nil && m.search.picker != nil {
		return m.pickPasted(msg.Content) // a reference to the secret
	}
	if p := m.prompt; p != nil && p.input != nil {
		in, cmd := p.input.Update(msg)
		p.input = &in
		return cmd
	}
	if l := m.focusedFilter(); l != nil && l.Filtering() {
		return l.UpdateFilter(msg)
	}
	if m.prompt != nil {
		return nil
	}
	return m.openText(msg.Content)
}

type clipboardTextMsg string

// clipCheckEvery throttles the clipboard check on key presses, which
// covers terminals that don't report focus.
const clipCheckEvery = 3 * time.Second

// maybeCheckClipboard checks the clipboard if it hasn't been for a while.
func (m *Model) maybeCheckClipboard() tea.Cmd {
	if now := m.now(); now.Sub(m.clipChecked) >= clipCheckEvery {
		m.clipChecked = now
		return m.checkClipboard()
	}
	return nil
}

// checkClipboard reads the clipboard in the background to look for a
// lazysecrets:// link. Only such links are kept; anything else (it may well
// be a password) is dropped as soon as it has been looked at.
func (m Model) checkClipboard() tea.Cmd {
	clip := m.clip
	return func() tea.Msg {
		text, err := clip.Read()
		if err != nil {
			return nil
		}
		text = strings.TrimSpace(text)
		if !isLink(text) {
			return clipboardTextMsg("")
		}
		return clipboardTextMsg(text)
	}
}

// isLink reports whether s looks like a lazysecrets:// link, valid or not.
func isLink(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), ref.Scheme+"://")
}

// offerClipboardLink pops up a link found on the clipboard, once: whatever
// the user picks, that link isn't offered again. It isn't offered while
// another popup is open, or if it points at what's selected already (e.g.
// just shared with s). A lazysecrets:// link that doesn't parse is
// reported, so a typo doesn't fail silently.
func (m *Model) offerClipboardLink(text string) {
	if text == "" || text == m.dismissedLink || m.prompt != nil || m.search != nil {
		return
	}
	r, err := ref.Parse(text)
	if err != nil {
		m.dismissedLink = text
		m.showMenu("Link on clipboard can't be read", []string{m.st.Error.Render(err.Error())}, nil)
		return
	}
	if m.isSelected(r) {
		return
	}
	m.dismissedLink = text
	m.offerRef("Link on clipboard", r, true)
}

// isSelected reports whether r points at the selected container, or the
// selected object in it.
func (m Model) isSelected(r ref.Ref) bool {
	c, ok := m.containers.Selected()
	if !ok || c.Source.Kind != r.Kind || !matchesContainer(c, r) {
		return false
	}
	if r.IsContainer() {
		return true
	}
	o, ok := m.detail.Secret()
	return ok && o.Name == r.Name && (r.Type == "" || o.Type == r.Type) &&
		(r.Version == "" || o.Version == r.Version)
}
