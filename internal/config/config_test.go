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
	c, err := Load(write(t, `
icons: false
sourcesView: flat
clipboardClear: 45s
timeout: 1m
providers:
  azure:
    hideSubscriptions: [platform-mgmt]
    vaults:
      - https://kv-foo.vault.azure.net/
  databricks:
    enabled: false
    hideProfiles: [test]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Icons == nil || *c.Icons || c.ClipboardClear == nil || *c.ClipboardClear != 45*time.Second ||
		c.Timeout != time.Minute || len(c.Providers.Azure.Vaults) != 1 || c.Providers.Databricks.HideProfiles[0] != "test" {
		t.Errorf("config = %+v", c)
	}
	if c.SourcesView != "flat" {
		t.Errorf("sourcesView = %q", c.SourcesView)
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
		c, err := Load(path)
		if err != nil || c.Icons != nil || c.ClipboardClear != nil {
			t.Errorf("%s: %+v, %v", name, c, err)
		}
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	for name, content := range map[string]string{
		"typo":       "icon: true\n",
		"bad vault":  "providers:\n  azure:\n    vaults: [kv-foo]\n",
		"old layout": "azure:\n  vaults: [https://kv.vault.azure.net/]\n",
		"duration":   "timeout: soon\n",
		"view":       "sourcesView: tree\n",
		"releases":   "releases: [git.example.com/team/lazysecrets]\n",
		"http":       "releases: [http://git.example.com/team/lazysecrets]\n",
	} {
		if _, err := Load(write(t, content)); err == nil {
			t.Errorf("%s: no error", name)
		} else if !strings.Contains(err.Error(), "config.yaml") {
			t.Errorf("%s: error doesn't name the file: %v", name, err)
		}
	}
}

func TestLoadAllLayers(t *testing.T) {
	user := write(t, `
icons: false
theme:
  azureColor: [blue]
  errorColor: [red, bold]
providers:
  azure:
    hideSubscriptions: [a]
`)
	theme := write(t, `
theme:
  azureColor: ["#82aaff"]
  activeBorderColor: ["#ff966c", bold]
`)
	c, err := LoadAll([]string{user, filepath.Join(t.TempDir(), "missing.yaml"), theme})
	if err != nil {
		t.Fatal(err)
	}
	if c.Icons == nil || *c.Icons || len(c.Providers.Azure.HideSubscriptions) != 1 {
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

func TestReleases(t *testing.T) {
	c, err := Load(write(t, "releases: [https://git.example.com/team/lazysecrets]\n"))
	if err != nil || len(c.Releases) != 1 {
		t.Errorf("releases = %v, %v", c.Releases, err)
	}
}

func TestLoadDelay(t *testing.T) {
	c, err := Load(write(t, "loadDelay: 300ms\n"))
	if err != nil || c.LoadDelay == nil || *c.LoadDelay != 300*time.Millisecond {
		t.Errorf("loadDelay = %v, %v", c.LoadDelay, err)
	}
	if _, err := Load(write(t, "loadDelay: 1m\n")); err == nil {
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
