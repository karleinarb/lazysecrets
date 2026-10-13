package app

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/browser"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

// Updater finds and installs newer releases (internal/update).
type Updater struct {
	Latest  func(context.Context) (update.Release, error)
	Install func(context.Context, update.Release) error
}

// updateEvery is how often lazysecrets looks for a newer release on its
// own unless the config says otherwise; within that, the last answer (in
// the state file) is used.
const updateEvery = 24 * time.Hour

type updateMsg struct {
	release update.Release
	err     error
	asked   bool // the user asked (U), rather than the daily check
}

type installedMsg struct {
	release update.Release
	err     error
}

// startUpdateCheck sets up the startup look for a newer release: the
// state file's answer when it looked within updateEvery, else a quiet
// look at the server (fetchOnStart, for Init).
func (m *Model) startUpdateCheck() {
	if m.updater == nil || !m.updateCheck {
		return
	}
	last := m.store.UpdateCheck()
	if m.now().Sub(last.At) < m.updateEvery {
		m.offer(update.Release{Tag: last.Tag, Body: last.Notes, URL: last.URL})
		return
	}
	m.fetchOnStart = true
}

func (m Model) fetchLatest(asked bool) tea.Cmd {
	latest := m.updater.Latest
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		r, err := latest(ctx)
		return updateMsg{release: r, err: err, asked: asked}
	}
}

// offer remembers r as the update to offer, if it's newer.
func (m *Model) offer(r update.Release) {
	m.newer = nil
	if update.Newer(m.version, r.Tag) {
		m.newer = &r
	}
}

func (m *Model) handleUpdate(msg updateMsg) tea.Cmd {
	if msg.err != nil {
		if msg.asked {
			m.flash = flash{text: "couldn't check for a newer lazysecrets: " + msg.err.Error(), isErr: true}
		}
		return nil // the daily check stays quiet: offline, or the server's down
	}
	m.store.SetUpdateCheck(store.UpdateCheck{
		At: m.now(), Tag: msg.release.Tag, Notes: msg.release.Body, URL: msg.release.URL,
	})
	m.offer(msg.release)
	if msg.asked {
		if m.newer == nil {
			m.flash = done("lazysecrets " + m.versionLabel() + " is the latest (" + msg.release.Tag + ")")
			return nil
		}
		m.showUpdate()
	}
	return nil
}

// update is the update key: the newer release, or a look for one.
func (m *Model) update() tea.Cmd {
	switch {
	case m.updater == nil:
		m.flash = warning("updates aren't available in this build")
		return nil
	case m.newer != nil:
		m.showUpdate()
		return nil
	case !update.Newer(m.version, "v999.0.0"): // dev builds aren't versions
		m.flash = warning("this is a development build (" + m.versionLabel() + "): update it with go install or install.sh")
		return nil
	}
	m.flash = flash{text: "looking for a newer lazysecrets…"}
	return m.fetchLatest(true)
}

// showUpdate offers the newer release: its changelog, and updating.
func (m *Model) showUpdate() {
	r := *m.newer
	lines := []string{m.st.Muted.Render("You have " + m.versionLabel() + ".")}
	if notes := r.Notes(m.version); notes != "" {
		lines = append(lines, "")
		for _, l := range strings.Split(notes, "\n") {
			if h, ok := strings.CutPrefix(l, "### "); ok {
				l = m.st.Label.Render(h)
			} else if h, ok := strings.CutPrefix(l, "## "); ok { // a release, when several are new
				l = m.st.TitleFocused.Render(h)
			}
			lines = append(lines, l)
		}
	}
	// A popup doesn't scroll: several releases behind, the rest is on
	// the release pages.
	if most := max(6, m.h-12); len(lines) > most {
		lines = append(lines[:most-1], m.st.Muted.Render("… and more on the release pages"))
	}
	opts := []option{{key: "u", label: "Update now", tip: "Download, check and replace this lazysecrets; restart to use it.",
		run: func(m *Model) tea.Cmd { return m.install(r) }}}
	if r.URL != "" {
		opts = append(opts, option{key: "o", label: "Open the release page", run: func(m *Model) tea.Cmd {
			switch err := m.openURL(r.URL); {
			case errors.Is(err, browser.ErrRemote):
				cmd := m.copyText("link to the release", r.URL, false)
				m.flash.text += " (can't open a browser over SSH)"
				return cmd
			case err != nil:
				m.flash = flash{text: "open: " + err.Error(), isErr: true}
			}
			return nil
		}})
	}
	m.showMenu("lazysecrets "+r.Tag, lines, opts)
}

func (m *Model) install(r update.Release) tea.Cmd {
	m.flash = flash{text: "downloading lazysecrets " + r.Tag + "…"}
	install := m.updater.Install
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return installedMsg{release: r, err: install(ctx, r)}
	}
}

func (m *Model) handleInstalled(msg installedMsg) {
	if msg.err != nil {
		m.flash = flash{text: "update failed: " + msg.err.Error(), isErr: true}
		return
	}
	m.newer = nil
	m.updated = msg.release.Tag
	m.flash = done("updated to lazysecrets " + msg.release.Tag + " · restart lazysecrets to use it")
}
