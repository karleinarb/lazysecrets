package app

import (
	"fmt"
	"math"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
)

// A revealed value doesn't stay on screen: it hides after the configured
// timeout, counting down in the detail pane, and when the terminal loses
// focus (switching windows, screen sharing another app).

// startRevealTimer starts the countdown for a value just revealed.
func (m *Model) startRevealTimer() tea.Cmd {
	m.revealToken++
	if m.revealTimeout <= 0 {
		m.revealDeadline = time.Time{}
		return nil
	}
	m.revealDeadline = m.now().Add(m.revealTimeout)
	m.showHidesIn()
	return m.revealTickCmd()
}

// revealTickCmd fires when the displayed second next changes.
func (m *Model) revealTickCmd() tea.Cmd {
	token := m.revealToken
	return tea.Tick(untilNextSecond(m.revealDeadline, m.now()), func(time.Time) tea.Msg { return revealTickMsg{token} })
}

// untilNextSecond is how long until a countdown to deadline shows its
// next whole second.
func untilNextSecond(deadline, now time.Time) time.Duration {
	if wait := deadline.Sub(now) % time.Second; wait > 0 {
		return wait
	}
	return time.Second
}

// secondsLeft is a countdown to deadline, in whole seconds rounded up.
func secondsLeft(deadline, now time.Time) int {
	return max(0, int(math.Ceil(deadline.Sub(now).Seconds())))
}

func (m *Model) revealTick(msg revealTickMsg) tea.Cmd {
	if msg.token != m.revealToken || m.revealDeadline.IsZero() || m.detail.State() != panes.ValueShown {
		return nil // hidden meanwhile, or another value shown
	}
	if !m.now().Before(m.revealDeadline) {
		m.hideValue()
		return nil
	}
	m.showHidesIn()
	return m.revealTickCmd()
}

func (m *Model) showHidesIn() {
	m.detail.SetHidesIn(fmt.Sprintf("%ds", secondsLeft(m.revealDeadline, m.now())))
}

// hideValue hides the revealed value and stops its countdown.
func (m *Model) hideValue() {
	m.detail.Hide()
	m.revealToken++
	m.revealDeadline = time.Time{}
}
