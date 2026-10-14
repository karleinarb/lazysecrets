package listing

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
)

// counting counts a provider's requests.
type counting struct {
	provider.Provider
	calls atomic.Int32
}

func (c *counting) Sources(ctx context.Context) ([]provider.Source, error) {
	c.calls.Add(1)
	return c.Provider.Sources(ctx)
}

func (c *counting) Containers(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	c.calls.Add(1)
	return c.Provider.Containers(ctx, src)
}

func (c *counting) Objects(ctx context.Context, ct provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	c.calls.Add(1)
	return c.Provider.Objects(ctx, ct, t)
}

// run lists, returning the entries and, apart, the lists that failed.
func run(t *testing.T, o Options) ([]Entry, cachefile.File, []Entry) {
	t.Helper()
	if o.Cache.Version == 0 {
		o.Cache = cachefile.New()
	}
	o.Timeout = func(provider.Kind) time.Duration { return 5 * time.Second }
	var got, failed []Entry
	f, _ := Run(context.Background(), o, func(e Entry) error {
		if e.Error != "" {
			failed = append(failed, e)
		} else {
			got = append(got, e)
		}
		return nil
	})
	return got, f, failed
}

func has(es []Entry, kind, path string) bool {
	for _, e := range es {
		if e.Kind == kind && strings.Join(e.Path, "/") == path {
			return true
		}
	}
	return false
}

func TestListLoadsAndCaches(t *testing.T) {
	t.Parallel()
	az := &counting{Provider: fake.Azure(0)}
	got, f, errs := run(t, Options{Providers: []provider.Provider{az, fake.Repos()}, MaxAge: time.Minute})
	for _, want := range [][2]string{
		{"source", "platform-prod"}, {"vault", "platform-prod/kv-app-prod"}, {"secret", "platform-prod/kv-app-prod/db-password"},
		{"source", "lazysecrets-demo"}, {"secret", "lazysecrets-demo/.env/DATABASE_URL"},
	} {
		if !has(got, want[0], want[1]) {
			t.Errorf("no %s %s", want[0], want[1])
		}
	}
	if len(errs) != 1 || errs[0].Kind != "vault" || errs[0].Path[1] != "kv-restricted" || !strings.Contains(errs[0].Error, "403") || errs[0].Link == "" {
		t.Errorf("failed = %+v", errs)
	}
	seen := map[string]bool{}
	for _, e := range got {
		if seen[e.Link] {
			t.Errorf("%s listed twice", e.Link)
		}
		seen[e.Link] = true
		if e.Provider != "azure" && e.Provider != "repo" {
			t.Errorf("provider %q", e.Provider)
		}
	}
	for _, e := range got {
		if e.Kind == "secret" && e.Provider == "azure" && (!strings.HasPrefix(e.Reference, "@Microsoft.KeyVault(") || e.URL == "") {
			t.Errorf("%s: reference %q, url %q", e.Link, e.Reference, e.URL)
		}
	}
	if len(f.Sources) != 1 || len(f.Containers) == 0 || len(f.Objects) == 0 {
		t.Errorf("not cached: %d sources, %d containers, %d objects", len(f.Sources), len(f.Containers), len(f.Objects))
	}
	for k := range f.Containers {
		if cachefile.ProviderOf(k) != az.ID() {
			t.Errorf("cached a repo: %s", k)
		}
	}

	// Again with that cache: the same, without asking Azure.
	before := az.calls.Load()
	again, _, _ := run(t, Options{Providers: []provider.Provider{az, fake.Repos()}, Cache: f, MaxAge: time.Minute})
	if len(again) != len(got) {
		t.Errorf("%d entries from the cache, %d loaded", len(again), len(got))
	}
	// The vault that couldn't be read wasn't cached: it's asked again.
	if n := az.calls.Load() - before; n != 1 {
		t.Errorf("%d requests with a fresh cache, want 1 (kv-restricted)", n)
	}

	// Stale: loaded again, but each entry is still listed once.
	before = az.calls.Load()
	stale, _, _ := run(t, Options{Providers: []provider.Provider{az}, Cache: f, MaxAge: time.Minute,
		Now: func() time.Time { return time.Now().Add(time.Hour) }})
	if az.calls.Load()-before < 3 {
		t.Error("stale lists weren't loaded again")
	}
	links := map[string]bool{}
	for _, e := range stale {
		if links[e.Link] {
			t.Errorf("%s listed twice", e.Link)
		}
		links[e.Link] = true
	}
}

func TestListCachedOnly(t *testing.T) {
	t.Parallel()
	az := &counting{Provider: fake.Azure(0)}
	_, f, _ := run(t, Options{Providers: []provider.Provider{az}, MaxAge: time.Minute})
	before := az.calls.Load()
	got, _, errs := run(t, Options{Providers: []provider.Provider{az, fake.Repos()}, Cache: f, Cached: true,
		Now: func() time.Time { return time.Now().Add(24 * time.Hour) }})
	if az.calls.Load() != before || len(errs) != 0 {
		t.Errorf("--cached asked Azure: %d requests, %v", az.calls.Load()-before, errs)
	}
	if !has(got, "secret", "platform-prod/kv-app-prod/db-password") || !has(got, "secret", "lazysecrets-demo/.env/DATABASE_URL") {
		t.Error("cached secrets or repos missing")
	}
}

func TestListCachedFirst(t *testing.T) {
	t.Parallel()
	az := fake.Azure(0)
	_, f, _ := run(t, Options{Providers: []provider.Provider{az}, MaxAge: time.Minute})
	// Forget one vault's secrets: they load, after everything cached.
	var dropped string
	for k := range f.Objects {
		if strings.Contains(k, "kv-app-prod") && strings.HasSuffix(k, "|Secret") {
			dropped = k
			delete(f.Objects, k)
			break
		}
	}
	if dropped == "" {
		t.Fatalf("no kv-app-prod in %v", f.Objects)
	}
	got, _, _ := run(t, Options{Providers: []provider.Provider{az}, Cache: f, MaxAge: time.Minute})
	last := got[len(got)-1]
	if last.Kind != "secret" || last.Path[1] != "kv-app-prod" {
		t.Errorf("last = %+v, want a kv-app-prod secret, loaded after the cache", last)
	}
}

func TestListStopsWhenTheReaderGoes(t *testing.T) {
	t.Parallel()
	gone := errors.New("broken pipe")
	n := 0
	var errs []Entry
	Run(context.Background(), Options{
		Providers: []provider.Provider{fake.Azure(0), fake.Repos()}, Cache: cachefile.New(), MaxAge: time.Minute,
		Timeout: func(provider.Kind) time.Duration { return time.Second },
	}, func(e Entry) error {
		if e.Error != "" {
			errs = append(errs, e)
		}
		n++
		if n == 2 {
			return gone
		}
		return nil
	})
	if n != 2 || len(errs) != 0 {
		t.Errorf("emitted %d after the reader went, errs %v", n, errs)
	}
}

func TestListKinds(t *testing.T) {
	t.Parallel()
	// Sources only: nothing under them is asked for.
	az := &counting{Provider: fake.Azure(0)}
	got, _, _ := run(t, Options{Providers: []provider.Provider{az}, MaxAge: time.Minute, Kinds: []string{"source"}})
	if len(got) == 0 || az.calls.Load() != 1 {
		t.Errorf("%d sources, %d requests, want just the sources'", len(got), az.calls.Load())
	}
	for _, e := range got {
		if e.Kind != "source" {
			t.Errorf("listed a %s", e.Kind)
		}
	}
	// Keys, asked for: loaded, though Go to wouldn't; no secrets.
	got, _, failed := run(t, Options{Providers: []provider.Provider{fake.Azure(0)}, MaxAge: time.Minute, Kinds: []string{"key"}})
	if !has(got, "key", "platform-prod/kv-app-prod/signing-key") {
		t.Errorf("no keys: %+v", got)
	}
	for _, e := range got {
		if e.Kind != "key" {
			t.Errorf("listed a %s", e.Kind)
		}
	}
	if len(failed) == 0 {
		t.Error("a list that failed wasn't listed")
	}
}

func TestErrorsAreOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"403 Forbidden: Caller is not authorized\nCaller: appid=x;oid=y\nVault: kv;location=z": "403 Forbidden: Caller is not authorized",
		"\n  timeout\tafter 30s\n": "timeout after 30s",
		"":                         "unreadable",
	} {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListMarks(t *testing.T) {
	t.Parallel()
	az := fake.Azure(0)
	var starredLink string
	got, _, _ := run(t, Options{Providers: []provider.Provider{az}, MaxAge: time.Minute, Kinds: []string{"secret"},
		Mark: func(link, account string) Marks {
			if starredLink == "" && strings.HasSuffix(link, "/db-password") {
				starredLink = link
			}
			if strings.HasSuffix(link, "/api-key") {
				return Marks{Recent: true, LastUsed: time.Now(), Uses: 3, Note: "rotate in May"}
			}
			if strings.HasSuffix(link, "/legacy-token") { // used, but too long ago
				return Marks{LastUsed: time.Now().AddDate(-2, 0, 0), Uses: 9}
			}
			return Marks{Starred: link == starredLink && account == az.ID()}
		}})
	var starred, recent int
	for _, e := range got {
		if e.Starred {
			starred++
			if e.Link != starredLink {
				t.Errorf("%s starred", e.Link)
			}
		}
		if (e.Note != "") != strings.HasSuffix(e.Link, "/api-key") {
			t.Errorf("%s: note %q", e.Link, e.Note)
		}
		if e.Recent {
			recent++
			if e.Uses != 3 || e.LastUsed.IsZero() {
				t.Errorf("%s: %d uses, last %v", e.Link, e.Uses, e.LastUsed)
			}
		} else if e.Uses != 0 || !e.LastUsed.IsZero() {
			t.Errorf("%s not recent, but %d uses, last %v", e.Link, e.Uses, e.LastUsed)
		}
	}
	if starred != 1 || recent == 0 {
		t.Errorf("%d starred, %d recent", starred, recent)
	}
}

func TestFailedListNotRetried(t *testing.T) {
	t.Parallel()
	az := &counting{Provider: fake.Azure(0)}
	_, f, _ := run(t, Options{Providers: []provider.Provider{az}, MaxAge: time.Minute})
	// Everything stale: sources reload, and their vaults are queued again
	// as each list arrives. kv-restricted fails once, and isn't asked again.
	before := az.calls.Load()
	_, _, failed := run(t, Options{Providers: []provider.Provider{az}, Cache: f, MaxAge: time.Minute,
		Now: func() time.Time { return time.Now().Add(time.Hour) }})
	if len(failed) != 1 {
		t.Errorf("failed: %+v", failed)
	}
	fresh := &counting{Provider: fake.Azure(0)}
	run(t, Options{Providers: []provider.Provider{fresh}, MaxAge: time.Minute})
	if n := az.calls.Load() - before; n > fresh.calls.Load() {
		t.Errorf("%d requests with a stale cache, more than %d from scratch: something was asked twice", n, fresh.calls.Load())
	}
}

func TestRunWithoutTimeout(t *testing.T) {
	t.Parallel()
	Run(context.Background(), Options{Providers: []provider.Provider{fake.Repos()}, Cache: cachefile.New()},
		func(Entry) error { return nil }) // no Timeout: a default, not a panic
}
