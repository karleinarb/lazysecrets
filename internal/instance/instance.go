// Package instance lets a lazysecrets:// link go to a lazysecrets that is
// already running, rather than a new window.
//
// Each running lazysecrets listens on a Unix socket of its own in a
// directory only the user can read (Unix sockets work the same on macOS,
// Linux and Windows 10 and later). The link handler asks each one when it
// was last used and whether it can be brought into view, and hands the
// link to the best one, which shows it and comes to the front.
package instance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dir holds the sockets: $XDG_RUNTIME_DIR/lazysecrets if set, else
// run/ under the state directory.
func Dir(stateDir string) string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "lazysecrets")
	}
	return filepath.Join(stateDir, "run")
}

// Status is what an instance tells the link handler about itself.
type Status struct {
	PID        int       `json:"pid"`
	LastActive time.Time `json:"lastActive"` // last key press or focus
	// Reach is how readily it can be shown: 2 in view (outside tmux, or
	// in a tmux session a client shows), 1 in a tmux session that a tmux
	// client can be switched to, 0 not at all.
	Reach int `json:"reach"`
	// App is the terminal app it would show in (macOS), preferring the
	// app the link was clicked in.
	App string `json:"app,omitempty"`
}

type request struct {
	Op    string `json:"op"` // "status" or "open"
	Link  string `json:"link,omitempty"`
	Front string `json:"front,omitempty"` // app the link was clicked in
}

type response struct {
	Status
	Err string `json:"err,omitempty"`
}

// Server is the running lazysecrets' end.
type Server struct {
	ln   net.Listener
	path string
	open func(link, front string) error // shows the link and comes to the front

	mu     sync.Mutex
	active time.Time
}

// Listen starts listening in dir. open is called, from another goroutine,
// with each link handed over and the app it was clicked in; it should
// return quickly, as the link handler waits for it, and come to the front
// (Raise) afterwards.
func Listen(dir string, open func(link, front string) error) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700) // a pre-existing directory too
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(path) // a crashed process with this PID
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, path: path, open: open, active: time.Now()}
	go s.serve()
	return s, nil
}

// Touch records that the user just used this instance.
func (s *Server) Touch() {
	s.mu.Lock()
	s.active = time.Now()
	s.mu.Unlock()
}

// Close stops listening and removes the socket.
func (s *Server) Close() error {
	err := s.ln.Close()
	_ = os.Remove(s.path)
	return err
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var req request
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&req); err != nil {
		return
	}
	var resp response
	switch req.Op {
	case "status":
		_, app, reach := place(req.Front)
		s.mu.Lock()
		resp.Status = Status{PID: os.Getpid(), LastActive: s.active, Reach: reach, App: app}
		s.mu.Unlock()
	case "open":
		if err := s.open(req.Link, req.Front); err != nil {
			resp.Err = err.Error()
		}
	default:
		resp.Err = fmt.Sprintf("unknown request %q", req.Op)
	}
	_ = json.NewEncoder(c).Encode(resp)
}

// Found is a running instance.
type Found struct {
	Status
	path string
}

// Running asks every instance in dir for its status for a link clicked in
// front, removing sockets no one answers on (left by a crash).
func Running(ctx context.Context, dir, front string) []Found {
	entries, _ := os.ReadDir(dir)
	var (
		mu  sync.Mutex
		out []Found
		wg  sync.WaitGroup
	)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sock") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp response
			err := call(ctx, path, request{Op: "status", Front: front}, &resp)
			if errors.Is(err, errGone) {
				_ = os.Remove(path)
				return
			}
			if err != nil || resp.Err != "" {
				return
			}
			mu.Lock()
			out = append(out, Found{resp.Status, path})
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// Best is the instance to hand a link clicked in front to: the most
// recently used of those most readily shown. A link clicked in a terminal
// goes only to one showing in that terminal. ok is false if there's none.
func Best(found []Found, front string, terminal bool) (Found, bool) {
	var best Found
	for _, f := range found {
		if terminal && f.App != front {
			continue
		}
		if f.Reach > best.Reach || (f.Reach == best.Reach && f.LastActive.After(best.LastActive)) {
			best = f
		}
	}
	return best, best.Reach > 0
}

// Open hands link, clicked in front, to f.
func Open(ctx context.Context, f Found, link, front string) error {
	var resp response
	if err := call(ctx, f.path, request{Op: "open", Link: link, Front: front}, &resp); err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	return nil
}

var errGone = errors.New("no instance listening")

func call(ctx context.Context, path string, req request, resp *response) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("%w: %v", errGone, err)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return err
	}
	return json.NewDecoder(c).Decode(resp)
}
