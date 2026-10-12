// Package store keeps what lazysecrets remembers between runs in one JSON
// file in the XDG state directory: a record per subscription, vault or
// secret the user has starred, annotated or used, keyed by its
// lazysecrets:// link, and the layout. It never holds secret values.
//
// JSON rather than YAML (the config's format) because the app writes it,
// and so an editor plugin can read it with a built-in parser
// (vim.json.decode).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Node is what's remembered about one subscription, vault or secret. Name,
// Path, Source and Account are context, so lists of nodes (favourites,
// recent) can show and jump to them before their subscription is loaded.
type Node struct {
	Name    string   `json:"name,omitempty"`
	Path    []string `json:"path,omitempty"`    // parents' names: [source] or [source, vault]
	Source  string   `json:"source,omitempty"`  // ID of the subscription or workspace profile
	Account string   `json:"account,omitempty"` // provider ID that last read it

	// StarredBy lists the accounts (provider IDs) the item is starred
	// for: a star is the item through that account. A note describes the
	// item itself, whichever account shows it.
	StarredBy []string `json:"starredBy,omitempty"`
	Note      string   `json:"note,omitempty"`

	LastUsed time.Time `json:"lastUsed,omitzero"`
	Uses     int       `json:"uses,omitempty"`
}

// keep reports whether the record holds anything worth keeping; context
// alone isn't.
func (n Node) keep() bool { return len(n.StarredBy) > 0 || n.Note != "" || n.Uses > 0 }

// StarredFor reports whether the item is starred for account.
func (n Node) StarredFor(account string) bool { return slices.Contains(n.StarredBy, account) }

// ToggleStar stars or unstars the item for account, reporting whether
// it's starred now.
func (n *Node) ToggleStar(account string) bool {
	if i := slices.Index(n.StarredBy, account); i >= 0 {
		n.StarredBy = slices.Delete(n.StarredBy, i, i+1)
		return false
	}
	n.StarredBy = append(n.StarredBy, account)
	return true
}

// UpdateCheck is the last check for a newer release.
func (s *Store) UpdateCheck() UpdateCheck {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Update
}

// SetUpdateCheck records a check for a newer release.
func (s *Store) SetUpdateCheck(c UpdateCheck) error {
	return s.save(func(d *file) { d.Update = c })
}

// Place is where lazysecrets was left when it last ran in dir.
func (s *Store) Place(dir string) (Place, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Places[dir]
	return p, ok
}

// SetPlace remembers where lazysecrets was left in dir.
func (s *Store) SetPlace(dir string, p Place) error {
	return s.save(func(d *file) {
		if d.Places == nil {
			d.Places = map[string]Place{}
		}
		d.Places[dir] = p
		for len(d.Places) > maxPlaces {
			oldest := ""
			for k, v := range d.Places {
				if oldest == "" || v.At.Before(d.Places[oldest].At) {
					oldest = k
				}
			}
			delete(d.Places, oldest)
		}
	})
}

// Layout is the user's pane sizing, as offsets from the default layout so
// it adapts to the terminal size.
type Layout struct {
	Split   int    `json:"split,omitempty"`  // columns the divider moved
	Heights [3]int `json:"heights,omitzero"` // rows added to each left pane
}

type file struct {
	Version int             `json:"version"`
	Nodes   map[string]Node `json:"nodes"`
	Layout  Layout          `json:"layout,omitzero"`
	// Flags are one-off choices, like "don't ask again".
	Flags map[string]bool `json:"flags,omitempty"`
	// Update is the last check for a newer release.
	Update UpdateCheck `json:"update,omitzero"`
	// Places is where lazysecrets was left, by the folder it ran in.
	Places map[string]Place `json:"places,omitempty"`
}

// Place is what was selected when lazysecrets quit: an item's link, and
// the account and source it was read through.
type Place struct {
	Link    string    `json:"link"`
	Account string    `json:"account"`
	Source  string    `json:"source"`
	At      time.Time `json:"at"`
}

// maxPlaces bounds Places: the folders used most recently are kept.
const maxPlaces = 100

// UpdateCheck is the last look for a newer release: when, and what the
// latest was (its tag, changelog and page), so the next start within a
// day shows it without asking again.
type UpdateCheck struct {
	At    time.Time `json:"at,omitzero"`
	Tag   string    `json:"tag,omitempty"`
	Notes string    `json:"notes,omitempty"`
	URL   string    `json:"url,omitempty"`
}

const version = 1

type Store struct {
	path string
	mu   sync.Mutex
	data file
}

// Load reads path; a missing file is an empty store. path "" gives a store
// that never saves, for tests and --demo.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	d, err := read(path)
	if err != nil {
		return nil, err
	}
	s.data = d
	return s, nil
}

func read(path string) (file, error) {
	d := file{Version: version, Nodes: map[string]Node{}}
	if path == "" {
		return d, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("%s: %w", path, err)
	}
	if d.Version > version {
		return d, fmt.Errorf("%s: written by a newer lazysecrets (version %d)", path, d.Version)
	}
	if d.Nodes == nil {
		d.Nodes = map[string]Node{}
	}
	return d, nil
}

// Node returns the record for key, empty if there is none.
func (s *Store) Node(key string) Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Nodes[key]
}

// Has reports whether there's a record for key.
func (s *Store) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data.Nodes[key]
	return ok
}

// Nodes returns a copy of all records.
func (s *Store) Nodes() map[string]Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.data.Nodes)
}

// Update changes the record for key and saves. Another lazysecrets may
// have saved in the meantime, so the file is read again first and only
// this record is changed in it. A record left with nothing worth keeping
// is removed.
func (s *Store) Update(key string, change func(*Node)) error {
	return s.save(func(d *file) {
		n := d.Nodes[key]
		change(&n)
		if n.keep() {
			d.Nodes[key] = n
		} else {
			delete(d.Nodes, key)
		}
	})
}

// Usage records — ones kept only for their use — are pruned when they
// go unused this long, and beyond this many, oldest first.
const (
	usageMaxAge = 90 * 24 * time.Hour
	usageMax    = 100
)

// Use counts a use of key at the given time, with change updating its
// context, and prunes old usage records.
func (s *Store) Use(key string, at time.Time, change func(*Node)) error {
	return s.save(func(d *file) {
		n := d.Nodes[key]
		change(&n)
		n.Uses++
		n.LastUsed = at
		d.Nodes[key] = n
		prune(d.Nodes, at)
	})
}

// prune drops usage records unused since usageMaxAge before now, and the
// oldest beyond usageMax. Starred and noted records stay.
func prune(nodes map[string]Node, now time.Time) {
	var usage []string
	for k, n := range nodes {
		if len(n.StarredBy) > 0 || n.Note != "" {
			continue
		}
		if now.Sub(n.LastUsed) > usageMaxAge {
			delete(nodes, k)
			continue
		}
		usage = append(usage, k)
	}
	if len(usage) <= usageMax {
		return
	}
	slices.SortFunc(usage, func(a, b string) int { return nodes[b].LastUsed.Compare(nodes[a].LastUsed) })
	for _, k := range usage[usageMax:] {
		delete(nodes, k)
	}
}

// Flag reports a one-off choice.
func (s *Store) Flag(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Flags[name]
}

// SetFlag records a one-off choice.
func (s *Store) SetFlag(name string, on bool) error {
	return s.save(func(d *file) {
		if d.Flags == nil {
			d.Flags = map[string]bool{}
		}
		if on {
			d.Flags[name] = true
		} else {
			delete(d.Flags, name)
		}
	})
}

func (s *Store) Layout() Layout {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Layout
}

func (s *Store) SetLayout(l Layout) error {
	return s.save(func(d *file) { d.Layout = l })
}

// save applies change to the file as it is now on disk (or to memory, for
// an unsaved store) and writes it atomically, readable by the user only.
func (s *Store) save(change func(*file)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.data
	if s.path != "" {
		var err error
		if d, err = read(s.path); err != nil {
			return err
		}
	} else {
		d.Nodes = maps.Clone(d.Nodes)
	}
	change(&d)
	d.Version = version
	if s.path != "" {
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		if err := writeAtomic(s.path, append(b, '\n')); err != nil {
			return err
		}
	}
	s.data = d
	return nil
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
