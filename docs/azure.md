# Azure Key Vault

lazysecrets reads Azure with your az CLI login: run `az login` once, and
it reuses it.

## Accounts

Every account signed in to az gets its own group in the Sources pane
(`az login` again with another account to add one), and each account's
vaults are read with that account. A subscription several accounts can see
is listed under each, since their permissions can differ.

A service principal is used instead when `AZURE_CLIENT_ID`,
`AZURE_TENANT_ID` and a secret or certificate are set in the environment.

On an account's group row, the fourth pane shows who it signs in as: name,
user, tenant, object ID, whether the session used MFA, directory roles
(built-in ones by name) and its subscriptions, read from az's account list
and the token lazysecrets already has. `y` on
the row copies the object ID, the one access policies and role
assignments ask for; `l` goes into the pane, where `j`/`k` pick any
field, `/` filters them (`/tenant`) and `y` copies the selected one.

If an account's az login has expired, the status line says so once, and the
Details pane on that account's rows says it until you sign in: press `L`
there to run `az login` for it without leaving lazysecrets, and everything
reloads.

## Permissions

Vaults are discovered with Resource Graph, so you need read access to the
vault resources (e.g. *Reader*). Key Vault data-plane access is separate
(Azure RBAC or access policies). With RBAC, typically:

| To | Role |
|---|---|
| list secrets, keys and certificates | *Key Vault Reader* |
| read secret values | *Key Vault Secrets User* |
| read certificate details | *Key Vault Certificate User* |

A missing permission is shown in the pane, shortened to what was denied
(the action, or the access policy permission), with what to check: a
role or group to activate in PIM, access from the vault's owner, or, when
its firewall refused, the network you're on.

## Vaults you can't discover

A vault you can read but not see through Resource Graph can be pinned in
the [config](config.md); it's listed under "Pinned vaults" and read with
az's default account:

```yaml
providers:
  azure:
    vaults:
      - https://kv-foo.vault.azure.net/
    hideSubscriptions: [platform-mgmt] # by name or ID
```

## What's shown

Secrets, keys and certificates, each on their own tab of the Secrets pane
(`[`/`]`). `space` reveals a secret's value, or the public material of a
key (PEM public key) or certificate; private key material is never
fetched. `v` lists an item's versions. Items expiring within
`ui.expiryWarningDays` (30 by default) are marked `⚠ 12d`, expired ones `✗`.

The status pane (`0`) lists them across every vault loaded, opened or
cached, soonest first, and its line counts them (`⚠ 3 expiring`); `enter`
on one goes to it. Expiry dates come with the lists. It says how many
vaults it covers; `ctrl+a` in search loads the rest.

On a vault, the right pane shows its settings: resource group, location,
SKU, whether access is by Azure roles or access policies, soft delete and
purge protection, which networks can reach it, private endpoints and tags.
They come with the list of vaults, so they cost no extra requests and are
cached with it.
