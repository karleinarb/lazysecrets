// Package ref parses and builds references to vaults, scopes and secrets:
// lazysecrets:// links, and the formats people already share (Key Vault
// identifier URLs, App Service @Microsoft.KeyVault references, portal links
// and Databricks {{secrets/scope/key}}).
//
// A link names a vault, scope or object; what to do with it is up to
// whoever opens it:
//
//	lazysecrets://azure/subscriptions/<subscription-id>
//	lazysecrets://azure/<vault-host>[/<secrets|keys|certificates>[/<name>[/<version>]]]
//	lazysecrets://databricks/<workspace-host>[/<scope>[/<key>]]
//	lazysecrets://repo/<repo>[/<file>[/<key>]]
//
// A repo is its git remote without scheme or .git
// (git.kebcloud.no/keb/infra), the file its path in the repo and the key
// its path in the file (db.password); slashes inside them are escaped.
//
// Vault hosts always contain dots, so "subscriptions" can't be one.
//
// Every path segment is percent-encoded, so names may hold any character.
// Query parameters are ignored (early links carried ?action=).
package ref

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

const Scheme = "lazysecrets"

// Ref points at a container, or an object in one.
type Ref struct {
	Kind provider.Kind
	// Host is the vault host (Azure), workspace host (Databricks) or repo
	// (Repo). It is empty for a bare {{secrets/scope/key}}, whose workspace
	// is unknown.
	Host string
	// Scope is the Databricks scope or the repo's file; empty for Azure.
	Scope string
	// Subscription is set for a link to an Azure subscription.
	Subscription string
	// Type is the object type, or for an Azure vault link the tab to show
	// (empty: the default tab).
	Type    provider.ObjectType
	Name    string // object name; empty for a container link
	Version string
}

// IsSource reports whether r points at a subscription or workspace.
func (r Ref) IsSource() bool {
	if r.Kind != provider.KindAzure {
		return r.Scope == ""
	}
	return r.Subscription != ""
}

// IsContainer reports whether r points at a vault or scope.
func (r Ref) IsContainer() bool { return r.Name == "" && !r.IsSource() }

// ContainerName is the vault name (from its host) or the scope.
func (r Ref) ContainerName() string {
	if r.Kind != provider.KindAzure {
		return r.Scope
	}
	name, _, _ := strings.Cut(r.Host, ".")
	return name
}

// VaultURL is the Azure vault's URL, as providers store it in
// Container.Detail.
func (r Ref) VaultURL() string { return "https://" + r.Host + "/" }

var collections = map[provider.ObjectType]string{
	provider.TypeSecret:      "secrets",
	provider.TypeKey:         "keys",
	provider.TypeCertificate: "certificates",
}

func typeOf(collection string) (provider.ObjectType, bool) {
	for t, c := range collections {
		if strings.EqualFold(c, collection) {
			return t, true
		}
	}
	return "", false
}

// IdentifierURL is the plain Key Vault URL of an Azure reference, e.g.
// https://kv.vault.azure.net/secrets/name[/version], usable without
// lazysecrets (az keyvault secret show --id). Empty for Databricks.
func (r Ref) IdentifierURL() string {
	if r.Kind != provider.KindAzure {
		return ""
	}
	u := "https://" + r.Host
	if r.Type == "" {
		return u + "/"
	}
	for _, seg := range []string{collections[r.Type], r.Name, r.Version} {
		if seg != "" {
			u += "/" + url.PathEscape(seg)
		}
	}
	return u
}

// String is the lazysecrets:// link.
func (r Ref) String() string {
	if r.Kind == provider.KindAzure && r.Subscription != "" {
		return Scheme + "://azure/subscriptions/" + url.PathEscape(r.Subscription)
	}
	segs := []string{strings.ToLower(string(r.Kind)), r.Host}
	switch r.Kind {
	case provider.KindDatabricks, provider.KindRepo:
		segs = append(segs, r.Scope, r.Name)
	default:
		if r.Type != "" {
			segs = append(segs, collections[r.Type])
		}
		segs = append(segs, r.Name, r.Version)
	}
	for len(segs) > 2 && segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
		if r.Kind == provider.KindRepo && i > 0 {
			// Repos and files have slashes: keep them in their part.
			segs[i] = strings.ReplaceAll(segs[i], "/", "%2F")
		}
	}
	return Scheme + "://" + strings.Join(segs, "/")
}

// ForSource is a link to a subscription or workspace.
func ForSource(src provider.Source) (Ref, error) {
	switch src.Kind {
	case provider.KindAzure:
		return Ref{Kind: provider.KindAzure, Subscription: src.ID}, nil
	case provider.KindDatabricks:
		if src.Detail == "" {
			return Ref{}, errors.New("workspace host unknown")
		}
		return Ref{Kind: provider.KindDatabricks, Host: strings.ToLower(src.Detail)}, nil
	case provider.KindRepo:
		return Ref{Kind: provider.KindRepo, Host: strings.ToLower(src.Detail)}, nil
	}
	return Ref{}, fmt.Errorf("can't link to %s", src.Kind)
}

// ForContainer is a link to a vault or scope.
func ForContainer(c provider.Container, tab provider.ObjectType) (Ref, error) {
	switch c.Source.Kind {
	case provider.KindAzure:
		host, err := vaultHost(c.Detail)
		if err != nil {
			return Ref{}, err
		}
		if tab == provider.TypeSecret {
			tab = "" // the default tab
		}
		return Ref{Kind: provider.KindAzure, Host: host, Type: tab}, nil
	case provider.KindDatabricks:
		if c.Source.Detail == "" {
			return Ref{}, errors.New("workspace host unknown")
		}
		return Ref{Kind: provider.KindDatabricks, Host: strings.ToLower(c.Source.Detail), Scope: c.Name}, nil
	case provider.KindRepo:
		return Ref{Kind: provider.KindRepo, Host: strings.ToLower(c.Source.Detail), Scope: c.Name}, nil
	}
	return Ref{}, fmt.Errorf("can't link to %s", c.Source.Kind)
}

// ForObject is a link to an object, pinned to its version if given.
func ForObject(o provider.Object, version string) (Ref, error) {
	r, err := ForContainer(o.Container, o.Type)
	if err != nil {
		return Ref{}, err
	}
	r.Name, r.Type = o.Name, o.Type
	if r.Kind == provider.KindAzure {
		r.Version = version // Databricks secrets have no versions
	}
	return r, nil
}

func vaultHost(vaultURL string) (string, error) {
	u, err := url.Parse(vaultURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("not a vault URL: %q", vaultURL)
	}
	return strings.ToLower(u.Host), nil
}

var (
	errUnknown = errors.New("not a lazysecrets link or a Key Vault/Databricks reference")

	// @Microsoft.KeyVault(SecretUri=…) or (VaultName=…;SecretName=…[;SecretVersion=…])
	appServiceRef = regexp.MustCompile(`(?i)^@Microsoft\.KeyVault\((.*)\)$`)
	// {{secrets/scope/key}}
	databricksRef = regexp.MustCompile(`^\{\{\s*secrets/([^/{}\s]+)/([^/{}\s]+)\s*\}\}$`)
	// …/providers/Microsoft.KeyVault/vaults/<name> in a portal link
	portalVault = regexp.MustCompile(`(?i)/providers/Microsoft\.KeyVault/vaults/([A-Za-z0-9-]+)`)
	// …/asset/Microsoft_Azure_KeyVault/<Secret|Key|Certificate>/<identifier URL>
	portalAsset = regexp.MustCompile(`(?i)/asset/Microsoft_Azure_KeyVault/[A-Za-z]+/(https://\S+)$`)
)

// Parse reads any supported reference.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(strings.ToLower(s), Scheme+"://"):
		return parseLink(s)
	case appServiceRef.MatchString(s):
		return parseAppService(appServiceRef.FindStringSubmatch(s)[1])
	case databricksRef.MatchString(s):
		m := databricksRef.FindStringSubmatch(s)
		return Ref{Kind: provider.KindDatabricks, Scope: m[1], Name: m[2], Type: provider.TypeSecret}, nil
	case strings.Contains(s, "portal.azure.com"):
		return parsePortal(s)
	case strings.HasPrefix(strings.ToLower(s), "https://"):
		return parseIdentifier(s)
	}
	return Ref{}, errUnknown
}

func parseLink(s string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, fmt.Errorf("bad link: %w", err)
	}
	segs, err := pathSegments(u.EscapedPath())
	if err != nil {
		return Ref{}, err
	}
	var r Ref
	if len(segs) == 0 || segs[0] == "" {
		return Ref{}, errors.New("link has no vault or workspace")
	}
	r.Host = strings.ToLower(segs[0])
	rest := segs[1:]

	switch strings.ToLower(u.Host) {
	case "azure":
		r.Kind = provider.KindAzure
		if r.Host == "subscriptions" {
			if len(rest) != 1 || rest[0] == "" {
				return Ref{}, errors.New("subscription link needs exactly one subscription ID")
			}
			return Ref{Kind: provider.KindAzure, Subscription: rest[0]}, nil
		}
		if len(rest) > 3 {
			return Ref{}, errors.New("azure link has too many parts")
		}
		if len(rest) > 0 {
			t, ok := typeOf(rest[0])
			if !ok {
				return Ref{}, fmt.Errorf("unknown object type %q (want secrets, keys or certificates)", rest[0])
			}
			r.Type = t
		}
		if len(rest) > 1 {
			r.Name = rest[1]
		}
		if len(rest) > 2 {
			r.Version = rest[2]
		}
	case "databricks", "repo":
		r.Kind = provider.KindDatabricks
		if strings.EqualFold(u.Host, "repo") {
			r.Kind = provider.KindRepo
		}
		if len(rest) > 2 {
			return Ref{}, fmt.Errorf("%s link has too many parts", strings.ToLower(u.Host))
		}
		if len(rest) > 0 {
			r.Scope = rest[0]
		}
		if len(rest) > 1 {
			r.Name, r.Type = rest[1], provider.TypeSecret
		}

	default:
		return Ref{}, fmt.Errorf("unknown provider %q in link", u.Host)
	}
	return r, nil
}

// pathSegments splits an escaped path on / and unescapes each segment, so
// an escaped slash (%2F) stays inside its segment.
func pathSegments(escaped string) ([]string, error) {
	escaped = strings.Trim(escaped, "/")
	if escaped == "" {
		return nil, nil
	}
	parts := strings.Split(escaped, "/")
	for i, p := range parts {
		s, err := url.PathUnescape(p)
		if err != nil {
			return nil, fmt.Errorf("bad link: %w", err)
		}
		parts[i] = s
	}
	return parts, nil
}

// parseIdentifier reads a Key Vault URL: a vault, or an object identifier
// like https://kv.vault.azure.net/secrets/name[/version].
func parseIdentifier(s string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Ref{}, errUnknown
	}
	host := strings.ToLower(u.Host)
	if !strings.Contains(host, ".vault.") {
		return Ref{}, errUnknown
	}
	segs, err := pathSegments(u.EscapedPath())
	if err != nil {
		return Ref{}, err
	}
	r := Ref{Kind: provider.KindAzure, Host: host}
	if len(segs) > 0 {
		t, ok := typeOf(segs[0])
		if !ok {
			return Ref{}, fmt.Errorf("unknown Key Vault path %q", u.Path)
		}
		r.Type = t
	}
	if len(segs) > 1 {
		r.Name = segs[1]
	}
	if len(segs) > 2 {
		r.Version = segs[2]
	}
	return r, nil
}

func parseAppService(args string) (Ref, error) {
	fields := map[string]string{}
	for _, kv := range strings.Split(args, ";") {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			fields[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	if uri := fields["secreturi"]; uri != "" {
		return parseIdentifier(uri)
	}
	vault, name := fields["vaultname"], fields["secretname"]
	if vault == "" || name == "" {
		return Ref{}, errors.New("@Microsoft.KeyVault reference needs SecretUri, or VaultName and SecretName")
	}
	return Ref{
		Kind: provider.KindAzure, Host: strings.ToLower(vault) + ".vault.azure.net",
		Type: provider.TypeSecret, Name: name, Version: fields["secretversion"],
	}, nil
}

// parsePortal reads Azure portal links to a vault or a Key Vault object.
func parsePortal(s string) (Ref, error) {
	if m := portalAsset.FindStringSubmatch(s); m != nil {
		return parseIdentifier(m[1])
	}
	if m := portalVault.FindStringSubmatch(s); m != nil {
		// Portal vault links don't carry the cloud; assume public Azure.
		return Ref{Kind: provider.KindAzure, Host: strings.ToLower(m[1]) + ".vault.azure.net"}, nil
	}
	return Ref{}, errUnknown
}
