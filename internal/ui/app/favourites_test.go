package app

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// jump opens the favourites list, filters it and jumps to the first match.
func (d *driver) jump(filter string) {
	d.t.Helper()
	d.keys("F")
	if d.m.search == nil || d.m.search.scope().id != scopeFavs {
		d.t.Fatalf("F didn't open Go to on Favourites:\n%s", d.screen())
	}
	for _, r := range filter {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	d.keys("enter")
}

func TestStarVaultSortsFirstAndJumps(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "j", "f") // star kv-shared-prod
	if items := d.m.containers.Items(); items[0].Name != "kv-shared-prod" {
		t.Errorf("starred vault not first: %s", items[0].Name)
	}
	if !strings.Contains(d.screen(), "★ kv-shared-prod") {
		t.Errorf("no star:\n%s", d.screen())
	}
	if strings.Contains(d.screen(), "Favourites (") {
		t.Error("the old Favourites row is back")
	}

	d.keys("1", "j") // elsewhere
	d.keys("F")
	if !strings.Contains(d.screen(), "platform-prod / kv-shared-prod") {
		t.Fatalf("favourites list:\n%s", d.screen())
	}
	d.keys("esc")
	d.jump("shared")
	if src, c, _ := d.selected(); src != "platform-prod" || c != "kv-shared-prod" {
		t.Errorf("jumped to %s/%s", src, c)
	}
	if d.m.focus != paneContainers {
		t.Errorf("focus %d after jumping to a vault", d.m.focus)
	}
}

func TestJumpToStarredSourceAndSecret(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "j", "f") // star api-key in platform-prod / kv-app-prod
	d.keys("1", "j", "f") // star platform-dev (it moves to the top of its group)
	d.keys("j", "j", "j") // down to Databricks prod

	d.jump("platform-dev")
	if src, _, _ := d.selected(); src != "platform-dev" || d.m.focus != paneContainers {
		t.Errorf("source jump: %s, focus %d", src, d.m.focus)
	}
	d.jump("api")
	if src, c, s := d.selected(); src != "platform-prod" || c != "kv-app-prod" || s != "api-key" {
		t.Errorf("secret jump: %s/%s/%s", src, c, s)
	}
}

func TestFavouritesListEmpty(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("F")
	if d.m.search == nil || !strings.Contains(d.screen(), "nothing starred yet") {
		t.Errorf("empty favourites:\n%s", d.screen())
	}
}

func TestStarSecretSortsFirst(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "j", "f") // star api-key
	if _, _, s := d.selected(); s != "api-key" {
		t.Errorf("cursor left the starred item: %q", s)
	}
	rows := d.m.secrets.Items()
	if rows[0].Name != "api-key" || !strings.Contains(d.screen(), "★ api-key") {
		t.Errorf("starred secret not first: %v\n%s", rows[0].Name, d.screen())
	}
	// Still first after leaving and coming back (cached list).
	d.keys("2", "j", "k")
	if d.m.secrets.Items()[0].Name != "api-key" {
		t.Error("starred secret not first after returning")
	}
}

func TestNoStarringVersions(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("v", "f")
	if d.m.store.Node(objectKey(*d.m.versionsOf)).StarredFor("Azure") || !strings.Contains(d.screen(), "rather than a version") {
		t.Errorf("starring a version:\n%s", d.screen())
	}
}

func TestFavouritesPersist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	load := func() *store.Store {
		s, err := store.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	d := newDriverOpts(t, Options{Store: load()})
	d.keys("2", "f", "3", "j", "f")

	d2 := newDriverOpts(t, Options{Store: load()})
	d2.keys("F")
	if !strings.Contains(d2.screen(), "platform-prod / kv-app-prod") ||
		!strings.Contains(d2.screen(), "platform-prod / kv-app-prod / api-key") {
		t.Errorf("favourites not restored:\n%s", d2.screen())
	}
	d2.keys("esc")
	if d2.m.secrets.Items()[0].Name != "api-key" {
		t.Error("secret favourite not restored")
	}
}

func TestStarSourceMovesItToTheTop(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j", "f") // star platform-dev
	if src, _, _ := d.selected(); src != "platform-dev" {
		t.Errorf("cursor left the starred source: %q", src)
	}
	rows := d.m.sources.Items()
	if rows[1].source.Name != "platform-dev" || !strings.Contains(d.screen(), "★ platform-dev") {
		t.Errorf("not first in its group: %q\n%s", rows[1].source.Name, d.screen())
	}

	// Flat: starred sources come before every other provider's.
	// platform-dev now leads its group: platform-prod, header, prod, dev.
	d.keys("j", "j", "j", "j", "f", "`") // also star Databricks dev
	var flat []string
	for _, r := range d.m.sources.Items() {
		flat = append(flat, r.source.Name)
	}
	if strings.Join(flat[:2], ",") != "platform-dev,dev" {
		t.Errorf("flat order = %v", flat)
	}

	d.keys("`", "k") // grouped again; dev leads its group, so k is the header
	d.keys("f")
	if !strings.Contains(d.screen(), "select a subscription or workspace to star") {
		t.Errorf("starring a header:\n%s", d.screen())
	}
}

// denying refuses to list objects, like an account without access.
type denying struct{ *counting }

func (d denying) Objects(context.Context, provider.Container, provider.ObjectType) ([]provider.Object, error) {
	d.objects++
	return nil, fake.ErrForbidden
}

// kvKey is kv-app-prod's store key, kvSharedKey kv-shared-prod's.
const (
	kvKey       = "lazysecrets://azure/kv-app-prod.vault.azure.net"
	kvSharedKey = "lazysecrets://azure/kv-shared-prod.vault.azure.net"
)

// starredWithoutAccount is kv-app-prod starred with no account recorded,
// like one starred by an account that's no longer signed in.
func starredWithoutAccount(t *testing.T) *store.Store {
	t.Helper()
	s, _ := store.Load("")
	if err := s.Update(kvKey, func(n *store.Node) {
		n.Name, n.Path, n.Source, n.StarredBy = "kv-app-prod", []string{"platform-prod"}, "sub-prod", []string{"Azure:carol"}
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOldFavouriteFindsTheAccountWithAccess(t *testing.T) {
	t.Parallel()
	alice := &counting{Provider: fake.Azure(0).As("Azure:alice", "Azure · alice")}
	bob := &counting{Provider: fake.Azure(0).As("Azure:bob", "Azure · bob")}
	mem := starredWithoutAccount(t)
	d := newDriverOpts(t, Options{Store: mem}, denying{alice}, bob)

	d.jump("kv-app") // both accounts see platform-prod: which one?
	if !strings.Contains(d.screen(), "Several accounts can see kv-app-prod") {
		t.Fatalf("no account choice:\n%s", d.screen())
	}
	d.keys("2") // bob
	if _, c, s := d.selected(); c != "kv-app-prod" || s != "db-password" {
		t.Fatalf("favourite not browsable: %q/%q\n%s", c, s, d.screen())
	}
	if got := mem.Node(kvKey).Account; got != "Azure:bob" {
		t.Errorf("favourite bound to %q, want Azure:bob", got)
	}
	d.keys("R") // used through bob now
	if !strings.Contains(d.screen(), "platform-prod / kv-app-prod · bob") {
		t.Errorf("recent should show the account:\n%s", d.screen())
	}
	d.keys("esc")

	// Reads go through bob, and alice isn't asked again.
	before := alice.objects
	d.keys("space")
	if bob.values != 1 || alice.values != 0 {
		t.Errorf("reveal: alice %d, bob %d", alice.values, bob.values)
	}
	d.keys("2", "r")
	if alice.objects != before {
		t.Errorf("alice asked again after binding: %d → %d", before, alice.objects)
	}
}

func TestOldFavouriteNoAccountHasAccess(t *testing.T) {
	t.Parallel()
	alice := &counting{Provider: fake.Azure(0).As("Azure:alice", "Azure · alice")}
	bob := &counting{Provider: fake.Azure(0).As("Azure:bob", "Azure · bob")}
	mem := starredWithoutAccount(t)
	d := newDriverOpts(t, Options{Store: mem}, denying{alice}, denying{bob})

	d.jump("kv-app")
	d.keys("1") // alice: no access
	if !strings.Contains(d.screen(), "403 Forbidden") {
		t.Errorf("no access error:\n%s", d.screen())
	}
	if mem.Node(kvKey).Account != "" {
		t.Error("favourite bound although no account worked")
	}
}

func TestFavouritesPopupKeepsItsSize(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j", "f", "2", "j", "f", "3", "j", "f") // a subscription, a vault, a secret
	d.keys("F")
	size := func() (int, int) {
		box := d.m.searchViewBox()
		return lipgloss.Width(box), lipgloss.Height(box)
	}
	w0, h0 := size()
	for _, k := range []string{"down", "down", "up"} {
		d.send(tea.KeyPressMsg{Code: map[string]rune{"down": tea.KeyDown, "up": tea.KeyUp}[k]})
		if w, h := size(); w != w0 || h != h0 {
			t.Fatalf("popup changed from %dx%d to %dx%d moving %s", w0, h0, w, h, k)
		}
	}
	d.send(tea.KeyPressMsg{Code: 'z', Text: "z"}) // filter down to nothing
	if w, h := size(); w != w0 || h != h0 {
		t.Errorf("popup changed from %dx%d to %dx%d while filtering", w0, h0, w, h)
	}
}

func TestStoreKeysAreLinks(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("f", "2", "f", "3", "j", "f")
	var keys []string
	for k := range d.m.store.Nodes() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{
		"lazysecrets://azure/kv-app-prod.vault.azure.net",
		"lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/api-key",
		"lazysecrets://azure/subscriptions/sub-prod",
	}
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v", keys)
	}
	n := d.m.store.Node(want[1])
	if n.Name != "api-key" || n.Source != "sub-prod" || n.Account != "Azure" || !slices.Equal(n.Path, []string{"platform-prod", "kv-app-prod"}) {
		t.Errorf("context = %+v", n)
	}
}

func TestFavouritesShowNotes(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "j", "f", "n") // star kv-shared-prod and note it
	for _, r := range "shared certs" {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	d.keys("enter", "F")
	line := ""
	for l := range strings.SplitSeq(d.screen(), "\n") {
		if strings.Contains(l, "kv-shared-prod") && strings.Contains(l, "│") && strings.Contains(l, "/") {
			line = l
		}
	}
	if !strings.Contains(line, "shared certs") {
		t.Fatalf("no note in the favourites row %q:\n%s", line, d.screen())
	}
	for _, r := range "certs" { // the filter matches notes
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(d.m.search.shown) != 1 {
		t.Errorf("filtering by note: %d entries", len(d.m.search.shown))
	}
}

func TestSecretWithoutContextJumpsThroughItsVault(t *testing.T) {
	t.Parallel()
	mem, _ := store.Load("")
	_ = mem.Update(kvKey, func(n *store.Node) {
		n.Name, n.Path, n.Source, n.Account, n.StarredBy = "kv-app-prod", []string{"platform-prod"}, "sub-prod", "Azure", []string{"Azure"}
	})
	_ = mem.Update(kvKey+"/secrets/api-key", func(n *store.Node) { n.StarredBy = []string{"Azure"} })
	// counting hides Locate, like an account that can't find the vault
	// in Resource Graph: opening it as a link would land in Opened links.
	d := newDriverOpts(t, Options{Store: mem}, &counting{Provider: fake.Azure(0)})
	d.keys("j") // elsewhere
	d.jump("api-key")
	if src, c, s := d.selected(); src != "platform-prod" || c != "kv-app-prod" || s != "api-key" {
		t.Errorf("jumped to %s/%s/%s", src, c, s)
	}
	if strings.Contains(d.screen(), "Opened links") {
		t.Errorf("went through Opened links:\n%s", d.screen())
	}
}

func TestLinkedVaultKeepsKnownContext(t *testing.T) {
	t.Parallel()
	n := store.Node{Source: "sub-prod", Path: []string{"platform-prod", "kv-app-prod"}}
	// A vault opened from a link: account known, subscription not.
	withContext("api-key", []string{"kv-app-prod.vault.azure.net", "kv-app-prod"},
		provider.Source{Kind: provider.KindAzure, Provider: "Azure:bob"})(&n)
	if n.Source != "sub-prod" || n.Path[0] != "platform-prod" || n.Account != "Azure:bob" {
		t.Errorf("context = %+v", n)
	}
}

func TestToggleStarInGoTo(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "j", "f")          // star kv-shared-prod
	d.keys("3", "j", "space", "2") // use smtp-password, unstarred
	smtp := kvSharedKey + "/secrets/smtp-password"
	ctrlF := tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}

	d.keys("R", "/")
	d.typeText("smtp")
	d.send(ctrlF) // star it from Recent, typing
	if !d.m.store.Node(smtp).StarredFor("Azure") || !strings.Contains(d.screen(), "★ ") {
		t.Fatalf("ctrl+f didn't star a recent item:\n%s", d.screen())
	}
	d.send(ctrlF) // and back
	if d.m.store.Node(smtp).StarredFor("Azure") {
		t.Error("second ctrl+f didn't unstar")
	}
	if d.m.search == nil || d.m.search.input.Value() != "smtp" || len(d.m.search.shown) == 0 {
		t.Fatalf("entry or query gone after toggling:\n%s", d.screen())
	}

	// Unstarring a favourite keeps it listed until Go to opens again.
	d.keys("esc", "esc", "F") // stop typing, close, open on Favourites
	v := d.m.search
	for v.res < len(v.shown)-1 && v.shown[v.res].star != kvSharedKey {
		d.send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if v.shown[v.res].star != kvSharedKey {
		t.Fatalf("selected %q", v.shown[v.res].star)
	}
	d.keys("f") // browsing: f
	if d.m.store.Node(kvSharedKey).StarredFor("Azure") {
		t.Error("favourite still starred")
	}
	if v.shown[v.res].star != kvSharedKey || v.shown[v.res].starred {
		t.Error("unstarred favourite didn't stay in place, unstarred")
	}
	d.keys("esc")
	if strings.Contains(d.screen(), "★ kv-shared-prod") {
		t.Errorf("Vaults pane still shows the star:\n%s", d.screen())
	}
}

func TestStarWhileFiltering(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "/")
	for _, r := range "api-k" {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	d.send(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !d.m.store.Node(kvKey + "/secrets/api-key").StarredFor("Azure") {
		t.Errorf("ctrl+f while filtering didn't star:\n%s", d.screen())
	}
}

func TestStarIsPerAccount(t *testing.T) {
	t.Parallel()
	alice := fake.Azure(0).As("Azure:alice", "Azure · alice")
	bob := fake.Azure(0).As("Azure:bob", "Azure · bob")
	d := newDriverOpts(t, Options{}, alice, bob)
	// Rows: alice header, platform-prod, platform-dev, bob header, ….
	d.keys("f") // alice's platform-prod
	n := d.m.store.Node("lazysecrets://azure/subscriptions/sub-prod")
	if !n.StarredFor("Azure:alice") || n.StarredFor("Azure:bob") {
		t.Errorf("starred by %v, want alice only", n.StarredBy)
	}
	if c := strings.Count(d.screen(), "★ platform-prod"); c != 1 {
		t.Errorf("star shown %d times, want once:\n%s", c, d.screen())
	}
	// A note is about the thing: it shows under both.
	d.keys("n")
	d.typeText("shared note")
	d.keys("enter")
	// On both accounts' rows, and in the selected one's details.
	if n := strings.Count(d.screen(), "shared note"); n != 3 {
		t.Errorf("note shown %d times, want 3:\n%s", n, d.screen())
	}
	d.keys("F")
	var labels []string
	for _, o := range d.m.search.shown {
		labels = append(labels, ansiStrip(o.label))
	}
	if len(labels) != 1 || !strings.Contains(labels[0], "platform-prod · alice") {
		t.Errorf("favourites should list alice's star only: %q", labels)
	}
}

func TestRecentIcon(t *testing.T) {
	t.Parallel()
	mem, _ := store.Load("")
	_ = mem.Use("lazysecrets://azure/"+kv+"/secrets/api-key", time.Now(), func(*store.Node) {})
	d := newDriverOpts(t, Options{Store: mem, RecentIcon: true})
	d.keys("3")
	scr := d.screen()
	if !strings.Contains(scr, "◷ api-key") || strings.Contains(scr, "◷ db-password") {
		t.Errorf("want ◷ on api-key only:\n%s", scr)
	}
	off := newDriverOpts(t, Options{Store: mem})
	off.keys("3")
	if strings.Contains(off.screen(), "◷") {
		t.Errorf("◷ without ui.recentIcon:\n%s", off.screen())
	}
}
