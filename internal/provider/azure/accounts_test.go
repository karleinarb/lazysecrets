package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const profileJSON = "\xef\xbb\xbf" + `{"installationId": "x", "subscriptions": [
 {"id": "s-admin-1", "name": "platform", "state": "Enabled", "isDefault": false, "tenantId": "t1", "tenantDisplayName": "Contoso",
  "user": {"name": "admin.alice@contoso.onmicrosoft.com", "type": "user"}},
 {"id": "s-admin-2", "name": "old", "state": "Disabled", "isDefault": false, "tenantId": "t1",
  "user": {"name": "admin.alice@contoso.onmicrosoft.com", "type": "user"}},
 {"id": "s-alice-1", "name": "app-dev", "state": "Enabled", "isDefault": false, "tenantId": "t1",
  "user": {"name": "alice@contoso.com", "type": "user"}},
 {"id": "s-alice-2", "name": "app-prod", "state": "Enabled", "isDefault": true, "tenantId": "t1",
  "user": {"name": "alice@contoso.com", "type": "user"}}
]}`

func TestReadAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azureProfile.json")
	if err := os.WriteFile(path, []byte(profileJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("accounts = %+v", got)
	}
	// Default account first, anchored on the default subscription.
	if got[0].user != "alice@contoso.com" || got[0].anchor != "s-alice-2" || !got[0].isDefault {
		t.Errorf("default account = %+v", got[0])
	}
	// Anchored on an enabled subscription, not the disabled one.
	if got[1].user != "admin.alice@contoso.onmicrosoft.com" || got[1].anchor != "s-admin-1" || got[1].subscriptions != 2 {
		t.Errorf("second account = %+v", got[1])
	}

	if accts, err := readAccounts(filepath.Join(t.TempDir(), "missing.json")); err != nil || accts != nil {
		t.Errorf("missing profile: %v, %v", accts, err)
	}
}

func TestAccountLabels(t *testing.T) {
	got := accountLabels([]account{
		{user: "alice@contoso.com", tenant: "t1"},
		{user: "admin@a.example", tenant: "t1"},
		{user: "admin@b.example", tenant: "t1"},
		{user: "guest@x.example", tenant: "t1", tenantName: "Home"},
		{user: "guest@x.example", tenant: "t2", tenantName: "Partner"},
	})
	want := []string{"alice", "admin@a.example", "admin@b.example", "guest · Home", "guest · Partner"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %q\nwant      %q", got, want)
	}
}

func TestCLICredentialPinsAccount(t *testing.T) {
	var calls [][]string
	c := newCLICredential("alice@contoso.com", "t1", func() ([]account, error) {
		return []account{{user: "alice@contoso.com", tenant: "t1", anchor: "s-alice-2"}}, nil
	})
	c.run = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		exp := time.Now().Add(time.Hour).Unix()
		return []byte(fmt.Sprintf(`{"accessToken": "tok", "expires_on": %d}`, exp)), nil
	}
	ctx := context.Background()
	vault := policy.TokenRequestOptions{Scopes: []string{"https://vault.azure.net/.default"}}

	// Same tenant (Key Vault's challenge names it): pin with --subscription only.
	vault.TenantID = "t1"
	if tok, err := c.GetToken(ctx, vault); err != nil || tok.Token != "tok" {
		t.Fatalf("token: %v, %v", tok, err)
	}
	if got := strings.Join(calls[0], " "); !strings.Contains(got, "--subscription s-alice-2") || strings.Contains(got, "--tenant") {
		t.Errorf("same tenant args: %s", got)
	}

	// Cached: no second az call.
	if _, err := c.GetToken(ctx, vault); err != nil || len(calls) != 1 {
		t.Errorf("token not cached: %d calls", len(calls))
	}

	// Another tenant: --tenant only, since az refuses both.
	vault.TenantID = "t2"
	if _, err := c.GetToken(ctx, vault); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls[1], " "); !strings.Contains(got, "--tenant t2") || strings.Contains(got, "--subscription") {
		t.Errorf("other tenant args: %s", got)
	}
}

func TestCLICredentialRenewsAndReportsErrors(t *testing.T) {
	n := 0
	c := newCLICredential("", "", nil)
	c.run = func(context.Context, ...string) ([]byte, error) {
		n++
		if n == 2 {
			return nil, errors.New("AADSTS700082: The refresh token has expired. Please run 'az login'")
		}
		// Expires inside the refresh margin, so the next call renews.
		return []byte(`{"accessToken": "tok", "expiresOn": "` + time.Now().Add(time.Minute).Format("2006-01-02 15:04:05.000000") + `"}`), nil
	}
	opts := policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}
	if _, err := c.GetToken(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetToken(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "AADSTS700082") {
		t.Errorf("err = %v", err)
	}
}

// jwt is an unsigned token with claims, as az would print it.
func jwt(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(claims)) + ".sig"
}

func TestCLICredentialFollowsAzAndRefusesOtherUsers(t *testing.T) {
	// az's profile has one entry per subscription: signing in as bob,
	// who also sees s-shared, moved it from alice to bob.
	profile := []account{
		{user: "alice@contoso.com", tenant: "t1", anchor: "s-shared"},
		{user: "bob@contoso.com", tenant: "t1", anchor: "s-bob"},
	}
	var args []string
	issuedTo := "alice@contoso.com"
	c := newCLICredential("alice@contoso.com", "t1", func() ([]account, error) { return profile, nil })
	c.run = func(_ context.Context, a ...string) ([]byte, error) {
		args = a
		exp := time.Now().Add(time.Hour).Unix()
		return []byte(fmt.Sprintf(`{"accessToken": %q, "expires_on": %d}`, jwt(`{"upn":"`+issuedTo+`"}`), exp)), nil
	}
	arm := policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}
	if _, err := c.GetToken(context.Background(), arm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--subscription s-shared") {
		t.Errorf("args %q", args)
	}

	// Now alice has another subscription, and az would hand out bob's
	// token for the old one: the new pin is used, and bob's token refused.
	profile[0].anchor = "s-alice"
	c.cache = map[string]azcore.AccessToken{}
	issuedTo = "bob@contoso.com"
	_, err := c.GetToken(context.Background(), arm)
	if !strings.Contains(strings.Join(args, " "), "--subscription s-alice") {
		t.Errorf("args %q", args)
	}
	if err == nil || !strings.Contains(err.Error(), "token for bob@contoso.com, not alice@contoso.com") {
		t.Errorf("err = %v", err)
	}

	// Alice gone from az entirely: sign in again.
	profile = profile[1:]
	if _, err := c.GetToken(context.Background(), arm); err == nil || !strings.Contains(err.Error(), "sign in as alice@contoso.com") {
		t.Errorf("err = %v", err)
	}
}

func TestTokenUser(t *testing.T) {
	users := []string{"bob@contoso.com"}
	for claims, want := range map[string]bool{
		`{"upn":"Bob@Contoso.com"}`:                        true,
		`{"unique_name":"live.com#bob@contoso.com"}`:       true,
		`{"upn":"alice@contoso.com"}`:                      false,
		`{"appid":"00000000-0000-0000-0000-000000000000"}`: false,
	} {
		if _, got := tokenUser(tokenClaims(jwt(claims)), users); got != want {
			t.Errorf("%s: %v", claims, got)
		}
	}
	if _, got := tokenUser(tokenClaims("not-a-jwt"), users); got {
		t.Error("not a JWT")
	}
}
