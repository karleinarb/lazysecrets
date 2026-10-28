package instance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// In nvim (lazysecrets.nvim), the tmux pane can be in view while
// lazysecrets isn't: the plugin hides its window when you leave it. The
// plugin says (state) and brings it back (reveal), when nvim isn't in the
// middle of something a window popping up would break.

// nvimState is what lazysecrets.nvim says of this lazysecrets: "shown",
// "hidden" (and can be brought back), "busy", or "" outside nvim, or with
// no answer (an older plugin, a plain :terminal): reach as before.
func nvimState(ctx context.Context) string {
	switch s := nvim(ctx, "state"); s {
	case "shown", "hidden", "busy":
		return s
	}
	return ""
}

// nvimReveal brings this lazysecrets' window back, if the plugin hid it.
func nvimReveal(ctx context.Context) { nvim(ctx, "reveal") }

// nvim calls the plugin's fn with this process' pid, in the nvim that
// started it ($NVIM), and returns what it says.
func nvim(ctx context.Context, fn string) string {
	addr := os.Getenv("NVIM")
	if addr == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond) // within the link handler's second; a stuck nvim is skipped
	defer cancel()
	expr := fmt.Sprintf(`luaeval("require('lazysecrets').%s(%d)")`, fn, os.Getpid())
	out, err := exec.CommandContext(ctx, "nvim", "--server", addr, "--remote-expr", expr).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
