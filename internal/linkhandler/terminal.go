// Package linkhandler makes lazysecrets:// links open lazysecrets: it
// registers the scheme with the operating system, and launches a new
// terminal window running "lazysecrets open <link>" when a link is opened.
//
// The terminal is the one install ran in, detected from its environment,
// unless the config names another or gives a command of its own.
package linkhandler

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Terminals lists the terminals launch knows, by name.
var Terminals = []string{"kitty", "ghostty", "wezterm", "iterm2", "terminal", "alacritty", "gnome-terminal", "konsole", "foot"}

// Detect names the terminal from its environment (getenv), or "" if it
// isn't one launch knows. Terminal-specific variables come first: inside
// tmux, TERM_PROGRAM is "tmux" but the terminal's own variables are
// usually inherited.
func Detect(getenv func(string) string) string {
	switch {
	case getenv("KITTY_WINDOW_ID") != "" || getenv("TERM") == "xterm-kitty":
		return "kitty"
	case getenv("GHOSTTY_RESOURCES_DIR") != "" || getenv("TERM") == "xterm-ghostty":
		return "ghostty"
	case getenv("WEZTERM_EXECUTABLE") != "" || getenv("WEZTERM_PANE") != "":
		return "wezterm"
	case getenv("LC_TERMINAL") == "iTerm2" || getenv("ITERM_SESSION_ID") != "":
		return "iterm2"
	case getenv("ALACRITTY_WINDOW_ID") != "" || getenv("ALACRITTY_SOCKET") != "":
		return "alacritty"
	case getenv("KONSOLE_VERSION") != "":
		return "konsole"
	case getenv("GNOME_TERMINAL_SCREEN") != "":
		return "gnome-terminal"
	case getenv("TERM") == "foot":
		return "foot"
	}
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return "iterm2"
	case "Apple_Terminal":
		return "terminal"
	case "WezTerm":
		return "wezterm"
	case "ghostty":
		return "ghostty"
	}
	return ""
}

// Placeholder in a custom command stands for the command to run in it.
const Placeholder = "{cmd}"

// Command is the command that opens a new window of terminal running
// inner, for the operating system goos. custom, if set, is used instead,
// with Placeholder replaced by inner's arguments.
func Command(goos, terminal string, custom, inner []string) ([]string, error) {
	if len(custom) > 0 {
		if !slices.Contains(custom, Placeholder) {
			return nil, fmt.Errorf("linkHandler.command must contain %s, where the lazysecrets command goes", Placeholder)
		}
		var out []string
		for _, a := range custom {
			if a == Placeholder {
				out = append(out, inner...)
			} else {
				out = append(out, a)
			}
		}
		return out, nil
	}
	if goos == "darwin" {
		// Apps are started through open(1), so they needn't be on PATH.
		// -n: a new instance, which opens a window even if one runs.
		app := func(name string, args ...string) []string {
			return append([]string{"open", "-na", name, "--args"}, append(args, inner...)...)
		}
		switch terminal {
		case "kitty":
			// Its own instance, so quit it with the window: kitty
			// otherwise stays running without windows.
			return app("kitty", "-o", "macos_quit_when_last_window_closed=yes"), nil
		case "ghostty":
			return app("Ghostty", "-e"), nil
		case "wezterm":
			return app("WezTerm", "start", "--"), nil
		case "alacritty":
			return app("Alacritty", "-e"), nil
		case "iterm2":
			return osascript(`tell application "iTerm"
	activate
	create window with default profile command %s
end tell`, inner), nil
		case "terminal":
			return osascript(`tell application "Terminal"
	activate
	do script %s
end tell`, inner), nil
		}
	} else {
		switch terminal {
		case "kitty", "foot":
			return append([]string{terminal}, inner...), nil
		case "ghostty", "alacritty", "konsole":
			return append([]string{terminal, "-e"}, inner...), nil
		case "wezterm":
			return append([]string{"wezterm", "start", "--"}, inner...), nil
		case "gnome-terminal":
			return append([]string{"gnome-terminal", "--"}, inner...), nil
		}
	}
	if terminal == "" {
		return nil, fmt.Errorf("no terminal set: run lazysecrets handler install from your terminal, or set linkHandler.terminal or linkHandler.command in the config")
	}
	return nil, fmt.Errorf("unknown terminal %q (known: %s); linkHandler.command runs any other", terminal, strings.Join(Terminals, ", "))
}

// osascript runs an AppleScript with inner, shell-quoted, as a string
// literal in place of %s.
func osascript(script string, inner []string) []string {
	return []string{"osascript", "-e", fmt.Sprintf(script, appleString(shellJoin(inner)))}
}

// shellJoin quotes args for a POSIX shell.
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// appleString is s as an AppleScript string literal.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Launch opens a new terminal window running argv, without waiting for
// it.
func Launch(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Executable is the path of the running lazysecrets, to register. A
// binary from "go run" is deleted when it exits, so it's refused.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.Contains(exe, "go-build") {
		return "", fmt.Errorf("this is a temporary binary from go run: build or install lazysecrets first (go install ./cmd/lazysecrets), then run handler install with it")
	}
	return exe, nil
}

// passEnv lists the variables carried from the install's environment to
// lazysecrets in the new window: apps start with a minimal environment,
// without Homebrew's PATH (so no az) or the user's XDG directories, so
// the window would use another cache and miss running instances' sockets.
var passEnv = []string{
	"PATH", "LAZYSECRETS_CONFIG_FILE", "LAZYSECRETS_ICONS",
	"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "XDG_DATA_HOME",
	"AZURE_CONFIG_DIR", "DATABRICKS_CONFIG_FILE",
}

// Env returns NAME=value for each variable to carry that is set.
func Env(getenv func(string) string) []string {
	var out []string
	for _, k := range passEnv {
		if v := getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
