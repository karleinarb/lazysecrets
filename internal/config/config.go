// Package config loads the optional YAML config file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"git.kebcloud.no/keb/lazysecrets/internal/commands"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

type Config struct {
	Icons          *bool          `yaml:"icons"`
	ClipboardClear *time.Duration `yaml:"clipboardClear"`
	// RevealTimeout hides a revealed value after this long; 0s never.
	// Defaults to 60s.
	RevealTimeout *time.Duration `yaml:"revealTimeout"`
	// ExpiryWarningDays marks secrets, keys and certificates expiring
	// within this many days; 0 marks only expired ones. Defaults to 30.
	ExpiryWarningDays *int `yaml:"expiryWarningDays"`
	// HideOnBlur hides a revealed value when the terminal loses focus.
	// Defaults to true.
	HideOnBlur *bool         `yaml:"hideOnBlur"`
	Timeout    time.Duration `yaml:"timeout"`
	// LoadDelay is how long the cursor must rest on a row before its
	// contents load; 0s loads at once. Defaults to 150ms.
	LoadDelay *time.Duration `yaml:"loadDelay"`
	// SourcesView is "grouped" (default) or "flat"; ` + "`" + ` toggles it.
	SourcesView string `yaml:"sourcesView"`
	// History counts uses of items for the Recent list; defaults to true.
	History *bool `yaml:"history"`
	// UpdateCheck looks for a newer release once a day, in the
	// background; defaults to true. U and lazysecrets update work either
	// way.
	UpdateCheck *bool `yaml:"updateCheck"`
	// Releases are the repositories updates come from, tried in order:
	// on GitHub, or a Gitea or Forgejo server. Defaults to GitHub, then
	// git.kebcloud.no (update.DefaultServers).
	Releases []string `yaml:"releases"`
	// Cache keeps what was loaded (never values) between runs, so lists
	// show at once and refresh in the background; defaults to true.
	Cache *bool `yaml:"cache"`
	// CollapseGroups starts the Sources pane with its groups folded;
	// defaults to true.
	CollapseGroups *bool     `yaml:"collapseGroups"`
	Providers      Providers `yaml:"providers"`
	// LinkHandler sets how lazysecrets:// links open; see linkhandler.
	LinkHandler LinkHandler `yaml:"linkHandler"`
	// CustomCommands run shell commands on the selected item; see
	// package commands.
	CustomCommands []commands.Command `yaml:"customCommands"`
	// Keybinding sets keys per context and action, replacing the
	// defaults of each action given; see keys.Map.Apply.
	Keybinding map[string]map[string][]string `yaml:"keybinding"`
	// Theme maps roles to colours, like lazygit's gui.theme; see
	// styles.Roles.
	Theme map[string][]string `yaml:"theme"`
}

type LinkHandler struct {
	// Terminal to open links in, overriding the one handler install
	// detected: kitty, ghostty, wezterm, iterm2, terminal, alacritty,
	// gnome-terminal, konsole or foot.
	Terminal string `yaml:"terminal"`
	// Command opens any other terminal: its arguments, with {cmd} where
	// the lazysecrets command goes, e.g. [foot, "{cmd}"].
	Command []string `yaml:"command"`
	// Reuse hands links to a running lazysecrets, the one last used that
	// can be brought into view, before opening a new window. Defaults to
	// true.
	Reuse *bool `yaml:"reuse"`
	// Prompt offers to set up the link handler at startup when links
	// don't open lazysecrets. Defaults to true.
	Prompt *bool `yaml:"prompt"`
}

type Providers struct {
	Azure      Azure      `yaml:"azure"`
	Databricks Databricks `yaml:"databricks"`
	Repos      Repos      `yaml:"repos"`
}

// Repos are git repositories' secret files (SOPS, .env). Inside a repo,
// that repo is the only one shown; elsewhere, the ones in Paths and under
// Roots.
type Repos struct {
	// Enabled defaults to true.
	Enabled *bool    `yaml:"enabled"`
	Paths   []string `yaml:"paths"`
	// Roots are folders of repos. Defaults to ~/git; an empty list
	// turns it off.
	Roots *[]string `yaml:"roots"`
	// Include and Exclude are globs relative to a repo's root (** for any
	// depth), for repos without their own .lazysecrets.yaml. Without them,
	// a repo's .sops.yaml creation_rules say where to look.
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

type Azure struct {
	// Enabled defaults to true.
	Enabled *bool `yaml:"enabled"`
	// HideSubscriptions lists subscriptions to leave out, by name or ID.
	HideSubscriptions []string `yaml:"hideSubscriptions"`
	// Vaults are shown under "Pinned vaults", for vaults that can't be
	// discovered (e.g. data-plane access only).
	Vaults []string `yaml:"vaults"`
}

type Databricks struct {
	// Enabled defaults to true.
	Enabled      *bool    `yaml:"enabled"`
	HideProfiles []string `yaml:"hideProfiles"`
}

// Dir is $XDG_CONFIG_HOME/lazysecrets, defaulting to ~/.config/lazysecrets
// on every platform, like most terminal tools.
func Dir() (string, error) {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

// CacheDir is $XDG_CACHE_HOME/lazysecrets, defaulting to
// ~/.cache/lazysecrets. It holds what was loaded, to show it at once.
func CacheDir() (string, error) {
	return xdgDir("XDG_CACHE_HOME", ".cache")
}

// StateDir is $XDG_STATE_HOME/lazysecrets, defaulting to
// ~/.local/state/lazysecrets. It holds data the app writes, like favourites.
func StateDir() (string, error) {
	return xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

func xdgDir(env, fallback string) (string, error) {
	base := os.Getenv(env)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, fallback)
	}
	return filepath.Join(base, "lazysecrets"), nil
}

// Load reads path. A missing file is an empty config. Unknown keys are an
// error, so typos don't silently do nothing.
func Load(path string) (Config, error) {
	return LoadAll([]string{path})
}

// LoadAll reads several config files in order, each on top of the ones
// before, like lazygit's LG_CONFIG_FILE: a later file overrides the
// settings it sets, and theme roles merge one by one. That lets an editor
// plugin add a generated theme file to the user's own config. Missing
// files are skipped.
func LoadAll(paths []string) (Config, error) {
	var c Config
	keybinding := map[string]map[string][]string{}
	for _, path := range paths {
		// Keybindings layer per action: a later file setting one action
		// in a context keeps the others set before it.
		c.Keybinding = nil
		if err := decodeInto(&c, path); err != nil {
			return c, err
		}
		for ctx, actions := range c.Keybinding {
			if keybinding[ctx] == nil {
				keybinding[ctx] = map[string][]string{}
			}
			for name, keys := range actions {
				keybinding[ctx][name] = keys
			}
		}
	}
	c.Keybinding = keybinding
	return c, c.validate(strings.Join(paths, ", "))
}

func decodeInto(c *Config, path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (c Config) validate(path string) error {
	if v := c.SourcesView; v != "" && v != "grouped" && v != "flat" {
		return fmt.Errorf("%s: sourcesView: %q is not grouped or flat", path, v)
	}
	for _, cmd := range c.CustomCommands {
		if err := cmd.Validate(); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if d := c.ExpiryWarningDays; d != nil && *d < 0 {
		return fmt.Errorf("%s: expiryWarningDays: %d is negative", path, *d)
	}
	if d := c.LoadDelay; d != nil && (*d < 0 || *d > 5*time.Second) {
		return fmt.Errorf("%s: loadDelay: %s is not between 0s and 5s", path, *d)
	}
	for i, r := range c.Releases {
		if _, err := update.API(r); err != nil || !strings.HasPrefix(r, "https://") {
			return fmt.Errorf("%s: releases[%d]: %q is not an https:// repository address, like %s", path, i, r, update.GitHubRepo)
		}
	}
	for i, v := range c.Providers.Azure.Vaults {
		if !strings.HasPrefix(v, "https://") {
			return fmt.Errorf("%s: providers.azure.vaults[%d]: %q is not an https:// vault URL", path, i, v)
		}
	}
	return nil
}

// On reports whether an optional enabled flag is unset or true.
func On(enabled *bool) bool { return enabled == nil || *enabled }

// Hidden reports whether name or id appears in list, ignoring case.
func Hidden(list []string, name, id string) bool {
	for _, h := range list {
		if strings.EqualFold(h, name) || strings.EqualFold(h, id) {
			return true
		}
	}
	return false
}
