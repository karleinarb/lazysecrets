package azure

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// A vault's settings come with the listing, from Resource Graph or the
// Key Vault management API, with no requests of their own: where it is,
// how access to it works, and how it's protected.

type vaultInfo struct {
	group, location, sku        string
	rbac, softDelete, purge     *bool
	retention                   *int32
	publicAccess, defaultAction string
	ipRules, subnets, endpoints int
	policies                    int
	tags                        map[string]string
}

// vaultQuery lists vaults with just the settings shown, not their whole
// properties (access policies can be long).
const vaultQuery = `resources
| where type =~ 'microsoft.keyvault/vaults'
| project id, name, uri = tostring(properties.vaultUri), location, resourceGroup, tags,
  sku = tostring(properties.sku.name), rbac = properties.enableRbacAuthorization,
  softDelete = properties.enableSoftDelete, retention = properties.softDeleteRetentionInDays,
  purge = properties.enablePurgeProtection, publicAccess = tostring(properties.publicNetworkAccess),
  defaultAction = tostring(properties.networkAcls.defaultAction),
  ipRules = array_length(properties.networkAcls.ipRules),
  subnets = array_length(properties.networkAcls.virtualNetworkRules),
  endpoints = array_length(properties.privateEndpointConnections),
  policies = array_length(properties.accessPolicies)`

// graphInfo reads a vaultQuery row's settings.
func graphInfo(m map[string]any) vaultInfo {
	str := func(k string) string { s, _ := m[k].(string); return s }
	num := func(k string) int { f, _ := m[k].(float64); return int(f) }
	flag := func(k string) *bool {
		if b, ok := m[k].(bool); ok {
			return &b
		}
		return nil
	}
	v := vaultInfo{
		group: str("resourceGroup"), location: str("location"), sku: str("sku"),
		rbac: flag("rbac"), softDelete: flag("softDelete"), purge: flag("purge"),
		publicAccess: str("publicAccess"), defaultAction: str("defaultAction"),
		ipRules: num("ipRules"), subnets: num("subnets"), endpoints: num("endpoints"), policies: num("policies"),
	}
	if f, ok := m["retention"].(float64); ok {
		r := int32(f)
		v.retention = &r
	}
	if tags, ok := m["tags"].(map[string]any); ok {
		v.tags = map[string]string{}
		for k, t := range tags {
			v.tags[k] = fmt.Sprint(t)
		}
	}
	return v
}

// armInfo reads a vault's settings from the management API.
func armInfo(v *armkeyvault.Vault) vaultInfo {
	info := vaultInfo{location: deref(v.Location), tags: map[string]string{}}
	if id, err := arm.ParseResourceID(deref(v.ID)); err == nil {
		info.group = id.ResourceGroupName
	}
	for k, t := range v.Tags {
		info.tags[k] = deref(t)
	}
	pr := v.Properties
	if pr == nil {
		return info
	}
	if pr.SKU != nil && pr.SKU.Name != nil {
		info.sku = string(*pr.SKU.Name)
	}
	info.rbac, info.softDelete, info.purge = pr.EnableRbacAuthorization, pr.EnableSoftDelete, pr.EnablePurgeProtection
	info.retention, info.publicAccess = pr.SoftDeleteRetentionInDays, deref(pr.PublicNetworkAccess)
	if acl := pr.NetworkACLs; acl != nil {
		if acl.DefaultAction != nil {
			info.defaultAction = string(*acl.DefaultAction)
		}
		info.ipRules, info.subnets = len(acl.IPRules), len(acl.VirtualNetworkRules)
	}
	info.endpoints, info.policies = len(pr.PrivateEndpointConnections), len(pr.AccessPolicies)
	return info
}

func (v vaultInfo) attrs() []provider.Attr {
	var out []provider.Attr
	add := func(label, value string) {
		if value != "" {
			out = append(out, provider.Attr{Label: label, Value: value})
		}
	}
	add("group", v.group)
	add("location", v.location)
	add("SKU", v.sku)
	// Who may read secrets is decided by Azure roles or by the vault's
	// own access policies, never both.
	if on(v.rbac, false) {
		add("access", "Azure roles (RBAC)")
	} else {
		add("access", fmt.Sprintf("access policies (%d)", v.policies))
	}
	if on(v.softDelete, true) {
		days := int32(90)
		if v.retention != nil {
			days = *v.retention
		}
		add("soft delete", fmt.Sprintf("on, %d days", days))
	} else {
		add("soft delete", "off")
	}
	add("purge prot.", map[bool]string{true: "on", false: "off"}[on(v.purge, false)])
	switch {
	case strings.EqualFold(v.publicAccess, "disabled"):
		add("network", "private endpoints only")
	case strings.EqualFold(v.defaultAction, "deny"):
		add("network", fmt.Sprintf("selected networks (%d IP ranges, %d subnets)", v.ipRules, v.subnets))
	default:
		add("network", "all networks")
	}
	if v.endpoints > 0 {
		add("private", fmt.Sprintf("%d endpoint%s", v.endpoints, map[bool]string{true: "s"}[v.endpoints > 1]))
	}
	for i, k := range slices.Sorted(maps.Keys(v.tags)) {
		label := ""
		if i == 0 {
			label = "tags"
		}
		out = append(out, provider.Attr{Label: label, Value: k + ": " + v.tags[k]})
	}
	return out
}

// on is *b, or def when it isn't set.
func on(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}
