package panes

import (
	"strings"
	"testing"
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
