package main

import (
	"context"
	"strings"
	"testing"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

func TestValue(t *testing.T) {
	providers := []provider.Provider{fake.Azure(0), fake.Databricks(0)}
	st, _ := store.Load("")
	for link, want := range map[string]string{
		"lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password":                  "correct-horse-battery-staple",
		"lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password/0a77c4":           "original-password",
		"lazysecrets://databricks/adb-1111111111111111.11.azuredatabricks.net/app/storage-key": "storage-demo-key",
	} {
		r, _ := ref.Parse(link)
		v, _, err := readKey(context.Background(), providers, st, r)
		if err != nil || v.Reveal() != want {
			t.Errorf("%s: %q, %v", link, v.Reveal(), err)
		}
	}
	for link, wantErr := range map[string]string{
		"lazysecrets://azure/kv-app-prod.vault.azure.net":                      "not a secret",
		"lazysecrets://databricks/adb-9.9.azuredatabricks.net/app/storage-key": "no Databricks profile",
		"{{secrets/app/storage-key}}":                                          "names no workspace",
	} {
		r, _ := ref.Parse(link)
		if _, _, err := readKey(context.Background(), providers, st, r); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: %v, want %q", link, err, wantErr)
		}
	}
}

func TestStarredRepos(t *testing.T) {
	st, _ := store.Load("")
	st.Update("lazysecrets://repo/git.example.com%2Fa/secrets%2Fx.yaml/db.password", func(n *store.Node) {
		n.Source, n.StarredBy = "/repos/a", []string{"Repos"}
	})
	st.Update("lazysecrets://repo/git.example.com%2Fb", func(n *store.Node) { n.Source, n.Note = "/repos/b", "noted, not starred" })
	st.Update("lazysecrets://azure/kv.vault.azure.net", func(n *store.Node) { n.Source, n.StarredBy = "sub", []string{"Azure"} })
	if got := starredRepos(st); len(got) != 1 || got[0] != "/repos/a" {
		t.Errorf("starred repos %v", got)
	}
}
