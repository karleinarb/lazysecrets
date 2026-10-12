package azure

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// account is one az CLI identity in one tenant.
type account struct {
	user, tenant  string
	userType      string // "user" or "servicePrincipal"
	tenantName    string
	cloud         string // az's environment: AzureCloud, AzureChinaCloud, …
	defaultSub    string // az's default subscription's name, if this account's
	anchor        string // a subscription az maps to this account, to pin it
	anchorEnabled bool
	isDefault     bool
	subscriptions int // how many subscriptions az maps to it
}

// azProfilePath is az's account list. It holds no tokens.
func azProfilePath() (string, error) {
	dir := os.Getenv("AZURE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".azure")
	}
	return filepath.Join(dir, "azureProfile.json"), nil
}

// readAccounts lists the accounts signed in to az, default first.
func readAccounts(path string) ([]account, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // az writes a BOM
	var profile struct {
		Subscriptions []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			State      string `json:"state"`
			IsDefault  bool   `json:"isDefault"`
			TenantID   string `json:"tenantId"`
			TenantName string `json:"tenantDisplayName"`
			Cloud      string `json:"environmentName"`
			User       struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"user"`
		} `json:"subscriptions"`
	}
	if err := json.Unmarshal(b, &profile); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var out []account
	for _, s := range profile.Subscriptions {
		if s.User.Name == "" || s.TenantID == "" {
			continue
		}
		i := slices.IndexFunc(out, func(a account) bool { return a.user == s.User.Name && a.tenant == s.TenantID })
		if i < 0 {
			out = append(out, account{user: s.User.Name, tenant: s.TenantID, tenantName: s.TenantName, userType: s.User.Type, cloud: s.Cloud})
			i = len(out) - 1
		}
		a := &out[i]
		a.subscriptions++
		// Anchor on az's default subscription if it is this account's,
		// otherwise on an enabled one.
		enabled := s.State == "Enabled"
		switch {
		case s.IsDefault:
			a.anchor, a.anchorEnabled, a.isDefault, a.defaultSub = s.ID, enabled, true, s.Name
		case a.isDefault:
		case a.anchor == "" || enabled && !a.anchorEnabled:
			a.anchor, a.anchorEnabled = s.ID, enabled
		}
	}
	// az leaves the tenant's name out of some entries: take it from
	// another in the same tenant.
	for i := range out {
		for _, b := range out {
			if out[i].tenantName == "" && b.tenant == out[i].tenant {
				out[i].tenantName = b.tenantName
			}
		}
	}
	slices.SortStableFunc(out, func(a, b account) int {
		if a.isDefault != b.isDefault {
			if a.isDefault {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.user, b.user)
	})
	return out, nil
}

// accountLabels names accounts as briefly as stays unambiguous: the user
// name before the @, the full user if another user shares that, plus the
// tenant if one user is signed in to several.
func accountLabels(accounts []account) []string {
	short := func(u string) string { s, _, _ := strings.Cut(u, "@"); return s }
	labels := make([]string, len(accounts))
	for i, a := range accounts {
		l, tenants := short(a.user), 0
		for _, b := range accounts {
			if b.user != a.user && short(b.user) == short(a.user) {
				l = a.user
			}
			if b.user == a.user {
				tenants++
			}
		}
		if tenants > 1 {
			l += " · " + cmp.Or(a.tenantName, a.tenant)
		}
		labels[i] = l
	}
	return labels
}

// NewAccounts returns one provider per account signed in to az, the default
// first, or a single provider if AZURE_* configures a service principal or
// az has no accounts yet. Pinned vaults go to the first.
func NewAccounts(s Settings) ([]*Provider, error) {
	if os.Getenv("AZURE_CLIENT_ID") != "" && os.Getenv("AZURE_TENANT_ID") != "" {
		cred, err := azidentity.NewEnvironmentCredential(nil)
		if err != nil {
			return nil, err
		}
		p, err := newWithOptions(authCredential{cred: cred}, options{Settings: s})
		if err != nil {
			return nil, err
		}
		return []*Provider{p}, nil
	}

	path, err := azProfilePath()
	if err != nil {
		return nil, err
	}
	accounts, err := readAccounts(path)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		// Not signed in yet: the first call fails with an AuthError and
		// the UI offers az login.
		cred := authCredential{cred: newCLICredential("", "", nil), login: []string{"az", "login"}}
		p, err := newWithOptions(cred, options{Settings: s})
		if err != nil {
			return nil, err
		}
		return []*Provider{p}, nil
	}

	labels := accountLabels(accounts)
	var out []*Provider
	for i, a := range accounts {
		settings := s
		if i > 0 {
			settings.Vaults = nil
		}
		label := ""
		if len(accounts) > 1 {
			label = string(provider.KindAzure) + " · " + labels[i]
		}
		cred := authCredential{
			cred:    newCLICredential(a.user, a.tenant, func() ([]account, error) { return readAccounts(path) }),
			login:   []string{"az", "login", "--tenant", a.tenant},
			account: label, // so "sign-in required" says which account
		}
		p, err := newWithOptions(cred, options{Settings: settings})
		if err != nil {
			return nil, err
		}
		p.account = &accountDetails{account: a, claims: cred.cred.(*cliCredential).claims}
		if label != "" {
			p.id = string(provider.KindAzure) + ":" + a.user + "/" + a.tenant
			p.label = label
		}
		out = append(out, p)
	}
	return out, nil
}
