package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/cachefile"
	"git.kebcloud.no/keb/lazysecrets/internal/expiry"
	"git.kebcloud.no/keb/lazysecrets/internal/listing"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/icons"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

// listCmd prints every source, vault and secret lazysecrets can find, one
// per line, for fzf, editor pickers and scripts:
//
//	lazysecrets list | fzf --ansi --delimiter='\t' --with-nth='2..-3' --nth='2..' --accept-nth=-1
//
// Each line is the kind, a mark (✗ or ⚠, ★, ◷ and the provider's icon),
// the name, a status (why it couldn't be read, or that it has expired or
// expires soon; else empty), its note (muted, or empty), the account and
// the link, tab-separated. The mark has a field of its own so a search can
// leave it out (fzf --nth), and ^prod finds names starting with prod. The
// name is coloured from the theme,
// with the provider's icon (as in the TUI: --icons, LAZYSECRETS_ICONS,
// ui.icons); the link never is.
//
// What's cached prints at once; what's missing or older than --max-age
// follows as it loads. A list that can't be read is a line too, as it
// fails: the vault or source, then ✗ and why in the theme's error colour.
// They're in the output rather than on stderr, which would draw over fzf.
func listCmd(providers []provider.Provider, st *store.Store, cachePath string, timeout func(provider.Kind) time.Duration, theme styles.Styles, withIcons, withRecent bool, expiryWarn time.Duration, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	cached := fs.Bool("cached", false, "only what's cached, and repos: load nothing from Azure or Databricks")
	maxAge := fs.Duration("max-age", 2*time.Minute, "load cached lists older than this again")
	format := fs.String("format", "text", "text: kind, mark, name, status, note, account and link, tab-separated; json: an object per line")
	color := fs.String("color", "always", "colour the names (text): always or never; fzf needs --ansi")
	iconsOn := fs.Bool("icons", withIcons, "put the provider's Nerd Font icon before names (text)")
	stars := fs.Bool("stars", true, "put ★ before starred names, in the theme's favouriteColor (text)")
	recent := fs.Bool("recent", withRecent, "put ◷ before names used lately, in mutedColor (text; default ui.recentIcon)")
	notes := fs.Bool("notes", true, "start a note with a note icon (✎ without --icons) (text; the note is printed either way)")
	expiry := fs.Bool("expiry", true, "mark and say which have expired (✗ expired) or expire within ui.expiryWarningDays (⚠ expires in 12d) (text)")
	kinds := fs.String("kind", "", "only these kinds, comma-separated: "+strings.Join(listing.Kinds, ", ")+" (vault: also scopes and repo files)")
	showErrors := fs.Bool("errors", true, "list the lists that can't be read, with why (text: ✗ lines; json: \"error\")")
	hidden := fs.Bool("hidden", false, "list what's hidden too, as H does in the TUI: Spring and compose keys that don't look secret, example files, and files and repos with only those (json: \"hidden\")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: lazysecrets list [--kind secret,…] [--cached] [--max-age 2m] [--format text|json]")
	}
	var only []string
	for k := range strings.SplitSeq(*kinds, ",") {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		if !slices.Contains(listing.Kinds, k) {
			return fmt.Errorf("--kind: %q is not one of %s", k, strings.Join(listing.Kinds, ", "))
		}
		only = append(only, k)
	}
	if *color != "always" && *color != "never" {
		return fmt.Errorf("--color: %q is not always or never", *color)
	}
	now := time.Now()
	m := marks{theme: theme, icons: icons.None, stars: *stars, recent: *recent, expiry: *expiry,
		color: *color == "always", warn: expiryWarn, now: now}
	if *iconsOn {
		m.icons = icons.NerdFont
	}
	var write func(listing.Entry) error
	switch *format {
	case "text":
		write = func(e listing.Entry) error {
			colored := *color == "always"
			mark := m.of(e)
			name := nameOf(e, theme, colored)
			// Why it couldn't be read (vaults, sources), or when it
			// expires (secrets, keys, certificates): never both.
			status := errorOf(e, theme, colored)
			if status == "" && *expiry {
				status = expiryOf(e, theme, expiryWarn, now, colored)
			}
			note := noteOf(e, theme, m.icons, *notes, colored)
			// A tab or line break in a name would break the line up.
			_, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Kind, mark, oneLine.Replace(name), oneLine.Replace(status),
				oneLine.Replace(note), e.Account, e.Link)
			return err
		}
	case "json":
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		write = func(e listing.Entry) error { return enc.Encode(e) }
	default:
		return fmt.Errorf("--format: %q is not text or json", *format)
	}

	// When the reader goes away (a pick in fzf), writing fails instead of
	// killing the process, so what loaded is still saved.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	var gone error
	emit := func(e listing.Entry) error {
		if e.Error != "" && !*showErrors {
			return nil
		}
		if err := write(e); err != nil {
			gone = err
			return err
		}
		return nil
	}

	f := cachefile.New()
	if cachePath != "" {
		f = cachefile.Read(cachePath)
	}
	f, loaded := listing.Run(context.Background(), listing.Options{
		Providers: providers, Cache: f, MaxAge: *maxAge, Cached: *cached, Kinds: only, Timeout: timeout, Hidden: *hidden,
		Mark: func(link, account string) listing.Marks {
			n := st.Node(link)
			return listing.Marks{Starred: n.StarredFor(account), Recent: n.UsedLately(now),
				LastUsed: n.LastUsed, Uses: n.Uses, Note: n.Note}
		},
	}, emit)
	// Only what was loaded: list never moves a cursor, so the TUI's stay.
	if cachePath != "" && loaded {
		f.Cursor = nil
		if err := cachefile.Save(cachePath, f, cachefile.Keep(providers), time.Now()); err != nil {
			return err
		}
	}
	if gone != nil && !errors.Is(gone, syscall.EPIPE) {
		return gone
	}
	return nil // or the reader has what it wanted
}

var oneLine = strings.NewReplacer("\t", " ", "\r\n", " ", "\n", " ", "\r", " ")

// noteOf is the entry's note, muted, after a note icon (✎ without Nerd
// Font icons) with icon; or "".
func noteOf(e listing.Entry, theme styles.Styles, iconSet icons.Set, icon, color bool) string {
	if e.Note == "" {
		return ""
	}
	note := e.Note
	if icon {
		note = cmp.Or(iconSet.Note, "✎") + " " + note
	}
	return renderer(color)(theme.Muted, note)
}

// expiryOf says that a secret, key or certificate has expired, in red,
// or when it will if that's within warn (expires in 12d), in yellow, or
// is "". Its mark (✗, ⚠) is before the name.
func expiryOf(e listing.Entry, theme styles.Styles, warn time.Duration, now time.Time, color bool) string {
	render := renderer(color)
	switch state, left := expiry.Of(e.Expires, now, warn); state {
	case expiry.Expired:
		return render(theme.Error, "expired")
	case expiry.Soon:
		return render(theme.Warning, "expires in "+expiry.Left(left))
	}
	return ""
}

// errorOf is why a list couldn't be read, in red, or "". Its mark (✗)
// is before the name.
func errorOf(e listing.Entry, theme styles.Styles, color bool) string {
	return renderer(color)(theme.Error, e.Error)
}

// nameOf is an entry's name for the text format: the path with the
// item's own name standing out, and the account.
func nameOf(e listing.Entry, theme styles.Styles, color bool) string {
	render := renderer(color)
	var name string
	if n := len(e.Path); n > 0 {
		if n > 1 {
			name = render(theme.Muted, strings.Join(e.Path[:n-1], " / ")+" / ")
		}
		name += e.Path[n-1]
	}
	if e.AccountLabel != "" {
		name += render(theme.Muted, " · "+e.AccountLabel)
	}
	return name
}

// marks says what goes before the name, in a field of its own so a
// search leaves it out: ✗ in red when something's wrong (it couldn't be
// read, or has expired) or ⚠ when it expires within warn; ★ for a starred
// entry and ◷ for one used lately, as the TUI marks them; then the
// provider's icon. The marks are right-aligned in room for every kind
// turned on, so they sit by the icon and names line up.
type marks struct {
	theme                        styles.Styles
	icons                        icons.Set
	stars, recent, expiry, color bool
	warn                         time.Duration // ui.expiryWarningDays
	now                          time.Time
}

func (m marks) of(e listing.Entry) string {
	render := renderer(m.color)
	var parts []string
	room := 0 // marks turned on
	mark := func(on bool, st lipgloss.Style, s string) {
		room++
		if on {
			parts = append(parts, render(st, s))
		}
	}
	// What's wrong, then what to watch: room for one of them.
	state := expiry.None
	if m.expiry {
		state, _ = expiry.Of(e.Expires, m.now, m.warn)
	}
	switch {
	case e.Error != "" || state == expiry.Expired:
		mark(true, m.theme.Error, "✗")
	case state == expiry.Soon:
		mark(true, m.theme.Warning, "⚠")
	default:
		mark(false, m.theme.Error, "")
	}
	if m.stars {
		mark(e.Starred, m.theme.Favourite, "★")
	}
	if m.recent {
		mark(e.Recent, m.theme.Muted, "◷")
	}
	pad := strings.Repeat("  ", room-len(parts)) // each mark and its space
	kind := provider.Kind("")
	for _, k := range []provider.Kind{provider.KindAzure, provider.KindDatabricks, provider.KindRepo} {
		if strings.EqualFold(string(k), e.Provider) {
			kind = k
		}
	}
	if icon := strings.TrimSpace(m.icons.Prefix(kind)); icon != "" {
		parts = append(parts, render(m.theme.Kind(kind), icon))
	} else if len(parts) == 0 {
		pad = strings.TrimSuffix(pad, " ") // no mark after the room
	}
	return pad + strings.Join(parts, " ")
}

func renderer(color bool) func(lipgloss.Style, string) string {
	return func(st lipgloss.Style, s string) string {
		if !color || s == "" {
			return s
		}
		return st.Render(s)
	}
}
