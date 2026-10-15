// Package clipboard copies text to the system clipboard, or via the OSC52
// terminal escape sequence over SSH or when no native clipboard exists.
package clipboard

import (
	"crypto/sha256"
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

type Clipboard struct {
	native bool
	read   func() (string, error)
	write  func(string) error
}

// ErrorMsg reports a failed clipboard write.
type ErrorMsg struct{ Err error }

// New picks the native clipboard locally and OSC52 over SSH.
// LAZYSECRETS_CLIPBOARD_FILE substitutes a file, for end-to-end tests that
// mustn't touch the real clipboard.
func New() Clipboard {
	if path := os.Getenv("LAZYSECRETS_CLIPBOARD_FILE"); path != "" {
		return Clipboard{
			native: true,
			read: func() (string, error) {
				b, err := os.ReadFile(path)
				return string(b), err
			},
			write: func(s string) error { return os.WriteFile(path, []byte(s), 0o600) },
		}
	}
	remote := os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
	return Clipboard{
		native: !remote && !clipboard.Unsupported,
		read:   clipboard.ReadAll,
		write:  clipboard.WriteAll,
	}
}

// Write puts s on the clipboard.
func (c Clipboard) Write(s string) tea.Cmd {
	if !c.native {
		return tea.SetClipboard(s)
	}
	return func() tea.Msg {
		if err := c.write(s); err != nil {
			return ErrorMsg{Err: err}
		}
		return nil
	}
}

// ErrNoRead is returned where the clipboard can't be read back (OSC52).
var ErrNoRead = errors.New("clipboard can't be read here")

// Read returns the clipboard's text, where that's possible.
func (c Clipboard) Read() (string, error) {
	if !c.native {
		return "", ErrNoRead
	}
	return c.read()
}

// Fingerprint identifies copied text for Clear without keeping it in memory.
func Fingerprint(s string) [32]byte { return sha256.Sum256([]byte(s)) }

// Clear empties the clipboard if it still holds the text with fingerprint
// fp, so it doesn't wipe something the user copied since. OSC52 can't read
// the clipboard back, so there it always clears.
func (c Clipboard) Clear(fp [32]byte) tea.Cmd {
	if !c.native {
		return tea.SetClipboard("")
	}
	return func() tea.Msg {
		cur, err := c.read()
		if err != nil || Fingerprint(cur) != fp {
			return nil
		}
		if err := c.write(""); err != nil {
			return ErrorMsg{Err: err}
		}
		return nil
	}
}
