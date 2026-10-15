package app

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/commands"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
)

// Custom commands (customCommands in the config) run a shell command on
// the selected item: by their key in the pane they're for, or from the
// commands menu (x). Prompts ask for text first; commands that get the
// secret value ask before running. The value reaches the command on its
// input or in an environment variable, never on its command line.

// commandTimeout bounds commands whose output is captured; terminal ones
// run as long as the user likes.
const commandTimeout = 5 * time.Minute

type commandValueMsg struct {
	cmd      commands.Command
	rendered string
	value    provider.SecretValue
	err      error
}

type commandDoneMsg struct {
	cmd commands.Command
	out string
	err error
}

// paneContext is the custom command context of a pane.
func paneContext(p pane) string {
	switch p {
	case paneSources:
		return commands.Sources
	case paneContainers:
		return commands.Vaults
	}
	return commands.Secrets
}

// commandsHere are the custom commands for the focused pane.
func (m *Model) commandsHere() []commands.Command {
	if m.focus == paneStatus {
		return nil
	}
	var out []commands.Command
	for _, c := range m.commands {
		if c.Context == commands.Global || c.Context == paneContext(m.focus) {
			out = append(out, c)
		}
	}
	return out
}

// commandForKey is the custom command a key runs here, if any.
func (m *Model) commandForKey(msg tea.KeyPressMsg) (commands.Command, bool) {
	for _, c := range m.commandsHere() {
		if c.Key != "" && c.Key == msg.String() {
			return c, true
		}
	}
	return commands.Command{}, false
}

// showCommands lists the custom commands for the focused pane.
func (m *Model) showCommands() tea.Cmd {
	cmds := m.commandsHere()
	if len(cmds) == 0 {
		m.flash = warning("no custom commands for the " + strings.ToLower(paneNames[m.focus]) + " pane: see customCommands in the config")
		return nil
	}
	var opts []option
	for _, c := range cmds {
		label := cmp.Or(c.Description, c.Command)
		if c.Key != "" {
			label = m.st.Label.Render(fmt.Sprintf("%-8s", c.Key)) + label
		}
		opts = append(opts, option{label: label, tip: c.Command, run: func(m *Model) tea.Cmd { return m.startCommand(c) }})
	}
	return m.showFilterMenu("Commands", opts)
}

// startCommand runs c on the selection: prompts, then confirmation, then
// the command.
func (m *Model) startCommand(c commands.Command) tea.Cmd {
	it, obj, ok := m.commandItem(c.Context)
	if !ok {
		m.flash = warning(map[string]string{
			commands.Sources: "select a subscription or workspace to run " + c.Description + " on",
			commands.Vaults:  "select a vault or scope to run " + c.Description + " on",
			commands.Secrets: "select a secret to run " + c.Description + " on",
		}[c.Context])
		return nil
	}
	it.Prompt = map[string]commands.Q{}
	return m.askPrompt(c, it, obj, 0)
}

// askPrompt asks c's prompts in turn, then confirms or runs it.
func (m *Model) askPrompt(c commands.Command, it commands.Item, obj *provider.Object, i int) tea.Cmd {
	if i == len(c.Prompts) {
		return m.confirmCommand(c, it, obj)
	}
	p := c.Prompts[i]
	in := newTextInput(p.Title+": ", 48)
	in.SetValue(p.Default)
	in.CursorEnd()
	m.prompt = &modal{
		title: cmp.Or(c.Description, "Command"), input: &in,
		submit: func(m *Model, text string) tea.Cmd {
			it.Prompt[p.Name] = commands.Q(text)
			return m.askPrompt(c, it, obj, i+1)
		},
		footer: footer(hint(m.keys.PopupSelect, "next"), hint(m.keys.PopupClose, "cancel")),
	}
	return m.prompt.input.Focus()
}

func (m *Model) confirmCommand(c commands.Command, it commands.Item, obj *provider.Object) tea.Cmd {
	rendered, err := c.Render(it)
	if err != nil {
		m.flash = flash{text: c.Description + ": " + err.Error(), isErr: true}
		return nil
	}
	if !c.Confirms() {
		return m.runCommand(c, rendered, obj)
	}
	var lines []string
	for _, l := range strings.Split(ansi.Wrap(rendered, 70, " "), "\n") {
		lines = append(lines, m.st.Muted.Render(l))
	}
	switch {
	case c.Value == "stdin":
		lines = append(lines, "", "It gets the value of "+string(it.Name)+" on its input.")
	case strings.HasPrefix(c.Value, "env:"):
		lines = append(lines, "", "It gets the value of "+string(it.Name)+" in $"+strings.TrimPrefix(c.Value, "env:")+".")
	}
	m.showMenu("Run "+cmp.Or(c.Description, "command")+"?", lines, []option{
		{key: "y", label: "Run it", run: func(m *Model) tea.Cmd { return m.runCommand(c, rendered, obj) }},
	})
	return nil
}

// runCommand fetches the value if the command takes it, then runs it.
func (m *Model) runCommand(c commands.Command, rendered string, obj *provider.Object) tea.Cmd {
	if c.Value == "" || obj == nil {
		return m.execCommand(c, rendered, provider.SecretValue{})
	}
	o := *obj
	p := m.providerFor(o.Container.Source)
	m.flash = flash{text: "fetching " + o.Name + " for " + c.Description + "…"}
	timeout := m.timeout.of(o.Container.Source.Kind)
	m.usedObject(o)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		v, err := p.Value(ctx, o)
		return commandValueMsg{cmd: c, rendered: rendered, value: v, err: err}
	}
}

// execCommand runs the command as its output mode says.
func (m *Model) execCommand(c commands.Command, rendered string, value provider.SecretValue) tea.Cmd {
	shell := cmp.Or(os.Getenv("SHELL"), "/bin/sh")
	setValue := func(cmd *exec.Cmd) {
		switch {
		case c.Value == "stdin":
			cmd.Stdin = strings.NewReader(value.Reveal())
		case strings.HasPrefix(c.Value, "env:"):
			cmd.Env = append(os.Environ(), strings.TrimPrefix(c.Value, "env:")+"="+value.Reveal())
		}
	}
	if c.Out() == commands.Terminal {
		// Pause before coming back, reading the terminal itself: the
		// command's input may be taken.
		script := rendered + "\nprintf '\\n%s' 'Press enter to return to lazysecrets'; read -r _ </dev/tty"
		cmd := exec.Command(shell, "-c", script)
		setValue(cmd)
		m.flash = flash{}
		return tea.ExecProcess(cmd, func(err error) tea.Msg { return commandDoneMsg{cmd: c, err: err} })
	}
	m.flash = flash{text: "running " + cmp.Or(c.Description, "command") + "…"}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, shell, "-c", rendered)
		setValue(cmd)
		out, err := cmd.CombinedOutput()
		return commandDoneMsg{cmd: c, out: string(out), err: err}
	}
}

func (m *Model) handleCommandValue(msg commandValueMsg) tea.Cmd {
	if msg.err != nil {
		m.noteErr(msg.err)
		m.flash = flash{text: msg.cmd.Description + ": " + msg.err.Error(), isErr: true}
		return nil
	}
	return m.execCommand(msg.cmd, msg.rendered, msg.value)
}

// maxOutputLines caps what a popup shows of a command's output.
const maxOutputLines = 30

func (m *Model) handleCommandDone(msg commandDoneMsg) {
	name := cmp.Or(msg.cmd.Description, "command")
	if msg.cmd.Out() != commands.Popup || m.prompt != nil || m.search != nil {
		switch {
		case msg.err != nil:
			out := strings.TrimSpace(msg.out)
			if i := strings.LastIndexByte(out, '\n'); i >= 0 {
				out = out[i+1:] // the last line, usually the error
			}
			m.flash = flash{text: strings.TrimSpace(name + " failed: " + msg.err.Error() + " " + out), isErr: true}
		default:
			m.flash = done(name)
		}
		return
	}
	lines := strings.Split(strings.TrimRight(ansi.Strip(msg.out), "\n"), "\n")
	if len(lines) > maxOutputLines {
		lines = append(lines[:maxOutputLines], m.st.Muted.Render(fmt.Sprintf("… %d more lines", len(lines)-maxOutputLines)))
	}
	title := "✓ " + name
	if msg.err != nil {
		title = "✗ " + name + ": " + msg.err.Error()
	}
	m.showMenu(title, lines, nil)
}

// commandItem fills a command's fields from the selection: the source,
// and the vault and secret as deep as the context goes (global: as deep
// as the focus). ok is false if the context's item isn't selected.
func (m *Model) commandItem(ctx string) (commands.Item, *provider.Object, bool) {
	depth := map[string]int{commands.Sources: 0, commands.Vaults: 1, commands.Secrets: 2}[ctx]
	if ctx == commands.Global {
		depth = min(int(m.focus), 2)
	}
	var it commands.Item
	src, ok := m.selectedSource()
	if !ok && depth == 0 {
		return it, nil, false
	}
	if ok {
		it.Kind, it.Account = commands.Q(strings.ToLower(string(src.Kind))), commands.Q(accountOf(src))
		it.Source, it.SourceID, it.URL = commands.Q(src.Name), commands.Q(src.ID), commands.Q(src.URL)
		it.Note = commands.Q(m.store.Node(sourceKey(src)).Note)
		if r, err := ref.ForSource(src); err == nil {
			it.Link = commands.Q(r.String())
		}
	}
	if depth == 0 {
		return it, nil, true
	}
	c, ok := m.containers.Selected()
	if !ok {
		return it, nil, ctx == commands.Global
	}
	it.Vault, it.URL, it.Note = commands.Q(c.Name), commands.Q(c.URL), commands.Q(m.store.Node(storeKey(c)).Note)
	if c.Source.Kind == provider.KindAzure {
		it.VaultURL = commands.Q(c.Detail)
	}
	if r, err := ref.ForContainer(c, m.objType); err == nil {
		it.Link = commands.Q(r.String())
	}
	if depth == 1 {
		return it, nil, true
	}
	o, ok := m.detail.Secret()
	if !ok {
		return it, nil, ctx == commands.Global
	}
	version := ""
	if m.versionsOf != nil {
		version = o.Version // run from the versions view: that version
	}
	it.Name, it.Type, it.Version = commands.Q(o.Name), commands.Q(strings.ToLower(string(o.Type))), commands.Q(version)
	it.Reference, it.URL, it.Note = commands.Q(o.Reference), commands.Q(o.URL), commands.Q(m.store.Node(objectKey(o)).Note)
	if !o.Expires.IsZero() {
		it.Expires = commands.Q(o.Expires.Format(time.RFC3339))
	}
	if r, err := ref.ForObject(o, version); err == nil {
		it.Link = commands.Q(r.String())
	}
	o.Version = version // "": the current value, like copying it
	return it, &o, true
}

func newTextInput(prompt string, width int) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.SetWidth(width)
	return in
}
