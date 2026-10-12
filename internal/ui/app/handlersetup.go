package app

import tea "charm.land/bubbletea/v2"

// At startup, if lazysecrets:// links don't open lazysecrets, a popup
// offers to set that up, once per start until the user says not to ask
// again; that choice is kept in the store.

// HandlerSetup offers to set up the link handler.
type HandlerSetup struct {
	Reason string // why links don't open lazysecrets
	// Install sets it up, returning what links open in ("kitty").
	Install func() (string, error)
}

const (
	flagNoHandlerPrompt = "noLinkHandlerPrompt"
	setupTitle          = "Open links in lazysecrets?"
)

// deferHandlerSetup puts the question back to show later, if a link
// popup is about to replace it.
func (m *Model) deferHandlerSetup() {
	if m.prompt != nil && m.prompt.title == setupTitle {
		m.askHandler = true
	}
}

type handlerInstalledMsg struct {
	opens string
	err   error
}

// askHandlerSetup shows the popup, once no other popup is in the way.
func (m *Model) askHandlerSetup() {
	if !m.askHandler || m.prompt != nil || m.search != nil {
		return
	}
	m.askHandler = false
	setup := m.handlerSetup
	m.prompt = &modal{
		title: setupTitle,
		lines: []string{
			setup.Reason + ".",
			m.st.Muted.Render("Set up, clicking a lazysecrets:// link (in Slack, a browser…) opens it here."),
		},
		options: []option{
			{key: "i", label: "Set it up", run: func(*Model) tea.Cmd {
				return func() tea.Msg {
					opens, err := setup.Install()
					return handlerInstalledMsg{opens, err}
				}
			}},
			{key: "s", label: "Not now", tip: "Asks again next time lazysecrets starts.", run: func(*Model) tea.Cmd { return nil }},
			{key: "n", label: "Don't ask again", tip: "lazysecrets handler install sets it up later.", run: func(m *Model) tea.Cmd {
				if err := m.store.SetFlag(flagNoHandlerPrompt, true); err != nil {
					m.flash = flash{text: "saving: " + err.Error(), isErr: true}
				}
				return nil
			}},
		},
	}
}

func (m *Model) handlerInstalled(msg handlerInstalledMsg) {
	switch {
	case msg.err != nil:
		m.flash = flash{text: "setting up links: " + msg.err.Error(), isErr: true}
	case msg.opens != "":
		m.flash = done("lazysecrets:// links now open here, or in a new " + msg.opens + " window")
	default:
		m.flash = done("lazysecrets:// links now open in lazysecrets")
	}
}
