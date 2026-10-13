package app

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// cacheTTL is how long cached metadata is shown without reloading. Older
// entries are still shown immediately, then reloaded in the background.
const cacheTTL = 2 * time.Minute

// cache holds metadata: never secret values or errors. It's kept between
// runs in the cache file (persist.go).
type cache struct {
	containers map[string]entry[[]provider.Container] // by source key
	objects    map[string]entry[[]provider.Object]    // by container key (ID|type)
	details    map[string]entry[provider.Object]      // Describe results by ID@version
	// cursor remembers the selected child per parent key, so returning to
	// a subscription or vault puts the cursor back where it was.
	cursor map[string]string
}

type entry[T any] struct {
	value T
	at    time.Time
}

func newCache() cache {
	return cache{
		containers: map[string]entry[[]provider.Container]{},
		objects:    map[string]entry[[]provider.Object]{},
		details:    map[string]entry[provider.Object]{},
		cursor:     map[string]string{},
	}
}

func (e entry[T]) fresh(now time.Time) bool { return now.Sub(e.at) < cacheTTL }

// detailKey includes the account: the result carries its container, and
// later requests for the object are routed by it.
func detailKey(o provider.Object) string {
	return containerKey(o.Container) + "|" + o.ID + "@" + o.Version
}

func (c cache) rememberCursor(parent, child string) {
	if parent != "" && child != "" {
		c.cursor[parent] = child
	}
}

// refreshShown shows the selected secret's newer metadata when its list
// reloaded with it changed from before (a cached list refreshed in the
// background), and describes it afresh: its cached details came from
// the old listing.
func (m *Model) refreshShown(before []provider.Object) tea.Cmd {
	s, ok := m.secrets.Selected()
	if !ok || m.secrets.SelectedID() != m.selSecret {
		return nil // a new selection is shown as usual
	}
	i := slices.IndexFunc(before, func(o provider.Object) bool { return o.ID == s.ID })
	if i < 0 || sameListing(before[i], s) {
		return nil
	}
	m.detail.Refresh(s)
	delete(m.cache.details, detailKey(s))
	return m.describe(s)
}

// sameListing reports whether a and b list the same: what a list
// says of an object, not what describing it adds.
func sameListing(a, b provider.Object) bool {
	enabled := func(e *bool) bool { return e == nil || *e }
	return a.Name == b.Name && a.Version == b.Version && enabled(a.Enabled) == enabled(b.Enabled) &&
		a.Updated.Equal(b.Updated) && a.Expires.Equal(b.Expires) && a.ContentType == b.ContentType &&
		maps.Equal(a.Tags, b.Tags) && a.Reference == b.Reference && a.URL == b.URL &&
		slices.Equal(a.Attrs, b.Attrs)
}
