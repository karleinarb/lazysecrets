package app

import (
	"charm.land/lipgloss/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// Layout: the left column stacks Sources, Vaults and Secrets; the detail
// pane fills the rest. The user can move the divider between them, change
// the left panes' heights, and zoom the focused pane.

type zoom int

const (
	zoomNormal zoom = iota
	zoomHalf        // the focused pane gets most of the screen
	zoomFull        // only the focused pane
	zoomCount
)

const (
	// Divider between the left column and the detail pane.
	splitStep   = 4
	minLeftW    = 20
	minDetailW  = 30
	defaultMinW = 28
	defaultMaxW = 48

	// Heights of the left panes.
	heightStep = 2
	minPaneH   = 4 // border, title and two rows
	tinyPaneH  = 3 // a squeezed pane in half zoom: border and one row
)

func (m Model) defaultLeftWidth() int { return max(defaultMinW, min(defaultMaxW, m.w/3)) }

// leftWidth is the default width moved by the user's divider offset,
// kept within bounds that leave both sides usable.
func (m Model) leftWidth() int {
	return max(minLeftW, min(m.w-minDetailW, m.defaultLeftWidth()+m.split))
}

// moveSplit moves the divider by delta columns. The offset is stored as
// what actually applied, so after hitting a limit the other direction
// responds immediately.
func (m *Model) moveSplit(delta int) {
	m.split += delta
	m.split = m.leftWidth() - m.defaultLeftWidth()
}

func defaultHeights(avail int) [3]int {
	src, cont := max(minPaneH, avail/4), max(minPaneH, avail/3)
	return [3]int{src, cont, avail - src - cont}
}

// leftHeights applies the user's height changes to the default split.
func (m Model) leftHeights(avail int) [3]int {
	h := defaultHeights(avail)
	for i := range h {
		h[i] += m.heights[i]
	}
	return fitHeights(h, avail, minPaneH)
}

// fitHeights makes the heights sum to avail with none below lo, taking
// from or giving to the tallest panes.
func fitHeights(h [3]int, avail, lo int) [3]int {
	for i := range h {
		h[i] = max(lo, h[i])
	}
	for sum(h) > avail {
		i := tallest(h)
		if h[i] <= lo {
			break // the terminal is too short; the layout check catches it
		}
		h[i]--
	}
	for sum(h) < avail {
		h[tallest(h)]++
	}
	return h
}

func sum(h [3]int) int { return h[0] + h[1] + h[2] }

func tallest(h [3]int) int {
	i := 0
	for j := range h {
		if h[j] > h[i] {
			i = j
		}
	}
	return i
}

// resizeFocused makes the focused left pane taller (delta > 0) or shorter,
// trading rows with the pane below it, or above it for the bottom pane.
// It reports false if the detail pane is focused.
func (m *Model) resizeFocused(delta int) bool {
	if m.focus > paneSecrets {
		return false
	}
	avail := m.listAvail()
	h := m.leftHeights(avail)
	i := int(m.focus)
	j := i + 1
	if i == int(paneSecrets) {
		j = i - 1
	}
	delta = max(delta, minPaneH-h[i]) // don't shrink below the minimum
	delta = min(delta, h[j]-minPaneH) // or squeeze the neighbour below it
	h[i] += delta
	h[j] -= delta
	base := defaultHeights(avail)
	for k := range h {
		m.heights[k] = h[k] - base[k]
	}
	return true
}

// saveLayout remembers the divider and heights for the next run.
func (m *Model) saveLayout() {
	if err := m.store.SetLayout(store.Layout{Split: m.split, Heights: m.heights}); err != nil {
		m.flash = flash{text: "saving layout: " + err.Error(), isErr: true}
	}
}

func (m Model) leftAvail() int { return m.h - lipgloss.Height(m.footerView()) }

// listAvail is the rows the three list panes share, under the status pane.
func (m Model) listAvail() int { return m.leftAvail() - statusH }

func (m *Model) cycleZoom(delta int) {
	m.zoom = (m.zoom + zoom(delta) + zoomCount) % zoomCount
}

func (m *Model) layout() {
	if m.w < minWidth || m.h < minHeight {
		return
	}
	m.help.SetWidth(max(0, m.w-1)) // one column in, like messages
	avail := m.leftAvail()
	leftW, h := m.leftWidth(), m.leftHeights(avail-statusH)

	switch m.zoom {
	case zoomHalf:
		if m.focus == paneDetail || m.focus == paneStatus {
			leftW = minLeftW
		} else {
			leftW = max(m.leftWidth(), m.w/2)
			h = [3]int{tinyPaneH, tinyPaneH, tinyPaneH}
			h[m.focus] = avail - statusH
			h = fitHeights(h, avail-statusH, tinyPaneH)
		}
	case zoomFull:
		// Only the focused pane is drawn; it gets the whole screen.
		switch m.focus {
		case paneDetail, paneStatus:
			leftW = 0
		default:
			leftW = m.w
			h = [3]int{}
			h[m.focus] = avail
		}
	}

	m.statusW = leftW
	m.sources.SetSize(leftW, h[0])
	m.containers.SetSize(leftW, h[1])
	m.secrets.SetSize(leftW, h[2])
	m.detail.SetSize(m.w-leftW, avail)
	m.fields.SetSize(m.w-leftW, avail)
}

// active reports whether p is drawn focused: it has focus and the terminal
// does too. Like lazygit, nothing is highlighted while you're in another
// window, so the focus is easy to spot when you come back.
func (m Model) active(p pane) bool { return m.focus == p && !m.termBlurred }

func (m Model) panesView() string {
	spin := m.spin.View()
	views := [paneStatus]func() string{
		func() string { return m.sources.View(m.st, m.active(paneSources), spin) },
		func() string { return m.containers.View(m.st, m.active(paneContainers), spin) },
		func() string { return m.secrets.View(m.st, m.active(paneSecrets), spin) },
		func() string {
			if m.fields.Shown() {
				return m.fields.View(m.st, m.active(paneDetail), spin)
			}
			return m.detail.View(m.st, m.active(paneDetail), spin)
		},
	}
	if m.zoom == zoomFull {
		if m.focus == paneStatus {
			return views[paneDetail]() // its rows
		}
		return views[m.focus]()
	}
	left := lipgloss.JoinVertical(lipgloss.Left, m.statusView(), views[0](), views[1](), views[2]())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, views[3]())
}
