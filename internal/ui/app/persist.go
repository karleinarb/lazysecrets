package app

import (
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// What was loaded is kept between runs in a cache file (Options.CacheFile):
// the Sources lists, vaults, secrets' metadata and where the cursor was in
// each. Starting again shows it at once, and lists older than cacheTTL
// reload in the background as usual. Values are never in it; neither are
// repos, whose files are read from disk anyway.
//
// And where lazysecrets was left, per folder it ran in, goes in the store,
// to go back there on the next start.

// persistEvery is how often a changed cache is saved while running, so
// little is lost if lazysecrets is killed rather than quit.
const persistEvery = 30 * time.Second

type persistTickMsg struct{}

type persistedMsg struct{ err error }

// keepers report whether a provider ID, or a cache key's, is an account
// that still exists and isn't a repo.
func (m *Model) keepers() (keep, keepKey func(string) bool) {
	keep = cachefile.Keep(m.providers)
	return keep, func(key string) bool { return keep(cachefile.ProviderOf(key)) }
}

// loadCacheFile fills the cache from the file, leaving out what's too
// old or belongs to accounts that aren't signed in any more.
func (m *Model) loadCacheFile() {
	f := cachefile.Read(m.cachePath)
	keep, keepKey := m.keepers()
	now := m.now()
	recent := func(at time.Time) bool { return now.Sub(at) < cachefile.MaxAge }
	for id, e := range f.Sources {
		if keep(id) && recent(e.At) {
			m.sourceResults[id] = sourcesMsg{gen: m.sourcesGen, provider: id, sources: e.Value}
			m.sourcesAt[id] = e.At
		}
	}
	for k, e := range f.Containers {
		if keepKey(k) && recent(e.At) {
			m.cache.containers[k] = entry[[]provider.Container]{e.Value, e.At}
		}
	}
	for k, e := range f.Objects {
		if keepKey(k) && recent(e.At) {
			m.cache.objects[k] = entry[[]provider.Object]{e.Value, e.At}
		}
	}
	for k, e := range f.Details {
		if keepKey(k) && recent(e.At) {
			m.cache.details[k] = entry[provider.Object]{e.Value, e.At}
		}
	}
	for k, v := range f.Cursor {
		if keepKey(k) {
			m.cache.cursor[k] = v
		}
	}
}

// cacheSnapshot is the cache as the file holds it.
func (m *Model) cacheSnapshot() cachefile.File {
	f := cachefile.New()
	keep, keepKey := m.keepers()
	f.Dropped, m.cacheDropped = m.cacheDropped, nil
	for id, r := range m.sourceResults {
		if r.err == nil && keep(id) {
			f.Sources[id] = cachefile.Stamped[[]provider.Source]{At: m.sourcesAt[id], Value: r.sources}
		}
	}
	for k, e := range m.cache.containers {
		if keepKey(k) {
			f.Containers[k] = cachefile.Stamped[[]provider.Container]{At: e.at, Value: e.value}
		}
	}
	for k, e := range m.cache.objects {
		if keepKey(k) {
			f.Objects[k] = cachefile.Stamped[[]provider.Object]{At: e.at, Value: e.value}
		}
	}
	for k, e := range m.cache.details {
		if keepKey(k) {
			f.Details[k] = cachefile.Stamped[provider.Object]{At: e.at, Value: e.value}
		}
	}
	for k, v := range m.cache.cursor {
		if keepKey(k) {
			f.Cursor[k] = v
		}
	}
	return f
}

// saveCache saves f, merged with the file (lazysecrets list may have
// loaded more since), from any goroutine.
func (m *Model) saveCache(f cachefile.File) func() error {
	path, keep, now := m.cachePath, cachefile.Keep(m.providers), m.now()
	return func() error { return cachefile.Save(path, f, keep, now) }
}

// persistTick saves the cache, and where lazysecrets is, in the
// background when they changed.
func (m *Model) persistTick() tea.Cmd {
	cmds := []tea.Cmd{tea.Tick(persistEvery, func(time.Time) tea.Msg { return persistTickMsg{} })}
	if m.cacheDirty && m.cachePath != "" {
		m.cacheDirty = false
		save := m.saveCache(m.cacheSnapshot())
		cmds = append(cmds, func() tea.Msg { return persistedMsg{save()} })
	}
	if p, ok := m.place(); ok && m.placeDir != "" && p.Link != m.savedPlace {
		m.savedPlace = p.Link
		st, dir := m.store, m.placeDir
		cmds = append(cmds, func() tea.Msg { return persistedMsg{st.SetPlace(dir, p)} })
	}
	return tea.Batch(cmds...)
}

// Persist saves the cache and where lazysecrets was left; run it after
// the program ends.
func (m Model) Persist() error {
	var errs []error
	if m.cachePath != "" {
		errs = append(errs, m.saveCache(m.cacheSnapshot())())
	}
	if m.placeDir != "" {
		if p, ok := m.place(); ok {
			errs = append(errs, m.store.SetPlace(m.placeDir, p))
		}
	}
	return errors.Join(errs...)
}

// place is the selected item, as deep as the focus.
func (m *Model) place() (store.Place, bool) {
	src, ok := m.selectedSource()
	if !ok || m.nav != nil {
		return store.Place{}, false
	}
	var r ref.Ref
	var err error
	switch c, cok := m.containers.Selected(); {
	case m.focus == paneSources || !cok:
		r, err = ref.ForSource(src)
	case m.focus == paneContainers:
		r, err = ref.ForContainer(c, m.objType)
	default:
		o, ook := m.detail.Secret()
		if !ook {
			r, err = ref.ForContainer(c, m.objType)
		} else {
			r, err = ref.ForObject(o, "")
		}
	}
	if err != nil {
		return store.Place{}, false
	}
	return store.Place{Link: r.String(), Account: src.Provider, Source: src.ID, At: m.now()}, true
}

// restorePlace goes back to where lazysecrets was left in dir, if that
// account is still signed in.
func (m *Model) restorePlace() {
	p, ok := m.store.Place(m.placeDir)
	if !ok {
		return
	}
	if _, ok := m.byID[p.Account]; !ok {
		return
	}
	r, err := ref.Parse(p.Link)
	if err != nil {
		return
	}
	m.startNav(r, actGo, sourceRowKey(p.Account, p.Source))
	m.nav.sourceOnly = r.IsSource()
	m.nav.quiet = true
}

func (m *Model) handlePersisted(msg persistedMsg) {
	if msg.err != nil {
		m.logErrOf("saving the cache or place", msg.err)
	}
}
