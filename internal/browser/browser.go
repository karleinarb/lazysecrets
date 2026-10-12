// Package browser opens URLs in the user's web browser.
package browser

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// ErrRemote is returned over SSH, where a browser would open on the remote
// machine rather than in front of the user.
var ErrRemote = errors.New("can't open a browser over SSH")

// Open starts the system's URL handler without waiting for it.
func Open(url string) error {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return ErrRemote
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap the process
	return nil
}
