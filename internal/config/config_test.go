package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	c, err := LoadAll([]string{write(t, `
ui:
  icons: false
  sourcesView: flat
values:
  clipboardClear: 45s
storage:
  history: false
providers:
  timeout: 1m
  azure:
    timeout: 2m
    hideSubscriptions: [platform-mgmt]
    vaults:
      - https://kv-foo.vault.azure.net/
  databricks:
    enabled: false
    hideProfiles: [test]
`)})
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.Icons == nil || *c.UI.Icons || c.Values.ClipboardClear == nil || *c.Values.ClipboardClear != 45*time.Second ||
		c.Providers.Timeout != time.Minute || c.Providers.Azure.Timeout != 2*time.Minute || len(c.Providers.Azure.Vaults) != 1 || c.Providers.Databricks.HideProfiles[0] != "test" {
		t.Errorf("config = %+v", c)
	}
	if c.UI.SourcesView != "flat" || On(c.Storage.History) || !On(c.Storage.Cache) {
		t.Errorf("ui = %+v, storage = %+v", c.UI, c.Storage)
	}
	if !On(c.Providers.Azure.Enabled) || On(c.Providers.Databricks.Enabled) {
		t.Error("enabled flags wrong")
	}
	az := c.Providers.Azure
	if !Hidden(az.HideSubscriptions, "PLATFORM-MGMT", "id") || Hidden(az.HideSubscriptions, "other", "id") {
		t.Error("Hidden matching wrong")
	}
}

func TestLoadMissingAndEmpty(t *testing.T) {
	for name, path := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "nope.yaml"),
		"empty":   write(t, ""),
	} {
		c, err := LoadAll([]string{path})
		if err != nil || c.UI.Icons != nil || c.Values.ClipboardClear != nil {
			t.Errorf("%s: %+v, %v", name, c, err)
		}
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	for name, content := range map[string]string{
		"typo":       "icon: true\n",
		"bad vault":  "providers:\n  azure:\n    vaults: [kv-foo]\n",
		"old layout": "azure:\n  vaults: [https://kv.vault.azure.net/]\n",
		"flat keys":  "icons: false\n",
		"duration":   "providers:\n  timeout: soon\n",
		"negative":   "providers:\n  repos:\n    timeout: -1s\n",
		"view":       "ui:\n  sourcesView: tree\n",
		"sources":    "updates:\n  sources: [git.example.com/team/lazysecrets]\n",
		"http":       "updates:\n  sources: [http://git.example.com/team/lazysecrets]\n",
		"interval":   "updates:\n  autoCheckInterval: 0s\n",
		"share key":  "sharing:\n  key: https://kv.vault.azure.net/\n",
		"workspace":  "sharing:\n  key: \"{{secrets/team/env-share}}\"\n",
		"short keys": "sharing:\n  keyRules:\n    minLength: 8\n",
		"classes":    "sharing:\n  keyRules:\n    minClasses: 5\n",
		"regexp":     "providers:\n  repos:\n    spring:\n      keys: [\"(pass\"]\n",
	} {
		if _, err := LoadAll([]string{write(t, content)}); err == nil {
			t.Errorf("%s: no error", name)
		} else if !strings.Contains(err.Error(), "config.yaml") {
			t.Errorf("%s: error doesn't name the file: %v", name, err)
		}
	}
}

func TestLoadAllLayers(t *testing.T) {
	user := write(t, `
ui:
  icons: false
theme:
  azureColor: [blue]
  errorColor: [red, bold]
providers:
  azure:
    hideSubscriptions: [a]
`)
	theme := write(t, `
ui:
  sourcesView: flat
theme:
  azureColor: ["#82aaff"]
  activeBorderColor: ["#ff966c", bold]
`)
	c, err := LoadAll([]string{user, filepath.Join(t.TempDir(), "missing.yaml"), theme})
	if err != nil {
		t.Fatal(err)
	}
	// A group set in both files merges: icons from one, sourcesView the other.
	if c.UI.Icons == nil || *c.UI.Icons || c.UI.SourcesView != "flat" || len(c.Providers.Azure.HideSubscriptions) != 1 {
		t.Errorf("user settings lost: %+v", c)
	}
	want := map[string][]string{
		"azureColor":        {"#82aaff"},     // overridden by the later file
		"errorColor":        {"red", "bold"}, // kept from the user's file
		"activeBorderColor": {"#ff966c", "bold"},
	}
	for role, v := range want {
		if strings.Join(c.Theme[role], ",") != strings.Join(v, ",") {
			t.Errorf("theme %s = %v, want %v", role, c.Theme[role], v)
		}
	}
}

func TestUpdates(t *testing.T) {
	c, err := LoadAll([]string{write(t, "updates:\n  autoCheck: false\n  autoCheckInterval: 168h\n  sources: [https://git.example.com/team/lazysecrets]\n")})
	if err != nil || On(c.Updates.AutoCheck) || *c.Updates.AutoCheckInterval != 7*24*time.Hour || len(c.Updates.Sources) != 1 {
		t.Errorf("updates = %+v, %v", c.Updates, err)
	}
}

func TestLoadDelay(t *testing.T) {
	c, err := LoadAll([]string{write(t, "ui:\n  loadDelay: 300ms\n")})
	if err != nil || c.UI.LoadDelay == nil || *c.UI.LoadDelay != 300*time.Millisecond {
		t.Errorf("loadDelay = %v, %v", c.UI.LoadDelay, err)
	}
	if _, err := LoadAll([]string{write(t, "ui:\n  loadDelay: 1m\n")}); err == nil {
		t.Error("a minute's delay accepted")
	}
}

func TestKeybindingLayers(t *testing.T) {
	a := write(t, "keybinding:\n  lists:\n    star: [x]\n    note: [N]\n")
	b := write(t, "keybinding:\n  lists:\n    note: [m]\n  filter:\n    star: [ctrl+x]\n")
	c, err := LoadAll([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	kb := c.Keybinding
	if got := kb["lists"]["star"]; len(got) != 1 || got[0] != "x" {
		t.Errorf("lists.star from the first file lost: %v", kb["lists"])
	}
	if got := kb["lists"]["note"]; len(got) != 1 || got[0] != "m" {
		t.Errorf("lists.note = %v, want the later file's", got)
	}
	if got := kb["filter"]["star"]; len(got) != 1 || got[0] != "ctrl+x" {
		t.Errorf("filter.star = %v", got)
	}
}
