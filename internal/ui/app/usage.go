package app

import (
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// Usage: revealing, copying, opening in the browser, sharing and jumping
// to an item count as using it. The store keeps when each item was last
// used and how often, for the Recent section of the jump list. The config's
// storage.history: false turns this off.

// used counts a use of the remembered item key, keeping its context.
func (m *Model) used(key string) {
	if !m.history {
		return
	}
	_ = m.store.Use(key, m.now(), func(*store.Node) {}) // best effort: never in the way
}

// usedItem counts a use of an item, recording its context.
func (m *Model) usedItem(key, name string, path []string, src provider.Source) {
	if !m.history {
		return
	}
	_ = m.store.Use(key, m.now(), withContext(name, path, src))
}

func (m *Model) usedSource(s provider.Source) {
	m.usedItem(sourceKey(s), s.Name, nil, s)
}

func (m *Model) usedContainer(c provider.Container) {
	m.usedItem(storeKey(c), c.Name, []string{c.Source.Name}, c.Source)
}

func (m *Model) usedObject(o provider.Object) {
	c := o.Container
	m.usedItem(objectKey(o), o.Name, []string{c.Source.Name, c.Name}, c.Source)
}
