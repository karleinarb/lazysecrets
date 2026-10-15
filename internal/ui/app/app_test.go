package app

import (
	"cmp"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"git.kebcloud.no/keb/lazysecrets/internal/browser"
	"git.kebcloud.no/keb/lazysecrets/internal/clipboard"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/keys"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

func TestMain(m *testing.M) {
	time.Local = time.UTC // golden output formats times in local time
	os.Exit(m.Run())
}

// driver runs a Model synchronously: every command is executed and its
// message fed back until the program is idle. Commands that don't return
// promptly (spinner and cursor-blink ticks) are abandoned.
type driver struct {
	t    *testing.T
	m    Model
	wait time.Duration // how long a command may take before it's dropped (default 50ms)
}

func newDriver(t *testing.T, providers ...provider.Provider) *driver {
	t.Helper()
	if len(providers) == 0 {
		providers = []provider.Provider{fake.Azure(0), fake.Databricks(0)}
	}
	return newDriverOpts(t, Options{}, providers...)
}

func newDriverOpts(t *testing.T, opts Options, providers ...provider.Provider) *driver {
	t.Helper()
	if len(providers) == 0 {
		providers = []provider.Provider{fake.Azure(0), fake.Databricks(0)}
	}
	if opts.Clipboard == nil {
		opts.Clipboard = &fakeClipboard{}
	}
	if opts.OpenURL == nil { // never launch a real browser from tests
		opts.OpenURL = func(string) error { return nil }
	}
	m := New(providers, opts)
	m.detail.Now = func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	d := &driver{t: t, m: m}
	d.send(tea.WindowSizeMsg{Width: 100, Height: 33}) // 30 for the main panes, 3 for status
	d.run(m.Init())
	return d
}

// fakeClipboard records writes instead of touching the real clipboard.
type fakeClipboard struct {
	content string
	cleared [][32]byte
}

func (c *fakeClipboard) Write(s string) tea.Cmd {
	c.content = s
	return nil
}

func (c *fakeClipboard) Read() (string, error) { return c.content, nil }

func (c *fakeClipboard) Clear(fp [32]byte) tea.Cmd {
	c.cleared = append(c.cleared, fp)
	if clipboard.Fingerprint(c.content) == fp {
		c.content = ""
	}
	return nil
}

func (d *driver) clip() *fakeClipboard { return d.m.clip.(*fakeClipboard) }

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	next, cmd := d.m.Update(msg)
	d.m = next.(Model)
	d.run(cmd)
}

func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(cmp.Or(d.wait, 50*time.Millisecond)):
		return
	}
	switch msg := msg.(type) {
	case nil, spinner.TickMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			d.run(c)
		}
	default:
		d.send(msg)
	}
}

func (d *driver) keys(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		d.send(keyPress(k))
	}
}

func (d *driver) typeText(s string) {
	d.t.Helper()
	for _, r := range s {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (d *driver) screen() string {
	return ansi.Strip(d.m.View().Content)
}

// sourceCount counts source rows, leaving out group headers.
func (d *driver) sourceCount() int {
	n := 0
	for _, p := range d.m.providers {
		if !d.m.collapsed[p.ID()] {
			n += len(d.m.sourceResults[p.ID()].sources)
		}
	}
	return n
}

func (d *driver) selected() (src, container, secret string) {
	s, _ := d.m.selectedSource()
	c, _ := d.m.containers.Selected()
	x, _ := d.m.secrets.Selected()
	return s.Name, c.Name, x.Name
}

func TestStartupCascadesToFirstSecret(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	src, c, s := d.selected()
	if src != "platform-prod" || c != "kv-app-prod" || s != "db-password" {
		t.Fatalf("selected = %q/%q/%q", src, c, s)
	}
	if got := d.sourceCount(); got != 4 {
		t.Errorf("sources = %d, want 4 (2 Azure + 2 Databricks)", got)
	}
	if d.m.anyLoading() {
		t.Error("still loading after idle")
	}
}

func TestGolden(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	golden.RequireEqual(t, d.screen())
}

func TestIcons(t *testing.T) {
	t.Parallel()
	const azure, dbx = "\U000F0805", "\U000F0F58"
	plain := newDriver(t).screen()
	if strings.Contains(plain, azure) || strings.Contains(plain, dbx) {
		t.Error("icons shown without the option")
	}
	d := newDriverOpts(t, Options{Icons: true})
	screen := d.screen()
	if !strings.Contains(screen, azure+" Azure") || !strings.Contains(screen, dbx+" Databricks") {
		t.Errorf("icons missing:\n%s", screen)
	}
	d.keys("3")
	if !strings.Contains(d.screen(), "source     "+azure+" Azure / platform-prod") {
		t.Errorf("detail source line missing icon:\n%s", d.screen())
	}
}

func TestObjectTypeTabs(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	if got := d.m.secrets.Tabs; strings.Join(got, ",") != "Secrets,Keys,Certs" {
		t.Fatalf("tabs = %v", got)
	}
	d.keys("3", "]")
	if d.m.objType != provider.TypeKey {
		t.Fatalf("objType = %s after ]", d.m.objType)
	}
	if _, _, s := d.selected(); s != "signing-key" {
		t.Fatalf("selected = %q", s)
	}
	screen := d.screen()
	for _, want := range []string{"key type   RSA 2048", "ops        sign, verify", "space to show the public key"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q:\n%s", want, screen)
		}
	}
	d.keys("space")
	if !strings.Contains(d.screen(), "BEGIN PUBLIC KEY") {
		t.Errorf("public key not shown:\n%s", d.screen())
	}

	d.keys("]") // certificates
	d.keys("3", "j")
	if _, _, s := d.selected(); s != "old-tls" {
		t.Fatalf("selected = %q", s)
	}
	if !strings.Contains(d.screen(), "(expired)") {
		t.Errorf("expired certificate not flagged:\n%s", d.screen())
	}
	d.keys("]") // wraps to secrets
	if d.m.objType != provider.TypeSecret {
		t.Fatalf("objType = %s, want wrap to Secret", d.m.objType)
	}
	d.keys("[")
	if d.m.objType != provider.TypeCertificate {
		t.Fatalf("objType = %s, want wrap back to Certificate", d.m.objType)
	}
}

func TestTabErrorsArePerType(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "j") // kv-shared-prod: secrets readable, keys forbidden
	if d.m.secrets.Err != nil {
		t.Fatalf("secrets tab errored: %v", d.m.secrets.Err)
	}
	d.keys("]")
	if d.m.secrets.Err == nil || !strings.Contains(d.screen(), "list keys") {
		t.Fatalf("keys tab should show permission error:\n%s", d.screen())
	}
	d.keys("]")
	if d.m.secrets.Err != nil || !strings.Contains(d.screen(), "no certificates") {
		t.Fatalf("certs tab should be empty without error:\n%s", d.screen())
	}
}

func TestTabPreferenceSurvivesDatabricks(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "]")           // Keys
	d.keys("1", "j", "j", "j") // Databricks prod: secrets only
	if d.m.objType != provider.TypeSecret || len(d.m.secrets.Tabs) != 1 {
		t.Fatalf("Databricks: objType = %s, tabs = %v", d.m.objType, d.m.secrets.Tabs)
	}
	if !strings.Contains(d.screen(), "[3] Secrets") {
		t.Errorf("single-type title missing:\n%s", d.screen())
	}
	d.keys("3", "]", "1") // no-op without tabs
	d.keys("k", "k", "k") // back to Azure prod
	if d.m.objType != provider.TypeKey {
		t.Fatalf("objType = %s, want Keys restored", d.m.objType)
	}
}

func TestCopyWithoutRevealing(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{ClipboardClear: 30 * time.Second})
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	d.m.now = func() time.Time { return clock }

	d.keys("y")
	if got := d.clip().content; got != "correct-horse-battery-staple" {
		t.Fatalf("clipboard = %q", got)
	}
	if strings.Contains(d.screen(), "correct-horse-battery-staple") {
		t.Error("copying revealed the value")
	}
	if !strings.Contains(d.screen(), "copied db-password · clipboard clears in 30s") {
		t.Errorf("no countdown:\n%s", d.screen())
	}
	if strings.Contains(d.screen(), "copying") {
		t.Errorf("stale progress message:\n%s", d.screen())
	}

	// The countdown survives key presses and ticks down.
	d.keys("j")
	clock = clock.Add(2500 * time.Millisecond)
	d.send(clipTickMsg{token: d.m.clipToken})
	if !strings.Contains(d.screen(), "clipboard clears in 28s") {
		t.Errorf("countdown not updated:\n%s", d.screen())
	}

	// A newer copy restarts the countdown; ticks for the old one are ignored.
	old := d.m.clipToken
	d.keys("y") // api-key
	clock = clock.Add(time.Minute)
	d.send(clipTickMsg{token: old})
	if d.clip().content == "" {
		t.Fatal("stale tick cleared the clipboard")
	}
	d.send(clipTickMsg{token: d.m.clipToken})
	if d.clip().content != "" {
		t.Errorf("clipboard not cleared: %q", d.clip().content)
	}
	if strings.Contains(d.screen(), "clears in") || !strings.Contains(d.screen(), "clipboard cleared") {
		t.Errorf("countdown not replaced by cleared notice:\n%s", d.screen())
	}
}

func TestCopyReferenceCancelsCountdown(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{ClipboardClear: 30 * time.Second})
	d.keys("y", "Y")
	if strings.Contains(d.screen(), "clears in") {
		t.Errorf("countdown kept after the value was replaced on the clipboard:\n%s", d.screen())
	}
}

func TestCopyRevealedValueAndReference(t *testing.T) {
	t.Parallel()
	d := newDriver(t) // ClipboardClear 0: no timer
	d.keys("space", "y")
	if d.clip().content != "correct-horse-battery-staple" {
		t.Fatalf("clipboard = %q", d.clip().content)
	}
	if strings.Contains(d.screen(), "clears in") {
		t.Error("clear notice shown with clearing disabled")
	}
	d.keys("Y")
	if want := "@Microsoft.KeyVault(SecretUri=https://kv-app-prod.vault.azure.net/secrets/db-password)"; d.clip().content != want {
		t.Errorf("reference = %q", d.clip().content)
	}
}

// expiring fails object listing with an AuthError until signed in.
type expiring struct {
	provider.Provider
	signedIn *bool
}

func (e expiring) Objects(ctx context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	if !*e.signedIn {
		return nil, &provider.AuthError{Kind: provider.KindAzure, Login: []string{"az", "login"}, Err: errors.New("AADSTS700082")}
	}
	return e.Provider.Objects(ctx, c, t)
}

func TestSignInFlow(t *testing.T) {
	t.Parallel()
	signedIn := false
	d := newDriver(t, expiring{fake.Azure(0), &signedIn})
	if d.m.auth == nil {
		t.Fatal("auth error not noticed")
	}
	if !strings.Contains(d.screen(), "press L to run az login") {
		t.Errorf("no sign-in hint:\n%s", d.screen())
	}
	if !d.m.keys.Login.Enabled() {
		t.Error("L not enabled")
	}
	next, cmd := d.m.Update(keyPress("L"))
	if cmd == nil {
		t.Fatal("L did not start az login")
	}
	d.m = next.(Model)

	signedIn = true
	d.send(loginDoneMsg{})
	if d.m.auth != nil || d.m.keys.Login.Enabled() {
		t.Error("auth state not cleared after sign-in")
	}
	if _, _, s := d.selected(); s != "db-password" {
		t.Errorf("objects not reloaded after sign-in, selected %q", s)
	}
}

func TestSignInFailureKeepsHint(t *testing.T) {
	t.Parallel()
	signedIn := false
	d := newDriver(t, expiring{fake.Azure(0), &signedIn})
	d.send(loginDoneMsg{err: errors.New("exit status 1")})
	if d.m.auth == nil || !strings.Contains(d.screen(), "sign-in failed: exit status 1") {
		t.Errorf("failure not reported:\n%s", d.screen())
	}
}

// describing adds an attribute in Describe.
type describing struct{ provider.Provider }

func (describing) Describe(_ context.Context, o provider.Object) (provider.Object, error) {
	o.Attrs = append(o.Attrs, provider.Attr{Label: "described", Value: o.Name})
	return o, nil
}

func TestDescribeUpdatesDetail(t *testing.T) {
	t.Parallel()
	d := newDriver(t, describing{fake.Azure(0)})
	d.keys("3") // the fourth pane follows focus: the secret
	if !strings.Contains(d.screen(), "described  db-password") {
		t.Fatalf("describe result not shown:\n%s", d.screen())
	}
	d.send(describeMsg{req: -1, obj: provider.Object{ID: "x"}})
	d.keys("3", "j")
	if !strings.Contains(d.screen(), "described  api-key") {
		t.Errorf("describe not refreshed on selection change:\n%s", d.screen())
	}
}

func TestOpenInBrowser(t *testing.T) {
	t.Parallel()
	var opened []string
	open := func(url string) error { opened = append(opened, url); return nil }
	d := newDriverOpts(t, Options{OpenURL: open})

	d.keys("o")                // subscription
	d.keys("2", "o")           // vault
	d.keys("3", "]", "o")      // key: its vault's key list
	d.keys("1", "j", "j", "j") // Databricks prod
	d.keys("o")                // workspace
	d.keys("2", "o")           // Databricks-backed scope: nothing to open
	if !strings.Contains(d.screen(), "no web page for app") {
		t.Errorf("missing no-page notice:\n%s", d.screen())
	}
	d.keys("j", "o") // AKV-backed scope: its vault

	want := []string{
		"https://portal.azure.com/#resource/subscriptions/sub-prod/overview",
		"https://portal.azure.com/#resource/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv-app-prod/overview",
		"https://portal.azure.com/#resource/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv-app-prod/keys",
		"https://adb-1111111111111111.11.azuredatabricks.net",
		"https://portal.azure.com/#resource/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv-app-prod/overview",
	}
	if strings.Join(opened, "\n") != strings.Join(want, "\n") {
		t.Errorf("opened:\n%s\nwant:\n%s", strings.Join(opened, "\n"), strings.Join(want, "\n"))
	}
}

func TestOpenOverSSHCopiesLink(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{OpenURL: func(string) error { return browser.ErrRemote }})
	d.keys("o")
	if d.clip().content != "https://portal.azure.com/#resource/subscriptions/sub-prod/overview" {
		t.Errorf("clipboard = %q", d.clip().content)
	}
	if !strings.Contains(d.screen(), "can't open a browser over SSH") {
		t.Errorf("no explanation:\n%s", d.screen())
	}
}

func TestVersions(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("v")
	if d.m.focus != paneSecrets || !strings.Contains(d.screen(), "[3] Versions of db-password") {
		t.Fatalf("versions view not shown:\n%s", d.screen())
	}
	screen := d.screen()
	for _, want := range []string{"3f9c2a   2026-09-01 current", "91d3e0   2026-03-01 expired", "0a77c4   2025-09-01 disabled"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing row %q:\n%s", want, screen)
		}
	}

	// space reads the selected version, not the latest.
	d.keys("j", "space")
	if !strings.Contains(d.screen(), "previous-password") || strings.Contains(d.screen(), "correct-horse") {
		t.Errorf("wrong version revealed:\n%s", d.screen())
	}

	// esc returns to the list with the cursor on the object.
	d.keys("esc")
	if d.m.versionsOf != nil || !strings.Contains(d.screen(), "[3] Secrets - Keys - Certs") {
		t.Fatalf("versions view not closed:\n%s", d.screen())
	}
	if _, _, s := d.selected(); s != "db-password" {
		t.Errorf("cursor = %q after closing versions", s)
	}
	if strings.Contains(d.screen(), "previous-password") {
		t.Error("old version's value still shown after closing versions")
	}
}

func TestVersionHistoryInDetail(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3") // the fourth pane follows focus: the secret
	screen := d.screen()
	for _, want := range []string{
		"versions   3 · v to browse",
		"   3f9c2a   2026-09-01 current",
		"   91d3e0   2026-03-01 expired",
		"   0a77c4   2025-09-01 disabled",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "▸ 3f9c2a") {
		t.Errorf("a cursor on a list that can't be moved in:\n%s", screen)
	}
	d.keys("3", "j") // api-key: only its current version
	if !strings.Contains(d.screen(), "versions   1") || strings.Contains(d.screen(), "v to browse") {
		t.Errorf("wrong history for api-key:\n%s", d.screen())
	}
	d.keys("1", "j", "j", "j") // Databricks: unsupported, so no section at all
	if strings.Contains(d.screen(), "versions ") {
		t.Errorf("versions shown for Databricks:\n%s", d.screen())
	}
}

func TestVersionCountPrimesVersionsView(t *testing.T) {
	t.Parallel()
	p := &counting{Provider: fake.Azure(0)}
	d := newDriver(t, p)
	before := p.versions
	d.keys("v")
	if p.versions != before {
		t.Errorf("v refetched versions already counted: %d → %d", before, p.versions)
	}
}

func TestBackspaceGoesBack(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("v")
	d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if d.m.versionsOf != nil {
		t.Fatal("backspace did not close the versions view")
	}
	// While typing a filter, backspace deletes a character instead.
	d.keys("/")
	d.typeText("api-x")
	d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !d.m.secrets.Filtering() || d.m.secrets.Len() != 1 {
		t.Errorf("backspace in filter: filtering=%v visible=%d", d.m.secrets.Filtering(), d.m.secrets.Len())
	}
}

func TestVersionsClosedByNavigation(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("v", "]") // switching tab leaves the versions view
	if d.m.versionsOf != nil || d.m.objType != provider.TypeKey {
		t.Errorf("tab switch: versionsOf=%v objType=%s", d.m.versionsOf, d.m.objType)
	}
	d.keys("[", "v", "2", "j") // another vault
	if d.m.versionsOf != nil {
		t.Error("vault switch did not close the versions view")
	}
}

func TestVersionsUnsupported(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("1", "j", "j", "j", "v") // Databricks
	if d.m.versionsOf != nil {
		t.Error("versions view stayed open")
	}
	if !strings.Contains(d.screen(), "versions aren't supported for Databricks") {
		t.Errorf("no notice:\n%s", d.screen())
	}
	if _, _, s := d.selected(); s != "storage-key" {
		t.Errorf("selection lost: %q", s)
	}
}

func TestNavigationReloadsChildren(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j") // platform-dev
	if _, c, s := d.selected(); c != "kv-app-dev" || s != "db-password" {
		t.Fatalf("after j on sources: %q/%q", c, s)
	}
	d.keys("j") // Databricks group header: nothing below it
	if _, c, _ := d.selected(); c != "" || d.m.containers.Title != "[2] Scopes" {
		t.Errorf("header selected but containers shown: %q / %q", c, d.m.containers.Title)
	}
	d.keys("j") // Databricks prod
	if d.m.containers.Title != "[2] Scopes" {
		t.Errorf("containers title = %q", d.m.containers.Title)
	}
	if _, c, s := d.selected(); c != "app" || s != "storage-key" {
		t.Fatalf("after j to Databricks: %q/%q", c, s)
	}
}

func TestPermissionErrorShownInPane(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("2", "G") // kv-restricted
	if d.m.secrets.Err == nil {
		t.Fatal("expected secrets error")
	}
	if !strings.Contains(d.screen(), "403 Forbidden") {
		t.Error("error not rendered")
	}
	if !strings.Contains(d.screen(), "name       kv-restricted") {
		t.Error("detail should show the container summary")
	}
}

func TestRevealAndHideOnSelectionChange(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	const plain = "correct-horse-battery-staple"
	if strings.Contains(d.screen(), plain) {
		t.Fatal("value visible before reveal")
	}
	d.keys("3", "space")
	if !strings.Contains(d.screen(), plain) {
		t.Fatalf("value not revealed:\n%s", d.screen())
	}
	d.keys("space")
	if strings.Contains(d.screen(), plain) {
		t.Fatal("value still visible after hide")
	}
	d.keys("space", "j")
	if strings.Contains(d.screen(), plain) {
		t.Fatal("value still visible after moving to another secret")
	}
	if d.m.detail.State() != 0 {
		t.Error("value state not reset after selection change")
	}
}

func TestRevealFromEveryPane(t *testing.T) {
	t.Parallel()
	const plain = "correct-horse-battery-staple"
	for _, p := range []string{"1", "2", "3", "4"} {
		d := newDriver(t)
		d.keys(p, "space")
		if !strings.Contains(d.screen(), plain) {
			t.Errorf("pane %s: value not revealed", p)
		}
		d.keys("space")
		if strings.Contains(d.screen(), plain) {
			t.Errorf("pane %s: value not hidden", p)
		}
	}
}

func TestRevealOnAKVBackedDatabricksScope(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j", "j", "j", "2", "j") // Databricks prod → kv-app-prod scope
	if _, c, _ := d.selected(); c != "kv-app-prod" {
		t.Fatalf("container = %q", c)
	}
	if !strings.Contains(d.screen(), "Azure Key Vault-backed: kv-app-prod") {
		t.Errorf("scope backing not shown with a secret selected:\n%s", d.screen())
	}
	d.keys("3", "space")
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") {
		t.Fatalf("value not revealed:\n%s", d.screen())
	}
}

func TestFilter(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "/")
	d.typeText("api-k")
	if d.m.secrets.Len() != 1 {
		t.Fatalf("visible = %d, want 1", d.m.secrets.Len())
	}
	d.keys("enter")
	if _, _, s := d.selected(); s != "api-key" {
		t.Fatalf("selected = %q", s)
	}
	// While filtering, q is text rather than quit.
	d.keys("/")
	_, cmd := d.m.Update(keyPress("q"))
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit while filtering")
		}
	}
	d.keys("esc") // leave filter mode, clearing the query
	d.keys("/")
	d.typeText("api-k")
	d.keys("enter", "esc") // apply, then esc clears the applied filter
	if d.m.secrets.Len() != 4 {
		t.Errorf("esc should clear the applied filter, visible = %d", d.m.secrets.Len())
	}
	if _, _, s := d.selected(); s != "api-key" {
		t.Errorf("clearing the filter should keep the selection, got %q", s)
	}
}

func TestMoveWhileFiltering(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "/")
	d.typeText("a") // all four secrets contain an a
	if d.m.secrets.Len() != 4 {
		t.Fatalf("visible = %d", d.m.secrets.Len())
	}
	_, _, initial := d.selected() // kept from before filtering: the middle row
	var seen []string
	for _, k := range []rune{'j', 'j', 'j', 'k'} {
		d.send(tea.KeyPressMsg{Code: k, Mod: tea.ModCtrl})
		_, _, s := d.selected()
		seen = append(seen, s)
	}
	if !d.m.secrets.Filtering() {
		t.Fatal("ctrl+j/k left filter mode")
	}
	// down to the bottom, stay there, back up one
	if seen[0] == initial || seen[2] != seen[1] || seen[3] != seen[0] {
		t.Errorf("from %q, ctrl+j ×3, ctrl+k selected %v", initial, seen)
	}
	if !strings.Contains(d.screen(), "ctrl+j/n down") {
		t.Errorf("filter help missing ctrl+j:\n%s", d.screen())
	}
}

func TestStaleResponsesDropped(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	before := d.m.containers.Len()
	d.send(containersMsg{req: -1, items: nil})
	if d.m.containers.Len() != before {
		t.Error("stale containersMsg was applied")
	}
	d.send(valueMsg{req: -1, value: provider.NewSecretValue([]byte("stale"))})
	if strings.Contains(d.screen(), "stale") {
		t.Error("stale valueMsg was applied")
	}
}

type failingSources struct{ provider.Provider }

func (failingSources) Sources(context.Context) ([]provider.Source, error) {
	return nil, errors.New("run az login")
}

func TestProviderErrorDoesNotHideOthers(t *testing.T) {
	t.Parallel()
	d := newDriver(t, failingSources{fake.Azure(0)}, fake.Databricks(0))
	if d.sourceCount() != 2 {
		t.Fatalf("sources = %d, want the 2 Databricks ones", d.sourceCount())
	}
	if !strings.Contains(d.screen(), "Azure: run az login") {
		t.Errorf("provider error not in status line:\n%s", d.screen())
	}
}

func TestFocusKeys(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	for _, tc := range []struct {
		key  string
		want pane
	}{
		{"tab", paneContainers}, {"enter", paneSecrets}, {"enter", paneDetail},
		{"enter", paneDetail}, {"esc", paneSecrets}, {"shift+tab", paneContainers},
		{"4", paneDetail}, {"tab", paneSources}, {"shift+tab", paneDetail},
		// l: what's selected, on the right; h: back to it.
		{"2", paneContainers}, {"l", paneDetail}, {"h", paneContainers}, {"h", paneSources}, {"l", paneDetail},
	} {
		d.keys(tc.key)
		if d.m.focus != tc.want {
			t.Fatalf("after %q focus = %d, want %d", tc.key, d.m.focus, tc.want)
		}
	}
}

func TestQuit(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"q", "ctrl+c"} {
		d := newDriver(t)
		_, cmd := d.m.Update(keyPress(k))
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestTooSmall(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.send(tea.WindowSizeMsg{Width: 30, Height: 8})
	if !strings.Contains(d.screen(), "Terminal too small") {
		t.Error("expected too-small message")
	}
}

// leftEdge is the width of the left column: the column of the first
// top-right corner on the first screen line.
func (d *driver) leftEdge() int {
	first := strings.Split(d.screen(), "\n")[0]
	return len([]rune(first[:strings.Index(first, "╮")])) + 1
}

func TestMoveDivider(t *testing.T) {
	t.Parallel()
	d := newDriver(t) // 100 wide: default left column 33
	start := d.leftEdge()
	if start != 33 {
		t.Fatalf("default left width = %d", start)
	}
	d.send(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	d.keys(">")
	if got := d.leftEdge(); got != start+2*splitStep {
		t.Errorf("after ctrl+→ and >: %d, want %d", got, start+2*splitStep)
	}
	d.keys("=")
	if got := d.leftEdge(); got != start {
		t.Errorf("= didn't reset: %d", got)
	}

	// Clamped, and the other direction responds at once after a limit.
	for range 20 {
		d.keys("<")
	}
	if got := d.leftEdge(); got != minLeftW {
		t.Errorf("min width = %d, want %d", got, minLeftW)
	}
	d.keys(">")
	if got := d.leftEdge(); got != minLeftW+splitStep {
		t.Errorf("after limit, > gave %d", got)
	}
	for range 30 {
		d.keys(">")
	}
	if got := d.leftEdge(); got != 100-minDetailW {
		t.Errorf("max width = %d, want %d", got, 100-minDetailW)
	}

	// Focus doesn't matter: it's the divider that moves.
	d.keys("=", "4", ">")
	if got := d.leftEdge(); got != start+splitStep {
		t.Errorf("from the detail pane: %d", got)
	}
}

func TestCopyAndRevealWhileFiltering(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3", "/")
	d.typeText("api-k")
	d.send(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if d.clip().content != "sk_live_51HxDEMO0000000000000000" {
		t.Errorf("ctrl+y copied %q", d.clip().content)
	}
	d.send(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !strings.Contains(d.screen(), "sk_live_51HxDEMO") {
		t.Error("ctrl+r didn't reveal")
	}
	if !d.m.secrets.Filtering() || d.m.secrets.Len() != 1 {
		t.Error("left filter mode or changed the query")
	}
	d.send(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if strings.Contains(d.screen(), "sk_live_51HxDEMO") {
		t.Error("second ctrl+r didn't hide")
	}
}

func TestCtrlNP(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3")
	d.send(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if _, _, s := d.selected(); s != "api-key" {
		t.Errorf("ctrl+n: %q", s)
	}
	d.keys("/")
	d.typeText("e")
	d.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}) // moves, doesn't type
	if !d.m.secrets.Filtering() || d.m.secrets.HasFilter() && strings.Contains(d.screen(), "/ep") {
		t.Error("ctrl+p typed into the filter")
	}
	d.keys("esc", "?")
	before := d.m.prompt.cursor
	d.send(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if d.m.prompt.cursor == before {
		t.Error("ctrl+n didn't move in the menu")
	}
}

func TestRevealedValueHides(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{RevealTimeout: 30 * time.Second, HideOnBlur: true})
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	d.m.now = func() time.Time { return clock }

	d.keys("space")
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") || !strings.Contains(d.screen(), "hides in 30s") {
		t.Fatalf("not revealed with a countdown:\n%s", d.screen())
	}
	clock = clock.Add(12 * time.Second)
	d.send(revealTickMsg{d.m.revealToken})
	if !strings.Contains(d.screen(), "hides in 18s") {
		t.Errorf("countdown:\n%s", d.screen())
	}
	clock = clock.Add(18 * time.Second)
	d.send(revealTickMsg{d.m.revealToken})
	if strings.Contains(d.screen(), "correct-horse") {
		t.Errorf("still shown after the timeout:\n%s", d.screen())
	}

	// Losing focus hides it at once.
	d.keys("space")
	d.send(tea.BlurMsg{})
	if strings.Contains(d.screen(), "correct-horse") {
		t.Errorf("still shown after losing focus:\n%s", d.screen())
	}
	// A tick from the earlier reveal does nothing.
	d.send(revealTickMsg{d.m.revealToken - 1})
}

func TestRevealTimeoutOff(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{})
	d.keys("space")
	d.send(tea.BlurMsg{})
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") || strings.Contains(d.screen(), "hides in") {
		t.Errorf("hidden though both are off:\n%s", d.screen())
	}
}

func TestExpiryMarkers(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{ExpiryWarning: 30 * 24 * time.Hour})
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	d.m.now = func() time.Time { return now }
	o := func(exp time.Time) provider.Object { return provider.Object{Name: "x", Expires: exp} }
	for _, c := range []struct {
		exp  time.Time
		want string
	}{
		{now.Add(-time.Hour), "✗ expired"},
		{now.Add(12*24*time.Hour + time.Hour), "⚠ 12d"},
		{now.Add(5 * time.Hour), "⚠ 5h"},
		{now.Add(60 * 24 * time.Hour), ""},
		{time.Time{}, ""},
	} {
		got := ansi.Strip(d.m.objectRender()(d.m.st, o(c.exp), 40))
		if c.want == "" && strings.ContainsAny(got, "✗⚠") || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("expires %v: %q, want %q", c.exp, got, c.want)
		}
	}
	if w := d.m.expiryWord(o(now.Add(-time.Hour))); w != "expired" {
		t.Errorf("filter word %q", w)
	}
}

func TestSourcesFavouritesTab(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("j", "f") // star platform-dev
	d.keys("]")
	if !d.m.sourcesFavs || !strings.Contains(d.screen(), "Favourites") {
		t.Fatalf("not on the Favourites tab:\n%s", d.screen())
	}
	var names []string
	for _, r := range d.m.sources.Items() {
		names = append(names, r.source.Name)
	}
	if strings.Join(names, ",") != "platform-dev" {
		t.Errorf("favourites tab rows = %v", names)
	}
	if src, _, _ := d.selected(); src != "platform-dev" {
		t.Errorf("cursor on %q, want the starred source", src)
	}
	d.keys("[")
	if d.m.sourcesFavs || len(d.m.sources.Items()) < 4 {
		t.Errorf("back on Sources: favs=%v, %d rows", d.m.sourcesFavs, len(d.m.sources.Items()))
	}
}

func TestRebindingShowsEverywhere(t *testing.T) {
	t.Parallel()
	km := keys.Default()
	if err := km.Apply(map[string]map[string][]string{"lists": {"reveal": {"z"}, "versions": {}}}); err != nil {
		t.Fatal(err)
	}
	d := newDriverOpts(t, Options{Keys: &km})
	d.keys("3")
	scr := d.screen()
	if !strings.Contains(scr, "z to reveal") || strings.Contains(scr, "space to reveal") {
		t.Errorf("detail hint doesn't follow the binding:\n%s", scr)
	}
	if !strings.Contains(lastLines(scr, 1), "z show") {
		t.Errorf("help bar: %q", lastLines(scr, 1))
	}
	d.keys("z")
	if bar := lastLines(d.screen(), 1); !strings.Contains(bar, "z hide") {
		t.Errorf("help bar with the value shown: %q", bar)
	}
	d.keys("z")
	if strings.Contains(scr, "to browse") {
		t.Errorf("hint for unbound versions:\n%s", scr)
	}
	d.keys("space")
	if strings.Contains(d.screen(), "correct-horse") {
		t.Error("space still reveals")
	}
	d.keys("z")
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") {
		t.Errorf("z doesn't reveal:\n%s", d.screen())
	}
}

// failingValue fails to read values with a multi-line error, like sops.
type failingValue struct{ provider.Provider }

const multiLineErr = "sops couldn't decrypt it:\nFailed to get the data key.\n\nGroup 0: FAILED\n  age1abc: FAILED\n    - | no identity matched any of the recipients"

func (failingValue) Value(context.Context, provider.Object) (provider.SecretValue, error) {
	return provider.SecretValue{}, errors.New(multiLineErr)
}

func TestMultiLineErrorsShownWhole(t *testing.T) {
	t.Parallel()
	d := newDriver(t, failingValue{fake.Azure(0)})
	d.keys("3", "space")
	scr := d.screen()
	for _, line := range strings.Split(multiLineErr, "\n") {
		if line != "" && !strings.Contains(scr, line) {
			t.Errorf("detail pane lacks %q:\n%s", line, scr)
		}
	}
	d.keys("y") // copying fails the same way: one line in the footer
	if !strings.Contains(scr, "Group 0") || !strings.Contains(d.screen(), "✗ copy failed: sops couldn't decrypt it: … (the whole message: space on the secret)") {
		t.Errorf("footer:\n%s", d.screen())
	}
}

// signedOut is a second Azure account whose sign-in has expired.
type signedOut struct{ provider.Provider }

func (signedOut) ID() string    { return "Azure:admin@contoso.com/t1" }
func (signedOut) Label() string { return "Azure · admin" }
func (signedOut) Sources(context.Context) ([]provider.Source, error) {
	return nil, &provider.AuthError{Kind: provider.KindAzure, Account: "Azure · admin",
		Login: []string{"az", "login", "--tenant", "t1"}, Err: errors.New("AADSTS70043")}
}

func TestSignInMessageOnceThenInDetails(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{CollapseGroups: false}, fake.Azure(0), signedOut{fake.Azure(0)})
	hint := "Azure · admin: sign-in required"
	if !strings.Contains(lastLines(d.screen(), 2), hint) { // the footer: one line
		t.Fatalf("not said at start:\n%s", d.screen())
	}
	d.keys("j")
	if strings.Contains(lastLines(d.screen(), 2), hint) {
		t.Errorf("still in the footer after moving:\n%s", d.screen())
	}

	// On the account's group, Details says it, and L signs in to it.
	d.keys("G")
	if r, _ := d.m.sources.Selected(); r.provider != "Azure:admin@contoso.com/t1" {
		t.Fatalf("selected %+v", r)
	}
	if scr := d.screen(); !strings.Contains(scr, "problem") || !strings.Contains(scr, hint) || !strings.Contains(scr, "press L to run az login") {
		t.Errorf("not in Details:\n%s", d.screen())
	}
	if a := d.m.loginFor(); a == nil || a.Account != "Azure · admin" {
		t.Errorf("L signs in to %+v", a)
	}

	// Elsewhere Details is as usual.
	d.keys("g")
	if strings.Contains(d.screen(), hint) {
		t.Errorf("shown for another account:\n%s", d.screen())
	}
}

// lastLines is the bottom n lines of a screen.
func lastLines(scr string, n int) string {
	lines := strings.Split(strings.TrimRight(scr, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// described is an account that can say who it is.
type described struct{ provider.Provider }

func (described) Account() (provider.Account, bool) {
	return provider.Account{
		Fields: []provider.Attr{{Label: "name", Value: "Alice Example"}, {Label: "object ID", Value: "oid-123"}},
		Copy:   "oid-123", CopyLabel: "object ID",
	}, true
}

func TestAccountInDetails(t *testing.T) {
	t.Parallel()
	d := newDriver(t, described{fake.Azure(0)})
	d.keys("g") // the group's row
	if r, _ := d.m.sources.Selected(); r.kind != rowGroup {
		t.Fatalf("not on the group: %+v", r)
	}
	if scr := d.screen(); !strings.Contains(scr, "Alice Example") || !strings.Contains(scr, "oid-123") {
		t.Errorf("account not in Details:\n%s", scr)
	}
	d.keys("y")
	if got := d.clip().content; got != "oid-123" {
		t.Errorf("y copied %q", got)
	}
	d.keys("j")
	if strings.Contains(d.screen(), "Alice Example") {
		t.Errorf("still shown on a subscription:\n%s", d.screen())
	}
}

func TestAccountPane(t *testing.T) {
	t.Parallel()
	d := newDriver(t, described{fake.Azure(0)})
	d.keys("g", "l") // the account's row, then its pane
	if d.m.focus != paneDetail {
		t.Fatalf("focus %v, want the fourth pane", d.m.focus)
	}
	scr := d.screen()
	if !strings.Contains(scr, "Alice Example") {
		t.Errorf("account not shown:\n%s", scr)
	}
	bar := lastLines(scr, 1)
	if !strings.Contains(bar, "copy") || strings.Contains(bar, "reveal") || strings.Contains(bar, "versions") {
		t.Errorf("help bar: %q", bar)
	}
	d.keys("j", "y")
	if got := d.clip().content; got != "oid-123" {
		t.Errorf("y on the object ID copied %q", got)
	}

	// / filters it, labels too.
	d.keys("/")
	d.typeText("object")
	d.keys("enter")
	if scr := d.screen(); !strings.Contains(scr, "oid-123") || strings.Contains(scr, "Alice Example") {
		t.Errorf("filtered:\n%s", scr)
	}
	d.keys("esc")
	if !strings.Contains(d.screen(), "Alice Example") {
		t.Errorf("filter not cleared:\n%s", d.screen())
	}

	d.keys("h")
	if d.m.focus != paneSources {
		t.Errorf("back to %v, want Sources", d.m.focus)
	}
}

func TestDetailsFollowFocus(t *testing.T) {
	t.Parallel()
	d := newDriver(t)
	d.keys("3") // a secret
	if !strings.Contains(d.screen(), "version    3f9c2a") {
		t.Fatalf("secret not shown:\n%s", d.screen())
	}
	d.keys("2") // its vault
	if scr := d.screen(); !strings.Contains(scr, "name       kv-app-prod") || strings.Contains(scr, "version    3f9c2a") {
		t.Errorf("vault not described:\n%s", scr)
	}
	d.keys("1") // its subscription
	if scr := d.screen(); !strings.Contains(scr, "name       platform-prod") || !strings.Contains(scr, "vaults     3") {
		t.Errorf("subscription not described:\n%s", scr)
	}
	// Into the fourth pane: it keeps describing the subscription, and
	// shift+tab goes back there.
	d.keys("4")
	if !strings.Contains(d.screen(), "name       platform-prod") {
		t.Errorf("lost the subscription:\n%s", d.screen())
	}
	d.keys("shift+tab")
	if d.m.focus != paneSources {
		t.Errorf("back to %v", d.m.focus)
	}
	// A value revealed from a left pane stays in view until hidden.
	d.keys("space")
	if !strings.Contains(d.screen(), "correct-horse-battery-staple") {
		t.Errorf("value not shown:\n%s", d.screen())
	}
}

func TestStatusLineCountsRepos(t *testing.T) {
	t.Parallel()
	d := newDriver(t, fake.Azure(0), fake.Repos())
	// The status line, not the pane, which an expired secret's ⚠ fills.
	if line := ansi.Strip(d.m.statusLine()); !strings.Contains(line, "1 account · 4 repos") {
		t.Errorf("status line: %q", line)
	}
	d = newDriver(t, fake.Azure(0))
	if line := strings.Split(d.screen(), "\n")[1]; strings.Contains(line, "repo") {
		t.Errorf("repos counted without any: %q", line)
	}
}

func TestStatusPane(t *testing.T) {
	t.Parallel()
	var opened []string
	d := newDriverOpts(t, Options{Version: "0.1.0", ConfigFiles: []string{"/cfg/config.yaml"}, StatePath: "/state/state.json",
		OpenURL: func(u string) error { opened = append(opened, u); return nil }},
		fake.Azure(0), signedOut{fake.Azure(0)})
	if line := ansi.Strip(d.m.statusLine()); !strings.Contains(line, "2 accounts · 1 need sign-in") {
		t.Errorf("status line: %q", line)
	}
	d.keys("0")
	if d.m.focus != paneStatus {
		t.Fatalf("focus %v", d.m.focus)
	}
	scr := d.screen()
	for _, want := range []string{logo[2], "version    v0.1.0", "GPL-3.0-or-later", "Azure · 2 subscriptions", "Azure · admin · sign-in required",
		"/cfg/config.yaml", "/state/state.json", "lazysecrets/…/docs"} {
		if !strings.Contains(scr, want) {
			t.Errorf("missing %q:\n%s", want, scr)
		}
	}
	// Into its rows: L signs in to the failing account, y copies a path,
	// o opens a link.
	d.keys("enter")
	if d.m.focus != paneDetail {
		t.Fatalf("enter: focus %v", d.m.focus)
	}
	d.m.fields.SelectWhere(func(f panes.Field) bool { return f.Provider == "Azure:admin@contoso.com/t1" })
	if a := d.m.loginFor(); a == nil || a.Account != "Azure · admin" {
		t.Errorf("L signs in to %+v", a)
	}
	d.m.fields.SelectWhere(func(f panes.Field) bool { return f.Label == "config" })
	d.keys("y")
	if got := d.clip().content; got != "/cfg/config.yaml" {
		t.Errorf("y copied %q", got)
	}
	d.m.fields.SelectWhere(func(f panes.Field) bool { return f.Label == "docs" })
	d.keys("o")
	if len(opened) != 1 || !strings.HasSuffix(opened[0], "/src/branch/main/docs") {
		t.Errorf("o opened %q", opened)
	}
	d.m.fields.SelectWhere(func(f panes.Field) bool { return f.Label == "version" })
	d.keys("o")
	if len(opened) != 2 || !strings.HasSuffix(opened[1], "/releases/tag/v0.1.0") {
		t.Errorf("o on the version opened %q", opened)
	}
	d.keys("shift+tab")
	if d.m.focus != paneStatus {
		t.Errorf("back to %v", d.m.focus)
	}
}

// empty is an account with nothing in it, like Databricks without profiles.
type empty struct{ provider.Provider }

func (empty) Sources(context.Context) ([]provider.Source, error) { return nil, nil }

func TestGetStarted(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{}, signedOut{fake.Azure(0)}, empty{fake.Databricks(0)})
	scr := d.screen()
	for _, want := range []string{"get started", "Azure      az login", "L runs it here",
		"databricks auth login --host", "lazysecrets --demo"} {
		if !strings.Contains(scr, want) {
			t.Errorf("missing %q:\n%s", want, scr)
		}
	}
	d.keys("l", "j")
	if a := d.m.loginFor(); a == nil || a.Account != "Azure · admin" {
		t.Errorf("L signs in to %+v", a)
	}
	d.keys("y")
	if got := d.clip().content; got != "az login" {
		t.Errorf("y copied %q", got)
	}

	// Once an account has something, an empty one says what it needs.
	d = newDriverOpts(t, Options{}, fake.Azure(0), empty{fake.Databricks(0)})
	d.m.sources.SelectWhere(func(r sourceRow) bool { return r.kind == rowGroup && r.provider == fake.Databricks(0).ID() })
	d.send(nil)
	if scr := d.screen(); strings.Contains(scr, "get started") || !strings.Contains(scr, "databricks auth login") {
		t.Errorf("empty Databricks:\n%s", scr)
	}
}

// detailed is an account whose vaults have details.
type detailed struct{ provider.Provider }

func (detailed) Details(_ context.Context, c provider.Container) ([]provider.Attr, error) {
	return []provider.Attr{{Label: "access", Value: "Azure roles (RBAC)"}, {Label: "git", Value: "tracked", Warn: true}}, nil
}

func TestContainerDetails(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{}, detailed{fake.Azure(0)})
	d.keys("2")
	if scr := d.screen(); !strings.Contains(scr, "access     Azure roles (RBAC)") || !strings.Contains(scr, "URI") {
		t.Errorf("details:\n%s", scr)
	}
	if st := d.m.details[d.m.containers.SelectedID()]; st.loading || len(st.attrs) != 2 {
		t.Errorf("state %+v", st)
	}
}

func TestExpiringInStatus(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{ExpiryWarning: 30 * 24 * time.Hour}, fake.Azure(0))
	d.keys("3", "]", "]") // load kv-app-prod's certificates
	// old-tls and legacy-token.
	if !strings.Contains(d.screen(), "⚠ 2 expiring") {
		t.Errorf("status line:\n%s", d.screen())
	}
	d.keys("0")
	scr := d.screen()
	for _, want := range []string{"expiring   ✗ expired  old-tls · kv-app-prod", "in 1 of 3 vaults (ctrl+a in search loads all)"} {
		if !strings.Contains(scr, want) {
			t.Errorf("missing %q:\n%s", want, scr)
		}
	}
	d.keys("3", "[", "[", "0", "enter") // elsewhere, then to it from the status pane
	d.m.fields.SelectWhere(func(f panes.Field) bool { return f.Label == "expiring" })
	d.keys("enter")
	if _, v, o := d.selected(); v != "kv-app-prod" || o != "old-tls" {
		t.Errorf("enter went to %s/%s\n%s", v, o, d.screen())
	}
}

func TestErrorLog(t *testing.T) {
	t.Parallel()
	d := newDriverOpts(t, Options{}, fake.Azure(0))
	d.keys("2", "j", "j", "3") // kv-restricted: its secrets can't be listed
	d.m.flash = flash{text: "copy failed: no clipboard", isErr: true}
	d.send(nil)
	d.send(nil) // still showing: logged once
	d.keys("0")
	scr := d.screen()
	if !strings.Contains(scr, "errors") || !strings.Contains(scr, "copy failed: no clipboard") || !strings.Contains(scr, "kv-restricted: 403 Forbidden") {
		t.Errorf("errors not listed:\n%s", scr)
	}
	if n := len(d.m.errLog); n != 2 || !strings.HasPrefix(d.m.errLog[0].text, "copy failed") {
		t.Errorf("log %+v", d.m.errLog)
	}
	d.m.logErr("403 Forbidden: again") // a new one, and an old one again: on top
	d.m.logErr(d.m.errLog[2].text)
	if len(d.m.errLog) != 3 || d.m.errLog[0].text == "403 Forbidden: again" {
		t.Errorf("log %+v", d.m.errLog)
	}
}

// hiding marks the secret db-password hidden, as the repo provider does
// a Spring key that doesn't look secret.
type hiding struct{ provider.Provider }

func (h hiding) Objects(ctx context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	objs, err := h.Provider.Objects(ctx, c, t)
	for i := range objs {
		objs[i].Hidden = objs[i].Name == "db-password"
	}
	return objs, err
}

func TestToggleHidden(t *testing.T) {
	t.Parallel()
	d := newDriver(t, hiding{fake.Azure(0)})
	if _, _, s := d.selected(); s == "db-password" || s == "" {
		t.Fatalf("selected %q: the hidden one, or none", s)
	}
	if strings.Contains(d.screen(), "db-password") || !strings.Contains(d.screen(), "1 hidden") {
		t.Fatalf("hidden one listed:\n%s", d.screen())
	}
	d.keys("H")
	if !strings.Contains(d.screen(), "db-password") || !strings.Contains(d.screen(), "showing hidden") {
		t.Fatalf("H doesn't show it:\n%s", d.screen())
	}
	d.keys("H")
	if strings.Contains(d.screen(), "db-password") {
		t.Fatalf("H again doesn't hide it:\n%s", d.screen())
	}
}
