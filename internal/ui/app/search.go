package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
)

// ctrl+s searches everything loaded: subscriptions and workspaces, vaults
// and scopes, and the secrets, keys and certificates of every vault opened
// this session. Azure has no search across vaults, so ctrl+a in the popup
// loads the rest in the background, a few requests at a time; results
// appear as lists arrive, and the panes use them too.

const crawlWorkers = 4

// crawl loads every vault's secrets for search.
type crawl struct {
	running bool
	queue   []crawlJob
	active  int
	pending map[string]bool // cache keys of lists queued or loading
	failed  map[string]bool // cache keys of lists that couldn't be read
}

type crawlJob struct {
	src       *provider.Source    // list its vaults or scopes
	container *provider.Container // list its secrets
	row       string              // the source's row key (containers cache key)
	force     bool                // reload what's loaded already
}

type crawlMsg struct {
	job        crawlJob
	containers []provider.Container
	objects    []provider.Object
	err        error
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// searchEntries lists everything loaded: the sources (with how much of
// each is loaded) and the vaults, scopes and secrets in them, each with
// its source's row key as group.
func (m *Model) searchEntries() (sources, items []option) {
	type entry struct {
		depth int
		sort  string
		opt   option
	}
	var es []entry
	// key is the store key, so ctrl+f stars the entry; mark shows how
	// much is loaded, reload (ctrl+r) loads it again.
	add := func(depth int, row string, kind provider.Kind, path []string, account, key, mark string, run, reload func(*Model) tea.Cmd) {
		n := m.store.Node(key)
		sort := strings.ToLower(strings.Join(path, "/")) + " " + account
		es = append(es, entry{depth, sort,
			option{label: m.pathLabel(kind, path, account), note: n.Note, star: key,
				starAccount: providerOf(row), starred: n.StarredFor(providerOf(row)),
				mark: mark, run: run, reload: reload, id: sort, group: row, kind: kind}})
	}
	for _, p := range m.providers {
		for _, src := range m.sourceResults[p.ID()].sources {
			src.Provider, src.ProviderLabel = p.ID(), p.Label()
			row := sourceRowKey(p.ID(), src.ID)
			add(0, row, src.Kind, []string{src.Name}, accountOf(src), sourceKey(src), m.sourceMark(row), func(m *Model) tea.Cmd {
				m.nav = &nav{sourceRow: row, sourceOnly: true}
				return nil
			}, func(m *Model) tea.Cmd { return m.loadSource(src) })
			for _, c := range m.cache.containers[row].value {
				r, err := ref.ForContainer(c, provider.TypeSecret)
				if err != nil {
					continue
				}
				reload := func(m *Model) tea.Cmd { return m.loadContainer(c) }
				add(1, row, c.Source.Kind, []string{src.Name, c.Name}, accountOf(src), storeKey(c), m.vaultMark(c), func(m *Model) tea.Cmd {
					m.startNav(r, actGo, row)
					return nil
				}, reload)
				for _, t := range []provider.ObjectType{provider.TypeSecret, provider.TypeKey, provider.TypeCertificate} {
					for _, o := range m.cache.objects[containerKey(c)+"|"+string(t)].value {
						r, err := ref.ForObject(o, "")
						if err != nil {
							continue
						}
						suffix := accountOf(src)
						if t != provider.TypeSecret {
							typ := strings.TrimSuffix(strings.ToLower(pluralLabels[t]), "s") // "key"
							suffix = strings.Trim(typ+" · "+suffix, " ·")
						}
						add(2, row, c.Source.Kind, []string{src.Name, c.Name, o.Name}, suffix, objectKey(o), " ", func(m *Model) tea.Cmd {
							m.startNav(r, actGo, row)
							return nil
						}, reload)
					}
				}
			}
		}
	}
	slices.SortStableFunc(es, func(a, b entry) int { return cmp.Or(a.depth-b.depth, strings.Compare(a.sort, b.sort)) })
	for _, e := range es {
		if e.depth == 0 {
			sources = append(sources, e.opt)
		} else {
			items = append(items, e.opt)
		}
	}
	return sources, items
}

// coverage is how much of everything search can see: sources whose
// vaults are listed, vaults whose secrets are loaded, and secrets. Lists
// that couldn't be read count as done.
type coverage struct {
	sources, sourcesListed int
	vaults, vaultsLoaded   int
	secrets, unreadable    int
}

func (c coverage) complete() bool {
	return c.sourcesListed == c.sources && c.vaultsLoaded == c.vaults
}

func (m *Model) searchCoverage() coverage { return m.coverageOf(nil) }

// providerFilter picks the providers a search or a load covers; nil is
// all of them.
type providerFilter func(provider.Provider) bool

func ofKind(k provider.Kind) providerFilter {
	return func(p provider.Provider) bool { return p.Kind() == k }
}

func ofProvider(id string) providerFilter {
	return func(p provider.Provider) bool { return p.ID() == id }
}

// coverageOf is searchCoverage for the providers in.
func (m *Model) coverageOf(in providerFilter) coverage {
	var c coverage
	for _, p := range m.providers {
		if in != nil && !in(p) {
			continue
		}
		for _, src := range m.sourceResults[p.ID()].sources {
			row := sourceRowKey(p.ID(), src.ID)
			c.sources++
			e, ok := m.cache.containers[row]
			if ok || m.crawl.failed[row] {
				c.sourcesListed++
			}
			if m.crawl.failed[row] {
				c.unreadable++
			}
			for _, v := range e.value {
				c.vaults++
				key := containerKey(v) + "|" + string(provider.TypeSecret)
				if objs, ok := m.cache.objects[key]; ok {
					c.vaultsLoaded++
					c.secrets += len(objs.value)
				} else if m.crawl.failed[key] {
					c.vaultsLoaded++
					c.unreadable++
				}
			}
		}
	}
	return c
}

// searchLines says what the search covers.
func (m *Model) searchLines() []string {
	c := m.searchCoverage()
	switch {
	case m.crawl.running:
		return []string{m.st.Notice.Render(fmt.Sprintf("Loading… %d lists left; results appear as they arrive.",
			len(m.crawl.queue)+m.crawl.active))}
	case c.complete():
		s := fmt.Sprintf("Searching everything: %s, %s, %s",
			plural(c.sources, "source"), plural(c.vaults, "vault"), plural(c.secrets, "secret"))
		if c.unreadable > 0 {
			s += fmt.Sprintf(" (%s unreadable)", plural(c.unreadable, "list"))
		}
		return []string{m.st.Success.Render("✓") + " " + m.st.Notice.Render(s)}
	}
	s := fmt.Sprintf("Only what's loaded: %d/%d vaults", c.vaultsLoaded, c.vaults)
	if c.sourcesListed < c.sources {
		s += fmt.Sprintf(", %d/%d sources listed", c.sourcesListed, c.sources)
	}
	return []string{m.st.Warning.Render("⚠ " + s + "." + withKey(m.loadAllKey(), " %s loads the rest."))}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// startCrawl loads every unlisted subscription's vaults and every vault's
// secrets not loaded yet.
func (m *Model) startCrawl() tea.Cmd { return m.startCrawlOf(nil) }

// startCrawlOf is startCrawl for the providers in.
func (m *Model) startCrawlOf(in providerFilter) tea.Cmd {
	for _, p := range m.providers {
		if in != nil && !in(p) {
			continue
		}
		for _, src := range m.sourceResults[p.ID()].sources {
			src.Provider, src.ProviderLabel = p.ID(), p.Label()
			m.queueSource(src, false)
		}
	}
	return m.crawlStart()
}

// loadSource loads (again) a subscription's vaults and their secrets.
func (m *Model) loadSource(src provider.Source) tea.Cmd {
	m.queueSource(src, true)
	return m.crawlStart()
}

// loadContainer loads (again) a vault's secrets.
func (m *Model) loadContainer(c provider.Container) tea.Cmd {
	m.queueContainer(c, sourceRowKey(c.Source.Provider, c.Source.ID), true)
	return m.crawlStart()
}

// crawlStart runs what's queued, and shows it in the popup.
func (m *Model) crawlStart() tea.Cmd {
	cmd := m.crawlNext()
	m.refreshSearch()
	return cmd
}

// queueSource queues listing src's vaults or, with them listed lately
// and not force, loading their secrets. Lists from an earlier run (the
// cache file) are listed again, so searching brings them up to date.
func (m *Model) queueSource(src provider.Source, force bool) {
	row := sourceRowKey(src.Provider, src.ID)
	if e, ok := m.cache.containers[row]; ok && e.fresh(m.now()) && !force {
		for _, c := range e.value {
			m.queueContainer(c, row, false)
		}
		return
	}
	m.enqueue(crawlJob{src: &src, row: row, force: force}, row)
}

// queueContainer queues loading c's secrets, unless loaded lately and not
// force.
func (m *Model) queueContainer(c provider.Container, row string, force bool) {
	key := containerKey(c) + "|" + string(provider.TypeSecret)
	if e, ok := m.cache.objects[key]; ok && e.fresh(m.now()) && !force {
		return
	}
	m.enqueue(crawlJob{container: &c, row: row, force: force}, key)
}

func (m *Model) enqueue(job crawlJob, key string) {
	if m.crawl.pending == nil {
		m.crawl.pending, m.crawl.failed = map[string]bool{}, map[string]bool{}
	}
	if m.crawl.pending[key] {
		return // queued or loading already
	}
	m.crawl.pending[key] = true
	m.crawl.queue = append(m.crawl.queue, job)
}

// crawlNext starts jobs up to crawlWorkers at a time.
func (m *Model) crawlNext() tea.Cmd {
	var cmds []tea.Cmd
	for m.crawl.active < crawlWorkers && len(m.crawl.queue) > 0 {
		job := m.crawl.queue[0]
		m.crawl.queue = m.crawl.queue[1:]
		m.crawl.active++
		cmds = append(cmds, m.crawlCmd(job))
	}
	m.crawl.running = m.crawl.active > 0 || len(m.crawl.queue) > 0
	return tea.Batch(cmds...)
}

func (m *Model) crawlCmd(job crawlJob) tea.Cmd {
	timeout := m.timeout
	if job.src != nil {
		p := m.providerFor(*job.src)
		src := *job.src
		return func() tea.Msg {
			ctx, cancel := contextWithTimeout(timeout)
			defer cancel()
			items, err := p.Containers(ctx, src)
			for i := range items {
				items[i].Source = src
			}
			return crawlMsg{job: job, containers: items, err: err}
		}
	}
	c := *job.container
	p := m.providerFor(c.Source)
	return func() tea.Msg {
		ctx, cancel := contextWithTimeout(timeout)
		defer cancel()
		items, err := p.Objects(ctx, c, provider.TypeSecret)
		for i := range items {
			items[i].Container = c
		}
		return crawlMsg{job: job, objects: items, err: err}
	}
}

func (m *Model) handleCrawl(msg crawlMsg) tea.Cmd {
	m.crawl.active--
	key := msg.job.row
	if msg.job.container != nil {
		key = containerKey(*msg.job.container) + "|" + string(provider.TypeSecret)
	}
	delete(m.crawl.pending, key)
	switch {
	case msg.err != nil:
		m.crawl.failed[key] = true
	case msg.job.src != nil:
		delete(m.crawl.failed, key)
		m.cache.containers[key] = entry[[]provider.Container]{msg.containers, m.now()}
		m.cacheDirty = true
		for _, c := range msg.containers {
			m.queueContainer(c, key, msg.job.force)
		}
	default:
		delete(m.crawl.failed, key)
		m.cache.objects[key] = entry[[]provider.Object]{msg.objects, m.now()}
		m.cacheDirty = true
	}
	cmd := m.crawlNext()
	m.refreshSearch()
	return cmd
}

// sourceMark shows how much of a subscription or workspace is loaded:
// ○ nothing, ◐ some vaults, ● every vault, ◌ loading, ✗ unreadable.
func (m *Model) sourceMark(row string) string {
	if m.crawl.pending[row] {
		return m.st.Muted.Render("◌")
	}
	if m.crawl.failed[row] {
		return m.st.Error.Render("✗")
	}
	e, ok := m.cache.containers[row]
	if !ok {
		return m.st.Muted.Render("○")
	}
	loaded, loading := 0, false
	for _, c := range e.value {
		key := containerKey(c) + "|" + string(provider.TypeSecret)
		loading = loading || m.crawl.pending[key]
		if _, ok := m.cache.objects[key]; ok || m.crawl.failed[key] {
			loaded++
		}
	}
	switch {
	case loading:
		return m.st.Muted.Render("◌")
	case loaded == len(e.value):
		return m.st.Success.Render("●")
	case loaded > 0:
		return m.st.Warning.Render("◐")
	}
	return m.st.Muted.Render("○")
}

// vaultMark marks a vault in search only when its secrets can't be read
// (✗) or are loading (◌): whether a subscription is loaded says whether
// its vaults are searchable.
func (m *Model) vaultMark(c provider.Container) string {
	key := containerKey(c) + "|" + string(provider.TypeSecret)
	switch {
	case m.crawl.pending[key]:
		return m.st.Muted.Render("◌")
	case m.crawl.failed[key]:
		return m.st.Error.Render("✗")
	}
	return " "
}

// providerOf is the provider ID in a source row key.
func providerOf(row string) string {
	p, _ := splitSourceRow(row)
	return p
}
