package app

import (
	"cmp"
	"fmt"
	"slices"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// The status pane lists what has expired or will soon, across every vault
// loaded (opened, searched, or from the cache): expiry dates come with
// the lists, so this makes no requests. enter on one goes to it.

type expiringItem struct {
	o   provider.Object
	row string // its source's row key, to go to it
}

// expiringItems are the loaded objects that have expired or will within
// the warning period, soonest first.
func (m Model) expiringItems() []expiringItem {
	var out []expiringItem
	for _, p := range m.providers {
		for _, src := range m.sourceResults[p.ID()].sources {
			row := sourceRowKey(p.ID(), src.ID)
			for _, c := range m.cache.containers[row].value {
				for _, t := range c.ObjectTypes() {
					for _, o := range m.cache.objects[containerKey(c)+"|"+string(t)].value {
						if state, _ := m.expiry(o); state != notExpiring {
							out = append(out, expiringItem{o, row})
						}
					}
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b expiringItem) int {
		return cmp.Or(a.o.Expires.Compare(b.o.Expires), cmp.Compare(a.o.Name, b.o.Name))
	})
	return out
}

// expiringFields are the status pane's rows for them, and how much of
// everything they cover.
func (m Model) expiringFields() []panes.Field {
	items := m.expiringItems()
	var out []panes.Field
	for _, it := range items {
		state, left := m.expiry(it.o)
		mark := "⚠ " + daysLeft(left)
		if state == expired {
			mark = "✗ expired"
		}
		f := panes.Field{Value: mark + "  " + it.o.Name + " · " + it.o.Container.Name, Err: state == expired, Row: it.row}
		if r, err := ref.ForObject(it.o, ""); err == nil {
			f.Link = r.String()
		}
		out = append(out, f)
	}
	cov := m.coverageOf(ofKind(provider.KindAzure))
	switch {
	case cov.vaults == 0 && len(items) == 0:
		return nil
	case !cov.complete():
		out = append(out, panes.Field{Value: fmt.Sprintf("in %d of %d vaults", cov.vaultsLoaded, cov.vaults) +
			withKey(m.keys.SearchLoadAll, " (%s in search loads all)"), Info: true})
	case len(items) == 0:
		out = append(out, panes.Field{Value: fmt.Sprintf("nothing within %d days", int(m.expiryWarning.Hours()/24)), Info: true})
	}
	out[0].Label = "expiring"
	return out
}
