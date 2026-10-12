package databricks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	dbconfig "github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/workspace"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func TestReadProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "databrickscfg")
	cfg := `[DEFAULT]
host      = https://adb-1.1.azuredatabricks.net
auth_type = azure-cli

[INFRA-DEV]
host      = adb-2.2.azuredatabricks.net
auth_type = azure-cli

[ACCOUNT]
host       = https://accounts.azuredatabricks.net
auth_type  = azure-cli

[acct-by-id]
host       = https://example.cloud.databricks.com
account_id = 123

[oauth]
host      = https://adb-3.3.azuredatabricks.net
auth_type = databricks-cli

[no-host]
token = x
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []profile{
		{name: "DEFAULT", host: "https://adb-1.1.azuredatabricks.net", authType: "azure-cli"},
		{name: "INFRA-DEV", host: "adb-2.2.azuredatabricks.net", authType: "azure-cli"},
		{name: "oauth", host: "https://adb-3.3.azuredatabricks.net", authType: "databricks-cli"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("profiles = %+v\nwant       %+v", got, want)
	}

	if got, err := readProfiles(filepath.Join(t.TempDir(), "missing")); err != nil || got != nil {
		t.Errorf("missing file: %v, %v", got, err)
	}
}

func TestLoginCommand(t *testing.T) {
	for authType, want := range map[string][]string{
		"azure-cli":      {"az", "login"},
		"databricks-cli": {"databricks", "auth", "login", "--profile", "p"},
		"":               {"databricks", "auth", "login", "--profile", "p"},
		"pat":            nil,
	} {
		if got := loginCommand(profile{name: "p", authType: authType}); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v, want %v", authType, got, want)
		}
	}
}

// fakeKeyVault stands in for the Azure provider.
type fakeKeyVault struct {
	secrets map[string]string // name → value, for vault URL kvURL
	err     error
	calls   int
}

const kvURL = "https://kv-app-prod.vault.azure.net/"

func (k *fakeKeyVault) Objects(_ context.Context, c provider.Container, _ provider.ObjectType) ([]provider.Object, error) {
	k.calls++
	if k.err != nil {
		return nil, k.err
	}
	if c.Detail != kvURL {
		return nil, errors.New("wrong vault " + c.Detail)
	}
	var out []provider.Object
	for name := range k.secrets {
		out = append(out, provider.Object{Name: name})
	}
	return out, nil
}

func (k *fakeKeyVault) Value(_ context.Context, o provider.Object) (provider.SecretValue, error) {
	k.calls++
	if k.err != nil {
		return provider.SecretValue{}, k.err
	}
	if o.Container.Detail != kvURL {
		return provider.SecretValue{}, errors.New("wrong vault " + o.Container.Detail)
	}
	return provider.NewSecretValue([]byte(k.secrets[o.Name])), nil
}

// workspaceAPI serves the secrets API. The "akv" scope behaves like an Azure
// Key Vault-backed scope whose secrets Databricks refuses to read.
func workspaceAPI(t *testing.T, status int) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			write(w, status, map[string]any{"error_code": "UNAUTHENTICATED", "message": "Token is expired"})
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/api/2.0/secrets/scopes/list":
			write(w, 200, map[string]any{"scopes": []any{
				map[string]any{"name": "app", "backend_type": "DATABRICKS"},
				map[string]any{"name": "akv", "backend_type": "AZURE_KEYVAULT",
					"keyvault_metadata": map[string]any{"dns_name": kvURL, "resource_id": "/x/kv-app-prod"}},
			}})
		case r.URL.Path == "/api/2.0/secrets/list" && q.Get("scope") == "app":
			write(w, 200, map[string]any{"secrets": []any{
				map[string]any{"key": "storage-key", "last_updated_timestamp": 1767225600000},
				map[string]any{"key": "client-secret"},
			}})
		case r.URL.Path == "/api/2.0/secrets/list" && q.Get("scope") == "locked":
			write(w, 403, map[string]any{"error_code": "PERMISSION_DENIED", "message": "User does not have READ permission on scope locked"})
		case r.URL.Path == "/api/2.0/secrets/get" && q.Get("scope") == "app":
			write(w, 200, map[string]any{"key": q.Get("key"),
				"value": base64.StdEncoding.EncodeToString([]byte("value-of-" + q.Get("key")))})
		case strings.HasPrefix(r.URL.Path, "/api/2.0/secrets/") && q.Get("scope") == "akv":
			write(w, 400, map[string]any{"error_code": "BAD_REQUEST",
				"message": "Reading secrets from Azure KeyVault-backed scopes is not supported"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestProvider(t *testing.T, srv *httptest.Server, kv KeyVault) *Provider {
	t.Helper()
	return &Provider{
		profiles: []profile{{name: "prod", host: srv.URL, authType: "azure-cli"}},
		keyVault: kv,
		newClient: func(profile) (*workspace.SecretsAPI, error) {
			return secretsClient(&dbconfig.Config{Host: srv.URL, Token: "token"})
		},
		clients: map[string]*workspace.SecretsAPI{},
		vaults:  map[string]string{},
	}
}

func scopes(t *testing.T, p *Provider) map[string]provider.Container {
	t.Helper()
	srcs, _ := p.Sources(context.Background())
	cs, err := p.Containers(context.Background(), srcs[0])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]provider.Container{}
	for _, c := range cs {
		out[c.Name] = c
	}
	return out
}

func TestScopesSecretsAndValues(t *testing.T) {
	p := newTestProvider(t, workspaceAPI(t, 0), nil)
	sc := scopes(t, p)
	if sc["app"].Detail != "Databricks-backed" || sc["akv"].Detail != "Azure Key Vault-backed: "+kvURL {
		t.Errorf("scope details = %q / %q", sc["app"].Detail, sc["akv"].Detail)
	}

	objs, err := p.Objects(context.Background(), sc["app"], provider.TypeSecret)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].Name != "client-secret" || objs[1].Name != "storage-key" {
		t.Fatalf("objects = %+v", objs)
	}
	if objs[1].Updated.IsZero() || objs[1].Reference != "{{secrets/app/storage-key}}" {
		t.Errorf("metadata = %+v", objs[1])
	}

	v, err := p.Value(context.Background(), objs[1])
	if err != nil || v.Reveal() != "value-of-storage-key" {
		t.Errorf("value = %q, %v", v.Reveal(), err)
	}
}

func TestAKVScopeFallsBackToKeyVault(t *testing.T) {
	kv := &fakeKeyVault{secrets: map[string]string{"db-password": "from-the-vault"}}
	p := newTestProvider(t, workspaceAPI(t, 0), kv)
	akv := scopes(t, p)["akv"]

	objs, err := p.Objects(context.Background(), akv, provider.TypeSecret)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Name != "db-password" || objs[0].Reference != "{{secrets/akv/db-password}}" {
		t.Fatalf("objects = %+v", objs)
	}
	v, err := p.Value(context.Background(), objs[0])
	if err != nil || v.Reveal() != "from-the-vault" {
		t.Errorf("value = %q, %v", v.Reveal(), err)
	}
}

func TestAKVFallbackForRememberedScope(t *testing.T) {
	// A favourite from an earlier session: scopes not listed yet.
	kv := &fakeKeyVault{secrets: map[string]string{"db-password": "from-the-vault"}}
	p := newTestProvider(t, workspaceAPI(t, 0), kv)
	akv := provider.Container{
		Source: provider.Source{Kind: provider.KindDatabricks, ID: "prod"},
		ID:     "prod/akv", Name: "akv", Detail: "Azure Key Vault-backed: " + kvURL,
	}
	v, err := p.Value(context.Background(), provider.Object{Container: akv, Name: "db-password"})
	if err != nil || v.Reveal() != "from-the-vault" {
		t.Errorf("value = %q, %v", v.Reveal(), err)
	}
}

func TestAKVFallbackFailureReportsBoth(t *testing.T) {
	kvAuth := &provider.AuthError{Kind: provider.KindAzure, Login: []string{"az", "login"}, Err: errors.New("expired")}
	p := newTestProvider(t, workspaceAPI(t, 0), &fakeKeyVault{err: kvAuth})
	akv := scopes(t, p)["akv"]

	_, err := p.Value(context.Background(), provider.Object{Container: akv, Name: "db-password"})
	if err == nil || !strings.Contains(err.Error(), "not supported") || !strings.Contains(err.Error(), "Key Vault fallback") {
		t.Fatalf("err = %v", err)
	}
	var auth *provider.AuthError
	if !errors.As(err, &auth) {
		t.Error("Key Vault auth error not reachable for the sign-in prompt")
	}
}

func TestNoFallbackForDatabricksBackedScopes(t *testing.T) {
	kv := &fakeKeyVault{}
	p := newTestProvider(t, workspaceAPI(t, 0), kv)
	app := scopes(t, p)["app"]
	if _, err := p.Value(context.Background(), provider.Object{Container: app, Name: "missing"}); err != nil {
		// app/get returns a value for any key; just make sure the vault wasn't asked
		t.Fatal(err)
	}
	if kv.calls != 0 {
		t.Errorf("Key Vault consulted for a Databricks-backed scope")
	}
}

func TestUnauthorizedIsAuthError(t *testing.T) {
	p := newTestProvider(t, workspaceAPI(t, 401), nil)
	srcs, _ := p.Sources(context.Background())
	_, err := p.Containers(context.Background(), srcs[0])
	var auth *provider.AuthError
	if !errors.As(err, &auth) {
		t.Fatalf("err = %v, want AuthError", err)
	}
	if auth.Kind != provider.KindDatabricks || !reflect.DeepEqual(auth.Login, []string{"az", "login"}) {
		t.Errorf("auth = %+v", auth)
	}
}

func TestClassify(t *testing.T) {
	pr := profile{name: "p", authType: "databricks-cli"}
	var auth *provider.AuthError
	if !errors.As(classify(pr, errors.New("default auth: azure-cli: cannot get access token: run az login")), &auth) {
		t.Error("token failure not classified as auth")
	}
	if errors.As(classify(pr, errors.New("dial tcp: no such host")), &auth) {
		t.Error("network error classified as auth")
	}
	if errors.As(classify(pr, context.Canceled), &auth) {
		t.Error("cancellation classified as auth")
	}
}

func TestDeniedSaysWhatToCheck(t *testing.T) {
	p := newTestProvider(t, workspaceAPI(t, 0), nil)
	_, err := p.Objects(context.Background(), provider.Container{Source: provider.Source{ID: "prod"}, Name: "locked"}, provider.TypeSecret)
	var d *provider.Denied
	// The test profile signs in through az: that's the first check.
	if !errors.As(err, &d) || !strings.Contains(err.Error(), "READ permission on scope locked\n\nCheck:\n• az is signed in as the account with access: profile prod uses your az login") ||
		!strings.Contains(err.Error(), "\n• you have access to the workspace") {
		t.Errorf("err %q", err)
	}
	if got := deniedChecks(profile{authType: "pat"}, "denied"); strings.Contains(got[0], "az") {
		t.Errorf("a token profile's checks %q", got)
	}
	if got := deniedChecks(profile{}, "Source IP address: 1.2.3.4 is blocked by Databricks IP ACL for workspace: 1"); !strings.Contains(got[0], "network") {
		t.Errorf("IP ACL checks %q", got)
	}

	// Both refused: one message, then both lists of checks.
	err = combine(&provider.Denied{Err: errors.New("dbx"), Checks: []string{"a", "b"}},
		&provider.Denied{Err: errors.New("kv"), Checks: []string{"b", "c"}})
	if got := err.Error(); got != "dbx; Key Vault fallback: kv\n\nCheck:\n• a\n• b\n• c" {
		t.Errorf("combined %q", got)
	}
}
