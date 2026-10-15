package app

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

// The status pane, like lazygit's: a line on top of the left panes with
// the version, a newer release and how the accounts are doing. Focused
// (0), the right pane has the rest: updates, each account, the files
// lazysecrets uses, links, and the changelog.

// logo heads the status pane's rows.
var logo = []string{
	` _                                        _`,
	`| | __ _ _____   _ ___  ___  ___ _ __ ___| |_ ___`,
	`| |/ _` + "`" + ` |_  / | | / __|/ _ \/ __| '__/ _ \ __/ __|`,
	`| | (_| |/ /| |_| \__ \  __/ (__| | |  __/ |_\__ \`,
	`|_|\__,_/___|\__, |___/\___|\___|_|  \___|\__|___/`,
	`             |___/`,
}

// statusH is the status pane's height: one line in a box.
const statusH = 3

// statusLine is what the status pane says while it isn't focused.
func (m Model) statusLine() string {
	var parts []string
	switch {
	case m.updated != "":
		parts = append(parts, m.st.Notice.Render("→ "+m.updated+" (restart)"))
	case m.newer != nil:
		parts = append(parts, m.st.Notice.Render("↑ "+m.newer.Tag+withKey(m.keys.Update, " %s")))
	}
	accounts, failing, repos := 0, 0, 0
	for _, p := range m.providers {
		if p.Kind() == provider.KindRepo {
			repos += len(m.sourceResults[p.ID()].sources) // those with secret files
			continue
		}
		accounts++
		if r, ok := m.sourceResults[p.ID()]; ok && r.err != nil {
			failing++
		}
	}
	if n := len(m.expiringItems()); n > 0 {
		parts = append(parts, m.st.Warning.Render(fmt.Sprintf("⚠ %d expiring", n)))
	}
	if accounts > 0 {
		s := plural(accounts, "account")
		if failing > 0 {
			s += " · " + m.st.Error.Render(fmt.Sprintf("%d need sign-in", failing))
		}
		parts = append(parts, s)
	}
	if repos > 0 {
		parts = append(parts, plural(repos, "repo"))
	}
	return " " + strings.Join(parts, m.st.Muted.Render(" · "))
}

func (m Model) statusView() string {
	title := "[0] lazysecrets"
	if v := m.versionLabel(); v != "" {
		title += " " + v
	}
	return panes.Box(m.st, panes.TitleStyle(m.st, m.active(paneStatus)).Render(title), "",
		[]string{m.statusLine()}, m.statusW, statusH, m.active(paneStatus))
}

// statusFields are the status pane's rows on the right.
func (m Model) statusFields() []panes.Field {
	var out []panes.Field
	add := func(f panes.Field) {
		if f.Value != "" {
			out = append(out, f)
		}
	}
	// A release's rows open its release page.
	version := panes.Field{Label: "version", Value: cmp.Or(m.versionLabel(), "dev")}
	if strings.HasPrefix(version.Value, "v") {
		version.URL = update.DefaultRepo + "/releases/tag/" + version.Value
	}
	add(version)
	upd := panes.Field{Label: "update", Value: m.updateStatus(), Info: m.newer == nil}
	if m.newer != nil {
		upd.URL = update.DefaultRepo + "/releases/tag/" + m.newer.Tag
	}
	add(upd)
	add(panes.Field{Label: "license", Value: "GPL-3.0-or-later · © 2026 Karl-Einar Bergstrøm", Info: true})

	for i, p := range m.providers {
		f := panes.Field{Value: p.Label() + " · " + m.accountStatus(p), Provider: p.ID()}
		if i == 0 {
			f.Label = "accounts"
		}
		var auth *provider.AuthError
		f.Err = errors.As(m.sourceResults[p.ID()].err, &auth)
		add(f)
	}

	out = append(out, m.expiringFields()...)
	out = append(out, m.errorFields()...)

	for i, path := range m.configFiles {
		label := ""
		if i == 0 {
			label = "config"
		}
		add(panes.Field{Label: label, Value: path, Path: path})
	}
	add(panes.Field{Label: "state", Value: m.statePath, Path: m.statePath})
	add(panes.Field{Label: "cache", Value: m.cachePath, Path: m.cachePath})

	// Short, without https://; y copies and o opens the whole URL.
	repo := strings.TrimPrefix(update.DefaultRepo, "https://")
	add(panes.Field{Label: "docs", Value: repo + "/…/docs", URL: update.DefaultRepo + "/src/branch/main/docs"})
	add(panes.Field{Label: "source", Value: repo, URL: update.DefaultRepo})
	// Issues are on the GitHub mirror.
	github := strings.TrimPrefix(update.GitHubRepo, "https://")
	add(panes.Field{Label: "issues", Value: github + "/issues", URL: update.GitHubRepo + "/issues"})
	add(panes.Field{Label: "releases", Value: repo + "/releases", URL: update.DefaultRepo + "/releases"})
	add(panes.Field{Label: "changelog", Value: repo + "/…/CHANGELOG.md", URL: update.DefaultRepo + "/src/branch/main/CHANGELOG.md"})
	return append(out, m.changelog...)
}

// updateStatus says whether a newer release is out, and when it was
// last checked.
func (m Model) updateStatus() string {
	switch {
	case m.updated != "":
		return "updated to " + m.updated + ": restart to use it"
	case m.newer != nil:
		return m.newer.Tag + " is out" + withKey(m.keys.Update, " · %s updates")
	}
	at := m.store.UpdateCheck().At
	if at.IsZero() {
		return ""
	}
	return "up to date · checked " + ago(m.now().Sub(at))
}

// accountStatus says whether an account's sources loaded.
func (m Model) accountStatus(p provider.Provider) string {
	r, done := m.sourceResults[p.ID()]
	var auth *provider.AuthError
	switch {
	case !done:
		return "loading…"
	case errors.As(r.err, &auth) && len(auth.Login) > 0:
		return "sign-in required" + withKey(m.keys.Login, " · %s signs in")
	case r.err != nil:
		return r.err.Error()
	}
	noun := map[provider.Kind]string{provider.KindAzure: "subscription", provider.KindDatabricks: "workspace", provider.KindRepo: "repo"}[p.Kind()]
	return plural(len(r.sources), cmp.Or(noun, "source"))
}

// ago is a duration as people say it: "just now", "5m ago", "2h ago".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// changelogFields are CHANGELOG.md as rows: each release's version and
// date, then its entries, marked by what kind they are.
func changelogFields(md string) []panes.Field {
	var out []panes.Field
	kind := ""
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "## "):
			version, date, _ := strings.Cut(strings.TrimPrefix(line, "## "), " – ")
			out = append(out, panes.Field{Label: version, Value: date, Info: true})
			kind = ""
		case strings.HasPrefix(line, "### "):
			kind = strings.ToLower(strings.TrimPrefix(line, "### "))
		case strings.HasPrefix(line, "- ") && len(out) > 0:
			text := update.PlainLinks(strings.TrimPrefix(line, "- "))
			if kind != "" {
				text = kind + ": " + text
			}
			out = append(out, panes.Field{Value: text})
		case line != "" && len(out) > 0 && !strings.HasPrefix(line, "<!--"):
			// A release's own words ("Hello world 👋").
			out = append(out, panes.Field{Value: update.PlainLinks(line)})
		}
	}
	return out
}
