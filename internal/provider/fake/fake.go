// Package fake is an in-memory provider used by tests and --demo mode.
package fake

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// Provider serves a fixed tree of sources, containers and objects,
// optionally sleeping before every call to simulate network latency.
type Provider struct {
	kind       provider.Kind
	id, label  string
	delay      time.Duration
	sources    []provider.Source
	containers map[string][]provider.Container // by source ID
	objects    map[listKey][]provider.Object
	errs       map[listKey]error
	values     map[string]string            // by object ID
	versions   map[string][]provider.Object // by object ID
	details    map[string][]provider.Attr   // by container ID
}

type listKey struct {
	container string
	typ       provider.ObjectType
}

var _ provider.Provider = (*Provider)(nil)

func newProvider(kind provider.Kind, delay time.Duration) *Provider {
	return &Provider{
		kind:       kind,
		delay:      delay,
		containers: map[string][]provider.Container{},
		objects:    map[listKey][]provider.Object{},
		errs:       map[listKey]error{},
		values:     map[string]string{},
		versions:   map[string][]provider.Object{},
	}
}

func (p *Provider) Kind() provider.Kind { return p.kind }

func (p *Provider) ID() string {
	if p.id != "" {
		return p.id
	}
	return string(p.kind)
}

func (p *Provider) Label() string {
	if p.label != "" {
		return p.label
	}
	return string(p.kind)
}

// As renames the provider, to simulate several accounts of one kind.
func (p *Provider) As(id, label string) *Provider {
	p.id, p.label = id, label
	return p
}

func (p *Provider) Sources(ctx context.Context) ([]provider.Source, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.sources, nil
}

func (p *Provider) Containers(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.containers[src.ID], nil
}

func (p *Provider) Objects(ctx context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	k := listKey{p.containerID(c), t}
	if err := p.errs[k]; err != nil {
		return nil, err
	}
	return p.objects[k], nil
}

// Describe returns o unchanged; the fixtures already carry their details.
func (p *Provider) Describe(ctx context.Context, o provider.Object) (provider.Object, error) {
	return o, ctx.Err()
}

func (p *Provider) Versions(ctx context.Context, o provider.Object) ([]provider.Object, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	if p.kind != provider.KindAzure { // only Key Vault keeps versions
		return nil, provider.ErrUnsupported
	}
	vs := p.versions[o.ID]
	if len(vs) == 0 { // every object has at least its current version
		return []provider.Object{o}, nil
	}
	out := make([]provider.Object, len(vs))
	for i, v := range vs {
		v.Container, v.Type, v.ID, v.Name, v.Reference, v.URL = o.Container, o.Type, o.ID, o.Name, o.Reference, o.URL
		out[i] = v
	}
	return out, nil
}

func (p *Provider) Value(ctx context.Context, o provider.Object) (provider.SecretValue, error) {
	if err := p.wait(ctx); err != nil {
		return provider.SecretValue{}, err
	}
	if o.ID == "" { // built from a link, like the real providers read it
		o.ID = p.containerID(o.Container) + "/" + strings.ToLower(string(cmp.Or(o.Type, provider.TypeSecret))) + "/" + o.Name
	}
	v, ok := p.values[o.ID+"@"+o.Version]
	if !ok {
		v, ok = p.values[o.ID]
	}
	if !ok {
		return provider.SecretValue{}, fmt.Errorf("%s %q not found", strings.ToLower(string(o.Type)), o.Name)
	}
	return provider.NewSecretValue([]byte(v)), nil
}

// Locate finds the source holding the vault (by host) or the workspace.
func (p *Provider) Locate(ctx context.Context, host string) (string, bool, error) {
	if err := p.wait(ctx); err != nil {
		return "", false, err
	}
	for _, src := range p.sources {
		if p.kind == provider.KindDatabricks && src.Detail == host {
			return src.ID, true, nil
		}
		for _, c := range p.containers[src.ID] {
			if c.Detail == "https://"+host {
				return src.ID, true, nil
			}
		}
	}
	return "", false, nil
}

func (p *Provider) wait(ctx context.Context) error {
	if p.delay <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(p.delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const portal = "https://portal.azure.com/#resource"

// containerID finds a container by ID, or by vault URL like the real Azure
// provider, which only needs the URL (vaults opened from links).
func (p *Provider) containerID(c provider.Container) string {
	for _, cs := range p.containers {
		for _, x := range cs {
			if x.ID == c.ID {
				return x.ID
			}
		}
	}
	// Not one listed here (built from a link): by vault URL, or a scope
	// by name in its workspace.
	for _, cs := range p.containers {
		for _, x := range cs {
			if c.Detail != "" && strings.TrimSuffix(x.Detail, "/") == strings.TrimSuffix(c.Detail, "/") {
				return x.ID
			}
			if c.Detail == "" && c.Name == x.Name && c.Source.ID != "" && c.Source.ID == x.Source.ID {
				return x.ID
			}
		}
	}
	return c.ID
}

func (p *Provider) addSource(id, name, detail string) provider.Source {
	s := provider.Source{Kind: p.kind, ID: id, Name: name, Detail: detail}
	if p.kind == provider.KindAzure {
		s.URL = portal + "/subscriptions/" + id + "/overview"
	} else {
		s.URL = "https://" + detail
	}
	p.sources = append(p.sources, s)
	return s
}

func (p *Provider) addContainer(src provider.Source, name, detail string, types ...provider.ObjectType) provider.Container {
	c := provider.Container{Source: src, ID: src.ID + "/" + name, Name: name, Detail: detail, Types: types}
	vault := name
	if p.kind == provider.KindDatabricks {
		vault, _ = strings.CutPrefix(detail, "Azure Key Vault-backed: ")
	}
	if p.kind == provider.KindAzure || (p.kind == provider.KindDatabricks && vault != detail) {
		c.URL = portal + "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/" + vault + "/overview"
	}
	p.containers[src.ID] = append(p.containers[src.ID], c)
	return c
}

func (p *Provider) add(c provider.Container, t provider.ObjectType, o provider.Object, value string) provider.Object {
	o.Container = c
	o.Type = t
	o.ID = c.ID + "/" + strings.ToLower(string(t)) + "/" + o.Name
	if base, ok := strings.CutSuffix(c.URL, "/overview"); ok {
		o.URL = base + map[provider.ObjectType]string{
			provider.TypeSecret: "/secrets", provider.TypeKey: "/keys", provider.TypeCertificate: "/certificates",
		}[t]
	}
	k := listKey{c.ID, t}
	p.objects[k] = append(p.objects[k], o)
	p.values[o.ID] = value
	return o
}

func (p *Provider) fail(c provider.Container, err error, types ...provider.ObjectType) {
	for _, t := range types {
		p.errs[listKey{c.ID, t}] = err
	}
}

var (
	base = time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC)
	yes  = true
	no   = false
)

// ErrForbidden mimics a data-plane permission error from a backend.
var ErrForbidden = errors.New("403 Forbidden: caller lacks permission to list secrets")

// ErrForbiddenKeys mimics a vault where the caller can read secrets but not keys.
var ErrForbiddenKeys = errors.New("403 Forbidden: caller lacks permission to list keys")

const (
	demoPublicKey = "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAdemo\n-----END PUBLIC KEY-----"
	demoCert      = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUDEMO\nDEMODEMODEMO\n-----END CERTIFICATE-----"
)

var allTypes = []provider.ObjectType{provider.TypeSecret, provider.TypeKey, provider.TypeCertificate}

// Azure returns a fake Azure provider with two subscriptions.
func Azure(delay time.Duration) *Provider {
	p := newProvider(provider.KindAzure, delay)
	secretRef := func(vault, name string) string {
		return fmt.Sprintf("@Microsoft.KeyVault(SecretUri=https://%s.vault.azure.net/secrets/%s)", vault, name)
	}
	objectURL := func(vault, kind, name string) string {
		return fmt.Sprintf("https://%s.vault.azure.net/%s/%s", vault, kind, name)
	}

	prod := p.addSource("sub-prod", "platform-prod", "tenant: example.onmicrosoft.com")
	kv := p.addContainer(prod, "kv-app-prod", "https://kv-app-prod.vault.azure.net", allTypes...)
	dbPassword := p.add(kv, provider.TypeSecret, provider.Object{
		Name: "db-password", Version: "3f9c2a", Enabled: &yes, Updated: base,
		Expires: base.AddDate(1, 0, 0), ContentType: "text/plain",
		Tags: map[string]string{"owner": "data", "env": "prod"}, Reference: secretRef("kv-app-prod", "db-password"),
	}, "correct-horse-battery-staple")
	p.versions[dbPassword.ID] = []provider.Object{
		{Version: "3f9c2a", Enabled: &yes, Updated: base, Expires: base.AddDate(1, 0, 0), ContentType: "text/plain"},
		{Version: "91d3e0", Enabled: &yes, Updated: base.AddDate(0, -6, 0), Expires: base.AddDate(0, -1, 0)},
		{Version: "0a77c4", Enabled: &no, Updated: base.AddDate(-1, 0, 0)},
	}
	p.values[dbPassword.ID+"@91d3e0"] = "previous-password"
	p.values[dbPassword.ID+"@0a77c4"] = "original-password"
	p.add(kv, provider.TypeSecret, provider.Object{
		Name: "api-key", Version: "a17b04", Enabled: &yes, Updated: base.AddDate(0, -2, 0),
		Reference: secretRef("kv-app-prod", "api-key"),
	}, "sk_live_51HxDEMO0000000000000000")
	p.add(kv, provider.TypeSecret, provider.Object{
		Name: "legacy-token", Version: "0c11de", Enabled: &no, Updated: base.AddDate(-1, 0, 0),
		Reference: secretRef("kv-app-prod", "legacy-token"),
	}, "deprecated")

	p.add(kv, provider.TypeKey, provider.Object{
		Name: "signing-key", Version: "5e21b7", Enabled: &yes, Updated: base.AddDate(0, -3, 0),
		Attrs: []provider.Attr{
			{Label: "key type", Value: "RSA 2048"},
			{Label: "ops", Value: "sign, verify"},
		},
		Reference: objectURL("kv-app-prod", "keys", "signing-key"),
	}, demoPublicKey)
	p.add(kv, provider.TypeKey, provider.Object{
		Name: "data-encryption", Version: "c40f19", Enabled: &yes, Updated: base.AddDate(0, -5, 0),
		Attrs: []provider.Attr{
			{Label: "key type", Value: "EC P-256"},
			{Label: "ops", Value: "wrapKey, unwrapKey"},
		},
		Reference: objectURL("kv-app-prod", "keys", "data-encryption"),
	}, demoPublicKey)

	p.add(kv, provider.TypeCertificate, provider.Object{
		Name: "app-tls", Version: "77e0aa", Enabled: &yes, Updated: base.AddDate(0, -1, 0),
		Expires: base.AddDate(0, 11, 0), ContentType: "application/x-pem-file",
		Attrs: []provider.Attr{
			{Label: "subject", Value: "CN=app.example.com"},
			{Label: "issuer", Value: "CN=Example Issuing CA"},
			{Label: "thumbprint", Value: "3B:1F:9A:0C:44:E2:7D:15"},
			{Label: "SANs", Value: "app.example.com, www.app.example.com"},
			{Label: "valid from", Value: "2026-08-01 09:30"},
		},
		Reference: objectURL("kv-app-prod", "certificates", "app-tls"),
	}, demoCert)
	p.add(kv, provider.TypeCertificate, provider.Object{
		Name: "old-tls", Version: "19ab3c", Enabled: &yes, Updated: base.AddDate(-1, 0, 0),
		Expires: base.AddDate(0, -6, 0), ContentType: "application/x-pkcs12",
		Attrs: []provider.Attr{
			{Label: "subject", Value: "CN=old.example.com"},
			{Label: "issuer", Value: "CN=Example Issuing CA"},
			{Label: "thumbprint", Value: "A0:77:12:DE:9B:03:C6:58"},
		},
		Reference: objectURL("kv-app-prod", "certificates", "old-tls"),
	}, demoCert)

	shared := p.addContainer(prod, "kv-shared-prod", "https://kv-shared-prod.vault.azure.net", allTypes...)
	p.add(shared, provider.TypeSecret, provider.Object{
		Name: "smtp-password", Enabled: &yes, Updated: base, Reference: secretRef("kv-shared-prod", "smtp-password"),
	}, "smtp-demo-password")
	p.fail(shared, ErrForbiddenKeys, provider.TypeKey)

	locked := p.addContainer(prod, "kv-restricted", "https://kv-restricted.vault.azure.net", allTypes...)
	p.fail(locked, ErrForbidden, allTypes...)

	dev := p.addSource("sub-dev", "platform-dev", "tenant: example.onmicrosoft.com")
	kvDev := p.addContainer(dev, "kv-app-dev", "https://kv-app-dev.vault.azure.net", allTypes...)
	p.add(kvDev, provider.TypeSecret, provider.Object{
		Name: "db-password", Enabled: &yes, Updated: base, Reference: secretRef("kv-app-dev", "db-password"),
	}, "dev-password")

	return p
}

// AzureOps returns a second fake Azure account. It shares platform-prod
// with Azure but sees a different vault there, and has a subscription of
// its own, like an admin account next to a personal one.
func AzureOps(delay time.Duration) *Provider {
	p := newProvider(provider.KindAzure, delay)
	secretRef := func(vault, name string) string {
		return fmt.Sprintf("@Microsoft.KeyVault(SecretUri=https://%s.vault.azure.net/secrets/%s)", vault, name)
	}

	prod := p.addSource("sub-prod", "platform-prod", "tenant: example.onmicrosoft.com")
	breakGlass := p.addContainer(prod, "kv-breakglass", "https://kv-breakglass.vault.azure.net", allTypes...)
	p.add(breakGlass, provider.TypeSecret, provider.Object{
		Name: "emergency-admin", Enabled: &yes, Updated: base.AddDate(0, -4, 0),
		Tags: map[string]string{"owner": "ops"}, Reference: secretRef("kv-breakglass", "emergency-admin"),
	}, "break-glass-demo-password")
	shared := p.addContainer(prod, "kv-shared-prod", "https://kv-shared-prod.vault.azure.net", allTypes...)
	p.add(shared, provider.TypeSecret, provider.Object{
		Name: "smtp-password", Enabled: &yes, Updated: base, Reference: secretRef("kv-shared-prod", "smtp-password"),
	}, "smtp-demo-password")
	// Unlike the first account, ops may read keys here.
	p.add(shared, provider.TypeKey, provider.Object{
		Name: "backup-encryption", Version: "e9a102", Enabled: &yes, Updated: base.AddDate(0, -2, 0),
		Attrs:     []provider.Attr{{Label: "key type", Value: "RSA-HSM 3072"}, {Label: "ops", Value: "wrapKey, unwrapKey"}},
		Reference: "https://kv-shared-prod.vault.azure.net/keys/backup-encryption",
	}, demoPublicKey)

	sandbox := p.addSource("sub-ops", "ops-sandbox", "tenant: example.onmicrosoft.com")
	kv := p.addContainer(sandbox, "kv-ops-tools", "https://kv-ops-tools.vault.azure.net", allTypes...)
	p.add(kv, provider.TypeSecret, provider.Object{
		Name: "pagerduty-token", Enabled: &yes, Updated: base.AddDate(0, -1, 0), Reference: secretRef("kv-ops-tools", "pagerduty-token"),
	}, "pd-demo-token")

	return p
}

// Databricks returns a fake Databricks provider with two workspace profiles,
// including an Azure Key Vault-backed scope. Scopes hold secrets only.
func Databricks(delay time.Duration) *Provider {
	p := newProvider(provider.KindDatabricks, delay)
	secret := func(c provider.Container, key, value string) {
		p.add(c, provider.TypeSecret, provider.Object{
			Name: key, Updated: base, Reference: fmt.Sprintf("{{secrets/%s/%s}}", c.Name, key),
		}, value)
	}

	prod := p.addSource("dbx-prod", "prod", "adb-1111111111111111.11.azuredatabricks.net")
	app := p.addContainer(prod, "app", "Databricks-backed")
	secret(app, "storage-key", "storage-demo-key")
	secret(app, "sp-client-secret", "sp-demo-secret")
	akv := p.addContainer(prod, "kv-app-prod", "Azure Key Vault-backed: kv-app-prod")
	secret(akv, "db-password", "correct-horse-battery-staple")

	dev := p.addSource("dbx-dev", "dev", "adb-2222222222222222.2.azuredatabricks.net")
	devApp := p.addContainer(dev, "app", "Databricks-backed")
	secret(devApp, "storage-key", "dev-storage-key")

	return p
}
