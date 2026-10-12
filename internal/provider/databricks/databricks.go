// Package databricks is the Databricks provider. Sources are workspace
// profiles from ~/.databrickscfg, containers are secret scopes, and objects
// are secrets. Secrets in Azure Key Vault-backed scopes are read through
// Databricks, falling back to the vault itself.
package databricks

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/databricks/databricks-sdk-go/apierr"
	"github.com/databricks/databricks-sdk-go/client"
	dbconfig "github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/workspace"
	"gopkg.in/ini.v1"

	"git.kebcloud.no/keb/lazysecrets/internal/config"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/azure"
)

// KeyVault reads Azure Key Vault secrets directly; the Azure provider
// implements it. Containers passed to it only have Detail (the vault URL)
// set.
type KeyVault interface {
	Objects(ctx context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error)
	Value(ctx context.Context, o provider.Object) (provider.SecretValue, error)
}

type profile struct {
	name     string
	host     string
	authType string
}

type Provider struct {
	profiles  []profile
	keyVault  KeyVault // nil disables the fallback
	newClient func(profile) (*workspace.SecretsAPI, error)

	mu      sync.Mutex
	clients map[string]*workspace.SecretsAPI // by profile name
	vaults  map[string]string                // AKV-backed scope ID → vault URL
}

var _ provider.Provider = (*Provider)(nil)

// New reads workspace profiles from the Databricks config file
// ($DATABRICKS_CONFIG_FILE or ~/.databrickscfg), leaving out hideProfiles.
// A missing file yields a provider with no sources.
func New(kv KeyVault, hideProfiles []string) (*Provider, error) {
	path := os.Getenv("DATABRICKS_CONFIG_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".databrickscfg")
	}
	all, err := readProfiles(path)
	if err != nil {
		return nil, err
	}
	var profiles []profile
	for _, pr := range all {
		if !config.Hidden(hideProfiles, pr.name, pr.name) {
			profiles = append(profiles, pr)
		}
	}
	return &Provider{
		profiles:  profiles,
		keyVault:  kv,
		newClient: profileClient,
		clients:   map[string]*workspace.SecretsAPI{},
		vaults:    map[string]string{},
	}, nil
}

// profileClient is a client for the secrets API alone. The SDK's
// WorkspaceClient would link in every Databricks service, some 10 MB.
func profileClient(p profile) (*workspace.SecretsAPI, error) {
	return secretsClient(&dbconfig.Config{Profile: p.name})
}

func secretsClient(cfg *dbconfig.Config) (*workspace.SecretsAPI, error) {
	c, err := client.New(cfg)
	if err != nil {
		return nil, err
	}
	return workspace.NewSecrets(c), nil
}

// readProfiles returns the workspace profiles in file order, skipping
// account-console profiles, which have no secret scopes.
func readProfiles(path string) ([]profile, error) {
	f, err := ini.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var out []profile
	for _, s := range f.Sections() {
		host := s.Key("host").String()
		if host == "" || s.HasKey("account_id") || strings.Contains(host, "accounts.") {
			continue
		}
		out = append(out, profile{name: s.Name(), host: host, authType: s.Key("auth_type").String()})
	}
	return out, nil
}

// loginCommand is what signs the user back in for a profile, or nil if
// there is nothing to run (e.g. a personal access token).
func loginCommand(p profile) []string {
	switch p.authType {
	case "azure-cli":
		return []string{"az", "login"}
	case "pat":
		return nil
	default:
		return []string{"databricks", "auth", "login", "--profile", p.name}
	}
}

func (p *Provider) Kind() provider.Kind { return provider.KindDatabricks }
func (p *Provider) ID() string          { return string(provider.KindDatabricks) }
func (p *Provider) Label() string       { return string(provider.KindDatabricks) }

func (p *Provider) Sources(context.Context) ([]provider.Source, error) {
	out := make([]provider.Source, 0, len(p.profiles))
	for _, pr := range p.profiles {
		host := strings.TrimPrefix(pr.host, "https://")
		out = append(out, provider.Source{
			Kind: provider.KindDatabricks, ID: pr.name, Name: pr.name,
			Detail: host, URL: "https://" + host,
		})
	}
	return out, nil
}

func (p *Provider) profile(name string) (profile, bool) {
	for _, pr := range p.profiles {
		if pr.name == name {
			return pr, true
		}
	}
	return profile{}, false
}

func (p *Provider) client(name string) (*workspace.SecretsAPI, profile, error) {
	pr, ok := p.profile(name)
	if !ok {
		return nil, pr, fmt.Errorf("unknown profile %q", name)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[name]; ok {
		return c, pr, nil
	}
	c, err := p.newClient(pr)
	if err != nil {
		return nil, pr, classify(pr, err)
	}
	p.clients[name] = c
	return c, pr, nil
}

func (p *Provider) Containers(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	w, pr, err := p.client(src.ID)
	if err != nil {
		return nil, err
	}
	scopes, err := w.ListScopesAll(ctx)
	if err != nil {
		return nil, classify(pr, err)
	}
	out := make([]provider.Container, 0, len(scopes))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range scopes {
		c := provider.Container{Source: src, ID: src.ID + "/" + s.Name, Name: s.Name, Detail: "Databricks-backed"}
		if s.BackendType == workspace.ScopeBackendTypeAzureKeyvault && s.KeyvaultMetadata != nil {
			c.Detail = akvDetail + s.KeyvaultMetadata.DnsName
			c.URL = azure.PortalResourceURL("", s.KeyvaultMetadata.ResourceId, "overview")
			p.vaults[c.ID] = s.KeyvaultMetadata.DnsName
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b provider.Container) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// vaultURL is the backing vault of an AKV-backed scope, or "". It falls
// back to the scope's Detail, so a scope remembered from an earlier session
// (a favourite) works before its workspace's scopes are listed.
func (p *Provider) vaultURL(c provider.Container) string {
	p.mu.Lock()
	url := p.vaults[c.ID]
	p.mu.Unlock()
	if url == "" {
		url, _ = strings.CutPrefix(c.Detail, akvDetail)
		if url == c.Detail {
			url = ""
		}
	}
	return url
}

const akvDetail = "Azure Key Vault-backed: "

func secretObject(c provider.Container, key string, updated time.Time) provider.Object {
	return provider.Object{
		Container: c,
		Type:      provider.TypeSecret,
		ID:        c.ID + "/" + key,
		Name:      key,
		Updated:   updated,
		Reference: fmt.Sprintf("{{secrets/%s/%s}}", c.Name, key),
		URL:       azure.ObjectListURL(c, provider.TypeSecret), // AKV-backed only
	}
}

func (p *Provider) Objects(ctx context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	if t != provider.TypeSecret {
		return nil, provider.ErrUnsupported
	}
	w, pr, err := p.client(c.Source.ID)
	if err != nil {
		return nil, err
	}
	var out []provider.Object
	secrets, err := w.ListSecretsAll(ctx, workspace.ListSecretsRequest{Scope: c.Name})
	if err == nil {
		for _, s := range secrets {
			var updated time.Time
			if s.LastUpdatedTimestamp > 0 {
				updated = time.UnixMilli(s.LastUpdatedTimestamp)
			}
			out = append(out, secretObject(c, s.Key, updated))
		}
	} else {
		dbxErr := classify(pr, err)
		out, err = p.vaultObjects(ctx, c)
		if err != nil {
			return nil, combine(dbxErr, err)
		}
	}
	slices.SortFunc(out, func(a, b provider.Object) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// vaultObjects lists an AKV-backed scope's secrets from the vault itself.
func (p *Provider) vaultObjects(ctx context.Context, c provider.Container) ([]provider.Object, error) {
	url := p.vaultURL(c)
	if url == "" || p.keyVault == nil || ctx.Err() != nil {
		return nil, errNoFallback
	}
	secrets, err := p.keyVault.Objects(ctx, provider.Container{Detail: url}, provider.TypeSecret)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Object, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, secretObject(c, s.Name, s.Updated))
	}
	return out, nil
}

func (p *Provider) Describe(ctx context.Context, o provider.Object) (provider.Object, error) {
	return o, ctx.Err()
}

func (p *Provider) Versions(context.Context, provider.Object) ([]provider.Object, error) {
	return nil, provider.ErrUnsupported
}

func (p *Provider) Value(ctx context.Context, o provider.Object) (provider.SecretValue, error) {
	w, pr, err := p.client(o.Container.Source.ID)
	if err != nil {
		return provider.SecretValue{}, err
	}
	resp, err := w.GetSecret(ctx, workspace.GetSecretRequest{Scope: o.Container.Name, Key: o.Name})
	if err == nil {
		b, derr := base64.StdEncoding.DecodeString(resp.Value)
		if derr != nil {
			return provider.SecretValue{}, fmt.Errorf("decoding secret: %w", derr)
		}
		return provider.NewSecretValue(b), nil
	}
	dbxErr := classify(pr, err)

	url := p.vaultURL(o.Container)
	if url == "" || p.keyVault == nil || ctx.Err() != nil {
		return provider.SecretValue{}, dbxErr
	}
	v, kvErr := p.keyVault.Value(ctx, provider.Object{
		Container: provider.Container{Detail: url},
		Type:      provider.TypeSecret,
		Name:      o.Name,
	})
	if kvErr != nil {
		return provider.SecretValue{}, combine(dbxErr, kvErr)
	}
	return v, nil
}

// deniedChecks are what to check when Databricks refuses: the network,
// when the workspace's IP access list refused, else access, and for a
// profile signed in through az, which account az is on.
func deniedChecks(pr profile, msg string) []string {
	if strings.Contains(msg, "IP ACL") || strings.Contains(msg, "IP access list") {
		return []string{
			"you're on a network the workspace lets in (VPN, office)",
			"the workspace's admin has let your network in",
		}
	}
	var checks []string
	if pr.authType == "azure-cli" {
		checks = append(checks, "az is signed in as the account with access: profile "+pr.name+" uses your az login (az account show)")
	}
	return append(checks,
		"you have access to the workspace",
		"the scope's manager has given you READ on it, or a group you're in",
	)
}

var errNoFallback = errors.New("no Key Vault fallback")

// combine reports both the Databricks and the Key Vault failure, with
// both their checks after. An auth error from either stays reachable
// with errors.As.
func combine(dbxErr, kvErr error) error {
	if errors.Is(kvErr, errNoFallback) {
		return dbxErr
	}
	var checks []string
	strip := func(err error) error {
		if d, ok := err.(*provider.Denied); ok {
			for _, c := range d.Checks {
				if !slices.Contains(checks, c) {
					checks = append(checks, c)
				}
			}
			return d.Err
		}
		return err
	}
	err := fmt.Errorf("%w; Key Vault fallback: %w", strip(dbxErr), strip(kvErr))
	if len(checks) > 0 {
		return &provider.Denied{Err: err, Checks: checks}
	}
	return err
}

// classify turns authentication failures into *provider.AuthError: a 401
// from the API, or failing to get a token before the request was sent.
func classify(pr profile, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var api *apierr.APIError
	if errors.As(err, &api) {
		if api.StatusCode == 403 {
			return &provider.Denied{Err: err, Checks: deniedChecks(pr, api.Message)}
		}
		if api.StatusCode != 401 {
			return err
		}
	} else if !strings.Contains(err.Error(), "cannot get access token") &&
		!strings.Contains(err.Error(), " auth: ") {
		return err
	}
	return &provider.AuthError{Kind: provider.KindDatabricks, Login: loginCommand(pr), Err: err}
}

var _ provider.Locator = (*Provider)(nil)

// Locate finds the profile for a workspace host. It doesn't contact the
// workspace.
func (p *Provider) Locate(_ context.Context, host string) (string, bool, error) {
	want := normalizeHost(host)
	for _, pr := range p.profiles {
		if normalizeHost(pr.host) == want {
			return pr.name, true, nil
		}
	}
	return "", false, nil
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(h, "https://")
	return strings.TrimSuffix(h, "/")
}
