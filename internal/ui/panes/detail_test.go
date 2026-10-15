package panes

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/icons"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

func TestWrappedValueKeepsItsStyle(t *testing.T) {
	red := "\x1b[31m"
	got := carryStyle([]string{red + "@Microsoft.KeyVault(SecretUri=https://kv", "-app-prod.vault.azure.net/secrets/x)\x1b[m", "after"})
	if !strings.HasPrefix(got[1], red) {
		t.Errorf("the wrapped line lost the style: %q", got[1])
	}
	if strings.HasPrefix(got[2], red) {
		t.Errorf("the style went on past its reset: %q", got[2])
	}
}

func TestEmptyValue(t *testing.T) {
	d := NewDetail(icons.None)
	d.SetSize(80, 20)
	d.ShowSecret(provider.Object{Name: "spring.datasource.password", Type: provider.TypeSecret})
	d.SetValue(provider.NewSecretValue(nil))
	if view := ansi.Strip(d.View(styles.Default(), true, "")); !strings.Contains(view, "∅ no value (an empty string)") {
		t.Errorf("empty value:\n%s", view)
	}
	d.SetValue(provider.NewSecretValue([]byte("empty")))
	if view := ansi.Strip(d.View(styles.Default(), true, "")); strings.Contains(view, "∅") || !strings.Contains(view, "empty") {
		t.Errorf("the word empty:\n%s", view)
	}
}
