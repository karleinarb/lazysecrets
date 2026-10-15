package azure

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func tags(m map[string]*string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = deref(v)
	}
	return out
}

const portal = "https://portal.azure.com/#"

// PortalResourceURL links to a blade of an Azure resource in the portal,
// e.g. "overview" or "secrets". tenant may be empty.
func PortalResourceURL(tenant, resourceID, blade string) string {
	if resourceID == "" {
		return ""
	}
	return portal + tenantPrefix(tenant) + "resource" + resourceID + "/" + blade
}

// portalAssetURL links to a Key Vault object's page: a version's with
// one in objectID; a secret's own (its versions) without.
func portalAssetURL(tenant string, t provider.ObjectType, objectID string) string {
	return portal + tenantPrefix(tenant) + "asset/Microsoft_Azure_KeyVault/" + string(t) + "/" + objectID
}

func tenantPrefix(tenant string) string {
	if tenant == "" {
		return ""
	}
	return "@" + tenant + "/"
}

// ObjectListURL links to the vault's list of the given object type,
// derived from the container's overview URL.
func ObjectListURL(c provider.Container, t provider.ObjectType) string {
	base, ok := strings.CutSuffix(c.URL, "/overview")
	if !ok {
		return ""
	}
	return base + "/" + t.Collection()
}

// objectURL is the data-plane URL of an object, e.g.
// https://kv.vault.azure.net/secrets/name.
func objectURL(c provider.Container, t provider.ObjectType, name string) string {
	return strings.TrimSuffix(c.Detail, "/") + "/" + t.Collection() + "/" + name
}

// secretObject is a secret, or a version of one, from a listing. Its URL
// is its page in the portal: the secret's (with its versions), or with
// a version, that version's.
func secretObject(c provider.Container, p *azsecrets.SecretProperties, tenant string) provider.Object {
	name := p.ID.Name()
	url := objectURL(c, provider.TypeSecret, name)
	o := provider.Object{
		Container:   c,
		Type:        provider.TypeSecret,
		ID:          url,
		Name:        name,
		Version:     p.ID.Version(),
		ContentType: deref(p.ContentType),
		Tags:        tags(p.Tags),
		Reference:   "@Microsoft.KeyVault(SecretUri=" + url + ")",
		URL:         portalAssetURL(tenant, provider.TypeSecret, strings.TrimSuffix(url+"/"+p.ID.Version(), "/")),
	}
	if a := p.Attributes; a != nil {
		o.Enabled, o.Updated, o.Expires = a.Enabled, deref(a.Updated), deref(a.Expires)
	}
	return o
}

func keyObject(c provider.Container, p *azkeys.KeyProperties) provider.Object {
	name := p.KID.Name()
	url := objectURL(c, provider.TypeKey, name)
	o := provider.Object{
		Container: c,
		Type:      provider.TypeKey,
		ID:        url,
		Name:      name,
		Version:   p.KID.Version(),
		Tags:      tags(p.Tags),
		Reference: url,
		URL:       ObjectListURL(c, provider.TypeKey),
	}
	if a := p.Attributes; a != nil {
		o.Enabled, o.Updated, o.Expires = a.Enabled, deref(a.Updated), deref(a.Expires)
	}
	return o
}

func certObject(c provider.Container, p *azcertificates.CertificateProperties) provider.Object {
	name := p.ID.Name()
	url := objectURL(c, provider.TypeCertificate, name)
	o := provider.Object{
		Container: c,
		Type:      provider.TypeCertificate,
		ID:        url,
		Name:      name,
		Version:   p.ID.Version(),
		Tags:      tags(p.Tags),
		Reference: url,
		URL:       ObjectListURL(c, provider.TypeCertificate),
	}
	if a := p.Attributes; a != nil {
		o.Enabled, o.Updated, o.Expires = a.Enabled, deref(a.Updated), deref(a.Expires)
	}
	if len(p.X509Thumbprint) > 0 {
		o.Attrs = []provider.Attr{{Label: "thumbprint", Value: hexColon(p.X509Thumbprint)}}
	}
	return o
}

// keyAttrs describes a JSON Web Key, e.g. "RSA-HSM 3072" or "EC P-256".
func keyAttrs(k *azkeys.JSONWebKey) []provider.Attr {
	if k == nil {
		return nil
	}
	kty := string(deref(k.Kty))
	desc := kty
	switch {
	case len(k.N) > 0:
		desc = fmt.Sprintf("%s %d", kty, new(big.Int).SetBytes(k.N).BitLen())
	case k.Crv != nil:
		desc = kty + " " + string(*k.Crv)
	}
	attrs := []provider.Attr{{Label: "key type", Value: desc}}
	var ops []string
	for _, op := range k.KeyOps {
		ops = append(ops, string(deref(op)))
	}
	if len(ops) > 0 {
		attrs = append(attrs, provider.Attr{Label: "ops", Value: strings.Join(ops, ", ")})
	}
	return attrs
}

// publicKeyPEM encodes the public half of a JSON Web Key as PEM. Key Vault
// never returns private material for keys, and this ignores it regardless.
func publicKeyPEM(k *azkeys.JSONWebKey) (string, error) {
	if k == nil {
		return "", errors.New("key has no key material")
	}
	var pub any
	switch {
	case len(k.N) > 0 && len(k.E) > 0:
		pub = &rsa.PublicKey{N: new(big.Int).SetBytes(k.N), E: int(new(big.Int).SetBytes(k.E).Int64())}
	case len(k.X) > 0 && len(k.Y) > 0:
		var curve elliptic.Curve
		switch deref(k.Crv) {
		case azkeys.CurveNameP256:
			curve = elliptic.P256()
		case azkeys.CurveNameP384:
			curve = elliptic.P384()
		case azkeys.CurveNameP521:
			curve = elliptic.P521()
		default:
			return "", fmt.Errorf("cannot encode curve %s as PEM", deref(k.Crv))
		}
		size := (curve.Params().BitSize + 7) / 8
		point := make([]byte, 1+2*size)
		point[0] = 4 // uncompressed
		copy(point[1:], leftPad(k.X, size))
		copy(point[1+size:], leftPad(k.Y, size))
		ec, err := ecdsa.ParseUncompressedPublicKey(curve, point)
		if err != nil {
			return "", err
		}
		pub = ec
	default:
		return "", fmt.Errorf("%s keys have no public key", deref(k.Kty))
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))), nil
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	return append(make([]byte, n-len(b)), b...)
}

// certAttrs describes an X.509 certificate.
func certAttrs(cert *x509.Certificate, thumbprint []byte) []provider.Attr {
	attrs := []provider.Attr{
		{Label: "subject", Value: cert.Subject.String()},
		{Label: "issuer", Value: cert.Issuer.String()},
	}
	if len(thumbprint) > 0 {
		attrs = append(attrs, provider.Attr{Label: "thumbprint", Value: hexColon(thumbprint)})
	}
	var sans []string
	sans = append(sans, cert.DNSNames...)
	sans = append(sans, cert.EmailAddresses...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	for _, u := range cert.URIs {
		sans = append(sans, u.String())
	}
	if len(sans) > 0 {
		attrs = append(attrs, provider.Attr{Label: "SANs", Value: strings.Join(sans, ", ")})
	}
	attrs = append(attrs, provider.Attr{Label: "valid from", Value: cert.NotBefore.Local().Format("2006-01-02 15:04")})
	return attrs
}

func certPEM(der []byte) string {
	return strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
}

func hexColon(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}

// cleanError shortens the SDK's multi-line HTTP errors to "403 Forbidden:
// <service message>", keeping the original in the chain.
func cleanError(err error) error {
	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		return err
	}
	msg := re.ErrorCode
	if re.RawResponse != nil {
		if body, rerr := runtime.Payload(re.RawResponse); rerr == nil {
			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
				msg = payload.Error.Message
			}
		}
	}
	se := &serviceError{status: re.StatusCode, msg: shortenDenied(msg), err: err}
	if se.status == http.StatusForbidden {
		return &provider.Denied{Err: se, Checks: deniedChecks(se.msg)}
	}
	return se
}

var policyDenied = regexp.MustCompile(`does not have (\w+ \w+) permission on key vault`)

// shortenDenied reduces Key Vault's RBAC denial, which runs to several
// sentences plus caller and resource IDs, to its first sentence and the
// missing action.
func shortenDenied(msg string) string {
	// Access policies: "The user, group or application 'appid=…;oid=…'
	// does not have secrets list permission on key vault 'kv;location=…'".
	if m := policyDenied.FindStringSubmatch(msg); m != nil {
		return "no " + m[1] + " permission in the vault's access policies"
	}
	const marker = "Action: '"
	i := strings.Index(msg, marker)
	if i < 0 {
		return msg
	}
	action, _, ok := strings.Cut(msg[i+len(marker):], "'")
	if !ok || action == "" {
		return msg
	}
	first := msg
	if k := strings.IndexAny(first, "\r\n"); k >= 0 {
		first = first[:k]
	}
	first, _, _ = strings.Cut(first, ". ")
	return strings.TrimSuffix(first, ".") + "; missing " + action
}

type serviceError struct {
	status int
	msg    string
	err    error
}

func (e *serviceError) Error() string {
	s := fmt.Sprintf("%d %s", e.status, http.StatusText(e.status))
	if e.msg != "" {
		s += ": " + e.msg
	}
	return s
}

// deniedChecks are what to check when Key Vault refuses: the network,
// for its firewall's refusals, else access.
func deniedChecks(msg string) []string {
	if strings.Contains(msg, "Client address is not authorized") || strings.Contains(msg, "network access") ||
		strings.Contains(msg, "private link") {
		return []string{
			"you're on a network the vault lets in (VPN, office)",
			"the vault's owner has let your network in",
		}
	}
	return []string{
		"your role or group is activated in PIM",
		"the vault's owner has given you access: a Key Vault role, or its access policies",
	}
}

func (e *serviceError) Unwrap() error { return e.err }
