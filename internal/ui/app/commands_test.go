package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/commands"
	"github.com/charmbracelet/x/ansi"
)

func commandDriver(t *testing.T, cmds ...commands.Command) *driver {
	t.Setenv("SHELL", "/bin/sh")
	d := newDriverOpts(t, Options{Commands: cmds})
	d.wait = 5 * time.Second
	return d
}

func TestCommandGetsValueOnInput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	d := commandDriver(t, commands.Command{
		Key: "K", Context: commands.Secrets, Description: "Save it",
		Command: "cat > " + out + " && echo {{.Name}} {{.Prompt.ns}} >> " + out,
		Value:   "stdin", Prompts: []commands.Prompt{{Name: "ns", Title: "Namespace", Default: "team a"}},
	})
	d.keys("3", "K") // db-password
	if !strings.Contains(d.screen(), "Namespace: team a") {
		t.Fatalf("no prompt:\n%s", d.screen())
	}
	d.keys("enter")
	scr := d.screen()
	// The command as shown, unwrapped: where it wraps depends on the
	// temporary path's length.
	shown := ""
	if d.m.prompt != nil {
		shown = ansi.Strip(strings.Join(d.m.prompt.lines, ""))
	}
	if !strings.Contains(scr, "Run Save it?") || !strings.Contains(shown, "db-password 'team a'") || !strings.Contains(shown, "on its input") {
		t.Fatalf("no confirmation with the command %q:\n%s", shown, scr)
	}
	if strings.Contains(scr, "correct-horse") {
		t.Fatal("value shown in the confirmation")
	}
	d.keys("y")
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("command didn't run: %v\n%s", err, d.screen())
	}
	if got := string(b); got != "correct-horse-battery-staple"+"db-password team a\n" {
		t.Errorf("command got %q", got)
	}
	if !strings.Contains(d.screen(), "✓ Save it") {
		t.Errorf("no confirmation message:\n%s", d.screen())
	}
}

func TestCommandPopupAndMenu(t *testing.T) {
	d := commandDriver(t,
		commands.Command{Context: commands.Vaults, Description: "Show vault", Command: "echo vault={{.Vault}} url={{.VaultURL}}", Output: commands.Popup},
		commands.Command{Context: commands.Secrets, Description: "Secrets only", Command: "true"},
	)
	d.keys("2", "x")
	if !strings.Contains(d.screen(), "Show vault") || strings.Contains(d.screen(), "Secrets only") {
		t.Fatalf("commands menu for the Vaults pane:\n%s", d.screen())
	}
	d.keys("enter")
	if !strings.Contains(d.screen(), "vault=kv-app-prod url=https://kv-app-prod.vault.azure.net") {
		t.Errorf("popup output:\n%s", d.screen())
	}
}

func TestCommandNeedsItsSelection(t *testing.T) {
	d := commandDriver(t, commands.Command{Key: "K", Context: commands.Secrets, Description: "Secret thing", Command: "true"})
	d.keys("1", "K") // Sources pane: not a secrets command's context
	if d.m.prompt != nil {
		t.Error("ran from the wrong pane")
	}
}
