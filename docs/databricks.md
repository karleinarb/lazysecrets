# Databricks

Workspaces come from the profiles in `~/.databrickscfg` (or
`$DATABRICKS_CONFIG_FILE`); account-console profiles are skipped. Each
workspace is a source, its secret scopes are the vaults.

Credentials are resolved the same way as the Databricks CLI, so
`auth_type = azure-cli` uses your az login and OAuth profiles use
`databricks auth login`. When a login expires, `L` runs the right one for
that profile.

Secrets in Azure Key Vault-backed scopes are read through Databricks; if
Databricks refuses, lazysecrets reads the backing vault directly with your
az login (which then needs the [Key Vault roles](azure.md#permissions)).

When Databricks refuses, the pane says what to check: access to the
workspace and READ on the scope, or, when the workspace's IP access list
refused, the network you're on.

Hide profiles you don't want in the [config](config.md):

```yaml
providers:
  databricks:
    hideProfiles: [test]
```
