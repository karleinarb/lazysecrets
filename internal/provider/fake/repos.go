package fake

import (
	"context"
	"strings"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

var _ provider.ContainerDetails = (*Provider)(nil)

// Details returns a file's details, like the repo provider's.
func (p *Provider) Details(ctx context.Context, c provider.Container) ([]provider.Attr, error) {
	attrs, ok := p.details[c.ID]
	if !ok {
		return nil, provider.ErrUnsupported
	}
	return attrs, ctx.Err()
}

// Repos returns a fake repo provider: three git repos with SOPS and plain
// .env files, the first one the repo lazysecrets was started in.
func Repos() *Provider {
	p := newProvider(provider.KindRepo, 0)
	p.id, p.label = "Repos", "Repos"
	p.details = map[string][]provider.Attr{}

	repo := func(name string, here bool) provider.Source {
		s := p.addSource("/home/dev/git/"+name, name, "github.com/acme/"+name)
		s.Here = here
		p.sources[len(p.sources)-1] = s
		return s
	}
	// file adds a file and its keys, alternating name and value. git and
	// commit are what git says of it: "tracked", "ignored" or "untracked".
	file := func(src provider.Source, path, method, git, commit string, kv ...string) {
		sops := method != ""
		detail := "env · plain text"
		if sops {
			detail = "sops · " + method
		}
		c := p.addContainer(src, path, detail, provider.TypeSecret)
		attrs := []provider.Attr{{Label: "format", Value: "env"}, {Label: "encrypted", Value: "no: plain text in the file"}}
		if sops {
			attrs = []provider.Attr{{Label: "format", Value: "sops " + format(path)}, {Label: "encryption", Value: method}}
		}
		for i := 0; i+1 < len(kv); i += 2 {
			o := provider.Object{Name: kv[i], Attrs: attrs}
			if sops {
				o.Updated = base
			}
			p.add(c, provider.TypeSecret, o, kv[i+1])
		}

		d := []provider.Attr{{Label: "encryption", Value: "none: plain text"}}
		if sops {
			d = []provider.Attr{{Label: "encryption", Value: detail}}
			keys := map[string]string{
				"age":      "age age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p",
				"azure_kv": "azure_kv https://kv-sops-prod.vault.azure.net/keys/sops/1f2e3d4c",
			}
			for i, m := range strings.Split(method, ", ") {
				label := ""
				if i == 0 {
					label = "keys" // on the first only, like the repo provider
				}
				d = append(d, provider.Attr{Label: label, Value: keys[m]})
			}
			d = append(d, provider.Attr{Label: "encrypted", Value: base.Local().Format("2006-01-02 15:04")})
		}
		switch {
		case git == "tracked" && !sops:
			d = append(d, provider.Attr{Label: "git", Value: "tracked: its values are in the repo's history", Warn: true})
		case git == "untracked":
			d = append(d, provider.Attr{Label: "git", Value: "untracked and not ignored: easy to commit by mistake", Warn: !sops})
		default:
			d = append(d, provider.Attr{Label: "git", Value: git})
		}
		if commit != "" {
			d = append(d, provider.Attr{Label: "commit", Value: commit})
		}
		p.details[c.ID] = d
	}

	api := repo("lazysecrets-demo", true)
	file(api, "deploy/prod/secrets.yaml", "age, azure_kv", "tracked", "4f9c2ab · 3 days ago",
		"database.password", "pr0d-Pg-7f3k9QwZ",
		"database.url", "postgres://app:pr0d-Pg-7f3k9QwZ@pg-demo-prod:5432/app",
		"stripe.secret_key", "sk_live_demo_51NxDemoKey0000",
		"jwt.signing_key", "c2lnbmluZy1rZXktZm9yLXRoZS1kZW1v",
		"gcp.service_account", `{"type":"service_account","project_id":"lazysecrets-demo","client_email":"app@lazysecrets-demo.iam.gserviceaccount.com","private_key_id":"demo1234"}`)
	file(api, "deploy/staging/secrets.yaml", "age", "tracked", "a71e05d · 2 weeks ago",
		"database.password", "stg-Pg-2b8d1L",
		"database.url", "postgres://app:stg-Pg-2b8d1L@pg-demo-stg:5432/app",
		"stripe.secret_key", "sk_test_demo_51NxDemoKey0000")
	file(api, "config/oauth.enc.json", "age", "tracked", "e03b6f1 · 2 months ago",
		"github.client_secret", "ghs_demo0000000000000000000000",
		"google.client_secret", "GOCSPX-demo-000000000000")
	file(api, ".env", "", "ignored", "",
		"DATABASE_URL", "postgres://app:local@localhost:5432/app",
		"STRIPE_SECRET_KEY", "sk_test_demo_local",
		"LOG_LEVEL", "debug")

	infra := repo("platform-infra", false)
	file(infra, "terraform/prod/secrets.enc.yaml", "azure_kv", "tracked", "9d40c3e · yesterday",
		"postgres.admin_password", "infra-Pg-admin-0Xq",
		"grafana.admin_password", "grafana-demo-admin",
		"acr.pull_token", "acr-demo-pull-token")
	file(infra, "k8s/monitoring/alertmanager.enc.yaml", "age", "tracked", "1c58e7a · 5 days ago",
		"slack.webhook_url", "https://hooks.slack.com/services/T000/B000/demo",
		"pagerduty.routing_key", "pd-demo-routing-key")
	file(infra, "scripts/.env", "", "tracked", "77b2d90 · 8 months ago",
		"AZURE_CLIENT_SECRET", "azure-demo-client-secret",
		"ARM_SUBSCRIPTION_ID", "00000000-0000-0000-0000-000000000000")

	web := repo("web-frontend", false)
	file(web, ".env.production", "age", "tracked", "b2e91f4 · 4 days ago",
		"SENTRY_AUTH_TOKEN", "sntrys_demo_token",
		"NEXT_PUBLIC_API_URL", "https://api.acme.example")
	file(web, ".env.local", "", "untracked", "",
		"NEXT_PUBLIC_API_URL", "http://localhost:8080",
		"ANALYTICS_KEY", "demo-analytics-key")

	return p
}

// format is a SOPS file's format, by its name.
func format(path string) string {
	switch {
	case strings.HasSuffix(path, ".json"):
		return "json"
	case strings.HasSuffix(path, ".yaml"):
		return "yaml"
	}
	return "dotenv"
}
