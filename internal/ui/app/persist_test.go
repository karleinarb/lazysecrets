package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

func TestCacheFileShowsLastRunAtOnce(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "lazysecrets", "cache.json")
	d := newDriverOpts(t, Options{CacheFile: file}, fake.Azure(0))
	d.keys("3", "space")
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") {
		t.Fatalf("value not revealed:\n%s", d.screen())
	}
	src, vault, secret := d.selected()
	if err := d.m.Persist(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "correct-horse") {
		t.Error("a value is in the cache file")
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("cache file mode %v", fi.Mode().Perm())
	}

	// Next run, with a backend too slow to answer during the test: the
	// lists show from the file, and the cursor is where it was.
	d2 := newDriverOpts(t, Options{CacheFile: file}, fake.Azure(time.Minute))
	if strings.Contains(d2.screen(), "cached") {
		t.Errorf("a fresh cache said its age:\n%s", d2.screen())
	}
	d2.keys("2")
	if _, v, _ := d2.selected(); v != vault {
		t.Errorf("vaults from the cache: selected %q, want %q\n%s", v, vault, d2.screen())
	}
	d2.keys("3")
	if s, v, o := d2.selected(); s != src || v != vault || o != secret {
		t.Errorf("selected %s/%s/%s, want %s/%s/%s\n%s", s, v, o, src, vault, secret, d2.screen())
	}
}

// Lists from an older cache say how old they are while they reload.
func TestCacheFileSaysItsAge(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "cache.json")
	d := newDriverOpts(t, Options{CacheFile: file}, fake.Azure(0))
	d.keys("3")
	at := time.Now().Add(-3 * time.Hour)
	for k, e := range d.m.cache.containers {
		d.m.cache.containers[k] = entry[[]provider.Container]{e.value, at}
	}
	for id := range d.m.sourcesAt {
		d.m.sourcesAt[id] = at
	}
	if err := d.m.Persist(); err != nil {
		t.Fatal(err)
	}
	d2 := newDriverOpts(t, Options{CacheFile: file}, fake.Azure(time.Minute))
	d2.keys("2")
	if n := strings.Count(d2.screen(), "cached 3h ago"); n != 2 {
		t.Errorf("Sources and Vaults should say they're from 3h ago (%d do):\n%s", n, d2.screen())
	}
}

func TestCacheFileDropsOldAndSignedOut(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "cache.json")
	az := fake.Azure(0)
	now := time.Now()
	f := cacheFile{
		Version: cacheFileVersion,
		Containers: map[string]stamped[[]provider.Container]{
			az.ID() + "|recent":     {now.Add(-time.Hour), []provider.Container{{Name: "kv-recent"}}},
			az.ID() + "|old":        {now.Add(-cacheMaxAge - time.Hour), []provider.Container{{Name: "kv-old"}}},
			"Azure:gone@x/t|recent": {now, []provider.Container{{Name: "kv-gone"}}},
		},
	}
	if err := writeCacheFile(file, f); err != nil {
		t.Fatal(err)
	}
	d := newDriverOpts(t, Options{CacheFile: file}, az)
	c := d.m.cache.containers
	if _, ok := c[az.ID()+"|recent"]; !ok {
		t.Error("dropped a recent entry")
	}
	if _, ok := c[az.ID()+"|old"]; ok {
		t.Error("kept an entry older than cacheMaxAge")
	}
	if _, ok := c["Azure:gone@x/t|recent"]; ok {
		t.Error("kept an entry of an account that isn't signed in")
	}
}

func TestBackWhereItWasLeft(t *testing.T) {
	t.Parallel()
	st, _ := store.Load("")
	dir := "/work/project"
	d := newDriverOpts(t, Options{Store: st, Dir: dir}, fake.Azure(0))
	d.keys("3", "j", "j")
	src, vault, secret := d.selected()
	if err := d.m.Persist(); err != nil {
		t.Fatal(err)
	}
	if p, ok := st.Place(dir); !ok || !strings.Contains(p.Link, secret) {
		t.Fatalf("place %+v", p)
	}

	d2 := newDriverOpts(t, Options{Store: st, Dir: dir}, fake.Azure(0))
	if s, v, o := d2.selected(); s != src || v != vault || o != secret {
		t.Errorf("back at %s/%s/%s, want %s/%s/%s\n%s", s, v, o, src, vault, secret, d2.screen())
	}

	// Another folder starts as usual; so does opening a link.
	d3 := newDriverOpts(t, Options{Store: st, Dir: "/elsewhere"}, fake.Azure(0))
	if _, _, o := d3.selected(); o == secret {
		t.Errorf("another folder went to %s too", o)
	}
	r, _ := ref.Parse("lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password")
	d4 := newDriverOpts(t, Options{Store: st, Dir: dir, Open: &r}, fake.Azure(0))
	if d4.m.nav != nil && d4.m.nav.quiet {
		t.Error("a link was overridden by where it was left")
	}
}
