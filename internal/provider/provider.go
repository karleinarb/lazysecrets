// Package provider defines the read-only interface every secret backend
// implements, and the types the UI works with.
//
// All backends map onto the same hierarchy:
//
//	Source     Azure subscription           | Databricks workspace profile
//	Container  Key Vault                    | secret scope
//	Object     secret, key or certificate   | secret
package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind identifies a backend.
type Kind string

const (
	KindAzure      Kind = "Azure"
	KindDatabricks Kind = "Databricks"
	// KindRepo is a git repository's encrypted secret files (SOPS).
	KindRepo Kind = "Repo"
)

// AuthError reports that the user must (re)authenticate with a backend, for
// example because their az CLI login has expired.
type AuthError struct {
	Kind Kind
	// Account names the identity when a provider has several, e.g.
	// "Azure · admin.alice"; empty otherwise.
	Account string
	// Login is the command that signs the user in, e.g. ["az", "login"].
	Login []string
	Err   error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("%s: not signed in or session expired: %v", e.Who(), e.Err)
}

// Who is the account, or the kind if there is just one.
func (e *AuthError) Who() string {
	if e.Account != "" {
		return e.Account
	}
	return string(e.Kind)
}

func (e *AuthError) Unwrap() error { return e.Err }

// ErrUnsupported is returned by optional operations a backend cannot perform,
// such as listing versions of a Databricks secret.
var ErrUnsupported = errors.New("not supported by this provider")

// Source is a top-level account: an Azure subscription or a Databricks
// workspace profile.
type Source struct {
	Kind Kind
	// Provider is the ID of the provider instance the source came from.
	// The app sets it; it routes requests when several providers share a
	// Kind, e.g. one per Azure account.
	Provider string
	// ProviderLabel is that provider's Label, also set by the app.
	ProviderLabel string
	ID            string
	Name          string
	Detail        string // e.g. tenant or workspace host
	URL           string // web page for the source, if any
	// Here marks the source lazysecrets was started in (the current git
	// repo): listed first, starred or not, and labelled.
	Here bool
	// Hidden marks a source, container or object the provider's rules
	// left out (a Spring file with no secret-looking keys): listed only
	// while the app shows hidden ones.
	Hidden bool `json:",omitempty"`
}

// ObjectType is the kind of object stored in a container.
type ObjectType string

const (
	TypeSecret      ObjectType = "Secret"
	TypeKey         ObjectType = "Key"
	TypeCertificate ObjectType = "Certificate"
)

// Collection is the type's path segment in Key Vault URLs and links, e.g.
// "secrets".
func (t ObjectType) Collection() string {
	switch t {
	case TypeSecret:
		return "secrets"
	case TypeKey:
		return "keys"
	case TypeCertificate:
		return "certificates"
	}
	return ""
}

// Container holds objects: a Key Vault or a Databricks secret scope.
type Container struct {
	Source Source
	ID     string
	Name   string
	Detail string // e.g. vault URL, or "Azure Key Vault-backed: kv-name"
	// Types lists the object types the container holds; empty means
	// secrets only.
	Types []ObjectType
	URL   string // web page for the container, if any
	// Attrs describe it further, from the listing (a vault's settings).
	Attrs  []Attr `json:",omitempty"`
	Hidden bool   `json:",omitempty"` // see Source.Hidden
}

// ObjectTypes returns the container's object types, defaulting to secrets.
func (c Container) ObjectTypes() []ObjectType {
	if len(c.Types) == 0 {
		return []ObjectType{TypeSecret}
	}
	return c.Types
}

// Attr is a type-specific metadata field, shown in provider order.
type Attr struct {
	Label string
	Value string
	Warn  bool // worth a look (a plain .env tracked by git): shown in red
}

// Object describes a secret, key or certificate without its value.
type Object struct {
	Container   Container
	Type        ObjectType
	ID          string
	Name        string
	Detail      string // shown after the name, e.g. "jasypt" for how its value is kept
	Hidden      bool   `json:",omitempty"` // see Source.Hidden
	Version     string
	Enabled     *bool
	Updated     time.Time
	Expires     time.Time
	ContentType string
	Tags        map[string]string
	// Attrs holds type-specific metadata, e.g. key type and size, or a
	// certificate's subject, issuer and thumbprint.
	Attrs []Attr
	// Reference is how the object is referenced from config, e.g.
	// "@Microsoft.KeyVault(SecretUri=...)" or "{{secrets/scope/key}}".
	Reference string
	URL       string // web page for the object (or its list), if any
}

// Provider is a read-only secret backend. Every method may block on the
// network and must honour ctx cancellation.
type Provider interface {
	Kind() Kind
	// ID identifies the instance, stable across runs, e.g. "Azure" or
	// "Azure:user@example.com/tenant".
	ID() string
	// Label names the instance in the UI, e.g. "Azure" or "Azure · user".
	Label() string
	Sources(ctx context.Context) ([]Source, error)
	Containers(ctx context.Context, src Source) ([]Container, error)
	// Objects lists objects of type t, one of c.ObjectTypes(). It returns
	// metadata only; values are fetched with Value.
	Objects(ctx context.Context, c Container, t ObjectType) ([]Object, error)
	// Describe returns o with details that listing doesn't include, such as
	// a key's type or a certificate's subject. It never fetches secret
	// values; it may return o unchanged.
	Describe(ctx context.Context, o Object) (Object, error)
	// Versions may return ErrUnsupported.
	Versions(ctx context.Context, o Object) ([]Object, error)
	// Value returns a secret's value, or the public material of a key (PEM
	// public key) or certificate (PEM certificate). Private key material is
	// never returned for keys.
	Value(ctx context.Context, o Object) (SecretValue, error)
}

// Current is implemented by providers that know the source lazysecrets
// was started in (the current git repo) and can find it faster than
// Sources lists everything, so it shows first.
type Current interface {
	Current(ctx context.Context) (Source, bool)
}

// Versioned is implemented by providers whose secrets have versions: it
// is Value, also returning the version read (the current one when
// o.Version is empty), so a link can pin it.
type Versioned interface {
	VersionedValue(ctx context.Context, o Object) (SecretValue, string, error)
}

// Locator is implemented by providers that can find where a container lives,
// to open links. address is a vault host (kv.vault.azure.net) or a
// workspace host. It returns the ID of the source holding it.
type Locator interface {
	Locate(ctx context.Context, address string) (sourceID string, ok bool, err error)
}

// Files is implemented by providers whose containers are files on disk
// (SOPS and .env files in repos), so they can be opened in an editor.
type Files interface {
	// File is where c is, and for o (nil for the whole file) the line its
	// key is on.
	File(c Container, o *Object) (File, error)
}

// Contents is implemented by providers whose containers are files, so
// they can be shared (package share): the file as it is.
type Contents interface {
	Contents(ctx context.Context, c Container) ([]byte, error)
}

// File is a container's file, to open.
type File struct {
	Path string
	Line int    // 1-based; 0 for the top
	Sops string // the sops command to edit it with; empty for a plain file
}

// Accounts is implemented by providers that can describe the identity
// they use, from what they have locally: for the Details pane on the
// provider's row. ok is false when there's nothing to say.
type Accounts interface {
	Account() (Account, bool)
}

// Account describes a signed-in identity.
type Account struct {
	Fields []Attr
	// Copy is what y copies on the provider's row, named CopyLabel.
	Copy, CopyLabel string
}

// Denied is a refusal with what to check, from what the service said: a
// role to activate in PIM, access to ask for, the network. Error is the
// refusal, then the checks as a list.
type Denied struct {
	Err    error
	Checks []string
}

func (d *Denied) Error() string {
	if len(d.Checks) == 0 {
		return d.Err.Error()
	}
	return d.Err.Error() + "\n\nCheck:\n• " + strings.Join(d.Checks, "\n• ")
}

func (d *Denied) Unwrap() error { return d.Err }

// DetailPlainEnv is the Detail of a container that is a plain (not
// encrypted) env file in a repo.
const DetailPlainEnv = "env · plain text"

// ContainerDetails is implemented by providers that can say more about a
// container than its listing does: how a file is encrypted and what git
// knows of it. ErrUnsupported means there's nothing more for c.
type ContainerDetails interface {
	Details(ctx context.Context, c Container) ([]Attr, error)
}
