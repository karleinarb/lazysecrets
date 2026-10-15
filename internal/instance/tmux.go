package instance

import (
	"cmp"
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
	name, session string
	activity      int // Unix seconds
	pid           int
	app           string
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
		activity, err1 := strconv.Atoi(f[0])
		pid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		cs = append(cs, client{name: f[2], session: f[3], activity: activity, pid: pid, app: linkhandler.HostApp(pid)})
	}
	slices.SortStableFunc(cs, func(a, b client) int { return cmp.Compare(b.activity, a.activity) })
	return cs
}

// place is where this instance would show: the tmux client (zero outside
// tmux), the app, and its reach.
func place(front string) (client, string, int) {
	if !inTmux() {
		return client{}, linkhandler.HostApp(os.Getpid()), inView
	}
	session, err := tmux("display-message", "-p", "-t", os.Getenv("TMUX_PANE"), "#{session_name}")
	if err != nil {
		return client{}, "", unreachable
	}
	cs := clients()
	if len(cs) == 0 {
		return client{}, "", unreachable
	}
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
	best := cs[0]
	for _, c := range cs[1:] {
		if rank(c) > rank(best) {
			best = c
		}
	}
	if best.session == session {
		return best, best.app, inView
	}
	return best, best.app, switchable
}

// Raise brings this instance into view for a link clicked in front: in
// tmux its window and pane, switching a client to its session if needed;
// then its app to the front, showing a hidden dropdown terminal.
func Raise(front string) {
	c, app, reach := place(front)
	if reach == unreachable {
		return
	}
	if inTmux() {
		pane := os.Getenv("TMUX_PANE")
		_, _ = tmux("select-window", "-t", pane)
		_, _ = tmux("select-pane", "-t", pane)
		if reach == switchable {
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
