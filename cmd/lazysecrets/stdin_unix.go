//go:build unix

package main

import (
	"os"
	"syscall"

	"golang.org/x/term"
)

// blockingStdin makes a terminal on stdin blocking again. fzf's become
// hands on its /dev/tty non-blocking; Go can't poll /dev/tty on macOS,
// so it reads it directly, and the TUI's first read would fail with
// EAGAIN ("resource temporarily unavailable").
func blockingStdin() {
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		_ = syscall.SetNonblock(fd, false)
	}
}
