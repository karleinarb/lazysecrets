package instance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// With a stand-in lazysecrets.nvim in a headless nvim: what it says comes
// back, for this process' pid, and an older plugin's error is "".
func TestNvim(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("needs nvim")
	}
	dir := t.TempDir()
	lua := filepath.Join(dir, "lua")
	os.MkdirAll(lua, 0o755)
	pid := strconv.Itoa(os.Getpid())
	os.WriteFile(filepath.Join(lua, "lazysecrets.lua"), []byte(`
local M = {}
function M.state(pid) return pid == `+pid+` and vim.g.state or "none" end
function M.reveal(pid) vim.g.revealed = pid; return true end
return M
`), 0o644)
	sock := filepath.Join(dir, "nvim.sock")
	nv := exec.Command("nvim", "--headless", "--clean", "--listen", sock, "--cmd", "set rtp^="+dir, "--cmd", "let g:state = 'hidden'")
	if err := nv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nv.Process.Kill(); nv.Wait() })
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Setenv("NVIM", sock)
	ctx := context.Background()

	if s := nvimState(ctx); s != "hidden" {
		t.Errorf("state %q", s)
	}
	nvimReveal(ctx)
	if got := nvim(ctx, "state"); got != "hidden" {
		t.Errorf("after reveal: %q", got)
	}
	out, _ := exec.Command("nvim", "--server", sock, "--remote-expr", "g:revealed").Output()
	if string(out) != pid {
		t.Errorf("revealed %q, want this pid %s", out, pid)
	}

	if got := nvim(ctx, "missing"); got != "" { // an older plugin
		t.Errorf("a function the plugin lacks: %q", got)
	}

	t.Setenv("NVIM", "")
	if s := nvimState(ctx); s != "" {
		t.Errorf("outside nvim: %q", s)
	}
}
