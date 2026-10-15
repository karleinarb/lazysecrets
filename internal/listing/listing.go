// Package listing lists what lazysecrets can find, for lazysecrets list:
// what Go to searches, without the TUI. What's cached comes first, at
// once; then lists that are missing or older than MaxAge load in the
// background, a few at a time, and what's new in them follows as they
// arrive. Repos are always read, from disk. Values are never listed.
package listing

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
)

// Workers is how many lists load at once, as in Go to.
const Workers = 4

// Kinds are the kinds of entry, from sources down.
var Kinds = []string{"source", "vault", "secret", "key", "certificate"}

// Entry is a source, vault or item, by its lazysecrets:// link.
type Entry struct {
	Link string `json:"link"`
	// Kind is source (a subscription, workspace or repo), vault (a Key
	// Vault, secret scope or repo file), secret, key or certificate.
	Kind string `json:"kind"`
	// Provider is azure, databricks or repo, as in links.
	Provider string `json:"provider"`
	// Path names it: its source's, vault's and own names.
	Path []string `json:"path"`
	// Account is the provider it's listed through: an az account
	// (Azure:<user>/<tenant>), Databricks or Repos. lazysecrets open and
	// get take it (--account) to use that account when several could.
	Account string `json:"account"`
	// AccountLabel names the az account, when there are several.
	AccountLabel string `json:"accountLabel,omitempty"`
	// Starred is starred for this account; Recent is used lately (Recent,
	// R, in the TUI): LastUsed is when, Uses how often.
	Starred  bool      `json:"starred"`
	Recent   bool      `json:"recent"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Uses     int       `json:"uses,omitempty"`
	// Expires is when a secret, key or certificate expires, if it does;
	// Enabled whether it's enabled, where the provider says (Azure).
	Expires time.Time `json:"expires,omitzero"`
	Enabled *bool     `json:"enabled,omitempty"`
	// Note is the user's note on it, if any.
	Note string `json:"note,omitempty"`
	// Reference is how config refers to a secret, where the provider has
	// a way (@Microsoft.KeyVault(...), {{secrets/scope/key}}); URL its
	// web page (portal, workspace), if any.
	Reference string `json:"reference,omitempty"`
	URL       string `json:"url,omitempty"`
	// Hidden is left out by its provider's rules (a Spring file's key
	// that doesn't look secret), listed only with Options.Hidden.
	Hidden bool `json:"hidden,omitempty"`
	// Error says why a source's vaults or a vault's secrets couldn't be
	// listed. The entry is then that source or vault, again, or with an
	// account's sources unreadable, the account without a link.
	Error string `json:"error,omitempty"`
}

type Options struct {
	Providers []provider.Provider
	// Cache is what was loaded before; Run adds what it loads.
	Cache cachefile.File
	// MaxAge: cached lists loaded longer ago are loaded again.
	MaxAge time.Duration
	// Cached lists only what's cached (and repos): nothing is loaded.
	Cached bool
	// Hidden lists what providers hide too, as H does in the TUI.
	Hidden bool
	// Kinds are the kinds of entry to list (Kinds' names); none is all.
	// Lists only needed for other kinds aren't loaded: sources and vaults
	// alone don't load any secrets. Lists that fail are always listed.
	Kinds []string
	// Mark tells what's remembered about the entry with link, seen
	// through account; nil marks nothing.
	Mark    func(link, account string) Marks
	Timeout func(provider.Kind) time.Duration // nil: 30s
	Now     func() time.Time
}

// Marks are what's remembered about an entry: starred for the account,
// used lately (Recent) and when and how often, and its note.
type Marks struct {
	Starred, Recent bool
	LastUsed        time.Time
	Uses            int
	Note            string
}

// Run lists everything, calling emit for each entry once, from this
// goroutine, and for each list that can't be loaded, as it fails. An
// error from emit (the reader went away) stops it. It returns the cache
// with what was loaded, and whether anything was.
func Run(ctx context.Context, o Options, emit func(Entry) error) (cachefile.File, bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	l := &lister{o: o, emit: emit, seen: map[string]bool{}, pending: map[string]bool{}, failed: map[string]bool{},
		results: make(chan result)}
	if l.o.Now == nil {
		l.o.Now = time.Now
	}
	if l.o.Timeout == nil {
		l.o.Timeout = func(provider.Kind) time.Duration { return 30 * time.Second }
	}
	for _, p := range o.Providers {
		if l.stopped != nil {
			break
		}
		l.provider(p)
	}
	l.start(ctx)
	for l.active > 0 {
		r := <-l.results
		l.active--
		if l.stopped == nil {
			l.handle(r)
			l.start(ctx)
		}
		if l.stopped != nil {
			cancel()
		}
	}
	return l.o.Cache, l.loaded
}

type job struct {
	p   provider.Provider
	src *provider.Source    // list its vaults
	c   *provider.Container // list its secrets, keys or certificates (t)
	t   provider.ObjectType
	key string // where the list is cached
}

type result struct {
	job
	sources    []provider.Source
	containers []provider.Container
	objects    []provider.Object
	err        error
}

type lister struct {
	o       Options
	emit    func(Entry) error
	seen    map[string]bool // provider ID + link of entries emitted
	pending map[string]bool // cache keys queued or loading
	failed  map[string]bool // cache keys that couldn't be loaded: not again
	loaded  bool            // something was cached
	queue   []job
	active  int
	results chan result
	stopped error // emit's error
}

func (l *lister) cacheable(p provider.Provider) bool { return p.Kind() != provider.KindRepo }

// load reports whether to load a list: a repo's always (from disk), else
// unless Cached, when it isn't cached or is older than MaxAge.
func (l *lister) load(p provider.Provider, cached bool, at time.Time) bool {
	if !l.cacheable(p) {
		return true
	}
	return !l.o.Cached && (!cached || l.o.Now().Sub(at) >= l.o.MaxAge)
}

func (l *lister) provider(p provider.Provider) {
	e, ok := l.o.Cache.Sources[p.ID()]
	if l.cacheable(p) && ok {
		for _, src := range sorted(e.Value, func(s provider.Source) string { return s.Name }) {
			l.source(p, src)
		}
	}
	if l.load(p, ok, e.At) {
		l.enqueue(job{p: p, key: p.ID()})
	}
}

// source emits src, and what's cached under it, and queues what's not.
func (l *lister) source(p provider.Provider, src provider.Source) {
	if src.Hidden && !l.o.Hidden {
		return
	}
	src.Provider, src.ProviderLabel = p.ID(), p.Label()
	l.put(p, sourceEntry(src))
	row := cachefile.Row(p.ID(), src.ID)
	e, ok := l.o.Cache.Containers[row]
	if l.cacheable(p) && ok {
		l.containers(p, src, e.Value)
	}
	if l.load(p, ok, e.At) {
		if l.wants("vault", "secret", "key", "certificate") {
			l.enqueue(job{p: p, src: &src, key: row})
		}
	}
}

func (l *lister) containers(p provider.Provider, src provider.Source, cs []provider.Container) {
	for _, c := range sorted(cs, func(c provider.Container) string { return c.Name }) {
		if c.Hidden && !l.o.Hidden {
			continue
		}
		c.Source = src
		l.put(p, containerEntry(c))
		for _, t := range c.ObjectTypes() {
			key := cachefile.ObjectsKey(c, t)
			e, ok := l.o.Cache.Objects[key]
			if l.cacheable(p) {
				l.objects(p, c, e.Value)
			}
			// Keys and certificates load only when asked for; else,
			// like Go to, only as far as the TUI opened them.
			asked := l.wants(strings.ToLower(string(t)))
			if t != provider.TypeSecret {
				asked = len(l.o.Kinds) > 0 && asked
			}
			if asked && l.load(p, ok, e.At) {
				l.enqueue(job{p: p, c: &c, t: t, key: key})
			}
		}
	}
}

func (l *lister) objects(p provider.Provider, c provider.Container, objs []provider.Object) {
	for _, o := range sorted(objs, func(o provider.Object) string { return o.Name }) {
		o.Container = c
		r, err := ref.ForObject(o, "")
		if err != nil || (o.Hidden && !l.o.Hidden) {
			continue
		}
		l.put(p, Entry{Link: r.String(), Kind: strings.ToLower(string(o.Type)), Provider: kindOf(c.Source.Kind),
			Path: []string{c.Source.Name, c.Name, o.Name}, AccountLabel: accountOf(c.Source),
			Expires: o.Expires, Enabled: o.Enabled, Reference: o.Reference, URL: o.URL, Hidden: o.Hidden})
	}
}

func sourceEntry(src provider.Source) Entry {
	e := Entry{Kind: "source", Provider: kindOf(src.Kind), Path: []string{src.Name}, AccountLabel: accountOf(src), URL: src.URL, Hidden: src.Hidden}
	if r, err := ref.ForSource(src); err == nil {
		e.Link = r.String()
	}
	return e
}

func containerEntry(c provider.Container) Entry {
	e := Entry{Kind: "vault", Provider: kindOf(c.Source.Kind), Path: []string{c.Source.Name, c.Name}, AccountLabel: accountOf(c.Source), URL: c.URL, Hidden: c.Hidden}
	if r, err := ref.ForContainer(c, provider.TypeSecret); err == nil {
		e.Link = r.String()
	}
	return e
}

// put emits e, unless it was already (or emitting has failed). Entries
// without a link (one couldn't be made) are left out, but not errors.
func (l *lister) put(p provider.Provider, e Entry) {
	if (e.Link == "" || !l.wants(e.Kind)) && e.Error == "" {
		return
	}
	e.Account = p.ID()
	if l.o.Mark != nil && e.Link != "" {
		m := l.o.Mark(e.Link, e.Account)
		e.Starred, e.Recent, e.Note = m.Starred, m.Recent, m.Note
		if m.Recent {
			e.LastUsed, e.Uses = m.LastUsed, m.Uses
		}
	}
	id := p.ID() + "\x00" + e.Link + "\x00" + e.Error
	if l.stopped != nil || l.seen[id] {
		return
	}
	l.seen[id] = true
	l.stopped = l.emit(e)
}

// wants reports whether any of kinds is listed.
func (l *lister) wants(kinds ...string) bool {
	if len(l.o.Kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if slices.Contains(l.o.Kinds, k) {
			return true
		}
	}
	return false
}

func (l *lister) enqueue(j job) {
	if l.pending[j.key] || l.failed[j.key] {
		return
	}
	l.pending[j.key] = true
	l.queue = append(l.queue, j)
}

func (l *lister) start(ctx context.Context) {
	for l.stopped == nil && l.active < Workers && len(l.queue) > 0 {
		j := l.queue[0]
		l.queue = l.queue[1:]
		l.active++
		go func() { l.results <- l.fetch(ctx, j) }()
	}
}

func (l *lister) fetch(ctx context.Context, j job) result {
	kind := j.p.Kind()
	ctx, cancel := context.WithTimeout(ctx, l.o.Timeout(kind))
	defer cancel()
	r := result{job: j}
	switch {
	case j.c != nil:
		r.objects, r.err = j.p.Objects(ctx, *j.c, j.t)
	case j.src != nil:
		r.containers, r.err = j.p.Containers(ctx, *j.src)
	default:
		r.sources, r.err = j.p.Sources(ctx)
	}
	return r
}

func (l *lister) handle(r result) {
	delete(l.pending, r.key)
	now := l.o.Now()
	if r.err != nil {
		l.failed[r.key] = true
		l.put(r.p, r.failed())
		return
	}
	l.loaded = l.loaded || l.cacheable(r.p)
	cache := l.cacheable(r.p)
	switch {
	case r.c != nil:
		if cache {
			l.o.Cache.Objects[r.key] = cachefile.Stamped[[]provider.Object]{At: now, Value: r.objects}
		}
		l.objects(r.p, *r.c, r.objects)
	case r.src != nil:
		r.containers = slices.Clone(r.containers) // the provider's may be shared
		for i := range r.containers {
			r.containers[i].Source = *r.src
		}
		if cache {
			l.o.Cache.Containers[r.key] = cachefile.Stamped[[]provider.Container]{At: now, Value: r.containers}
		}
		l.containers(r.p, *r.src, r.containers)
	default:
		if cache {
			l.o.Cache.Sources[r.key] = cachefile.Stamped[[]provider.Source]{At: now, Value: r.sources}
		}
		for _, src := range r.sources {
			l.source(r.p, src)
		}
	}
}

// failed is the entry for a list that couldn't be loaded.
func (r result) failed() Entry {
	var e Entry
	switch {
	case r.c != nil:
		e = containerEntry(*r.c)
	case r.src != nil:
		src := *r.src
		src.Provider, src.ProviderLabel = r.p.ID(), r.p.Label()
		e = sourceEntry(src)
	default:
		e = Entry{Kind: "source", Provider: kindOf(r.p.Kind()), Path: []string{r.p.Label()}}
	}
	e.Error = firstLine(r.err.Error())
	return e
}

// firstLine is an error's first line, the summary (Azure's go on with
// the caller's IDs and more): an entry is one line. Tabs, which would
// split it into columns, are spaces.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return strings.ReplaceAll(line, "\t", " ")
		}
	}
	return "unreadable"
}

func kindOf(k provider.Kind) string { return strings.ToLower(string(k)) }

// accountOf is the account part of a source's provider label ("alice" in
// "Azure · alice"), or "" when there's a single account.
func accountOf(src provider.Source) string {
	account, ok := strings.CutPrefix(src.ProviderLabel, string(src.Kind)+" · ")
	if !ok {
		return ""
	}
	return account
}

func sorted[T any](s []T, name func(T) string) []T {
	s = slices.Clone(s)
	slices.SortStableFunc(s, func(a, b T) int {
		return cmp.Compare(strings.ToLower(name(a)), strings.ToLower(name(b)))
	})
	return s
}
