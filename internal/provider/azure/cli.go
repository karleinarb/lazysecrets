package azure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// cliCredential gets tokens from the az CLI for one signed-in account.
//
// az can hold several accounts, but `az account get-access-token` can't be
// told which user to use: it picks the account a subscription belongs to.
// So each account is pinned with --subscription. az refuses --subscription
// together with --tenant, which the SDK's AzureCLICredential would pass for
// every Key Vault request (the vault's auth challenge names a tenant), so
// this sends --tenant only for vaults in another tenant.
//
// Which account a subscription belongs to changes: az's profile has one
// entry per subscription, and signing in to another account that sees it
// moves it there. So the subscription is looked up for every new token,
// and a token az issues for another of the signed-in users is refused.
//
// Tokens are cached in memory until shortly before they expire, so the
// many SDK clients don't each start an az process.
type cliCredential struct {
	user   string // the account's user; "" for az's default account
	tenant string // the account's tenant, if known
	// accounts reads az's accounts, to pin the user; nil for the default
	// account.
	accounts func() ([]account, error)
	run      func(ctx context.Context, args ...string) ([]byte, error)

	mu    sync.Mutex
	cache map[string]azcore.AccessToken // by resource and tenant argument
	// last is the claims of the latest token in the account's own
	// tenant, to describe the account (Provider.Account).
	last map[string]any
}

// claims are the latest token's claims, nil before the first.
func (c *cliCredential) claims() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func newCLICredential(user, tenant string, accounts func() ([]account, error)) *cliCredential {
	return &cliCredential{user: user, tenant: tenant, accounts: accounts, run: runAz, cache: map[string]azcore.AccessToken{}}
}

// pin is a subscription az maps to the user now, and the other users az
// holds.
func (c *cliCredential) pin() (subscription string, others []string, err error) {
	if c.accounts == nil {
		return "", nil, nil
	}
	accts, err := c.accounts()
	if err != nil {
		return "", nil, err
	}
	for _, a := range accts {
		switch {
		case a.user == c.user && a.tenant == c.tenant:
			subscription = a.anchor
		case !strings.EqualFold(a.user, c.user):
			others = append(others, a.user)
		}
	}
	if subscription == "" {
		return "", nil, fmt.Errorf("az has no subscriptions for %s any more: sign in as %s", c.user, c.user)
	}
	return subscription, others, nil
}

// refreshMargin renews tokens this long before they expire.
const refreshMargin = 5 * time.Minute

func (c *cliCredential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if len(opts.Scopes) != 1 {
		return azcore.AccessToken{}, errors.New("az: exactly one scope is supported")
	}
	if opts.Claims != "" {
		return azcore.AccessToken{}, errors.New("az: this request needs multifactor authentication; run az login again")
	}
	resource := strings.TrimSuffix(opts.Scopes[0], "/.default")
	otherTenant := opts.TenantID != "" && opts.TenantID != c.tenant
	key := resource + " " + opts.TenantID

	c.mu.Lock()
	defer c.mu.Unlock()
	if tok, ok := c.cache[key]; ok && time.Until(tok.ExpiresOn) > refreshMargin {
		return tok, nil
	}
	args := []string{"account", "get-access-token", "--output", "json", "--resource", resource}
	subscription, others, err := c.pin()
	switch {
	case otherTenant:
		// Another tenant: az picks the account itself.
		args = append(args, "--tenant", opts.TenantID)
	case err != nil:
		return azcore.AccessToken{}, err
	case subscription != "":
		args = append(args, "--subscription", subscription)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	tok, err := parseCLIToken(out)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	claims := tokenClaims(tok.Token)
	if u, ok := tokenUser(claims, others); ok {
		return azcore.AccessToken{}, fmt.Errorf("az gave a token for %s, not %s: sign in as %s again", u, c.user, c.user)
	}
	if !otherTenant && claims != nil {
		c.last = claims
	}
	c.cache[key] = tok
	return tok, nil
}

// tokenClaims reads a JWT's claims, without verifying it: they only
// describe the account and catch az picking the wrong one. nil if it
// isn't a JWT.
func tokenClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(b, &claims) != nil {
		return nil
	}
	return claims
}

// tokenUser reports which of users a token's claims are for, if any.
func tokenUser(claims map[string]any, users []string) (string, bool) {
	for _, name := range []string{"upn", "unique_name", "preferred_username", "email"} {
		v, _ := claims[name].(string)
		if i := strings.LastIndexByte(v, '#'); i >= 0 {
			v = v[i+1:] // live.com#user@example.com
		}
		for _, u := range users {
			if v != "" && strings.EqualFold(v, u) {
				return u, true
			}
		}
	}
	return "", false
}

func parseCLIToken(b []byte) (azcore.AccessToken, error) {
	var t struct {
		AccessToken string `json:"accessToken"`
		ExpiresOn   int64  `json:"expires_on"` // az 2.54+
		ExpiresOnS  string `json:"expiresOn"`  // older az: local time
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return azcore.AccessToken{}, fmt.Errorf("az: unexpected output: %w", err)
	}
	if t.AccessToken == "" {
		return azcore.AccessToken{}, errors.New("az: no access token in output")
	}
	exp := time.Unix(t.ExpiresOn, 0)
	if t.ExpiresOn == 0 {
		var err error
		exp, err = time.ParseInLocation("2006-01-02 15:04:05.999999", t.ExpiresOnS, time.Local)
		if err != nil {
			return azcore.AccessToken{}, fmt.Errorf("az: unexpected expiry %q", t.ExpiresOnS)
		}
	}
	return azcore.AccessToken{Token: t.AccessToken, ExpiresOn: exp}, nil
}

// azTimeout bounds one az call; a hung az would otherwise block a pane.
const azTimeout = 30 * time.Second

func runAz(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, azTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "az", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return nil, errors.New("az CLI not found; install it or set AZURE_* for a service principal")
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return out, nil
}
