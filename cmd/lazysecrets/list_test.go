package main

import (
	"strings"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/listing"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/icons"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

func TestListNames(t *testing.T) {
	e := listing.Entry{Provider: "azure", Path: []string{"platform-prod", "kv-app-prod", "db-password"}, AccountLabel: "alice"}
	if got := nameOf(e, styles.Default(), false); got != "platform-prod / kv-app-prod / db-password · alice" {
		t.Errorf("plain: %q", got)
	}
	// The item's own name isn't muted with its path.
	if got := nameOf(e, styles.Default(), true); !strings.Contains(got, "\x1b[mdb-password") {
		t.Errorf("name not set apart: %q", got)
	}
}

func TestListMarks(t *testing.T) {
	az := icons.NerdFont.Azure
	plain := marks{theme: styles.Default(), icons: icons.NerdFont, stars: true}
	noIcons := plain
	noIcons.icons = icons.None
	colored := noIcons
	colored.color = true
	for _, c := range []struct {
		m    marks
		e    listing.Entry
		want string
	}{
		// Room for the status mark and the star, so names line up.
		{plain, listing.Entry{Provider: "azure", Starred: true}, "  ★ " + az},
		{plain, listing.Entry{Provider: "azure"}, "    " + az},
		{marks{icons: icons.NerdFont}, listing.Entry{Provider: "azure", Starred: true}, "  " + az},
		{noIcons, listing.Entry{Provider: "azure", Starred: true}, "  ★"},
		{marks{}, listing.Entry{Provider: "azure", Starred: true}, " "},
		{colored, listing.Entry{Provider: "azure", Starred: true}, "  " + styles.Default().Favourite.Render("★")},
	} {
		if got := c.m.of(c.e); got != c.want {
			t.Errorf("%+v: %q, want %q", c.e, got, c.want)
		}
	}
}

func TestListRecentMarks(t *testing.T) {
	az := icons.NerdFont.Azure
	m := marks{icons: icons.NerdFont, stars: true, recent: true}
	for _, c := range []struct {
		e    listing.Entry
		want string
	}{
		// Right-aligned in room for three: by the icon, names lined up.
		{listing.Entry{Provider: "azure", Starred: true, Recent: true}, "  ★ ◷ " + az},
		{listing.Entry{Provider: "azure", Recent: true}, "    ◷ " + az},
		{listing.Entry{Provider: "azure"}, "      " + az},
	} {
		if got := m.of(c.e); got != c.want {
			t.Errorf("%+v: %q, want %q", c.e, got, c.want)
		}
	}
}

func TestListExpiry(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	warn := 30 * 24 * time.Hour
	for _, c := range []struct {
		expires time.Time
		want    string
	}{
		{time.Time{}, ""},
		{now.Add(-time.Hour), "expired"},
		{now.Add(12*24*time.Hour + time.Hour), "expires in 12d"},
		{now.Add(5 * time.Hour), "expires in 5h"},
		{now.Add(60 * 24 * time.Hour), ""},
	} {
		if got := expiryOf(listing.Entry{Expires: c.expires}, styles.Default(), warn, now, false); got != c.want {
			t.Errorf("expires %v: %q, want %q", c.expires, got, c.want)
		}
	}
	if got := expiryOf(listing.Entry{Expires: now.Add(-time.Hour)}, styles.Default(), warn, now, true); got != styles.Default().Error.Render("expired") {
		t.Errorf("expired isn't red: %q", got)
	}
}

func TestListNotes(t *testing.T) {
	e := listing.Entry{Note: "rotate in May"}
	if got := noteOf(e, styles.Default(), icons.NerdFont, true, false); got != icons.NerdFont.Note+" rotate in May" {
		t.Errorf("icons: %q", got)
	}
	if got := noteOf(e, styles.Default(), icons.None, true, false); got != "✎ rotate in May" {
		t.Errorf("no icons: %q", got)
	}
	if got := noteOf(e, styles.Default(), icons.NerdFont, false, false); got != "rotate in May" {
		t.Errorf("--notes=false: %q", got)
	}
	if got := noteOf(listing.Entry{}, styles.Default(), icons.NerdFont, true, true); got != "" {
		t.Errorf("no note: %q", got)
	}
}

func TestListStatusMark(t *testing.T) {
	az := icons.NerdFont.Azure
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	m := marks{icons: icons.NerdFont, stars: true, expiry: true, warn: 30 * 24 * time.Hour, now: now}
	for _, c := range []struct {
		e    listing.Entry
		want string
	}{
		// Right-aligned in room for two, as with ★ and ◷.
		{listing.Entry{Provider: "azure", Error: "403 Forbidden"}, "  ✗ " + az},
		{listing.Entry{Provider: "azure", Expires: now.Add(-time.Hour)}, "  ✗ " + az},
		{listing.Entry{Provider: "azure", Expires: now.Add(-time.Hour), Starred: true}, "✗ ★ " + az},
		{listing.Entry{Provider: "azure", Expires: now.Add(5 * 24 * time.Hour)}, "  ⚠ " + az},
		{listing.Entry{Provider: "azure", Expires: now.Add(60 * 24 * time.Hour), Starred: true}, "  ★ " + az},
	} {
		if got := m.of(c.e); got != c.want {
			t.Errorf("%+v: %q, want %q", c.e, got, c.want)
		}
	}
	m.expiry = false // --expiry=false: no ⚠ or ✗ for expiry; errors still
	if got := m.of(listing.Entry{Provider: "azure", Expires: now.Add(-time.Hour)}); got != "    "+az {
		t.Errorf("--expiry=false: %q", got)
	}
	m.expiry, m.color, m.theme = true, true, styles.Default()
	if got := m.of(listing.Entry{Error: "403"}); !strings.Contains(got, styles.Default().Error.Render("✗")) {
		t.Errorf("not a red ✗: %q", got)
	}
	if got := m.of(listing.Entry{Expires: now.Add(time.Hour)}); !strings.Contains(got, styles.Default().Warning.Render("⚠")) {
		t.Errorf("not a yellow ⚠: %q", got)
	}
}

func TestListErrors(t *testing.T) {
	e := listing.Entry{Provider: "azure", Path: []string{"platform-prod", "kv-restricted"}, Error: "403 Forbidden"}
	if got := nameOf(e, styles.Default(), false); got != "platform-prod / kv-restricted" {
		t.Errorf("the name has more: %q", got)
	}
	if got := errorOf(e, styles.Default(), false); got != "403 Forbidden" {
		t.Errorf("plain: %q", got)
	}
	if got := errorOf(e, styles.Default(), true); got != styles.Default().Error.Render("403 Forbidden") {
		t.Errorf("why isn't red: %q", got)
	}
	if got := errorOf(listing.Entry{}, styles.Default(), true); got != "" {
		t.Errorf("no error: %q", got)
	}
}
