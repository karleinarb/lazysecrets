// Package config loads the optional YAML config file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"git.kebcloud.no/keb/lazysecrets/internal/commands"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/repo"
	"git.kebcloud.no/keb/lazysecrets/internal/share"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

type Config struct {
	UI UI `yaml:"ui"`
	// Values keeps revealed and copied values from lingering.
	Values Values `yaml:"values"`
	// Storage is what's kept between runs.
	Storage Storage `yaml:"storage"`
	// Updates sets how lazysecrets looks for newer releases.
	Updates   Updates   `yaml:"updates"`
	Providers Providers `yaml:"providers"`
	// LinkHandler sets how lazysecrets:// links open; see linkhandler.
	LinkHandler LinkHandler `yaml:"linkHandler"`
	// Sharing sets how env files are shared; see package share.
	Sharing Sharing `yaml:"sharing"`
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

type UI struct {
	Icons *bool `yaml:"icons"`
	// RecentIcon marks items used lately (Recent, R) with ◷, in the lists
	// and in lazysecrets list. Defaults to false.
	RecentIcon *bool `yaml:"recentIcon"`
	// SourcesView is "grouped" (default) or "flat"; the backtick key
	// toggles it.
	SourcesView string `yaml:"sourcesView"`
	// CollapseGroups starts the Sources pane with its groups folded;
	// defaults to true.
	CollapseGroups *bool `yaml:"collapseGroups"`
	// LoadDelay is how long the cursor must rest on a row before its
	// contents load; 0s loads at once. Defaults to 150ms.
	LoadDelay *time.Duration `yaml:"loadDelay"`
	// ExpiryWarningDays marks secrets, keys and certificates expiring
	// within this many days; 0 marks only expired ones. Defaults to 30.
	ExpiryWarningDays *int `yaml:"expiryWarningDays"`
}

type Values struct {
	// RevealTimeout hides a revealed value after this long; 0s never.
	// Defaults to 60s.
	RevealTimeout *time.Duration `yaml:"revealTimeout"`
	// HideOnBlur hides a revealed value when the terminal loses focus.
	// Defaults to true.
	HideOnBlur     *bool          `yaml:"hideOnBlur"`
	ClipboardClear *time.Duration `yaml:"clipboardClear"`
}

type Storage struct {
	// Cache keeps what was loaded (never values) between runs, so lists
	// show at once and refresh in the background; defaults to true.
	Cache *bool `yaml:"cache"`
	// History counts uses of items for the Recent list; defaults to true.
	History *bool `yaml:"history"`
}

type Updates struct {
	// AutoCheck looks for a newer release in the background at startup,
	// at most once per AutoCheckInterval; defaults to true. U and
	// lazysecrets update work either way.
	AutoCheck *bool `yaml:"autoCheck"`
	// AutoCheckInterval defaults to 24h.
	AutoCheckInterval *time.Duration `yaml:"autoCheckInterval"`
	// Sources are the repositories updates come from, tried in
	// order: on GitHub, or a Gitea or Forgejo server. Defaults to GitHub,
	// then git.kebcloud.no (update.DefaultServers).
	Sources []string `yaml:"sources"`
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

type Sharing struct {
	// Key is the secret whose value encrypts shared files (its
	// lazysecrets:// link, Key Vault URL or reference), for repos whose
	// .lazysecrets.yaml doesn't name one.
	Key string `yaml:"key"`
	// KeyRules are what a key's value must be to share with it.
	KeyRules KeyRules `yaml:"keyRules"`
}

// KeyRules default to 32 characters of 2 kinds (share.DefaultRules).
type KeyRules struct {
	MinLength int `yaml:"minLength"` // at least 16
	// MinClasses is how many kinds of character: lowercase, uppercase,
	// digits, symbols.
	MinClasses int `yaml:"minClasses"`
}

type Providers struct {
	// Timeout bounds each backend request; defaults to 30s. Each
	// provider's own timeout overrides it for that provider.
	Timeout    time.Duration `yaml:"timeout"`
	Azure      Azure         `yaml:"azure"`
	Databricks Databricks    `yaml:"databricks"`
	Repos      Repos         `yaml:"repos"`
}

// Repos are git repositories' secret files (SOPS, .env). Inside a repo,
// that repo is the only one shown; elsewhere, the ones in Paths and under
// Roots.
type Repos struct {
	// Timeout overrides providers.timeout for these requests.
	Timeout time.Duration `yaml:"timeout"`
	// Enabled defaults to true.
	Enabled *bool    `yaml:"enabled"`
	Paths   []string `yaml:"paths"`
	// Roots are folders of repos. Defaults to repo.DefaultRoots (~/git,
	// ~/src, ~/code and the like); an empty list turns it off.
	Roots *[]string `yaml:"roots"`
	// Include and Exclude are globs relative to a repo's root (** for any
	// depth), for repos without their own .lazysecrets.yaml. Without them,
	// a repo's .sops.yaml creation_rules say where to look.
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
	// SecretPatterns say which keys of config files are secrets; Spring
	// and Compose override them for their files, list by list.
	SecretPatterns repo.SecretPatterns `yaml:"secretPatterns"`
	Spring         repo.FileType       `yaml:"spring"`
	Compose        repo.FileType       `yaml:"compose"`
}

type Azure struct {
	// Timeout overrides providers.timeout for these requests.
	Timeout time.Duration `yaml:"timeout"`
	// Enabled defaults to true.
	Enabled *bool `yaml:"enabled"`
	// HideSubscriptions lists subscriptions to leave out, by name or ID.
	HideSubscriptions []string `yaml:"hideSubscriptions"`
	// Vaults are shown under "Pinned vaults", for vaults that can't be
	// discovered (e.g. data-plane access only).
	Vaults []string `yaml:"vaults"`
}

type Databricks struct {
	// Timeout overrides providers.timeout for these requests.
	Timeout time.Duration `yaml:"timeout"`
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

// LoadAll reads several config files in order, each on top of the ones
// before, like lazygit's LG_CONFIG_FILE: a later file overrides the
// settings it sets, and theme roles merge one by one. That lets an editor
// plugin add a generated theme file to the user's own config. Missing
// files are skipped. Unknown keys are an error, so typos don't silently do
// nothing.
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
			maps.Copy(keybinding[ctx], actions)
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
	if v := c.UI.SourcesView; v != "" && v != "grouped" && v != "flat" {
		return fmt.Errorf("%s: ui.sourcesView: %q is not grouped or flat", path, v)
	}
	for _, cmd := range c.CustomCommands {
		if err := cmd.Validate(); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if d := c.UI.ExpiryWarningDays; d != nil && *d < 0 {
		return fmt.Errorf("%s: ui.expiryWarningDays: %d is negative", path, *d)
	}
	if d := c.UI.LoadDelay; d != nil && (*d < 0 || *d > 5*time.Second) {
		return fmt.Errorf("%s: ui.loadDelay: %s is not between 0s and 5s", path, *d)
	}
	if d := c.Updates.AutoCheckInterval; d != nil && *d <= 0 {
		return fmt.Errorf("%s: updates.autoCheckInterval: %s is not positive (autoCheck: false turns it off)", path, *d)
	}
	for i, r := range c.Updates.Sources {
		if _, err := update.API(r); err != nil || !strings.HasPrefix(r, "https://") {
			return fmt.Errorf("%s: updates.sources[%d]: %q is not an https:// repository address, like %s", path, i, r, update.GitHubRepo)
		}
	}
	if k := c.Sharing.Key; k != "" {
		if _, err := share.ParseKey(k); err != nil {
			return fmt.Errorf("%s: sharing.key: %w", path, err)
		}
	}
	if n := c.Sharing.KeyRules.MinLength; n != 0 && n < 16 {
		return fmt.Errorf("%s: sharing.keyRules.minLength: %d is under 16", path, n)
	}
	if n := c.Sharing.KeyRules.MinClasses; n < 0 || n > 4 {
		return fmt.Errorf("%s: sharing.keyRules.minClasses: %d is not between 1 and 4", path, n)
	}
	for name, d := range map[string]time.Duration{
		"providers.timeout":            c.Providers.Timeout,
		"providers.azure.timeout":      c.Providers.Azure.Timeout,
		"providers.databricks.timeout": c.Providers.Databricks.Timeout,
		"providers.repos.timeout":      c.Providers.Repos.Timeout,
	} {
		if d < 0 {
			return fmt.Errorf("%s: %s: %s is negative", path, name, d)
		}
	}
	if err := errors.Join(c.Providers.Repos.SecretPatterns.Check("providers.repos.secretPatterns"),
		c.Providers.Repos.Spring.SecretPatterns.Check("providers.repos.spring.secretPatterns"),
		c.Providers.Repos.Compose.SecretPatterns.Check("providers.repos.compose.secretPatterns")); err != nil {
		return fmt.Errorf("%s: %w", path, err)
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
