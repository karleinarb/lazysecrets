// Package azure is the Azure Key Vault provider. Sources are subscriptions,
// containers are vaults (found via Resource Graph), and objects are secrets,
// keys and certificates.
package azure

import (
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"git.kebcloud.no/keb/lazysecrets/internal/config"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

var objectTypes = []provider.ObjectType{provider.TypeSecret, provider.TypeKey, provider.TypeCertificate}

// Provider serves one Azure identity: an az CLI account or a service
// principal. Creating it doesn't contact Azure; authentication problems
// surface as *provider.AuthError from the first call.
type Provider struct {
	id, label string // "" for the single-account defaults

	cred    azcore.TokenCredential
	opts    options
	account *accountDetails // nil for a service principal from AZURE_*
	subs    *armsubscriptions.Client
	graph   *armresourcegraph.Client

	mu      sync.Mutex
	vaults  map[string]*vaultClients // by vault URL
	tenants map[string]string        // tenant ID by subscription ID, for portal links
}

type vaultClients struct {
	secrets *azsecrets.Client
	keys    *azkeys.Client
	certs   *azcertificates.Client
}

var _ provider.Provider = (*Provider)(nil)

// Settings come from the config file.
type Settings struct {
	// HideSubscriptions lists subscriptions to leave out, by name or ID.
	HideSubscriptions []string
	// Vaults are listed under a "Pinned vaults" source.
	Vaults []string
}

// pinnedID identifies the source holding vaults from the config file.
const pinnedID = "pinned-vaults"

// options lets tests point the SDK clients at a local server.
type options struct {
	Settings

	arm                     *arm.ClientOptions
	client                  azcore.ClientOptions
	skipChallengeValidation bool
}

func newWithOptions(cred azcore.TokenCredential, opts options) (*Provider, error) {
	subs, err := armsubscriptions.NewClient(cred, opts.arm)
	if err != nil {
		return nil, err
	}
	graph, err := armresourcegraph.NewClient(cred, opts.arm)
	if err != nil {
		return nil, err
	}
	return &Provider{
		cred: cred, opts: opts, subs: subs, graph: graph,
		vaults: map[string]*vaultClients{}, tenants: map[string]string{},
	}, nil
}

func (p *Provider) Kind() provider.Kind { return provider.KindAzure }

func (p *Provider) ID() string { return cmp.Or(p.id, string(provider.KindAzure)) }

func (p *Provider) Label() string { return cmp.Or(p.label, string(provider.KindAzure)) }

// Sources lists the subscriptions the signed-in identity can see, skipping
// disabled and deleted ones.
func (p *Provider) Sources(ctx context.Context) ([]provider.Source, error) {
	out, err := all(ctx, p.subs.NewListPager(nil), func(page armsubscriptions.ClientListResponse) (srcs []provider.Source) {
		for _, s := range page.Value {
			switch deref(s.State) {
			case armsubscriptions.SubscriptionStateDisabled, armsubscriptions.SubscriptionStateDeleted:
				continue
			}
			id, tenant := deref(s.SubscriptionID), deref(s.TenantID)
			if config.Hidden(p.opts.HideSubscriptions, deref(s.DisplayName), id) {
				continue
			}
			p.mu.Lock()
			p.tenants[id] = tenant
			p.mu.Unlock()
			srcs = append(srcs, provider.Source{
				Kind:   provider.KindAzure,
				ID:     id,
				Name:   deref(s.DisplayName),
				Detail: "subscription " + id,
				URL:    PortalResourceURL(tenant, "/subscriptions/"+id, "overview"),
			})
		}
		return srcs
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b provider.Source) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	if len(p.opts.Vaults) > 0 {
		out = append(out, provider.Source{
			Kind: provider.KindAzure, ID: pinnedID, Name: "Pinned vaults", Detail: "from the config file",
		})
	}
	return out, nil
}

// pinnedVaults lists the vaults from the config file. Without a resource ID
// they have no portal link.
func (p *Provider) pinnedVaults(src provider.Source) []provider.Container {
	out := make([]provider.Container, 0, len(p.opts.Vaults))
	for _, u := range p.opts.Vaults {
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(u, "https://"), ".")
		out = append(out, provider.Container{Source: src, ID: u, Name: name, Detail: u, Types: objectTypes})
	}
	return out
}

// Containers finds the subscription's vaults with Resource Graph, falling
// back to the Key Vault management API if the query fails.
func (p *Provider) Containers(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	if src.ID == pinnedID {
		return p.pinnedVaults(src), nil
	}
	vaults, err := p.graphVaults(ctx, src)
	if err != nil {
		var auth *provider.AuthError
		if errors.As(err, &auth) || ctx.Err() != nil {
			return nil, err
		}
		vaults, err = p.armVaults(ctx, src)
		if err != nil {
			return nil, err
		}
	}
	tenant := p.tenant(src.ID)
	for i := range vaults {
		vaults[i].URL = PortalResourceURL(tenant, vaults[i].ID, "overview")
	}
	slices.SortFunc(vaults, func(a, b provider.Container) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return vaults, nil
}

func (p *Provider) tenant(subscriptionID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tenants[subscriptionID]
}

func (p *Provider) graphVaults(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	var out []provider.Container
	req := armresourcegraph.QueryRequest{
		Query:         to.Ptr(vaultQuery),
		Subscriptions: []*string{to.Ptr(src.ID)},
		Options:       &armresourcegraph.QueryRequestOptions{ResultFormat: to.Ptr(armresourcegraph.ResultFormatObjectArray)},
	}
	for {
		resp, err := p.graph.Resources(ctx, req, nil)
		if err != nil {
			return nil, cleanError(err)
		}
		rows, ok := resp.Data.([]any)
		if !ok {
			return nil, fmt.Errorf("resource graph: unexpected result %T", resp.Data)
		}
		for _, r := range rows {
			if c, ok := graphVault(src, r); ok {
				out = append(out, c)
			}
		}
		if resp.SkipToken == nil || *resp.SkipToken == "" {
			return out, nil
		}
		req.Options.SkipToken = resp.SkipToken
	}
}

func graphVault(src provider.Source, row any) (provider.Container, bool) {
	m, ok := row.(map[string]any)
	if !ok {
		return provider.Container{}, false
	}
	id, _ := m["id"].(string)
	name, _ := m["name"].(string)
	uri, _ := m["uri"].(string)
	if id == "" || name == "" || uri == "" {
		return provider.Container{}, false
	}
	return provider.Container{Source: src, ID: id, Name: name, Detail: uri, Types: objectTypes, Attrs: graphInfo(m).attrs()}, true
}

func (p *Provider) armVaults(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	client, err := armkeyvault.NewVaultsClient(src.ID, p.cred, p.opts.arm)
	if err != nil {
		return nil, err
	}
	return all(ctx, client.NewListBySubscriptionPager(nil), func(page armkeyvault.VaultsClientListBySubscriptionResponse) (vaults []provider.Container) {
		for _, v := range page.Value {
			if v.Properties == nil || v.Properties.VaultURI == nil {
				continue
			}
			vaults = append(vaults, provider.Container{
				Source: src, ID: deref(v.ID), Name: deref(v.Name),
				Detail: *v.Properties.VaultURI, Types: objectTypes, Attrs: armInfo(v).attrs(),
			})
		}
		return vaults
	})
}

// all lists every page, items picking out what to keep of each.
func all[P, T any](ctx context.Context, pager *runtime.Pager[P], items func(P) []T) ([]T, error) {
	var out []T
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, cleanError(err)
		}
		out = append(out, items(page)...)
	}
	return out, nil
}

func (p *Provider) clients(vaultURL string) (*vaultClients, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.vaults[vaultURL]; ok {
		return c, nil
	}
	skip := p.opts.skipChallengeValidation
	s, err := azsecrets.NewClient(vaultURL, p.cred, &azsecrets.ClientOptions{
		ClientOptions: p.opts.client, DisableChallengeResourceVerification: skip,
	})
	if err != nil {
		return nil, err
	}
	k, err := azkeys.NewClient(vaultURL, p.cred, &azkeys.ClientOptions{
		ClientOptions: p.opts.client, DisableChallengeResourceVerification: skip,
	})
	if err != nil {
		return nil, err
	}
	c, err := azcertificates.NewClient(vaultURL, p.cred, &azcertificates.ClientOptions{
		ClientOptions: p.opts.client, DisableChallengeResourceVerification: skip,
	})
	if err != nil {
		return nil, err
	}
	vc := &vaultClients{secrets: s, keys: k, certs: c}
	p.vaults[vaultURL] = vc
	return vc, nil
}

func (p *Provider) Objects(ctx context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	vc, err := p.clients(c.Detail)
	if err != nil {
		return nil, err
	}
	var out []provider.Object
	switch t {
	case provider.TypeSecret:
		tenant := p.tenant(c.Source.ID)
		out, err = all(ctx, vc.secrets.NewListSecretPropertiesPager(nil), func(page azsecrets.ListSecretPropertiesResponse) (objs []provider.Object) {
			for _, s := range page.Value {
				// Certificate-backing secrets appear under Certificates.
				if s.ID != nil && !deref(s.Managed) {
					objs = append(objs, secretObject(c, s, tenant))
				}
			}
			return objs
		})
	case provider.TypeKey:
		out, err = all(ctx, vc.keys.NewListKeyPropertiesPager(nil), func(page azkeys.ListKeyPropertiesResponse) (objs []provider.Object) {
			for _, k := range page.Value {
				if k.KID != nil && !deref(k.Managed) {
					objs = append(objs, keyObject(c, k))
				}
			}
			return objs
		})
	case provider.TypeCertificate:
		out, err = all(ctx, vc.certs.NewListCertificatePropertiesPager(nil), func(page azcertificates.ListCertificatePropertiesResponse) (objs []provider.Object) {
			for _, cp := range page.Value {
				if cp.ID != nil {
					objs = append(objs, certObject(c, cp))
				}
			}
			return objs
		})
	default:
		return nil, provider.ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b provider.Object) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// Describe fetches key and certificate details. Secrets are returned as-is:
// the only way to get more is GetSecret, which returns the value.
func (p *Provider) Describe(ctx context.Context, o provider.Object) (provider.Object, error) {
	vc, err := p.clients(o.Container.Detail)
	if err != nil {
		return o, err
	}
	switch o.Type {
	case provider.TypeKey:
		resp, err := vc.keys.GetKey(ctx, o.Name, o.Version, nil)
		if err != nil {
			return o, cleanError(err)
		}
		if resp.Key != nil {
			if resp.Key.KID != nil {
				o.Version = resp.Key.KID.Version()
				o.URL = portalAssetURL(p.tenant(o.Container.Source.ID), o.Type, string(*resp.Key.KID))
			}
			o.Attrs = keyAttrs(resp.Key)
		}
	case provider.TypeCertificate:
		resp, err := vc.certs.GetCertificate(ctx, o.Name, o.Version, nil)
		if err != nil {
			return o, cleanError(err)
		}
		if resp.ID != nil {
			o.Version = resp.ID.Version()
			o.URL = portalAssetURL(p.tenant(o.Container.Source.ID), o.Type, string(*resp.ID))
		}
		if pol := resp.Policy; pol != nil && pol.SecretProperties != nil {
			o.ContentType = deref(pol.SecretProperties.ContentType)
		}
		if cert, err := x509.ParseCertificate(resp.CER); err == nil {
			o.Attrs = certAttrs(cert, resp.X509Thumbprint)
		}
	}
	return o, nil
}

func (p *Provider) Versions(ctx context.Context, o provider.Object) ([]provider.Object, error) {
	vc, err := p.clients(o.Container.Detail)
	if err != nil {
		return nil, err
	}
	tenant := p.tenant(o.Container.Source.ID)
	var out []provider.Object
	switch o.Type {
	case provider.TypeSecret:
		out, err = all(ctx, vc.secrets.NewListSecretPropertiesVersionsPager(o.Name, nil), func(page azsecrets.ListSecretPropertiesVersionsResponse) (objs []provider.Object) {
			for _, s := range page.Value {
				if s.ID != nil {
					objs = append(objs, secretObject(o.Container, s, tenant))
				}
			}
			return objs
		})
	case provider.TypeKey:
		out, err = all(ctx, vc.keys.NewListKeyPropertiesVersionsPager(o.Name, nil), func(page azkeys.ListKeyPropertiesVersionsResponse) (objs []provider.Object) {
			for _, k := range page.Value {
				if k.KID != nil {
					objs = append(objs, keyObject(o.Container, k))
				}
			}
			return objs
		})
	case provider.TypeCertificate:
		out, err = all(ctx, vc.certs.NewListCertificatePropertiesVersionsPager(o.Name, nil), func(page azcertificates.ListCertificatePropertiesVersionsResponse) (objs []provider.Object) {
			for _, cp := range page.Value {
				if cp.ID != nil {
					objs = append(objs, certObject(o.Container, cp))
				}
			}
			return objs
		})
	default:
		return nil, provider.ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Reference = versionedReference(out[i])
		if o.Type != provider.TypeSecret { // secretObject's is the version's already
			out[i].URL = portalAssetURL(tenant, o.Type, out[i].ID+"/"+out[i].Version) // the version's page
		}
	}
	slices.SortFunc(out, func(a, b provider.Object) int { return b.Updated.Compare(a.Updated) })
	return out, nil
}

// versionedReference pins an object's reference to its version, e.g.
// @Microsoft.KeyVault(SecretUri=https://kv.vault.azure.net/secrets/name/v1).
func versionedReference(o provider.Object) string {
	if o.Version == "" {
		return o.Reference
	}
	if ref, ok := strings.CutSuffix(o.Reference, ")"); ok && o.Type == provider.TypeSecret {
		return ref + "/" + o.Version + ")"
	}
	return o.Reference + "/" + o.Version
}

// Value returns a secret's value, a key's public key or a certificate's
// public certificate, as of o.Version (latest if empty).
func (p *Provider) Value(ctx context.Context, o provider.Object) (provider.SecretValue, error) {
	v, _, err := p.VersionedValue(ctx, o)
	return v, err
}

// VersionedValue is Value, with the version read.
func (p *Provider) VersionedValue(ctx context.Context, o provider.Object) (provider.SecretValue, string, error) {
	vc, err := p.clients(o.Container.Detail)
	if err != nil {
		return provider.SecretValue{}, "", err
	}
	var text string
	switch o.Type {
	case provider.TypeSecret:
		resp, err := vc.secrets.GetSecret(ctx, o.Name, o.Version, nil)
		if err != nil {
			return provider.SecretValue{}, "", cleanError(err)
		}
		version := o.Version
		if resp.ID != nil {
			version = resp.ID.Version()
		}
		return provider.NewSecretValue([]byte(deref(resp.Value))), version, nil
	case provider.TypeKey:
		resp, err := vc.keys.GetKey(ctx, o.Name, o.Version, nil)
		if err != nil {
			return provider.SecretValue{}, "", cleanError(err)
		}
		if text, err = publicKeyPEM(resp.Key); err != nil {
			return provider.SecretValue{}, "", err
		}
	case provider.TypeCertificate:
		resp, err := vc.certs.GetCertificate(ctx, o.Name, o.Version, nil)
		if err != nil {
			return provider.SecretValue{}, "", cleanError(err)
		}
		if len(resp.CER) == 0 {
			return provider.SecretValue{}, "", errors.New("certificate has no public certificate yet (pending issuance?)")
		}
		text = certPEM(resp.CER)
	default:
		return provider.SecretValue{}, "", provider.ErrUnsupported
	}
	return provider.NewSecretValue([]byte(text)), o.Version, nil
}

var (
	_ provider.Locator   = (*Provider)(nil)
	_ provider.Versioned = (*Provider)(nil)
)

// validHost guards the Resource Graph query against odd input.
var validHost = regexp.MustCompile(`^[a-z0-9.-]+$`)

// Locate finds the subscription of the vault with the given host: a pinned
// vault, or one Resource Graph query across the account's subscriptions.
// Vaults in hidden subscriptions aren't found.
func (p *Provider) Locate(ctx context.Context, host string) (string, bool, error) {
	host = strings.ToLower(host)
	if !validHost.MatchString(host) {
		return "", false, fmt.Errorf("not a vault host: %q", host)
	}
	url := "https://" + host + "/"
	for _, v := range p.opts.Vaults {
		if strings.EqualFold(strings.TrimSuffix(v, "/")+"/", url) {
			return pinnedID, true, nil
		}
	}
	resp, err := p.graph.Resources(ctx, armresourcegraph.QueryRequest{
		Query: to.Ptr(`resources
| where type =~ 'microsoft.keyvault/vaults' and tostring(properties.vaultUri) =~ '` + url + `'
| project subscriptionId`),
		Options: &armresourcegraph.QueryRequestOptions{ResultFormat: to.Ptr(armresourcegraph.ResultFormatObjectArray)},
	}, nil)
	if err != nil {
		return "", false, cleanError(err)
	}
	rows, _ := resp.Data.([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		sub, _ := m["subscriptionId"].(string)
		if sub != "" && !config.Hidden(p.opts.HideSubscriptions, "", sub) {
			return sub, true, nil
		}
	}
	return "", false, nil
}
