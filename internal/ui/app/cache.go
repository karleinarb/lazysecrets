package app

import (
	"time"

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
