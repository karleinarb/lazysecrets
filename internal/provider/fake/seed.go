package fake

import (
	"cmp"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/store"
)

// Seed stars, notes and uses a few of the demo's secrets, so --demo
// shows Favourites (F), Recent (R), notes, and stars in lazysecrets list. alice and
// admin are the two Azure accounts' provider IDs.
func Seed(st *store.Store, alice, admin string) {
	type item struct {
		link, account, source string
		name                  string // else the link's last part
		note                  string
		path                  []string
		star                  bool
		uses                  int
		ago                   time.Duration
	}
	const azure, dbx = "lazysecrets://azure/", "lazysecrets://databricks/adb-1111111111111111.11.azuredatabricks.net/"
	for _, it := range []item{
		{link: azure + "kv-app-prod.vault.azure.net/secrets/db-password", account: alice, source: "sub-prod",
			path: []string{"platform-prod", "kv-app-prod"}, star: true, uses: 12, ago: 20 * time.Minute,
			note: "rotated by the data team each quarter"},
		{link: azure + "kv-app-prod.vault.azure.net/secrets/api-key", account: alice, source: "sub-prod",
			path: []string{"platform-prod", "kv-app-prod"}, uses: 4, ago: 2 * time.Hour},
		{link: azure + "kv-breakglass.vault.azure.net/secrets/emergency-admin", account: admin, source: "sub-prod",
			path: []string{"platform-prod", "kv-breakglass"}, star: true, note: "incidents only: tell #ops when used"},
		{link: azure + "kv-shared-prod.vault.azure.net/secrets/smtp-password", account: alice, source: "sub-prod",
			path: []string{"platform-prod", "kv-shared-prod"}, uses: 2, ago: 26 * time.Hour},
		{link: azure + "kv-app-prod.vault.azure.net/secrets/smtp-password", account: alice, source: "sub-prod",
			path: []string{"platform-prod", "kv-app-prod"}, note: "migrated to Microsoft Graph sendMail; delete after Q4"},
		{link: dbx + "app/storage-key", account: "Databricks", source: "dbx-prod",
			path: []string{"prod", "app"}, star: true, uses: 3, ago: 5 * time.Hour},
		// A subscription, for both accounts, and a repo's file.
		{link: azure + "subscriptions/sub-prod", account: alice, source: "sub-prod",
			name: "platform-prod", star: true},
		{link: azure + "subscriptions/sub-prod", account: admin, source: "sub-prod",
			name: "platform-prod", star: true},
		{link: "lazysecrets://repo/github.com%2Facme%2Flazysecrets-demo/deploy%2Fprod%2Fsecrets.yaml", account: "Repos",
			source: "/home/dev/git/lazysecrets-demo", name: "deploy/prod/secrets.yaml",
			path: []string{"lazysecrets-demo"}, star: true},
	} {
		context := func(n *store.Node) {
			n.Name = cmp.Or(it.name, it.link[strings.LastIndex(it.link, "/")+1:])
			n.Path, n.Source, n.Account = it.path, it.source, it.account
		}
		if it.star || it.note != "" {
			_ = st.Update(it.link, func(n *store.Node) {
				context(n)
				if it.star {
					n.ToggleStar(it.account)
				}
				n.Note = it.note
			})
		}
		for range it.uses {
			_ = st.Use(it.link, time.Now().Add(-it.ago), context)
		}
	}
}
