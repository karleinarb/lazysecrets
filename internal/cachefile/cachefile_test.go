package cachefile

import (
	"path/filepath"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func list(at time.Time, names ...string) Stamped[[]provider.Container] {
	var cs []provider.Container
	for _, n := range names {
		cs = append(cs, provider.Container{Name: n})
	}
	return Stamped[[]provider.Container]{At: at, Value: cs}
}

func TestSaveMerges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	now := time.Now()
	all := func(string) bool { return true }

	// The TUI saved two lists.
	tui := New()
	tui.Containers["az|a"] = list(now.Add(-time.Hour), "kv-a-old")
	tui.Containers["az|b"] = list(now.Add(-time.Minute), "kv-b")
	tui.Cursor["az|b"] = "kv-b"
	if err := Save(path, tui, all, now); err != nil {
		t.Fatal(err)
	}
	// lazysecrets list loads a again, and c.
	listed := Read(path)
	listed.Containers["az|a"] = list(now, "kv-a-new")
	listed.Containers["az|c"] = list(now, "kv-c")
	if err := Save(path, listed, all, now); err != nil {
		t.Fatal(err)
	}
	// The TUI saves what it has, older: list's lists stay.
	if err := Save(path, tui, all, now); err != nil {
		t.Fatal(err)
	}
	f := Read(path)
	for key, want := range map[string]string{"az|a": "kv-a-new", "az|b": "kv-b", "az|c": "kv-c"} {
		if got := f.Containers[key].Value; len(got) != 1 || got[0].Name != want {
			t.Errorf("%s = %v, want %s", key, got, want)
		}
	}
	if f.Cursor["az|b"] != "kv-b" {
		t.Error("cursor lost")
	}
}

func TestSaveDrops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	now := time.Now()
	f := New()
	f.Containers["az|old"] = list(now.Add(-MaxAge-time.Hour), "kv-old")
	f.Containers["gone|x"] = list(now, "kv-gone")
	f.Containers["az|x"] = list(now, "kv-x")
	if err := Save(path, f, func(id string) bool { return true }, now); err != nil {
		t.Fatal(err)
	}
	// Signed out of "gone" since: dropped from the file too.
	if err := Save(path, New(), func(id string) bool { return id == "az" }, now); err != nil {
		t.Fatal(err)
	}
	got := Read(path).Containers
	if _, ok := got["az|old"]; ok {
		t.Error("kept a list older than MaxAge")
	}
	if _, ok := got["gone|x"]; ok {
		t.Error("kept a signed-out account's list")
	}
	if _, ok := got["az|x"]; !ok {
		t.Error("dropped a list on disk the save didn't have")
	}
}

func TestReadOtherVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	f := New()
	f.Version = Version + 1
	f.Containers["az|x"] = list(time.Now(), "kv-x")
	if err := write(path, f); err != nil {
		t.Fatal(err)
	}
	if got := Read(path); len(got.Containers) != 0 || got.Version != Version {
		t.Errorf("read another version's file: %+v", got)
	}
}

func TestSaveDroppedAndCursors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	now := time.Now()
	all := func(string) bool { return true }
	f := New()
	f.Containers["az|bad"] = list(now, "kv-old")
	f.Cursor["az|x"] = "kv-tui"
	if err := Save(path, f, all, now); err != nil {
		t.Fatal(err)
	}
	// The TUI fails to load it again and drops it: it stays gone.
	f = New()
	f.Dropped = map[string]bool{"az|bad": true}
	if err := Save(path, f, all, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(path).Containers["az|bad"]; ok {
		t.Error("a dropped list came back")
	}
	// lazysecrets list saves without cursors: the TUI's stay.
	listed := New()
	listed.Cursor = nil
	listed.Containers["az|y"] = list(now, "kv-y")
	if err := Save(path, listed, all, now); err != nil {
		t.Fatal(err)
	}
	if Read(path).Cursor["az|x"] != "kv-tui" {
		t.Error("the TUI's cursor was lost")
	}
}
