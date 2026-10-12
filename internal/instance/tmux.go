package instance

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"git.kebcloud.no/keb/lazysecrets/internal/linkhandler"
)

// Where an instance shows depends on its terminal app (on macOS): for an
// instance in tmux, the app of a tmux client showing its session, or one
// that could be switched to it. front is the app the link was clicked in;
// clients in it are preferred, so a link clicked in a terminal opens
// there.

// tmux runs tmux on the server this instance runs in.
func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func inTmux() bool { return os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != "" }

type client struct {
	name, session, activity string // activity: Unix seconds
	pid                     int
	app                     string
}

// clients lists the tmux server's clients, the one last typed in first.
func clients() []client {
	out, err := tmux("list-clients", "-F", "#{client_activity} #{client_pid} #{client_name} #{session_name}")
	if err != nil {
		return nil
	}
	var cs []client
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, " ", 4)
		if len(f) < 4 {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		cs = append(cs, client{name: f[2], session: f[3], activity: f[0], pid: pid, app: linkhandler.HostApp(pid)})
	}
	// Activity has the same width for centuries: strings compare like
	// numbers.
	slices.SortStableFunc(cs, func(a, b client) int { return strings.Compare(b.activity, a.activity) })
	return cs
}

// place is where this instance would show: the tmux client (zero outside
// tmux), the app, and reach: 2 already on screen there (outside tmux, or
// a client on its session), 1 after switching a client, 0 nowhere.
func place(front string) (client, string, int) {
	if !inTmux() {
		return client{}, linkhandler.HostApp(os.Getpid()), 2
	}
	session, err := tmux("display-message", "-p", "-t", os.Getenv("TMUX_PANE"), "#{session_name}")
	if err != nil {
		return client{}, "", 0
	}
	cs := clients()
	// Best first: in the front app on its session, in the front app,
	// on its session, any.
	rank := func(c client) int {
		r := 0
		if front != "" && c.app == front {
			r += 2
		}
		if c.session == session {
			r++
		}
		return r
	}
	var best client
	found := false
	for _, c := range cs {
		if !found || rank(c) > rank(best) {
			best, found = c, true
		}
	}
	if !found {
		return client{}, "", 0
	}
	if best.session == session {
		return best, best.app, 2
	}
	return best, best.app, 1
}

// Raise brings this instance into view for a link clicked in front: in
// tmux its window and pane, switching a client to its session if needed;
// then its app to the front, showing a hidden dropdown terminal.
func Raise(front string) {
	c, app, reach := place(front)
	if reach == 0 {
		return
	}
	if inTmux() {
		pane := os.Getenv("TMUX_PANE")
		_, _ = tmux("select-window", "-t", pane)
		_, _ = tmux("select-pane", "-t", pane)
		if reach == 1 {
			_, _ = tmux("switch-client", "-c", c.name, "-t", pane)
		}
	}
	switch {
	case app == "" || app == front:
		// already in front (or unknown)
	case linkhandler.IsDropdown(app):
		linkhandler.ShowDropdown(app) // it isn't front, so hidden: this shows it
	default:
		// The bundle, not the name: "kitty" may pick an instance without
		// windows, which then opens a new one.
		linkhandler.Activate(app)
	}
}
