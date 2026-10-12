package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
)

func TestGroupedSources(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	screen := d.screen()
	for _, want := range []string{"▾ Azure (2)", "  platform-prod", "▾ Databricks (2)", "  prod"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q:\n%s", want, screen)
		}
	}
	if src, _, _ := d.selected(); src != "platform-prod" {
		t.Errorf("startup cursor on %q, want the first source", src)
	}
}

func TestFoldGroup(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("k", "enter") // Azure header: fold
	if !strings.Contains(d.screen(), "▸ Azure (2)") || strings.Contains(d.screen(), "platform-prod") {
		t.Fatalf("Azure not folded:\n%s", d.screen())
	}
	if d.m.focus != paneSources {
		t.Error("enter on a header moved focus")
	}
	d.keys("space") // unfold
	if !strings.Contains(d.screen(), "platform-prod") {
		t.Fatalf("Azure not unfolded:\n%s", d.screen())
	}
	// enter on a source still drills in.
	d.keys("j", "enter")
	if d.m.focus != paneContainers {
		t.Error("enter on a source didn't move to vaults")
	}
}

func TestToggleFlatKeepsSelection(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j", "j", "j") // Databricks prod
	d.keys("`")
	if !d.m.flatSources || strings.Contains(d.screen(), "▾") {
		t.Fatalf("not flat:\n%s", d.screen())
	}
	if !strings.Contains(d.screen(), "Databricks prod") {
		t.Errorf("flat rows should show the provider:\n%s", d.screen())
	}
	if src, _, _ := d.selected(); src != "prod" {
		t.Errorf("selection lost: %q", src)
	}
	d.keys("`")
	if src, _, _ := d.selected(); d.m.flatSources || src != "prod" {
		t.Errorf("back to grouped: flat=%v selected=%q", d.m.flatSources, src)
	}
}

func TestFlatSourcesOption(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{FlatSources: true})
	if strings.Contains(d.screen(), "▾") || d.m.sources.Len() != 4 {
		t.Errorf("FlatSources ignored:\n%s", d.screen())
	}
}

func TestGroupHeaderShowsProviderError(t *testing.T) {
	t.Parallel()
	d := newDriver(t, failingSources{fake.Azure(0)}, fake.Databricks(0))
	if !strings.Contains(d.screen(), "▾ Azure (!)") {
		t.Errorf("no error marker:\n%s", d.screen())
	}
	if src, _, _ := d.selected(); src != "prod" {
		t.Errorf("cursor should skip to the first working source, got %q", src)
	}
}

// slowSources delivers Azure's sources after Databricks', like the real
// Azure provider (network) versus Databricks (a local file).
func TestStartupPrefersFirstProviderWhenItAnswersLast(t *testing.T) {
	t.Parallel()
	m := New([]provider.Provider{fake.Azure(0), fake.Databricks(0)}, Options{Clipboard: &fakeClipboard{}})
	d := &driver{t: t, m: m}
	d.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	d.send(sourcesMsg{gen: m.sourcesGen, provider: "Databricks", sources: mustSources(t, fake.Databricks(0))})
	if src, _, _ := d.selected(); src != "prod" {
		t.Fatalf("before Azure answers: %q", src)
	}
	d.send(sourcesMsg{gen: m.sourcesGen, provider: "Azure", sources: mustSources(t, fake.Azure(0))})
	if src, _, _ := d.selected(); src != "platform-prod" {
		t.Errorf("cursor not moved to the first Azure source: %q", src)
	}

	// Once the user has moved, late results don't move the cursor.
	d2 := &driver{t: t, m: New([]provider.Provider{fake.Azure(0), fake.Databricks(0)}, Options{Clipboard: &fakeClipboard{}})}
	d2.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	d2.send(sourcesMsg{gen: d2.m.sourcesGen, provider: "Databricks", sources: mustSources(t, fake.Databricks(0))})
	d2.keys("j") // dev
	d2.send(sourcesMsg{gen: d2.m.sourcesGen, provider: "Azure", sources: mustSources(t, fake.Azure(0))})
	if src, _, _ := d2.selected(); src != "dev" {
		t.Errorf("cursor moved after the user navigated: %q", src)
	}
}

func mustSources(t *testing.T, p provider.Provider) []provider.Source {
	t.Helper()
	s, err := p.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTwoAccounts(t *testing.T) {
	t.Parallel()
	alice := &counting{Provider: fake.Azure(0).As("Azure:alice", "Azure · alice")}
	bob := &counting{Provider: fake.Azure(0).As("Azure:bob", "Azure · bob")}
	d := newDriver(t, alice, bob)

	screen := d.screen()
	if !strings.Contains(screen, "▾ Azure · alice (2)") || !strings.Contains(screen, "▾ Azure · bob (2)") {
		t.Fatalf("one group per account expected:\n%s", screen)
	}

	// Both accounts see platform-prod; bob's is a different row, and
	// browsing it goes through bob's provider.
	d.keys("j", "j", "j") // alice dev, bob header, bob prod
	if src, c, s := d.selected(); src != "platform-prod" || c != "kv-app-prod" || s != "db-password" {
		t.Fatalf("bob's platform-prod: %s/%s/%s", src, c, s)
	}
	c, _ := d.m.containers.Selected()
	if c.Source.Provider != "Azure:bob" || d.m.containers.SelectedID() != "Azure:bob|"+c.ID {
		t.Errorf("vault not tied to bob: provider %q, key %q", c.Source.Provider, d.m.containers.SelectedID())
	}
	if bob.objects != 1 {
		t.Errorf("bob listed objects %d times, want 1", bob.objects)
	}
	before := alice.objects
	d.keys("space") // reveal goes through bob too
	if alice.values != 0 || bob.values != 1 || alice.objects != before {
		t.Errorf("reveal routed wrong: alice values %d, bob values %d", alice.values, bob.values)
	}

	if !strings.Contains(d.screen(), "source     Azure · bob / platform-prod") {
		t.Errorf("detail should name the account:\n%s", d.screen())
	}

	// Flat: the account is shown on each row.
	d.keys("`")
	if !strings.Contains(d.screen(), "platform-prod · bob") {
		t.Errorf("flat rows should name the account:\n%s", d.screen())
	}
}

func TestFilterSourcesByProvider(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("/")
	d.typeText("databr")
	d.m.sources.Top()
	var names []string
	for range d.m.sources.Len() {
		r, _ := d.m.sources.Selected()
		names = append(names, r.source.Name)
		d.m.sources.Move(1)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "dev,prod" {
		t.Errorf("\"databr\" matched %v, want the Databricks workspaces", names)
	}
}

func TestFilteringShowsAccounts(t *testing.T) {
	t.Parallel()
	alice := fake.Azure(0).As("Azure:alice", "Azure · alice")
	bob := fake.Azure(0).As("Azure:bob", "Azure · bob")
	d := newDriver(t, alice, bob)
	d.keys("k", "enter") // fold alice: filtering still finds her sources
	d.keys("/")
	d.typeText("dev")
	screen := d.screen()
	if !strings.Contains(screen, "platform-dev · ali") || !strings.Contains(screen, "platform-dev · bob") {
		t.Errorf("filtered rows should name the account:\n%s", screen)
	}
	d.keys("esc") // clear: back to groups
	if !strings.Contains(d.screen(), "▸ Azure · alice") || strings.Contains(d.screen(), "platform-dev · bob") {
		t.Errorf("not grouped again after clearing:\n%s", d.screen())
	}
}
