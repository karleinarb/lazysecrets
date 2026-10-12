package azure

import (
	"fmt"
	"slices"
	"strings"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// accountDetails describes an az account from az's account list and the
// claims of the latest token lazysecrets got for it: nothing is fetched
// for it.
type accountDetails struct {
	account
	claims func() map[string]any
}

var _ provider.Accounts = (*Provider)(nil)

// Account describes the az account, once a token has been fetched for it
// (the user's name and object ID come from the token).
func (p *Provider) Account() (provider.Account, bool) {
	if p.account == nil {
		return provider.Account{}, false
	}
	a := p.account
	claims := a.claims()
	str := func(k string) string { s, _ := claims[k].(string); return s }

	var out provider.Account
	add := func(label, value string) {
		if value != "" {
			out.Fields = append(out.Fields, provider.Attr{Label: label, Value: value})
		}
	}
	add("name", str("name"))
	user := a.user
	if a.userType == "servicePrincipal" {
		user += " (service principal)"
	}
	add("user", user)
	add("tenant", a.tenantName)
	add("tenant ID", a.tenant)
	if oid := str("oid"); oid != "" {
		add("object ID", oid)
		out.Copy, out.CopyLabel = oid, "object ID"
	}
	if claims != nil {
		add("sign-in", signInMethods(claims["amr"]))
		add("roles", directoryRoles(claims["wids"]))
	}
	subs := fmt.Sprint(a.subscriptions)
	if a.defaultSub != "" {
		subs += " · default " + a.defaultSub
	}
	add("subs", subs)
	if a.cloud != "" && a.cloud != "AzureCloud" {
		add("cloud", a.cloud)
	}
	return out, len(out.Fields) > 0
}

// signInMethods says how the session was signed in to, from the amr
// claim: with MFA or not, and by what.
func signInMethods(amr any) string {
	list, _ := amr.([]any)
	var methods []string
	mfa := false
	for _, m := range list {
		s, _ := m.(string)
		switch s {
		case "mfa", "ngcmfa":
			mfa = true
		case "pwd":
			methods = append(methods, "password")
		case "rsa":
			methods = append(methods, "Windows Hello or certificate")
		case "fido":
			methods = append(methods, "security key")
		case "wia":
			methods = append(methods, "Windows sign-in")
		case "otp", "sms":
			methods = append(methods, "one-time code")
		}
	}
	if len(list) == 0 {
		return ""
	}
	how := strings.Join(methods, ", ")
	switch {
	case mfa && how != "":
		return "MFA · " + how
	case mfa:
		return "MFA"
	case how != "":
		return "no MFA · " + how
	}
	return "no MFA"
}

// builtinRoles names Entra ID's built-in directory roles by template ID,
// the IDs a token's wids claim lists.
var builtinRoles = map[string]string{
	"62e90394-69f5-4237-9190-012177145e10": "Global Administrator",
	"f2ef992c-3afb-46b9-b7cf-a126ee74c451": "Global Reader",
	"e8611ab8-c189-46e8-94e1-60213ab1f814": "Privileged Role Administrator",
	"7be44c8a-adaf-4e2a-84d6-ab2649e08a13": "Privileged Authentication Administrator",
	"194ae4cb-b126-40b2-bd5b-6091b380977d": "Security Administrator",
	"5d6b6bb7-de71-4623-b4af-96380a352509": "Security Reader",
	"5f2222b1-57c3-48ba-8ad5-d4759f1fde6f": "Security Operator",
	"9b895d92-2cd3-44c7-9d02-a6ac2d5ea5c3": "Application Administrator",
	"158c047a-c907-4556-b7ef-446551a6b5f7": "Cloud Application Administrator",
	"cf1c38e5-3621-4004-a7cb-879624dced7c": "Application Developer",
	"fe930be7-5e62-47db-91af-98c3a49a38b1": "User Administrator",
	"fdd7a751-b60b-444a-984c-02652fe8fa1c": "Groups Administrator",
	"729827e3-9c14-49f7-bb1b-9608f156bbb8": "Helpdesk Administrator",
	"966707d0-3269-4727-9be2-8c3a10f19b9d": "Password Administrator",
	"c4e39bd9-1100-46d3-8c65-fb160da0071f": "Authentication Administrator",
	"0526716b-113d-4c15-b2c8-68e3c22b9f80": "Authentication Policy Administrator",
	"8ac3fc64-6eca-42ea-9e69-59f4c7b60eb2": "Hybrid Identity Administrator",
	"b0f54661-2d74-4c50-afa3-1ec803f12efe": "Billing Administrator",
	"4d6ac14f-3453-41d0-bef9-a3e0c569773a": "License Administrator",
	"17315797-102d-40b4-93e0-432062caca18": "Compliance Administrator",
	"29232cdf-9323-42fd-ade2-1d097af3e4de": "Exchange Administrator",
	"f28a1f50-f6e7-4571-818b-6a12f2af6b6c": "SharePoint Administrator",
	"69091246-20e8-4a56-aa4d-066075b2a7a8": "Teams Administrator",
	"3a2c62db-5318-420d-8d74-23affee5d9d5": "Intune Administrator",
	"11648597-926c-4cf3-9c36-bcebb0ba8dcc": "Power Platform Administrator",
	"e3973bdf-4987-49ae-837a-ba8e231c7286": "Azure DevOps Administrator",
	"9360feb5-f418-4baa-8175-e2a00bac4301": "Directory Writers",
	"88d8e3e3-8f55-4a1e-953a-9b9898b8876b": "Directory Readers",
	"95e79109-95c0-4d8e-aee3-d01accf2d47b": "Guest Inviter",
}

// memberRole is in every member's token: not worth listing.
const memberRole = "b79fbf4d-3ef9-4689-8143-76b194e85509"

// directoryRoles names the roles in a wids claim: built-in ones by name,
// others by their ID's first part.
func directoryRoles(wids any) string {
	list, _ := wids.([]any)
	var names []string
	for _, w := range list {
		id, _ := w.(string)
		switch name, ok := builtinRoles[strings.ToLower(id)]; {
		case id == "" || id == memberRole:
		case ok:
			names = append(names, name)
		default:
			short, _, _ := strings.Cut(id, "-")
			names = append(names, "role "+short)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
