package app

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
)

// counting records how often lists and details are fetched.
type counting struct {
	provider.Provider
	containers, objects, describes, values, versions int
}

func (c *counting) Versions(ctx context.Context, o provider.Object) ([]provider.Object, error) {
	c.versions++
	return c.Provider.Versions(ctx, o)
}

func (c *counting) Containers(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	c.containers++
	return c.Provider.Containers(ctx, src)
}

func (c *counting) Objects(ctx context.Context, ct provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	c.objects++
	return c.Provider.Objects(ctx, ct, t)
}

func (c *counting) Describe(ctx context.Context, o provider.Object) (provider.Object, error) {
	c.describes++
	return c.Provider.Describe(ctx, o)
}

func (c *counting) Value(ctx context.Context, o provider.Object) (provider.SecretValue, error) {
	c.values++
	return c.Provider.Value(ctx, o)
}

func newCachingDriver(t *testing.T) (*driver, *counting, *time.Time) {
	t.Helper()
	p := &counting{Provider: fake.Azure(0)}
	d := newDriver(t, p)
	// Entries from the initial load were stamped with the real clock.
	clock := time.Now()
	d.m.now = func() time.Time { return clock }
	return d, p, &clock
}

func TestCacheAvoidsRefetchAndRestoresCursor(t *testing.T) {
	t.Parallel()
	d, p, _ := newCachingDriver(t)
	d.keys("2", "j", "3", "j") // kv-shared-prod... then back
	d.keys("2", "k", "3", "j") // kv-app-prod, cursor to api-key
	d.keys("1", "j", "k")      // away to platform-dev and back
	before := *p

	d.keys("j", "k")
	if p.containers != before.containers || p.objects != before.objects {
		t.Errorf("fresh cache refetched: containers %d→%d, objects %d→%d",
			before.containers, p.containers, before.objects, p.objects)
	}
	if _, c, s := d.selected(); c != "kv-app-prod" || s != "api-key" {
		t.Errorf("cursor not restored: %s/%s", c, s)
	}
}

func TestStaleCacheShownThenReloaded(t *testing.T) {
	t.Parallel()
	d, p, clock := newCachingDriver(t)
	d.keys("j") // platform-dev
	*clock = clock.Add(cacheTTL + time.Second)
	before := p.containers
	d.keys("k") // back to platform-prod: stale
	if p.containers != before+1 {
		t.Errorf("stale entry not reloaded: %d calls, want %d", p.containers, before+1)
	}
	if _, c, _ := d.selected(); c != "kv-app-prod" {
		t.Errorf("selection lost across background reload: %q", c)
	}
}

func TestRefreshBypassesCache(t *testing.T) {
	t.Parallel()
	d, p, _ := newCachingDriver(t)
	before := p.objects
	d.keys("3", "r")
	if p.objects != before+1 {
		t.Errorf("r did not reload: %d calls, want %d", p.objects, before+1)
	}
}

func TestValuesAndErrorsNotCached(t *testing.T) {
	t.Parallel()
	d, p, _ := newCachingDriver(t)
	d.keys("space", "space", "space")
	if p.values != 2 {
		t.Errorf("value fetched %d times for two reveals, want 2", p.values)
	}

	d.keys("2", "G") // kv-restricted: 403
	if _, ok := d.m.cache.objects[d.m.selContainer]; ok {
		t.Error("error result cached")
	}
	before := p.objects
	d.keys("k", "j")
	if p.objects != before+2 {
		t.Errorf("failed list not retried: %d calls, want %d", p.objects, before+2)
	}
}

func TestSignInDropsCache(t *testing.T) {
	t.Parallel()
	d, _, _ := newCachingDriver(t)
	d.send(loginDoneMsg{})
	// Everything is reloaded fresh, so only the current selection is cached.
	if n := len(d.m.cache.containers); n != 1 {
		t.Errorf("containers cached after sign-in = %d, want 1", n)
	}
}

// runAll runs cmd and the commands it batches, ignoring their messages.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			runAll(c)
		}
	}
}

func TestMovingOnDropsUnsettledLoads(t *testing.T) {
	t.Parallel()
	c := &counting{Provider: fake.Azure(0)}
	d := newDriverOpts(t, Options{}, c)
	d.m.settle = 20 * time.Millisecond
	before := c.containers

	// j to platform-dev and straight back: its load never goes out.
	m1, passed := d.m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, back := m1.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	runAll(back)
	runAll(passed)
	if c.containers != before {
		t.Errorf("passing over a subscription loaded it: %d calls", c.containers-before)
	}

	// Stopping on it loads it once the cursor settles.
	_, stop := d.m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	runAll(stop)
	if c.containers != before+1 {
		t.Errorf("stopping on a subscription: %d calls, want 1", c.containers-before)
	}
}
