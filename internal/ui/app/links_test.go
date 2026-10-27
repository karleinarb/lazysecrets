package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

const kv = "kv-app-prod.vault.azure.net"

// open pastes a reference and, for an object, chooses to go to it.
func (d *driver) open(link string) {
	d.t.Helper()
	d.openWith(link, "o")
}

// openWith pastes a reference and answers the "Open link" menu with k.
func (d *driver) openWith(link, k string) {
	d.t.Helper()
	d.send(tea.PasteMsg{Content: link})
	if d.m.prompt != nil && d.m.prompt.title == "Open link" {
		d.keys(k)
	}
}

func TestOpenSecretLink(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("1", "j") // start elsewhere: platform-dev
	d.open("lazysecrets://azure/" + kv + "/secrets/api-key")
	if src, c, s := d.selected(); src != "platform-prod" || c != "kv-app-prod" || s != "api-key" {
		t.Fatalf("opened %s/%s/%s", src, c, s)
	}
	// Opened: on its details, as enter on it would; its value hidden.
	if d.m.focus != paneDetail || d.m.detail.State() != panes.ValueHidden {
		t.Errorf("focus %d, value state %d", d.m.focus, d.m.detail.State())
	}
}

func TestOpenAcceptsOtherFormats(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"https://" + kv + "/secrets/api-key",
		"@Microsoft.KeyVault(SecretUri=https://" + kv + "/secrets/api-key)",
		"@Microsoft.KeyVault(VaultName=kv-app-prod;SecretName=api-key)",
	} {
		d := newDriver(t)
		d.open(in)
		if _, _, s := d.selected(); s != "api-key" {
			t.Errorf("%s: selected %q", in, s)
		}
	}
}

func TestOpenVaultLinkOnTab(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("1", "j")
	d.open("lazysecrets://azure/" + kv + "/certificates")
	if _, c, s := d.selected(); c != "kv-app-prod" || d.m.objType != provider.TypeCertificate || s != "app-tls" {
		t.Errorf("vault link: %q tab %s selected %q", c, d.m.objType, s)
	}
	if d.m.focus != paneContainers {
		t.Errorf("focus = %d, want the vaults pane", d.m.focus)
	}
}

func TestOpenVersionLink(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.open("lazysecrets://azure/" + kv + "/secrets/db-password/91d3e0")
	if d.m.versionsOf == nil {
		t.Fatal("versions view not opened")
	}
	if o, _ := d.m.secrets.Selected(); o.Version != "91d3e0" {
		t.Errorf("selected version %q", o.Version)
	}
}

func TestOpeningAsksWhatToDo(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.send(tea.PasteMsg{Content: "lazysecrets://azure/" + kv + "/secrets/db-password"})
	screen := d.screen()
	for _, want := range []string{"Open link", "platform-prod / kv-app-prod / db-password", "o  Go to it",
		"c  Go to it and copy its value", "r  Go to it and show its value", "x  Dismiss"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	if d.clip().content != "" {
		t.Fatal("did something before being asked")
	}
	d.keys("c")
	if d.clip().content != "correct-horse-battery-staple" || strings.Contains(d.screen(), "correct-horse") {
		t.Errorf("copy: clipboard %q, revealed %v", d.clip().content, strings.Contains(d.screen(), "correct-horse"))
	}

	d2 := newDriver(t)
	d2.keys("1", "j")
	d2.openWith("lazysecrets://azure/"+kv+"/secrets/api-key", "r")
	if _, _, s := d2.selected(); s != "api-key" || !strings.Contains(d2.screen(), "sk_live_51HxDEMO") {
		t.Errorf("show: selected %q\n%s", s, d2.screen())
	}

	// Old links carrying ?action= still open, and still ask.
	d3 := newDriver(t)
	d3.send(tea.PasteMsg{Content: "lazysecrets://azure/" + kv + "/secrets/db-password?action=copy"})
	if d3.m.prompt == nil || d3.clip().content != "" {
		t.Error("an old ?action=copy link acted without asking")
	}
}

func TestOpenDatabricksLinks(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.open("lazysecrets://databricks/adb-2222222222222222.2.azuredatabricks.net/app/storage-key")
	if src, c, s := d.selected(); src != "dev" || c != "app" || s != "storage-key" {
		t.Errorf("databricks link: %s/%s/%s", src, c, s)
	}

	// A bare {{secrets/…}} reference: the app scope is in both workspaces.
	d.open("{{secrets/app/storage-key}}")
	screen := d.screen()
	if d.m.prompt == nil || !strings.Contains(screen, "1  Open in prod") || !strings.Contains(screen, "2  Open in dev") ||
		!strings.Contains(screen, "x  Dismiss") {
		t.Fatalf("no choice:\n%s", screen)
	}
	d.keys("1")
	if src, _, s := d.selected(); src != "prod" || s != "storage-key" {
		t.Errorf("after choosing prod: %s/%s", src, s)
	}
}

// unlocatable hides Locate, like an account that can read a vault's
// secrets but not see the vault resource.
type unlocatable struct{ provider.Provider }

func TestOpenUnlocatableVaultUsesOpenedLinks(t *testing.T) {
	t.Parallel()
	d := newDriver(t, unlocatable{fake.Azure(0)}, fake.Databricks(0))
	d.open("lazysecrets://azure/" + kv + "/secrets/api-key")
	if !strings.Contains(d.screen(), "↗ Opened links (1)") {
		t.Fatalf("no Opened links row:\n%s", d.screen())
	}
	if _, c, s := d.selected(); c != "kv-app-prod" || s != "api-key" {
		t.Errorf("opened %s/%s", c, s)
	}
	if c, _ := d.m.containers.Selected(); c.Source.Provider == "" {
		t.Error("the account that could read it wasn't recorded")
	}
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.open("hello")
	if !strings.Contains(d.screen(), "open: not a lazysecrets link") {
		t.Errorf("bad paste:\n%s", d.screen())
	}
	d.open("lazysecrets://azure/" + kv + "/secrets/nope")
	if !strings.Contains(d.screen(), "open: nope not found in kv-app-prod") {
		t.Errorf("missing secret:\n%s", d.screen())
	}
	d.open("lazysecrets://databricks/adb-9.9.azuredatabricks.net/app")
	if !strings.Contains(d.screen(), "no Databricks profile for workspace adb-9.9") {
		t.Errorf("unknown workspace:\n%s", d.screen())
	}
}

func TestPasteWhileFilteringFilters(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "/")
	d.send(tea.PasteMsg{Content: "api-k"})
	if d.m.secrets.Len() != 1 || d.m.nav != nil {
		t.Errorf("paste in filter: visible %d", d.m.secrets.Len())
	}
}

func TestOpenPromptAndStartupOpen(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.send(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	d.send(tea.PasteMsg{Content: "lazysecrets://azure/" + kv + "/secrets/api-key"})
	d.keys("enter", "o")
	if _, _, s := d.selected(); s != "api-key" {
		t.Errorf("ctrl+o: selected %q", s)
	}

	r, _ := ref.Parse("lazysecrets://azure/" + kv + "/secrets/api-key")
	d2 := newDriverOpts(t, Options{Open: &r})
	if d2.m.prompt == nil || d2.m.prompt.title != "Open link" {
		t.Fatal("open at startup didn't ask what to do")
	}
	d2.keys("o")
	if _, _, s := d2.selected(); s != "api-key" || d2.m.focus != paneDetail {
		t.Errorf("open at startup: selected %q, focus %v, want the details", s, d2.m.focus)
	}

	// --action: no question, it does that.
	d3 := newDriverOpts(t, Options{Open: &r, OpenAction: "go"})
	if d3.m.prompt != nil {
		t.Fatalf("--action go asked: %q", d3.m.prompt.title)
	}
	if _, _, s := d3.selected(); s != "api-key" || d3.m.focus != paneSecrets {
		t.Errorf("--action go: selected %q, focus %v, want the secrets list, like Go to", s, d3.m.focus)
	}
	d4 := newDriverOpts(t, Options{Open: &r, OpenAction: "copy"})
	if _, _, s := d4.selected(); s != "api-key" || d4.clip().content == "" {
		t.Errorf("--action copy: selected %q, copied %q", s, d4.clip().content)
	}
}

func TestShareLinks(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "s") // s: the plain link, straight away
	if want := "lazysecrets://azure/" + kv + "/secrets/db-password"; d.clip().content != want {
		t.Errorf("s = %q", d.clip().content)
	}
	if strings.Contains(d.screen(), "copies its value") {
		t.Error("share menu still offers action links")
	}
	for k, want := range map[string]string{
		"o": "lazysecrets://azure/" + kv + "/secrets/db-password",
		"u": "https://" + kv + "/secrets/db-password",
		"y": "@Microsoft.KeyVault(SecretUri=https://" + kv + "/secrets/db-password)",
	} {
		d.keys("S", k)
		if d.clip().content != want {
			t.Errorf("S %s = %q, want %q", k, d.clip().content, want)
		}
	}
	d.keys("2", "S", "u") // vault menu: plain vault URL
	if want := "https://" + kv + "/"; d.clip().content != want {
		t.Errorf("vault URL = %q", d.clip().content)
	}
	d.keys("]", "S", "t") // link that opens on the Keys tab
	if want := "lazysecrets://azure/" + kv + "/keys"; d.clip().content != want {
		t.Errorf("tab link = %q", d.clip().content)
	}
	d.keys("[", "S", "b")
	if !strings.HasPrefix(d.clip().content, "https://portal.azure.com/") {
		t.Errorf("portal link = %q", d.clip().content)
	}
	d.keys("2", "s")
	if want := "lazysecrets://azure/" + kv; d.clip().content != want {
		t.Errorf("vault link = %q", d.clip().content)
	}
	d.keys("3", "v", "j", "s") // from the versions view: that version
	if want := "lazysecrets://azure/" + kv + "/secrets/db-password/91d3e0"; d.clip().content != want {
		t.Errorf("version link = %q", d.clip().content)
	}
	d.keys("esc", "1", "j", "j", "j", "3", "S")
	if strings.Contains(d.screen(), "Key Vault URL") {
		t.Error("Databricks offered a Key Vault URL")
	}
	d.keys("o")
	if want := "lazysecrets://databricks/adb-1111111111111111.11.azuredatabricks.net/app/storage-key"; d.clip().content != want {
		t.Errorf("databricks link = %q", d.clip().content)
	}

	// Every shared link opens what was shared.
	d2 := newDriver(t)
	d2.open(d.clip().content)
	if src, c, s := d2.selected(); src != "prod" || c != "app" || s != "storage-key" {
		t.Errorf("round trip: %s/%s/%s", src, c, s)
	}
}

// blind sees vaults (Locate) but may not read them, like an account with
// Reader on the subscription but no Key Vault data-plane role.
type blind struct{ *fake.Provider }

func (blind) Objects(context.Context, provider.Container, provider.ObjectType) ([]provider.Object, error) {
	return nil, fake.ErrForbidden
}

func TestOpenAsksWhichAccount(t *testing.T) {
	t.Parallel()
	// IDs like the real ones: they contain a "/".
	alice := blind{fake.Azure(0).As("Azure:alice@example.com/t1", "Azure · alice")}
	bob := fake.Azure(0).As("Azure:bob@example.com/t1", "Azure · bob")
	d := newDriver(t, alice, bob)
	d.open("lazysecrets://azure/" + kv + "/secrets/api-key")
	screen := d.screen()
	for _, want := range []string{"Several accounts can see kv-app-prod",
		"1  Open with Azure · alice / platform-prod", "2  Open with Azure · bob / platform-prod", "x  Dismiss"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	d.keys("2")
	c, _ := d.m.containers.Selected()
	if _, _, s := d.selected(); s != "api-key" || c.Source.Provider != "Azure:bob@example.com/t1" {
		t.Errorf("opened %q via %q, want api-key via bob", s, c.Source.Provider)
	}

	// j/k and enter work too; x dismisses.
	d.open("lazysecrets://azure/" + kv + "/secrets/api-key")
	d.keys("x")
	if d.m.prompt != nil || d.m.nav != nil {
		t.Error("x didn't dismiss")
	}
	d.open("lazysecrets://azure/" + kv + "/secrets/legacy-token")
	d.keys("j", "enter")
	if c, _ := d.m.containers.Selected(); c.Source.Provider != "Azure:bob@example.com/t1" {
		t.Errorf("j enter picked %q", c.Source.Provider)
	}

	// lazysecrets open --account: no question, that account.
	r, _ := ref.Parse("lazysecrets://azure/" + kv + "/secrets/api-key")
	r.Account = "Azure:bob@example.com/t1"
	d.send(OpenLink(r))
	if d.m.prompt != nil && d.m.prompt.title == "Open link" {
		d.keys("o")
	}
	if strings.Contains(d.screen(), "Several accounts can see") {
		t.Fatalf("asked, with an account given:\n%s", d.screen())
	}
	if c, _ := d.m.containers.Selected(); c.Source.Provider != r.Account {
		t.Errorf("--account opened via %q", c.Source.Provider)
	}

	// Opening into a folded account unfolds it.
	d.keys("1", "g") // alice's header
	d.keys("enter")  // fold it
	d.open("lazysecrets://azure/" + kv + "/secrets/api-key")
	d.keys("1")
	if c, _ := d.m.containers.Selected(); c.Source.Provider != "Azure:alice@example.com/t1" {
		t.Errorf("folded account not opened: %q", c.Source.Provider)
	}
}

// offered reports whether a "Link on clipboard" popup is showing.
func (d *driver) offered() bool {
	return d.m.prompt != nil && strings.HasPrefix(d.m.prompt.title, "Link on clipboard")
}

func TestLinkOnClipboardPopsUp(t *testing.T) {
	t.Parallel()
	link := "lazysecrets://azure/" + kv + "/secrets/api-key"
	d := newDriverOpts(t, Options{Clipboard: &fakeClipboard{content: link}})
	screen := d.screen()
	for _, want := range []string{"Link on clipboard", "platform-prod / kv-app-prod / api-key", "o  Go to it", "c  Go to it and copy its value", "x  Dismiss"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("popup missing %q:\n%s", want, screen)
		}
	}
	d.keys("o")
	if _, _, s := d.selected(); s != "api-key" || d.offered() {
		t.Errorf("o opened %q, popup still shown: %v", s, d.offered())
	}
	d.send(tea.FocusMsg{}) // same link: offered once only
	if d.offered() {
		t.Error("offered again after opening")
	}
}

func TestClipboardLinkOfferedOnce(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	if d.offered() {
		t.Fatal("empty clipboard offered a link")
	}
	d.clip().content = "lazysecrets://azure/" + kv + "/secrets/api-key" // copied in another app
	d.send(tea.FocusMsg{})
	if !d.offered() {
		t.Fatal("not offered on focus")
	}
	d.keys("esc")
	d.clip().content = "something else"
	d.send(tea.FocusMsg{})
	d.clip().content = "lazysecrets://azure/" + kv + "/secrets/api-key" // copied again later
	d.send(tea.FocusMsg{})
	if d.offered() {
		t.Error("link offered a second time")
	}
	d.clip().content = "lazysecrets://azure/" + kv + "/secrets/legacy-token" // a different one
	d.send(tea.FocusMsg{})
	if !d.offered() {
		t.Error("new link not offered")
	}
}

func TestClipboardIgnoresOtherText(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{Clipboard: &fakeClipboard{content: "hunter2"}})
	if d.offered() || d.m.dismissedLink != "" {
		t.Error("non-link clipboard text used")
	}
	// A Key Vault URL isn't offered: only lazysecrets:// links are.
	d.clip().content = "https://" + kv + "/secrets/api-key"
	d.send(tea.FocusMsg{})
	if d.offered() {
		t.Error("Key Vault URL offered")
	}
}

func TestLinkToSelectionNotOffered(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "s") // share db-password: it's what's selected
	d.send(tea.FocusMsg{})
	if d.offered() {
		t.Fatal("offered a link to the selected secret")
	}
	d.keys("1", "j") // elsewhere now: the same link is worth offering
	d.send(tea.FocusMsg{})
	if !d.offered() || !strings.Contains(d.screen(), "platform-prod / kv-app-prod / db-password") {
		t.Errorf("own link not offered after moving away:\n%s", d.screen())
	}
}

func TestNoClipboardPopupOverAnotherPopup(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "S") // share menu open
	d.clip().content = "lazysecrets://azure/" + kv + "/secrets/api-key"
	d.send(tea.FocusMsg{})
	if d.offered() || !strings.HasPrefix(d.m.prompt.title, "Share") {
		t.Error("clipboard popup replaced the share menu")
	}
	d.keys("esc")
	d.send(tea.FocusMsg{}) // offered once the other popup is gone
	if !d.offered() {
		t.Error("link not offered after the share menu closed")
	}
}

func TestInvalidClipboardLinkIsReported(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{Clipboard: &fakeClipboard{content: "lazysecrets://azure/" + kv + "/secret/api-key"}})
	if !strings.Contains(d.screen(), "Link on clipboard can't be read") || !strings.Contains(d.screen(), `unknown object type "secret"`) {
		t.Fatalf("typo not reported:\n%s", d.screen())
	}
	d.keys("x")
	if d.m.prompt != nil {
		t.Error("x didn't dismiss the report")
	}
}

func TestOpenPromptPrefilledFromClipboard(t *testing.T) {
	t.Parallel()
	link := "lazysecrets://azure/" + kv + "/secrets/api-key"
	d := newDriverOpts(t, Options{Clipboard: &fakeClipboard{content: link}})
	d.keys("x") // the clipboard popup comes first
	d.send(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	d.keys("enter", "o")
	if _, _, s := d.selected(); s != "api-key" {
		t.Errorf("prefilled ctrl+o opened %q", s)
	}
}

func TestOpenPromptReadsClipboardFresh(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	// Copied in another app, no focus event: not offered, but ctrl+o
	// still picks it up, and so it does for a plain Key Vault URL.
	d.clip().content = "https://" + kv + "/secrets/api-key"
	d.send(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	d.keys("enter", "o")
	if _, _, s := d.selected(); s != "api-key" {
		t.Errorf("ctrl+o didn't use the clipboard: %q", s)
	}

	// Not prefilled with text that isn't a reference.
	d.clip().content = "hunter2"
	d.send(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if v := d.m.prompt.input.Value(); v != "" {
		t.Errorf("prefilled with %q", v)
	}
}

func TestClipboardCheckedOnKeysWithoutFocusEvents(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	clock := time.Now()
	d.m.now = func() time.Time { return clock }
	d.clip().content = "lazysecrets://azure/" + kv + "/secrets/api-key"

	clock = clock.Add(clipCheckEvery + time.Second)
	d.keys("j") // any key, once the throttle allows
	if !d.offered() {
		t.Fatal("not offered after a key press")
	}
	d.keys("x")
	d.clip().content = "lazysecrets://azure/" + kv + "/secrets/legacy-token"
	d.keys("k") // within the throttle window: not checked yet
	if d.offered() {
		t.Error("checked again within the throttle window")
	}
	clock = clock.Add(clipCheckEvery)
	d.keys("j")
	if !d.offered() {
		t.Error("new link not offered after the window")
	}
}

func TestOpenSourceLinks(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.send(tea.PasteMsg{Content: "lazysecrets://azure/subscriptions/sub-dev"})
	if src, _, _ := d.selected(); src != "platform-dev" || d.m.focus != paneContainers {
		t.Errorf("subscription link: %q, focus %d", src, d.m.focus)
	}
	d.send(tea.PasteMsg{Content: "lazysecrets://databricks/adb-2222222222222222.2.azuredatabricks.net"})
	if src, _, _ := d.selected(); src != "dev" {
		t.Errorf("workspace link: %q", src)
	}

	two := newDriver(t, fake.Azure(0).As("Azure:alice", "Azure · alice"), fake.Azure(0).As("Azure:bob", "Azure · bob"))
	two.send(tea.PasteMsg{Content: "lazysecrets://azure/subscriptions/sub-dev"})
	if !strings.Contains(two.screen(), "2  Open with Azure · bob / platform-dev") {
		t.Fatalf("no account choice:\n%s", two.screen())
	}
	two.keys("2")
	if r, _ := two.m.sources.Selected(); r.provider != "Azure:bob" || r.source.Name != "platform-dev" {
		t.Errorf("chose %s/%s", r.provider, r.source.Name)
	}
}

func TestHandedOverLinkAndActivity(t *testing.T) {
	t.Parallel()
	touched := 0
	d := newDriverOpts(t, Options{OnActive: func() { touched++ }})
	d.keys("j")
	d.send(tea.FocusMsg{})
	if touched != 2 {
		t.Errorf("OnActive called %d times, want 2", touched)
	}
	r, _ := ref.Parse("lazysecrets://azure/kv-shared-prod.vault.azure.net/secrets/smtp-password")
	d.send(OpenLink(r))
	if d.m.prompt == nil || !strings.Contains(d.screen(), "Open link") {
		t.Fatalf("handed-over link not offered:\n%s", d.screen())
	}
	d.keys("o")
	if _, c, s := d.selected(); c != "kv-shared-prod" || s != "smtp-password" {
		t.Errorf("went to %s/%s", c, s)
	}
}

func TestRefLabel(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	for link, want := range map[string]string{
		// The subscription from lists already loaded.
		"lazysecrets://azure/kv-app-prod.vault.azure.net/keys/signing/abc123": "platform-prod / kv-app-prod / signing · key · version abc123",
		"lazysecrets://azure/kv-app-prod.vault.azure.net/certificates":        "platform-prod / kv-app-prod · certificates tab",
		// Not loaded: the vault alone.
		"lazysecrets://azure/kv-elsewhere.vault.azure.net/secrets/x": "kv-elsewhere / x",
	} {
		r, err := ref.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if got := ansi.Strip(d.m.refLabel(r)); !strings.HasSuffix(got, want) {
			t.Errorf("%s: %q, want …%q", link, got, want)
		}
	}
}

func TestCopyFromLinkStaysPut(t *testing.T) {
	t.Parallel()
	alice := &counting{Provider: fake.Azure(0).As("Azure:alice", "Azure · alice")}
	bob := &counting{Provider: fake.Azure(0).As("Azure:bob", "Azure · bob")}
	d := newDriverOpts(t, Options{}, denyingValues{alice}, bob, fake.Databricks(0))
	d.keys("j") // platform-dev
	before := d.m.sources.SelectedID()

	r, _ := ref.Parse("lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password")
	d.send(OpenLink(r))
	d.keys("y")
	if got := d.clip().content; got != "correct-horse-battery-staple" {
		t.Fatalf("clipboard = %q (alice denies, bob reads)\n%s", got, d.screen())
	}
	if d.m.sources.SelectedID() != before || d.m.nav != nil || strings.Contains(d.screen(), "correct-horse") {
		t.Errorf("moved or revealed:\n%s", d.screen())
	}

	// A version, and a Databricks secret.
	r, _ = ref.Parse("lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password/91d3e0")
	d.send(OpenLink(r))
	d.keys("y")
	if got := d.clip().content; got != "previous-password" {
		t.Errorf("version: clipboard = %q", got)
	}
	r, _ = ref.Parse("lazysecrets://databricks/adb-1111111111111111.11.azuredatabricks.net/app/storage-key")
	d.send(OpenLink(r))
	d.keys("y")
	if got := d.clip().content; got == "" || got == "previous-password" {
		t.Errorf("databricks: clipboard = %q\n%s", got, d.screen())
	}
}

// denyingValues refuses to read values, like an account without access.
type denyingValues struct{ *counting }

func (d denyingValues) Value(context.Context, provider.Object) (provider.SecretValue, error) {
	return provider.SecretValue{}, fake.ErrForbidden
}

func TestShareWebPages(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("S") // a subscription
	if !strings.Contains(d.screen(), "o  Lazysecrets link") || !strings.Contains(d.screen(), "b  Web page (Azure portal)") {
		t.Fatalf("subscription share menu:\n%s", d.screen())
	}
	d.keys("o")
	if got := d.clip().content; got != "lazysecrets://azure/subscriptions/sub-prod" {
		t.Errorf("subscription link %q", got)
	}
	d.keys("3", "S", "b") // a secret's web page
	if got := d.clip().content; !strings.HasPrefix(got, "https://portal.azure.com/") {
		t.Errorf("secret web page %q", got)
	}
}

// A link whose lists are there already (a repo's files, or cached) gets
// to the secret in one update: the details describe it all the same, not
// the repo, with no need to press 3.
func TestOpenLinkToRepoSecret(t *testing.T) {
	t.Parallel()
	r, _ := ref.Parse("lazysecrets://repo/github.com%2Facme%2Forders-service/docker-compose.yml/api.DB_PASSWORD")
	d := newDriverOpts(t, Options{Open: &r}, fake.Repos())
	d.keys("o")
	o, ok := d.m.detail.Secret()
	if _, _, s := d.selected(); s != "api.DB_PASSWORD" || d.m.focus != paneDetail || d.m.detailOf() != paneSecrets || !ok || o.Name != "api.DB_PASSWORD" {
		t.Errorf("selected %q, focus %v describing %v, detail %q", s, d.m.focus, d.m.detailOf(), o.Name)
	}
}
