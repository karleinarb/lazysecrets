package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

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

// cacheFileVersion is the file's format; a file of another version is
// ignored, and replaced on the next save.
const cacheFileVersion = 1

// cacheMaxAge drops what hasn't been loaded again for this long.
const cacheMaxAge = 30 * 24 * time.Hour

// persistEvery is how often a changed cache is saved while running, so
// little is lost if lazysecrets is killed rather than quit.
const persistEvery = 30 * time.Second

type cacheFile struct {
	Version    int                                      `json:"version"`
	Sources    map[string]stamped[[]provider.Source]    `json:"sources,omitempty"` // by provider ID
	Containers map[string]stamped[[]provider.Container] `json:"containers,omitempty"`
	Objects    map[string]stamped[[]provider.Object]    `json:"objects,omitempty"`
	Details    map[string]stamped[provider.Object]      `json:"details,omitempty"`
	Cursor     map[string]string                        `json:"cursor,omitempty"`
}

type stamped[T any] struct {
	At    time.Time `json:"at"`
	Value T         `json:"value"`
}

type persistTickMsg struct{}

type persistedMsg struct{ err error }

// keepCached reports whether a cache key's account still exists and
// isn't a repo: every key starts with the provider ID.
func (m *Model) keepCached(key string) bool {
	id, _ := splitSourceRow(key)
	p, ok := m.byID[id]
	return ok && p.Kind() != provider.KindRepo
}

// loadCacheFile fills the cache from the file, leaving out what's too
// old or belongs to accounts that aren't signed in any more.
func (m *Model) loadCacheFile() {
	b, err := os.ReadFile(m.cachePath)
	if err != nil {
		return
	}
	var f cacheFile
	if json.Unmarshal(b, &f) != nil || f.Version != cacheFileVersion {
		return
	}
	now := m.now()
	recent := func(at time.Time) bool { return now.Sub(at) < cacheMaxAge }
	for id, e := range f.Sources {
		if m.keepCached(id+"|") && recent(e.At) {
			m.sourceResults[id] = sourcesMsg{gen: m.sourcesGen, provider: id, sources: e.Value}
			m.sourcesAt[id] = e.At
		}
	}
	for k, e := range f.Containers {
		if m.keepCached(k) && recent(e.At) {
			m.cache.containers[k] = entry[[]provider.Container]{e.Value, e.At}
		}
	}
	for k, e := range f.Objects {
		if m.keepCached(k) && recent(e.At) {
			m.cache.objects[k] = entry[[]provider.Object]{e.Value, e.At}
		}
	}
	for k, e := range f.Details {
		if m.keepCached(k) && recent(e.At) {
			m.cache.details[k] = entry[provider.Object]{e.Value, e.At}
		}
	}
	for k, v := range f.Cursor {
		if m.keepCached(k) {
			m.cache.cursor[k] = v
		}
	}
}

// cacheSnapshot is the cache as the file holds it.
func (m *Model) cacheSnapshot() cacheFile {
	f := cacheFile{
		Version:    cacheFileVersion,
		Sources:    map[string]stamped[[]provider.Source]{},
		Containers: map[string]stamped[[]provider.Container]{},
		Objects:    map[string]stamped[[]provider.Object]{},
		Details:    map[string]stamped[provider.Object]{},
		Cursor:     map[string]string{},
	}
	for id, r := range m.sourceResults {
		if r.err == nil && m.keepCached(id+"|") {
			f.Sources[id] = stamped[[]provider.Source]{m.sourcesAt[id], r.sources}
		}
	}
	for k, e := range m.cache.containers {
		if m.keepCached(k) {
			f.Containers[k] = stamped[[]provider.Container]{e.at, e.value}
		}
	}
	for k, e := range m.cache.objects {
		if m.keepCached(k) {
			f.Objects[k] = stamped[[]provider.Object]{e.at, e.value}
		}
	}
	for k, e := range m.cache.details {
		if m.keepCached(k) {
			f.Details[k] = stamped[provider.Object]{e.at, e.value}
		}
	}
	for k, v := range m.cache.cursor {
		if m.keepCached(k) {
			f.Cursor[k] = v
		}
	}
	return f
}

// writeCacheFile writes f atomically, readable by the user only.
func writeCacheFile(path string, f cacheFile) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // after a successful rename, nothing there
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// persistTick saves the cache, and where lazysecrets is, in the
// background when they changed.
func (m *Model) persistTick() tea.Cmd {
	cmds := []tea.Cmd{tea.Tick(persistEvery, func(time.Time) tea.Msg { return persistTickMsg{} })}
	if m.cacheDirty && m.cachePath != "" {
		m.cacheDirty = false
		f, path := m.cacheSnapshot(), m.cachePath
		cmds = append(cmds, func() tea.Msg { return persistedMsg{writeCacheFile(path, f)} })
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
		errs = append(errs, writeCacheFile(m.cachePath, m.cacheSnapshot()))
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
