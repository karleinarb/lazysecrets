package azure

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

type staticCred struct{ err error }

func (c staticCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if c.err != nil {
		return azcore.AccessToken{}, c.err
	}
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// fakeAzure serves just enough of ARM and the Key Vault data plane over TLS,
// including Key Vault's 401 auth challenge.
func fakeAzure(t *testing.T, cred azcore.TokenCredential, handler http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate",
				`Bearer authorization="https://login.microsoftonline.com/tenant", resource="https://vault.azure.net"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	client := azcore.ClientOptions{
		Transport: srv.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: srv.URL, Audience: "https://management.azure.com"},
			},
		},
	}
	p, err := newWithOptions(cred, options{
		arm:                     &arm.ClientOptions{ClientOptions: client},
		client:                  client,
		skipChallengeValidation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, srv
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func vault(srv *httptest.Server) provider.Container {
	return provider.Container{ID: "/subscriptions/s/vault", Name: "kv", Detail: srv.URL + "/", Types: objectTypes}
}

func TestTokenFailureIsAuthError(t *testing.T) {
	login := []string{"az", "login"}
	cred := authCredential{cred: staticCred{err: errors.New("AADSTS700082: refresh token expired")}, login: login}
	p, srv := fakeAzure(t, cred, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request sent without a token: %s", r.URL)
	})

	check := func(name string, err error) {
		t.Helper()
		var auth *provider.AuthError
		if !errors.As(err, &auth) {
			t.Fatalf("%s: error %v is not an AuthError", name, err)
		}
		if !reflect.DeepEqual(auth.Login, login) || !strings.Contains(auth.Error(), "AADSTS700082") {
			t.Errorf("%s: unexpected AuthError %+v", name, auth)
		}
	}
	_, err := p.Sources(context.Background())
	check("Sources (ARM)", err)
	_, err = p.Objects(context.Background(), vault(srv), provider.TypeSecret)
	check("Objects (Key Vault)", err)
	_, err = p.Containers(context.Background(), provider.Source{ID: "s"})
	check("Containers (no ARM fallback on auth errors)", err)
}

func TestAuthErrorNamesAccount(t *testing.T) {
	cred := authCredential{cred: staticCred{err: errors.New("AADSTS53003")}, account: "Azure · admin"}
	_, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"x"}})
	var auth *provider.AuthError
	if !errors.As(err, &auth) || auth.Who() != "Azure · admin" || !strings.HasPrefix(auth.Error(), "Azure · admin:") {
		t.Errorf("err = %v", err)
	}
}

func TestListSecretsSkipsManagedAndPages(t *testing.T) {
	var srvURL string
	p, srv := fakeAzure(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/secrets" && r.URL.Query().Get("page") == "":
			writeJSON(w, 200, map[string]any{
				"value": []any{
					map[string]any{"id": srvURL + "/secrets/b-secret", "contentType": "text/plain",
						"attributes": map[string]any{"enabled": true, "updated": 1767225600}, "tags": map[string]any{"env": "prod"}},
					map[string]any{"id": srvURL + "/secrets/tls-cert", "managed": true},
				},
				"nextLink": srvURL + "/secrets?page=2&api-version=" + r.URL.Query().Get("api-version"),
			})
		case r.URL.Path == "/secrets":
			writeJSON(w, 200, map[string]any{"value": []any{map[string]any{"id": srvURL + "/secrets/a-secret"}}})
		default:
			http.NotFound(w, r)
		}
	})
	srvURL = srv.URL

	got, err := p.Objects(context.Background(), vault(srv), provider.TypeSecret)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range got {
		names = append(names, o.Name)
	}
	if !reflect.DeepEqual(names, []string{"a-secret", "b-secret"}) {
		t.Fatalf("names = %v, want sorted and without the managed secret", names)
	}
	b := got[1]
	if b.ContentType != "text/plain" || b.Tags["env"] != "prod" || b.Enabled == nil || !*b.Enabled ||
		b.Updated.IsZero() || b.Type != provider.TypeSecret {
		t.Errorf("metadata not mapped: %+v", b)
	}
	if want := "@Microsoft.KeyVault(SecretUri=" + srv.URL + "/secrets/b-secret)"; b.Reference != want {
		t.Errorf("Reference = %q, want %q", b.Reference, want)
	}
}

func TestForbiddenIsShortened(t *testing.T) {
	p, srv := fakeAzure(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, map[string]any{"error": map[string]any{
			"code": "Forbidden", "message": "Caller is not authorized to perform action on resource.",
		}})
	})
	_, err := p.Objects(context.Background(), vault(srv), provider.TypeKey)
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "403 Forbidden: Caller is not authorized to perform action on resource.\n\nCheck:\n• your role or group is activated in PIM\n• "; !strings.HasPrefix(got, want) {
		t.Errorf("error = %q, want %q…", got, want)
	}
	if checks := deniedChecks("Client address is not authorized and caller is not a trusted service."); !strings.Contains(checks[0], "network") {
		t.Errorf("firewall checks = %q", checks)
	}
	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != 403 {
		t.Error("original ResponseError lost from chain")
	}
}

func TestShortenPolicyDenied(t *testing.T) {
	msg := "The user, group or application 'appid=04b07795;oid=1234;numgroups=83;iss=https://sts.windows.net/t/' does not have secrets list permission on key vault 'kv-app;location=norwayeast'. For help resolving this issue, please see https://go.microsoft.com/fwlink/?linkid=2125287"
	if got := shortenDenied(msg); got != "no secrets list permission in the vault's access policies" {
		t.Errorf("shortenDenied = %q", got)
	}
}

func TestShortenDenied(t *testing.T) {
	msg := "Caller is not authorized to perform action on resource.\r\nIf role assignments, deny assignments or role definitions were changed recently, please observe propagation time.\r\nCaller: appid=04b07795;oid=1234\r\nAction: 'Microsoft.KeyVault/vaults/secrets/readMetadata/action'\r\nResource: '/subscriptions/x/vaults/kv'"
	want := "Caller is not authorized to perform action on resource; missing Microsoft.KeyVault/vaults/secrets/readMetadata/action"
	for _, variant := range []string{msg, strings.ReplaceAll(msg, "\r\n", " ")} {
		if got := shortenDenied(variant); got != want {
			t.Errorf("got %q", got)
		}
	}
	if got := shortenDenied("Public network access is disabled."); got != "Public network access is disabled." {
		t.Errorf("unrelated message changed: %q", got)
	}
}

func TestSecretValue(t *testing.T) {
	var srvURL string
	p, srv := fakeAzure(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/secrets/db-password/v2" && r.URL.Path != "/secrets/db-password/" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"id": srvURL + r.URL.Path, "value": "value-of-" + r.URL.Path})
	})
	srvURL = srv.URL
	o := provider.Object{Container: vault(srv), Type: provider.TypeSecret, Name: "db-password"}
	for version, want := range map[string]string{"": "value-of-/secrets/db-password/", "v2": "value-of-/secrets/db-password/v2"} {
		o.Version = version
		v, err := p.Value(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		if v.Reveal() != want {
			t.Errorf("version %q: got %q", version, v.Reveal())
		}
	}
}

func TestDescribeCertificate(t *testing.T) {
	der, cert := selfSigned(t)
	thumb := sha1.Sum(der)
	var srvURL string
	p, srv := fakeAzure(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"id":     srvURL + "/certificates/app-tls/abc123",
			"cer":    base64.StdEncoding.EncodeToString(der),
			"x5t":    base64.RawURLEncoding.EncodeToString(thumb[:]),
			"policy": map[string]any{"secret_props": map[string]any{"contentType": "application/x-pem-file"}},
		})
	})
	srvURL = srv.URL
	o, err := p.Describe(context.Background(), provider.Object{Container: vault(srv), Type: provider.TypeCertificate, Name: "app-tls"})
	if err != nil {
		t.Fatal(err)
	}
	attrs := map[string]string{}
	for _, a := range o.Attrs {
		attrs[a.Label] = a.Value
	}
	if o.Version != "abc123" || o.ContentType != "application/x-pem-file" {
		t.Errorf("version/content type = %q/%q", o.Version, o.ContentType)
	}
	if attrs["subject"] != "CN=app.example.com" || attrs["SANs"] != "app.example.com, 10.0.0.1" ||
		!strings.HasPrefix(attrs["thumbprint"], hexColon(thumb[:2])) {
		t.Errorf("attrs = %v", attrs)
	}

	v, err := p.Value(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(v.Reveal()))
	if block == nil || block.Type != "CERTIFICATE" || !cert.Equal(mustParse(t, block.Bytes)) {
		t.Errorf("certificate PEM does not round-trip: %q", v.Reveal())
	}
}

func TestPublicKeyPEM(t *testing.T) {
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	ecX, ecY := ecPoint(t, ecKey)

	for name, tc := range map[string]struct {
		jwk      azkeys.JSONWebKey
		pub      any
		wantDesc string
	}{
		"rsa": {
			jwk: azkeys.JSONWebKey{Kty: to.Ptr(azkeys.KeyTypeRSAHSM), N: rsaKey.N.Bytes(),
				E: big.NewInt(int64(rsaKey.E)).Bytes(), KeyOps: []*azkeys.KeyOperation{to.Ptr(azkeys.KeyOperationSign)}},
			pub: &rsaKey.PublicKey, wantDesc: "RSA-HSM 2048",
		},
		"ec": {
			jwk: azkeys.JSONWebKey{Kty: to.Ptr(azkeys.KeyTypeEC), Crv: to.Ptr(azkeys.CurveNameP384), X: ecX, Y: ecY},
			pub: &ecKey.PublicKey, wantDesc: "EC P-384",
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := publicKeyPEM(&tc.jwk)
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode([]byte(out))
			got, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if !got.(interface{ Equal(crypto.PublicKey) bool }).Equal(tc.pub) {
				t.Error("public key does not round-trip")
			}
			if desc := keyAttrs(&tc.jwk)[0].Value; desc != tc.wantDesc {
				t.Errorf("key type = %q, want %q", desc, tc.wantDesc)
			}
		})
	}

	if _, err := publicKeyPEM(&azkeys.JSONWebKey{Kty: to.Ptr(azkeys.KeyTypeOct)}); err == nil {
		t.Error("symmetric key should have no public key")
	}
}

func TestPortalURLs(t *testing.T) {
	id := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
	c := provider.Container{URL: PortalResourceURL("t1", id, "overview")}
	if want := "https://portal.azure.com/#@t1/resource" + id + "/overview"; c.URL != want {
		t.Errorf("vault URL = %q", c.URL)
	}
	if got := ObjectListURL(c, provider.TypeCertificate); got != "https://portal.azure.com/#@t1/resource"+id+"/certificates" {
		t.Errorf("certificates URL = %q", got)
	}
	if got := ObjectListURL(provider.Container{}, provider.TypeSecret); got != "" {
		t.Errorf("URL without vault page = %q", got)
	}
	if got := PortalResourceURL("", "", "overview"); got != "" {
		t.Errorf("URL without resource = %q", got)
	}
	if got := portalAssetURL("", "Key", "https://kv.vault.azure.net/keys/k/v1"); got !=
		"https://portal.azure.com/#asset/Microsoft_Azure_KeyVault/Key/https://kv.vault.azure.net/keys/k/v1" {
		t.Errorf("asset URL = %q", got)
	}
}

func TestVersionedReference(t *testing.T) {
	sec := provider.Object{Type: provider.TypeSecret, Version: "v1",
		Reference: "@Microsoft.KeyVault(SecretUri=https://kv.vault.azure.net/secrets/s)"}
	if got := versionedReference(sec); got != "@Microsoft.KeyVault(SecretUri=https://kv.vault.azure.net/secrets/s/v1)" {
		t.Errorf("secret: %q", got)
	}
	key := provider.Object{Type: provider.TypeKey, Version: "v2", Reference: "https://kv.vault.azure.net/keys/k"}
	if got := versionedReference(key); got != "https://kv.vault.azure.net/keys/k/v2" {
		t.Errorf("key: %q", got)
	}
}

func TestHiddenSubscriptionsAndPinnedVaults(t *testing.T) {
	p, _ := fakeAzure(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"value": []any{
			map[string]any{"subscriptionId": "s1", "displayName": "keep-me", "state": "Enabled", "tenantId": "t"},
			map[string]any{"subscriptionId": "s2", "displayName": "platform-mgmt", "state": "Enabled", "tenantId": "t"},
			map[string]any{"subscriptionId": "s3", "displayName": "by-id", "state": "Enabled", "tenantId": "t"},
		}})
	})
	p.opts.Settings = Settings{
		HideSubscriptions: []string{"PLATFORM-MGMT", "s3"},
		Vaults:            []string{"https://kv-pinned.vault.azure.net"},
	}

	srcs, err := p.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range srcs {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"keep-me", "Pinned vaults"}) {
		t.Fatalf("sources = %v", names)
	}

	vaults, err := p.Containers(context.Background(), srcs[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(vaults) != 1 || vaults[0].Name != "kv-pinned" || vaults[0].Detail != "https://kv-pinned.vault.azure.net/" ||
		len(vaults[0].Types) != 3 {
		t.Errorf("pinned vaults = %+v", vaults)
	}
}

func TestGraphVault(t *testing.T) {
	src := provider.Source{ID: "sub"}
	c, ok := graphVault(src, map[string]any{"id": "/x/kv", "name": "kv", "uri": "https://kv.vault.azure.net/"})
	if !ok || c.Name != "kv" || c.Detail != "https://kv.vault.azure.net/" || len(c.Types) != 3 {
		t.Errorf("graphVault = %+v, %v", c, ok)
	}
	if _, ok := graphVault(src, map[string]any{"id": "/x/kv", "name": "kv"}); ok {
		t.Error("row without uri accepted")
	}
}

func selfSigned(t *testing.T) ([]byte, *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "app.example.com"},
		DNSNames:     []string{"app.example.com"},
		IPAddresses:  []net.IP{net.ParseIP("10.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, mustParse(t, der)
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ecPoint(t *testing.T, k *ecdsa.PrivateKey) (x, y []byte) {
	t.Helper()
	b, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	size := (len(b) - 1) / 2
	return b[1 : 1+size], b[1+size:]
}

func TestVaultSettings(t *testing.T) {
	want := []string{"group=rg-sec", "location=norwayeast", "SKU=premium", "access=Azure roles (RBAC)",
		"soft delete=on, 30 days", "purge prot.=on", "network=selected networks (1 IP ranges, 0 subnets)",
		"private=1 endpoint", "tags=env: prod", "=team: platform"}
	check := func(name string, attrs []provider.Attr) {
		t.Helper()
		var got []string
		for _, a := range attrs {
			got = append(got, a.Label+"="+a.Value)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s\n got %q\nwant %q", name, got, want)
		}
	}

	// Resource Graph: the query's columns, as JSON gives them.
	c, ok := graphVault(provider.Source{ID: "s"}, map[string]any{
		"id": "/x/kv", "name": "kv", "uri": "https://kv.vault.azure.net/", "location": "norwayeast",
		"resourceGroup": "rg-sec", "tags": map[string]any{"team": "platform", "env": "prod"},
		"sku": "premium", "rbac": true, "softDelete": nil, "retention": 30.0, "purge": true,
		"publicAccess": "", "defaultAction": "Deny", "ipRules": 1.0, "subnets": 0.0, "endpoints": 1.0, "policies": 0.0,
	})
	if !ok {
		t.Fatal("row not read")
	}
	check("resource graph", c.Attrs)

	// The management API, when Resource Graph fails.
	id := "/subscriptions/s/resourceGroups/rg-sec/providers/Microsoft.KeyVault/vaults/kv"
	p, _ := fakeAzure(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"value": []any{map[string]any{"id": id, "name": "kv", "location": "norwayeast",
			"tags": map[string]any{"team": "platform", "env": "prod"},
			"properties": map[string]any{
				"tenantId": "t", "vaultUri": "https://kv.vault.azure.net/", "sku": map[string]any{"family": "A", "name": "premium"},
				"enableRbacAuthorization": true, "enablePurgeProtection": true, "softDeleteRetentionInDays": 30,
				"networkAcls":                map[string]any{"defaultAction": "Deny", "ipRules": []any{map[string]any{"value": "10.0.0.0/8"}}},
				"privateEndpointConnections": []any{map[string]any{"id": "pe1"}},
			}}}})
	})
	vaults, err := p.armVaults(context.Background(), provider.Source{ID: "s"})
	if err != nil || len(vaults) != 1 {
		t.Fatalf("arm: %v %v", vaults, err)
	}
	check("management API", vaults[0].Attrs)
}
