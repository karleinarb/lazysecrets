package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/share"
)

// Sharing env files as an encrypted lazysecrets://share link (package
// share), and opening one: the files are encrypted with a secret's value,
// so whoever can read that secret can open the link.

// sharing is a share being made: the files read, and the key to use.
type sharing struct {
	repo  provider.Source
	files []share.File
	key   ref.Ref // zero until chosen
}

type shareFilesMsg struct {
	sharing
	err error
}

type sealedMsg struct {
	link ref.Ref
	n    int
	// from is the picker the key was picked in; nil for sharing.key's.
	from *picker
	err  error
}

// canShareFiles reports whether src's env files can be shared: it's a
// repo whose files can be read.
func (m Model) canShareFiles(src provider.Source) bool {
	_, ok := m.providerFor(src).(provider.Contents)
	return src.Kind == provider.KindRepo && ok
}

// shareFiles shares env files of a repo: cs, or with cs nil all its
// plain ones.
func (m *Model) shareFiles(src provider.Source, cs []provider.Container) tea.Cmd {
	p := m.providerFor(src)
	files, ok := p.(provider.Contents)
	if !ok {
		m.flash = warning("these files can't be shared")
		return nil
	}
	general, timeout := m.shareKey, m.timeout.of(src.Kind)
	m.flash = flash{text: "reading the files…"}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		msg := shareFilesMsg{sharing: sharing{repo: src}}
		if cs == nil {
			all, err := p.Containers(ctx, src)
			if err != nil {
				msg.err = err
				return msg
			}
			for _, c := range all {
				if c.Detail == provider.DetailPlainEnv && !c.Hidden { // not .env.example
					cs = append(cs, c)
				}
			}
			if len(cs) == 0 {
				msg.err = errors.New(src.Name + " has no plain env files")
				return msg
			}
		}
		for _, c := range cs {
			b, err := files.Contents(ctx, c)
			if err != nil {
				msg.err = err
				return msg
			}
			msg.files = append(msg.files, share.File{Path: c.Name, Data: b})
		}
		key, ok, err := share.DefaultKey(src.ID, general)
		if ok {
			msg.key = key
		}
		msg.err = err
		return msg
	}
}

func (m *Model) handleShareFiles(msg shareFilesMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.flash = flash{text: "share: " + msg.err.Error(), isErr: true}
	case msg.key == (ref.Ref{}):
		return m.pickShareKey(msg.sharing)
	default:
		m.confirmShare(msg.sharing)
	}
	return nil
}

// shareTitle names the action: "Share .env", "Share 2 env files".
func shareTitle(s sharing) string {
	if len(s.files) == 1 {
		return "Share " + path.Base(s.files[0].Path)
	}
	return "Share " + plural(len(s.files), "env file")
}

// sharedFiles lists the files being shared, under what's happening.
func (m *Model) sharedFiles(s sharing) []string {
	lines := []string{"Sharing " + plural(len(s.files), "env file") + " as an encrypted link:"}
	for _, f := range s.files {
		lines = append(lines, "  "+m.repoLabel(s.repo.Name, f.Path))
	}
	return lines
}

// confirmShare says what the link will hold before making it.
func (m *Model) confirmShare(s sharing) {
	lines := append(m.sharedFiles(s), "", m.st.Muted.Render("encrypted with ")+m.refLabel(s.key))
	m.showMenu(shareTitle(s), lines, []option{
		{key: "c", label: "Copy the link", run: func(m *Model) tea.Cmd { return m.seal(s, nil) }},
		{key: "k", label: "Encrypt with another secret…", run: func(m *Model) tea.Cmd { return m.pickShareKey(s) }},
	})
}

// pickShareKey asks which secret to encrypt with, in Go to, then shares
// with it: the picker showed what's shared, so there's no asking again.
// It stays open until the link is made, to say why a secret can't be
// used.
func (m *Model) pickShareKey(s sharing) tea.Cmd {
	cmd := m.openGoTo(tabSearch, true)
	p := &picker{
		title:   shareTitle(s),
		lines:   m.sharedFiles(s),
		heading: "Pick a secret to encrypt with",
		help: []string{
			m.st.Muted.Render("The recipient needs read access to it to decrypt the files."),
			m.st.Muted.Render("Its value isn't shown or put in the link."),
		},
	}
	p.pick = func(m *Model, key ref.Ref) tea.Cmd {
		s.key = key
		p.err, p.busy = "", "encrypting with "+key.Name+"…"
		cmd := m.seal(s, p)
		m.flash = flash{}
		return cmd
	}
	m.search.picker = p
	m.search.input.SetWidth(searchWidth(m.w) - 10) // inside the picker's border
	m.fillSearch(false)
	return tea.Batch(cmd, m.searchLoads())
}

// pickPasted picks a pasted reference to a secret, while picking one.
func (m *Model) pickPasted(text string) tea.Cmd {
	key, err := share.ParseKey(strings.TrimSpace(text))
	if err != nil {
		m.search.picker.err = "✗ " + capitalize(err.Error())
		return nil
	}
	return m.search.picker.choose(m, key)
}

// seal reads the key and encrypts the files with it, for the picker
// from (nil: the key wasn't picked).
func (m *Model) seal(s sharing, from *picker) tea.Cmd {
	read, rules := m.keyReader(), m.shareRules
	m.flash = flash{text: "encrypting with " + s.key.Name + "…"}
	return func() tea.Msg {
		v, version, err := read(s.key)
		if err != nil {
			return sealedMsg{from: from, err: &share.KeyError{Name: s.key.Name, Err: err}}
		}
		link, err := share.Seal(share.Header{Key: share.Pin(s.key, version), Repo: s.repo.Detail}, []byte(v.Reveal()), s.files, rules)
		return sealedMsg{link: link, n: len(s.files), from: from, err: err}
	}
}

func (m *Model) handleSealed(msg sealedMsg) tea.Cmd {
	if p := msg.from; p != nil {
		if m.search == nil || m.search.picker != p {
			return nil // the picker was closed: cancelled
		}
		p.busy = ""
		var bad *share.KeyError
		if errors.As(msg.err, &bad) {
			p.err = "✗ " + capitalize(bad.Error()) // to pick another
			return nil
		}
		m.search = nil
	}
	if msg.err != nil {
		m.noteErr(msg.err)
		m.flash = flash{text: "share: " + msg.err.Error(), isErr: true}
		return nil
	}
	m.dismissedLink = msg.link.String() // not offered back on focus
	return m.copyText("encrypted link to "+plural(msg.n, "file"), msg.link.String(), false)
}

// keyReader reads a key's value with the providers, the account that
// last read it first, off the UI goroutine.
func (m *Model) keyReader() func(ref.Ref) (provider.SecretValue, string, error) {
	providers, st, timeout := m.providers, m.store, m.timeout
	return func(key ref.Ref) (provider.SecretValue, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout.of(key.Kind))
		defer cancel()
		k := key
		k.Version = ""
		node := st.Node(k.String())
		return ref.Read(ctx, providers, key, node.Account, node.Source)
	}
}

// --- opening ---

type shareOpenedMsg struct {
	sealed share.Sealed
	files  []share.File
	dir    string // the repo's clone; "" when it isn't here
	err    error
}

// offerShare says what a share link holds before decrypting it: links
// can come from anywhere, so nothing is read or written unasked.
func (m *Model) offerShare(r ref.Ref) tea.Cmd {
	s, err := share.Read(r)
	if err != nil {
		m.showMenu("Share link can't be read", []string{m.st.Error.Render(err.Error())}, nil)
		return nil
	}
	from := ""
	if s.Repo != "" {
		from = m.st.Muted.Render(" from ") + m.repoLabel(s.Repo)
	}
	m.showMenu("Shared env files", []string{
		m.st.Muted.Render(plural(s.Files, "env file")) + from,
		m.st.Muted.Render("encrypted with ") + m.refLabel(s.Key),
	}, []option{
		{key: "o", label: "Decrypt…", tip: "reads the secret, then asks before saving", run: func(m *Model) tea.Cmd { return m.openShare(s) }},
	})
	return nil
}

// openShare reads the key, decrypts the files and finds the repo's clone.
func (m *Model) openShare(s share.Sealed) tea.Cmd {
	read := m.keyReader()
	providers, timeout := m.providers, m.timeout.of(provider.KindRepo)
	m.flash = flash{text: "decrypting with " + s.Key.Name + "…"}
	return func() tea.Msg {
		msg := shareOpenedMsg{sealed: s}
		v, _, err := read(s.Key)
		if err != nil {
			msg.err = &share.KeyError{Name: s.Key.Name, Err: err}
			return msg
		}
		if msg.files, msg.err = s.Open([]byte(v.Reveal())); msg.err != nil {
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		for _, p := range providers {
			if loc, ok := p.(provider.Locator); ok && p.Kind() == provider.KindRepo && s.Repo != "" {
				// Its clone, if it's on disk (the demo's aren't).
				if root, found, _ := loc.Locate(ctx, s.Repo); found && isDir(root) {
					msg.dir = root
					break
				}
			}
		}
		return msg
	}
}

func (m *Model) handleShareOpened(msg shareOpenedMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.noteErr(msg.err)
		m.openFailed(msg.sealed, msg.err, false)
	case msg.dir == "":
		return m.askShareDir(msg)
	default:
		targets, err := planSave(msg.files, msg.dir)
		if err != nil {
			m.flash = flash{text: "save: " + err.Error(), isErr: true}
			return nil
		}
		m.offerSave(targets, msg.dir)
	}
	return nil
}

// openFailed says why a share link couldn't be decrypted, in a popup
// where its offer was: its first line (wrapped), or, with whole, all of it
// (Key Vault's refusals say what to check on later lines). From there,
// the key's secret can be gone to: its version, which may be disabled.
func (m *Model) openFailed(s share.Sealed, err error, whole bool) {
	width := max(30, min(m.w-12, 76))
	text := capitalize(err.Error())
	first, rest, _ := strings.Cut(text, "\n")
	cut := !whole && rest != "" // more than the reason: what to check
	if !whole {
		text = first
	}
	var lines []string
	for i, l := range strings.Split(ansi.Wordwrap(text, width-2, " "), "\n") {
		if i == 0 {
			l = "✗ " + l
		} else if l != "" {
			l = "  " + l
		}
		lines = append(lines, m.st.Error.Render(l))
	}
	var opts []option
	if cut {
		opts = append(opts, option{key: "r", label: "Show the whole message", run: func(m *Model) tea.Cmd {
			m.openFailed(s, err, true)
			return nil
		}})
	}
	opts = append(opts, option{key: "g", label: "Go to " + s.Key.Name, run: func(m *Model) tea.Cmd { return m.openRef(s.Key, actGo) }})
	m.showMenu("Can't decrypt the shared files", lines, opts)
}

// askShareDir asks where to save files from a repo that isn't here. It
// stays until they're saved, saying why when they can't be.
func (m *Model) askShareDir(msg shareOpenedMsg) tea.Cmd {
	in := newTextInput("folder: ", m.pathInputWidth())
	dir := m.placeDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	in.SetValue(dir)
	in.CursorEnd()
	lines := []string{m.st.Muted.Render("Paths are from the repo's root.")}
	if msg.sealed.Repo != "" {
		lines = append([]string{m.repoLabel(msg.sealed.Repo) + m.st.Muted.Render(" isn't among your repos. Save where?")}, lines...)
	}
	p := &modal{title: "Save the files", input: &in, lines: lines, submitLabel: "save"}
	p.submit = func(m *Model, text string) tea.Cmd {
		dir := expandHome(strings.TrimSpace(text))
		targets, err := planSave(msg.files, dir)
		switch {
		case err == nil && slices.ContainsFunc(targets, func(t share.Target) bool { return t.Exists }):
			m.offerSave(targets, dir) // ask before replacing
			return nil
		case err == nil:
			// Chosen, with nothing to replace: saved at once.
			if err = m.writeShared(targets, dir, false); err == nil {
				return nil
			}
		}
		p.err = err.Error()
		m.prompt = p // again, to fix the folder
		return p.input.Focus()
	}
	m.prompt = p
	return p.input.Focus()
}

// planSave says where each file goes in dir, which must be a folder.
func planSave(files []share.File, dir string) ([]share.Target, error) {
	if !isDir(dir) {
		return nil, fmt.Errorf("no folder %s", dir)
	}
	return share.Plan(dir, files)
}

// offerSave shows where each file goes, then saves them as chosen:
// files there already are replaced only when asked.
func (m *Model) offerSave(targets []share.Target, dir string) {
	var lines []string
	exist := 0
	for _, t := range targets {
		file := "  " + m.repoLabel(filepath.Base(dir), t.Path)
		line := file + m.st.Muted.Render(" · new")
		if t.Exists {
			exist++
			line = file + m.st.Muted.Render(" · replaces the one there")
		}
		if t.Exposed {
			line += m.st.Error.Render(" · not ignored by git")
		}
		lines = append(lines, line)
	}
	lines = append([]string{m.st.Muted.Render("In " + dir + ":")}, lines...)
	write := func(onlyNew bool) func(*Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			if err := m.writeShared(targets, dir, onlyNew); err != nil {
				m.flash = flash{text: "save: " + err.Error(), isErr: true}
			}
			return nil
		}
	}
	var opts []option
	switch {
	case exist == 0:
		opts = append(opts, option{key: "w", label: "Save", run: write(false)})
	case exist == len(targets):
		opts = append(opts, option{key: "w", label: "Replace", run: write(false)})
	default:
		opts = append(opts,
			option{key: "w", label: fmt.Sprintf("Save all, replacing %d", exist), run: write(false)},
			option{key: "n", label: onlyNew(len(targets) - exist), run: write(true)},
		)
	}
	m.showMenu("Save the shared files", lines, opts)
}

// writeShared writes the files (with onlyNew, those not there yet) and
// says where, and which ones git doesn't ignore.
func (m *Model) writeShared(targets []share.Target, dir string, onlyNew bool) error {
	n := 0
	var exposed []string
	for _, t := range targets {
		if onlyNew && t.Exists {
			continue
		}
		if err := t.Write(); err != nil {
			return fmt.Errorf("%s: %w", t.Path, err)
		}
		n++
		if t.Exposed {
			exposed = append(exposed, t.Path)
		}
	}
	m.flash = done("saved " + plural(n, "file") + " in " + dir)
	if len(exposed) > 0 {
		m.flash = warning("saved " + plural(n, "file") + " in " + dir + "; git doesn't ignore " + strings.Join(exposed, ", "))
	}
	return nil
}

// isDir reports whether dir is a folder on disk.
func isDir(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// expandHome expands a leading ~/ to the home folder.
func expandHome(dir string) string {
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return dir
}

// onlyNew labels saving only the n files not there yet.
func onlyNew(n int) string {
	if n == 1 {
		return "Save only the new one"
	}
	return fmt.Sprintf("Save only the %d new ones", n)
}
