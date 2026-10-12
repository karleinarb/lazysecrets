package instance

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortDir is a temporary directory with a path short enough for a
// socket (macOS allows 104 bytes).
func shortDir(t *testing.T) string {
	d, err := os.MkdirTemp("", "ls")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestHandOver(t *testing.T) {
	t.Setenv("TMUX", "")
	dir := shortDir(t)
	got := make(chan string, 1)
	s, err := Listen(dir, func(link, front string) error { got <- link; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}

	// A socket left by a crashed instance.
	stale := filepath.Join(dir, "999999.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	found := Running(ctx, dir, "")
	if len(found) != 1 || found[0].PID != os.Getpid() || found[0].Reach != 2 {
		t.Fatalf("found %+v", found)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale socket left behind")
	}
	best, ok := Best(found, "", false)
	if !ok {
		t.Fatal("no instance to hand over to")
	}
	if err := Open(ctx, best, "lazysecrets://azure/kv.vault.azure.net", ""); err != nil {
		t.Fatal(err)
	}
	if l := <-got; l != "lazysecrets://azure/kv.vault.azure.net" {
		t.Errorf("got %q", l)
	}
}

func TestBest(t *testing.T) {
	now := time.Now()
	f := func(reach int, ago time.Duration) Found {
		return Found{Status: Status{Reach: reach, LastActive: now.Add(-ago), PID: int(ago)}}
	}
	// In view beats more recently used but needing a tmux switch.
	if b, _ := Best([]Found{f(1, time.Second), f(2, time.Hour), f(2, time.Minute)}, "", false); b.PID != int(time.Minute) {
		t.Errorf("best = %+v", b)
	}
	if _, ok := Best([]Found{f(0, 0)}, "", false); ok {
		t.Error("handed over to an instance that can't be shown")
	}
	if _, ok := Best(nil, "", false); ok {
		t.Error("best of none")
	}

	// Clicked in a terminal: only an instance showing there will do,
	// however recently another was used.
	kitty, dropdown := "/Applications/kitty.app", "/Applications/kitty.app/Contents/kitty-quick-access.app"
	inDropdown := f(2, time.Second)
	inDropdown.App = dropdown
	inKitty := f(1, time.Hour)
	inKitty.App = kitty
	if b, ok := Best([]Found{inDropdown, inKitty}, kitty, true); !ok || b.App != kitty {
		t.Errorf("clicked in kitty: %+v %v", b, ok)
	}
	if _, ok := Best([]Found{inDropdown}, kitty, true); ok {
		t.Error("clicked in kitty, went to the dropdown")
	}
	// Clicked in Slack: the one used last, wherever.
	if b, _ := Best([]Found{inDropdown, inKitty}, "/Applications/Slack.app", false); b.App != dropdown {
		t.Errorf("clicked in Slack: %+v", b)
	}
}
