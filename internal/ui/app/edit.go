package app

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/browser"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

// Opening a repo's file (e), or on the status pane the config: SOPS files with sops edit, which decrypts
// them for the editor and encrypts them again after. Other files, inside
// nvim: in that nvim, once lazysecrets has quit; else in $VISUAL or
// $EDITOR, coming back after; else in the system's default app. On a
// secret, editors that can open at a line start on its key.

type editDoneMsg struct {
	c      provider.Container
	name   string
	config bool // the config file: read at startup
	err    error
}

// sopsUnchanged is sops edit's exit code when the file wasn't changed.
const sopsUnchanged = 200

// edit opens the selected file, or the file of the selected secret.
func (m *Model) edit() tea.Cmd {
	if m.focus == paneDetail && m.fields.Shown() {
		f, ok := m.fields.Selected()
		if !ok || f.Path == "" {
			m.flash = warning("nothing to open in an editor here")
			return nil
		}
		name := filepath.Base(f.Path)
		return m.openInEditor(provider.File{Path: f.Path}, func(err error) tea.Msg { return editDoneMsg{name: name, err: err} })
	}
	var c provider.Container
	var o *provider.Object
	switch m.focus {
	case paneStatus: // like lazygit's status panel
		return m.editConfig()
	case paneSources:
		m.flash = warning("select a file to open it")
		return nil
	case paneContainers:
		sel, ok := m.containers.Selected()
		if !ok {
			return nil
		}
		c = sel
	default:
		sel, ok := m.detail.Secret()
		if !ok {
			return nil
		}
		c, o = sel.Container, &sel
	}
	files, ok := m.providerFor(c.Source).(provider.Files)
	if !ok {
		m.flash = warning("only SOPS and .env files in repos can be opened")
		return nil
	}
	f, err := files.File(c, o)
	if err != nil {
		m.flash = flash{text: "open: " + err.Error(), isErr: true}
		return nil
	}
	if o != nil {
		m.usedObject(*o)
	} else {
		m.usedContainer(c)
	}
	name := filepath.Base(f.Path)
	finished := func(err error) tea.Msg { return editDoneMsg{c: c, name: name, err: err} }

	if f.Sops != "" {
		cmd := exec.Command(f.Sops, "edit", f.Path)
		cmd.Dir = filepath.Dir(f.Path) // sops looks for .sops.yaml from here
		// sops starts SOPS_EDITOR (else EDITOR, else vim) on the decrypted
		// copy, which keeps the file's lines: start it on the key.
		editor := cmp.Or(m.getenv("SOPS_EDITOR"), m.getenv("EDITOR"), "vim")
		if at, ok := editorAtLine(editor, f.Line); ok {
			cmd.Env = append(os.Environ(), "SOPS_EDITOR="+at)
		}
		m.flash = flash{}
		return tea.ExecProcess(cmd, finished)
	}
	return m.openInEditor(f, finished)
}

// configHeader starts a config file made to be edited.
const configHeader = "# lazysecrets config: " + update.DefaultRepo + "/src/branch/main/docs/config.md\n"

// editConfig opens the user's config file (the first of them), as e
// opens a file, making it if it isn't there yet: e on the status pane.
func (m *Model) editConfig() tea.Cmd {
	if len(m.configFiles) == 0 {
		m.flash = warning("no config file")
		return nil
	}
	path := m.configFiles[0]
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err == nil {
			err = os.WriteFile(path, []byte(configHeader), 0o644)
		}
		if err != nil {
			m.flash = flash{text: "edit config: " + err.Error(), isErr: true}
			return nil
		}
	}
	name := filepath.Base(path)
	return m.openInEditor(provider.File{Path: path}, func(err error) tea.Msg {
		return editDoneMsg{name: name, config: true, err: err}
	})
}

// plusLine are editors that open at a line with +N before the file.
var plusLine = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "nano": true, "emacs": true, "emacsclient": true,
	"micro": true, "kak": true, "mg": true, "ne": true, "joe": true, "vis": true,
}

// editorAtLine is editor starting at line, for editors that take +N:
// sops adds the file after it.
func editorAtLine(editor string, line int) (string, bool) {
	fields := strings.Fields(editor)
	if line == 0 || len(fields) == 0 || !plusLine[filepath.Base(fields[0])] {
		return "", false
	}
	return editor + " +" + strconv.Itoa(line), true
}

// editorArgs opens f at its line, for editors known to take one.
func editorArgs(editor string, f provider.File) []string {
	fields := strings.Fields(editor)
	if f.Line == 0 || len(fields) == 0 {
		return []string{f.Path}
	}
	at := f.Path + ":" + strconv.Itoa(f.Line)
	switch base := filepath.Base(fields[0]); {
	case plusLine[base]:
		return []string{"+" + strconv.Itoa(f.Line), f.Path}
	case base == "hx" || base == "helix" || base == "subl" || base == "zed":
		return []string{at}
	case base == "code" || base == "codium" || base == "cursor" || base == "windsurf":
		return []string{"-g", at}
	}
	return []string{f.Path}
}

// handleEditDone reports how editing went and reloads the file's secrets:
// their names, lines or values may have changed.
func (m *Model) handleEditDone(msg editDoneMsg) tea.Cmd {
	var exit *exec.ExitError
	switch {
	case errors.As(msg.err, &exit) && exit.ExitCode() == sopsUnchanged:
		m.flash = flash{text: msg.name + " unchanged"}
		return nil
	case errors.Is(msg.err, exec.ErrNotFound):
		m.flash = flash{text: "open " + msg.name + ": " + msg.err.Error() + " (is sops installed?)", isErr: true}
		return nil
	case msg.err != nil:
		m.flash = flash{text: "open " + msg.name + ": " + msg.err.Error(), isErr: true}
		return nil
	}
	if msg.config {
		m.flash = done("closed " + msg.name + ": restart lazysecrets to use it")
		return nil
	}
	m.flash = done("closed " + msg.name)
	if c, ok := m.containers.Selected(); ok && containerKey(c) == containerKey(msg.c) {
		m.hideValue()
		m.secrets.Refresh()
		return m.loadObjects(c, m.objType, m.selContainer)
	}
	return nil
}

// nvimLua opens a file in the nvim lazysecrets runs in. If lazysecrets is
// that terminal's own process (the nvim plugin, or :terminal lazysecrets),
// it waits for the terminal to close, so the file opens in the window
// below rather than in one about to close. Otherwise (a shell in a
// terminal) it opens it in the previous window now. The autocmd isn't
// the buffer's own: snacks wipes the buffer when the terminal closes,
// which would delete it before it runs.
const nvimLua = `local path, line, pid = ...
local function open()
  local ok, err = pcall(vim.cmd, "edit " .. vim.fn.fnameescape(path))
  if not ok then vim.notify("lazysecrets: " .. err, vim.log.levels.ERROR) return end
  if line > 0 then pcall(vim.api.nvim_win_set_cursor, 0, { line, 0 }) vim.cmd("normal! zz") end
end
for _, b in ipairs(vim.api.nvim_list_bufs()) do
  if vim.bo[b].buftype == "terminal" and vim.b[b].terminal_job_pid == pid then
    vim.api.nvim_create_autocmd("TermClose", { callback = function(ev)
      if ev.buf ~= b then return end
      vim.defer_fn(open, 50)
      return true
    end })
    return
  end
end
pcall(vim.cmd, "wincmd p")
open()`

// nvimOpen asks the nvim at server to open path at line.
func nvimOpen(server, path string, line int) error {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	lua := strings.ReplaceAll(nvimLua, "\n", " ")
	expr := "luaeval('load(_A[1])(_A[2], _A[3], _A[4])', [" + q(lua) + ", " + q(path) + ", " +
		strconv.Itoa(line) + ", " + strconv.Itoa(os.Getpid()) + "])"
	out, err := exec.Command("nvim", "--server", server, "--remote-expr", expr).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// openInEditor opens f: in the nvim lazysecrets runs in, quitting; else
// in $VISUAL or $EDITOR, coming back after; else in the default app.
func (m *Model) openInEditor(f provider.File, finished func(error) tea.Msg) tea.Cmd {
	name := filepath.Base(f.Path)
	if server := m.getenv("NVIM"); server != "" {
		open := m.nvimOpen
		return func() tea.Msg {
			if err := open(server, f.Path, f.Line); err != nil {
				return finished(err)
			}
			return tea.QuitMsg{}
		}
	}
	if editor := cmpOr(m.getenv("VISUAL"), m.getenv("EDITOR")); editor != "" {
		// Through the shell, so EDITOR can carry arguments ("code -w").
		args := append([]string{"-c", editor + ` "$@"`, "lazysecrets"}, editorArgs(editor, f)...)
		m.flash = flash{}
		return tea.ExecProcess(exec.Command("/bin/sh", args...), finished)
	}
	switch err := m.openURL(f.Path); {
	case errors.Is(err, browser.ErrRemote):
		m.flash = warning("set $EDITOR to open files over SSH")
	case err != nil:
		m.flash = flash{text: "open: " + err.Error(), isErr: true}
	default:
		m.flash = done("opened " + name)
	}
	return nil
}
