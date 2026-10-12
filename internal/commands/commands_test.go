package commands

import (
	"strings"
	"testing"
)

func TestRenderQuotes(t *testing.T) {
	c := Command{Context: Secrets, Command: `kubectl create secret generic {{.Name}} -n {{.Prompt.ns}} # {{.Note}} {{raw .Name}}`}
	got, err := c.Render(Item{Name: "db-password", Note: "it's $(rm -rf ~)", Prompt: map[string]Q{"ns": "team a"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `kubectl create secret generic db-password -n 'team a' # 'it'\''s $(rm -rf ~)' db-password`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestValidate(t *testing.T) {
	ok := Command{Context: Secrets, Command: "gh secret set {{.Name}}", Value: "stdin"}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid command: %v", err)
	}
	if !ok.Confirms() || ok.Out() != Quiet {
		t.Error("defaults: confirm with a value, quiet output")
	}
	for _, c := range []struct {
		cmd  Command
		want string
	}{
		{Command{Context: "panes", Command: "x"}, "context"},
		{Command{Context: Vaults, Command: "x", Value: "stdin"}, "needs context: secrets"},
		{Command{Context: Secrets, Command: "x", Value: "env:1BAD"}, "not stdin or env:NAME"},
		{Command{Context: Secrets, Command: "x", Value: "stdin", Output: Terminal}, "terminal's input"},
		{Command{Context: Global, Command: "{{.Nope"}, "unclosed"},
		{Command{Context: Global, Command: "x", Output: "loud"}, "output"},
		{Command{Context: Global, Command: "echo {{.Nmae}}"}, "Nmae"},
		{Command{Context: Global, Command: "echo {{.Prompt.ns}}"}, "ns"},
	} {
		if err := c.cmd.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cmd, err, c.want)
		}
	}
}
