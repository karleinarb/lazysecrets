package linkhandler

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// macOS apps: where a link was clicked (the frontmost app; the link
// handler runs in the background, so it doesn't change it), and which app
// a process's terminal belongs to. Elsewhere these know nothing.

// App is a running macOS app: its bundle path and process ID.
type App struct {
	Bundle string
	PID    int
}

// FrontApp is the frontmost app.
func FrontApp() App {
	if runtime.GOOS != "darwin" {
		return App{}
	}
	asn, err := exec.Command("lsappinfo", "front").Output()
	if err != nil {
		return App{}
	}
	out, err := exec.Command("lsappinfo", "info", "-only", "pid", "-only", "bundlepath", strings.TrimSpace(string(asn))).Output()
	if err != nil {
		return App{}
	}
	var a App
	for _, l := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch strings.Trim(k, `"`) {
		case "pid":
			a.PID, _ = strconv.Atoi(v)
		case "LSBundlePath":
			a.Bundle = strings.Trim(v, `"`)
		}
	}
	return a
}

// HostApp is the app bundle among pid's ancestors (the terminal it runs
// in), "" if none.
func HostApp(pid int) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	for range 32 {
		out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return ""
		}
		ppid, comm, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		// The innermost bundle: kitty's quick access terminal is one
		// inside kitty.app.
		if i := strings.LastIndex(comm, ".app/Contents/MacOS/"); i >= 0 {
			return comm[:i+len(".app")]
		}
		if pid, err = strconv.Atoi(ppid); err != nil || pid <= 1 {
			return ""
		}
	}
	return ""
}

// IsTerminal reports whether bundle is a terminal app.
func IsTerminal(bundle string) bool {
	name := strings.ToLower(filepath.Base(bundle))
	for _, t := range []string{"kitty", "ghostty", "wezterm", "iterm", "terminal.app", "alacritty", "warp"} {
		if strings.Contains(name, t) {
			return true
		}
	}
	return false
}

// IsDropdown reports whether bundle is a terminal shown only by a hotkey
// (kitty's quick access terminal): activating it doesn't show it.
func IsDropdown(bundle string) bool { return strings.Contains(bundle, "quick-access") }

// ShowDropdown shows a hidden dropdown terminal (it toggles: call it only
// when the dropdown isn't frontmost).
func ShowDropdown(bundle string) {
	if i := strings.Index(bundle, "kitty.app"); i >= 0 {
		kitten := bundle[:i+len("kitty.app")] + "/Contents/MacOS/kitten"
		_ = exec.Command(kitten, "quick-access-terminal").Start()
	}
}

// Activate brings the app to the front.
func Activate(bundle string) {
	if runtime.GOOS == "darwin" && bundle != "" {
		_ = exec.Command("open", "-a", bundle).Run()
	}
}
