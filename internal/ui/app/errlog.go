package app

import (
	"errors"
	"slices"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// The status pane lists this session's errors, newest first: what a pane
// showed (a list that couldn't load) and what the footer flashed (a copy
// that failed), which go when you move on. Only the first line of each,
// and never kept after quitting.

// maxErrors is how many errors are kept.
const maxErrors = 20

type loggedErr struct {
	at   time.Time
	text string
}

// logErr records text; an error already there moves to the top, with
// the time it happened again.
func (m *Model) logErr(text string) {
	text, _, _ = strings.Cut(strings.TrimSpace(text), "\n")
	if text == "" {
		return
	}
	m.errLog = slices.DeleteFunc(m.errLog, func(e loggedErr) bool { return e.text == text })
	m.errLog = append([]loggedErr{{m.now(), text}}, m.errLog[:min(len(m.errLog), maxErrors-1)]...)
}

// logErrOf records err, about what, except a sign-in, which the footer
// and the account's details say.
func (m *Model) logErrOf(what string, err error) {
	var auth *provider.AuthError
	if !errors.As(err, &auth) {
		m.logErr(what + ": " + err.Error())
	}
}

// logFlash records an error the footer is showing, once.
func (m *Model) logFlash() {
	if m.flash.isErr && m.flash.text != m.loggedFlash {
		m.logErr(m.flash.text)
	}
	m.loggedFlash = m.flash.text
}

// errorFields are the status pane's rows for them.
func (m Model) errorFields() []panes.Field {
	out := make([]panes.Field, len(m.errLog))
	for i, e := range m.errLog {
		out[i] = panes.Field{Value: e.at.Format("15:04") + " " + e.text, Err: true}
	}
	if len(out) > 0 {
		out[0].Label = "errors"
	}
	return out
}
