// Package commands defines custom commands: shell commands from the
// config, run on the selected item with its details filled in.
//
//	customCommands:
//	  - key: K
//	    context: secrets
//	    description: Create Kubernetes secret
//	    command: kubectl create secret generic {{.Name}} -n {{.Prompt.namespace}} --from-file=value=/dev/stdin
//	    value: stdin
//	    prompts: [{name: namespace, title: Namespace, default: default}]
//	    output: popup
//
// Fields are shell-quoted when filled in ({{raw .Note}} opts out), and
// the secret value is never one of them: value: stdin or value: env:NAME
// hands it to the command without putting it on its command line.
package commands

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// Contexts say where a command applies: the pane with focus (the detail
// pane counts as secrets), or anywhere.
const (
	Sources = "sources"
	Vaults  = "vaults"
	Secrets = "secrets"
	Global  = "global"
)

// Outputs say how a command runs.
const (
	Terminal = "terminal" // in the full terminal, lazysecrets stepping aside
	Popup    = "popup"    // output captured and shown in a popup
	Quiet    = "quiet"    // in the background, success or failure in the footer
)

type Command struct {
	Key         string   `yaml:"key"`
	Context     string   `yaml:"context"`
	Description string   `yaml:"description"`
	Command     string   `yaml:"command"`
	Value       string   `yaml:"value"`   // "", "stdin" or "env:NAME"
	Confirm     *bool    `yaml:"confirm"` // defaults to true when the value is passed
	Output      string   `yaml:"output"`  // defaults to quiet
	Prompts     []Prompt `yaml:"prompts"`
}

// A Prompt asks for text before the command runs: {{.Prompt.name}}.
type Prompt struct {
	Name    string `yaml:"name"`
	Title   string `yaml:"title"`
	Default string `yaml:"default"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate checks a command from the config.
func (c Command) Validate() error {
	name := cmp.Or(c.Description, c.Command)
	switch {
	case c.Command == "":
		return fmt.Errorf("custom command %q has no command", c.Description)
	case c.Context != Sources && c.Context != Vaults && c.Context != Secrets && c.Context != Global:
		return fmt.Errorf("custom command %q: context %q is not sources, vaults, secrets or global", name, c.Context)
	case c.Output != "" && c.Output != Terminal && c.Output != Popup && c.Output != Quiet:
		return fmt.Errorf("custom command %q: output %q is not terminal, popup or quiet", name, c.Output)
	}
	if c.Value != "" {
		if c.Context != Secrets {
			return fmt.Errorf("custom command %q: value needs context: secrets", name)
		}
		env, isEnv := strings.CutPrefix(c.Value, "env:")
		if c.Value != "stdin" && !(isEnv && envName.MatchString(env)) {
			return fmt.Errorf("custom command %q: value %q is not stdin or env:NAME", name, c.Value)
		}
		if c.Value == "stdin" && c.Output == Terminal {
			return fmt.Errorf("custom command %q: output: terminal needs the terminal's input; use value: env:NAME", name)
		}
	}
	seen := map[string]bool{}
	for _, p := range c.Prompts {
		if p.Name == "" || seen[p.Name] {
			return fmt.Errorf("custom command %q: prompts need distinct names", name)
		}
		seen[p.Name] = true
	}
	// A trial run catches unknown fields ({{.Nmae}}) and prompts.
	trial := Item{Prompt: map[string]Q{}}
	for _, p := range c.Prompts {
		trial.Prompt[p.Name] = ""
	}
	if _, err := c.Render(trial); err != nil {
		return fmt.Errorf("custom command %q: %w", name, err)
	}
	return nil
}

// Confirms reports whether to ask before running.
func (c Command) Confirms() bool {
	if c.Confirm != nil {
		return *c.Confirm
	}
	return c.Value != ""
}

// Out is the output mode, quiet unless set.
func (c Command) Out() string {
	if c.Output == "" {
		return Quiet
	}
	return c.Output
}

// Item is what's filled into a command: the selected item's details,
// shell-quoted when printed, and the answers to its prompts.
type Item struct {
	Kind, Account, Source, SourceID     Q
	Vault, VaultURL                     Q
	Name, Type, Version                 Q
	Link, Reference, URL, Note, Expires Q
	Prompt                              map[string]Q
}

// Q is a field, shell-quoted when printed in a template.
type Q string

func (q Q) String() string { return Quote(string(q)) }

var safe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// Quote quotes s for a POSIX shell, leaving plain words as they are.
func Quote(s string) string {
	if safe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (c Command) template() (*template.Template, error) {
	return template.New("command").Option("missingkey=error").
		Funcs(template.FuncMap{"raw": func(q Q) string { return string(q) }}).
		Parse(c.Command)
}

// Render fills the item into the command.
func (c Command) Render(it Item) (string, error) {
	t, err := c.template()
	if err != nil {
		return "", err
	}
	if it.Prompt == nil {
		it.Prompt = map[string]Q{}
	}
	var b strings.Builder
	if err := t.Execute(&b, it); err != nil {
		return "", err
	}
	return b.String(), nil
}
