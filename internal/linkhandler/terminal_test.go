package linkhandler

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"KITTY_WINDOW_ID": "3", "TERM_PROGRAM": "tmux"}, "kitty"}, // kitty, inside tmux
		{map[string]string{"TERM_PROGRAM": "ghostty", "GHOSTTY_RESOURCES_DIR": "/x"}, "ghostty"},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, "wezterm"},
		{map[string]string{"TERM_PROGRAM": "tmux", "LC_TERMINAL": "iTerm2"}, "iterm2"},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, "terminal"},
		{map[string]string{"ALACRITTY_WINDOW_ID": "1"}, "alacritty"},
		{map[string]string{"TERM_PROGRAM": "vscode"}, ""},
	} {
		if got := Detect(env(c.env)); got != c.want {
			t.Errorf("Detect(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}

var inner = []string{"/usr/bin/env", "PATH=/opt/homebrew/bin:/usr/bin", "/usr/local/bin/lazysecrets", "open", "lazysecrets://azure/kv.vault.azure.net/secrets/it's"}

func TestCommandDarwin(t *testing.T) {
	got, err := Command("darwin", "kitty", nil, inner)
	if err != nil || !slices.Equal(got, append([]string{"open", "-na", "kitty", "--args", "-o", "macos_quit_when_last_window_closed=yes"}, inner...)) {
		t.Errorf("kitty: %q %v", got, err)
	}
	got, _ = Command("darwin", "ghostty", nil, inner)
	if !slices.Equal(got[:5], []string{"open", "-na", "Ghostty", "--args", "-e"}) {
		t.Errorf("ghostty: %q", got)
	}
	got, _ = Command("darwin", "iterm2", nil, inner)
	if got[0] != "osascript" || !strings.Contains(got[2], `create window with default profile command "'/usr/bin/env'`) {
		t.Errorf("iterm2: %q", got)
	}
}

// The AppleScript for Terminal.app (and iTerm2, if installed) compiles, with the quote in the link.
func TestAppleScriptCompiles(t *testing.T) {
	if _, err := exec.LookPath("osacompile"); err != nil {
		t.Skip("no osacompile")
	}
	terms := []string{"terminal"}
	if _, err := os.Stat("/Applications/iTerm.app"); err == nil {
		terms = append(terms, "iterm2") // its commands come from the app
	}
	for _, term := range terms {
		argv, _ := Command("darwin", term, nil, inner)
		out, err := exec.Command("osacompile", "-o", t.TempDir()+"/x.scpt", "-e", argv[2]).CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v: %s", term, err, out)
		}
	}
}

func TestCommandLinux(t *testing.T) {
	got, _ := Command("linux", "gnome-terminal", nil, inner)
	if !slices.Equal(got, append([]string{"gnome-terminal", "--"}, inner...)) {
		t.Errorf("gnome-terminal: %q", got)
	}
}

func TestCommandCustom(t *testing.T) {
	got, err := Command("linux", "kitty", []string{"foot", "--title", "secrets", Placeholder}, inner)
	if err != nil || !slices.Equal(got, append([]string{"foot", "--title", "secrets"}, inner...)) {
		t.Errorf("custom: %q %v", got, err)
	}
	if _, err := Command("linux", "", []string{"foot"}, inner); err == nil {
		t.Error("custom command without {cmd} accepted")
	}
}

func TestCommandErrors(t *testing.T) {
	if _, err := Command("darwin", "", nil, inner); err == nil || !strings.Contains(err.Error(), "handler install") {
		t.Errorf("no terminal: %v", err)
	}
	if _, err := Command("darwin", "xterm2000", nil, inner); err == nil || !strings.Contains(err.Error(), "unknown terminal") {
		t.Errorf("unknown terminal: %v", err)
	}
}

func TestShellJoinRoundTrip(t *testing.T) {
	out, err := exec.Command("sh", "-c", `printf '%s\n' `+shellJoin(inner)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !slices.Equal(got, inner) {
		t.Errorf("round trip = %q", got)
	}
}

func TestEnv(t *testing.T) {
	got := Env(env(map[string]string{"PATH": "/bin", "HOME": "/h", "AZURE_CONFIG_DIR": "/az"}))
	if !slices.Equal(got, []string{"PATH=/bin", "AZURE_CONFIG_DIR=/az"}) {
		t.Errorf("Env = %q", got)
	}
}

func TestIsTerminal(t *testing.T) {
	for b, want := range map[string]bool{
		"/Applications/kitty.app": true, "/Applications/kitty.app/Contents/kitty-quick-access.app": true,
		"/Applications/Ghostty.app": true, "/System/Applications/Utilities/Terminal.app": true,
		"/Applications/Slack.app": false, "/Applications/Safari.app": false, "": false,
	} {
		if IsTerminal(b) != want {
			t.Errorf("IsTerminal(%q) = %v", b, !want)
		}
	}
	if !IsDropdown("/Applications/kitty.app/Contents/kitty-quick-access.app") || IsDropdown("/Applications/kitty.app") {
		t.Error("IsDropdown")
	}
}
