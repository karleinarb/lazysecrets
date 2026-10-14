// Package cachefile is the cache of what lazysecrets has loaded
// (cache.json): the Sources lists, vaults, secrets' metadata and where
// the cursor was in each, never values. The TUI and lazysecrets list
// both read it and save to it; a save merges with what's on disk, so
// neither undoes what the other loaded.
package cachefile

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Version is the file's format; a file of another version is ignored,
// and replaced on the next save.
const Version = 1

// MaxAge drops what hasn't been loaded again for this long.
const MaxAge = 30 * 24 * time.Hour

// File is the cache. Every key starts with the provider ID: Sources are
// keyed by it, the rest by Row, Objects' keys and the like.
type File struct {
	Version    int                                      `json:"version"`
	Sources    map[string]Stamped[[]provider.Source]    `json:"sources,omitempty"` // by provider ID
	Containers map[string]Stamped[[]provider.Container] `json:"containers,omitempty"`
	Objects    map[string]Stamped[[]provider.Object]    `json:"objects,omitempty"`
	Details    map[string]Stamped[provider.Object]      `json:"details,omitempty"`
	Cursor     map[string]string                        `json:"cursor,omitempty"`
	// Dropped are keys deleted since the file was read (a list that
	// failed to load): Save deletes them from the file too, rather than
	// merging the old entry back. Not saved.
	Dropped map[string]bool `json:"-"`
}

// Stamped is a list or detail with when it was loaded.
type Stamped[T any] struct {
	At    time.Time `json:"at"`
	Value T         `json:"value"`
}

// New is an empty cache.
func New() File {
	return File{
		Version:    Version,
		Sources:    map[string]Stamped[[]provider.Source]{},
		Containers: map[string]Stamped[[]provider.Container]{},
		Objects:    map[string]Stamped[[]provider.Object]{},
		Details:    map[string]Stamped[provider.Object]{},
		Cursor:     map[string]string{},
	}
}

// Row keys a source's vaults or scopes.
func Row(providerID, sourceID string) string { return providerID + "|" + sourceID }

// ContainerKey keys what's cached about a vault or scope.
func ContainerKey(c provider.Container) string { return c.Source.Provider + "|" + c.ID }

// ObjectsKey keys a vault's secrets, keys or certificates.
func ObjectsKey(c provider.Container, t provider.ObjectType) string {
	return ContainerKey(c) + "|" + string(t)
}

// ProviderOf is the provider ID a key starts with.
func ProviderOf(key string) string {
	id, _, _ := strings.Cut(key, "|")
	return id
}

// Keep is Save's keep for these providers: their lists are kept, but not
// repos', whose files are read from disk anyway, nor other accounts'.
func Keep(providers []provider.Provider) func(providerID string) bool {
	keep := map[string]bool{}
	for _, p := range providers {
		keep[p.ID()] = p.Kind() != provider.KindRepo
	}
	return func(id string) bool { return keep[id] }
}

// Read reads path; a missing, unreadable or other version's file is an
// empty cache.
func Read(path string) File {
	f := New()
	b, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	var d File
	if json.Unmarshal(b, &d) != nil || d.Version != Version {
		return f
	}
	maps.Copy(f.Sources, d.Sources)
	maps.Copy(f.Containers, d.Containers)
	maps.Copy(f.Objects, d.Objects)
	maps.Copy(f.Details, d.Details)
	maps.Copy(f.Cursor, d.Cursor)
	return f
}

// Save writes f to path, merged with the file there: of a list in both,
// the one loaded last wins, and f's cursors win (a nil Cursor keeps the
// file's). Entries older than MaxAge, those of providers keep says no to
// (accounts signed out of), and f.Dropped are dropped, from the file's
// side as well. Saves from several lazysecrets take turns (a lock file),
// so none undoes another's.
func Save(path string, f File, keep func(providerID string) bool, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	out := New()
	ok := func(key string, at time.Time) bool { return keep(ProviderOf(key)) && now.Sub(at) < MaxAge }
	disk := Read(path)
	merge(out.Sources, disk.Sources, f.Sources, ok, f.Dropped)
	merge(out.Containers, disk.Containers, f.Containers, ok, f.Dropped)
	merge(out.Objects, disk.Objects, f.Objects, ok, f.Dropped)
	merge(out.Details, disk.Details, f.Details, ok, f.Dropped)
	for _, m := range []map[string]string{disk.Cursor, f.Cursor} {
		for k, v := range m {
			if keep(ProviderOf(k)) {
				out.Cursor[k] = v
			}
		}
	}
	return write(path, out)
}

// merge puts disk's entries in out, but not those dropped, then ours
// where they're newer.
func merge[T any](out, disk, ours map[string]Stamped[T], ok func(string, time.Time) bool, dropped map[string]bool) {
	for k, e := range disk {
		if ok(k, e.At) && !dropped[k] {
			out[k] = e
		}
	}
	for k, e := range ours {
		if old, seen := out[k]; ok(k, e.At) && (!seen || !e.At.Before(old.At)) {
			out[k] = e
		}
	}
}

// write writes f atomically, readable by the user only.
func write(path string, f File) error {
	b, err := json.Marshal(f)
	if err != nil {
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
