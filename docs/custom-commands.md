# Custom commands

Run your own shell commands on the selected item, like lazygit's custom
commands: by their key in the pane they're for, or from the commands menu
(`x`, also listed under `?`).

```yaml
customCommands:
  - key: K
    context: secrets               # sources | vaults | secrets | global
    description: Create Kubernetes secret
    command: kubectl create secret generic {{.Name}} -n {{.Prompt.ns}} --from-file=value=/dev/stdin
    value: stdin                   # the value on the command's input; or env:NAME
    prompts:
      - {name: ns, title: Namespace, default: default}
    output: popup                  # quiet (default) | popup | terminal
  - key: E
    context: secrets
    description: Add to .env
    command: printf '%s=%s\n' {{.Name}} "$(cat)" >> .env
    value: stdin
  - context: vaults
    description: Show in az
    command: az keyvault show --name {{.Vault}}
    output: terminal
```

## Fields

`.Kind`, `.Account`, `.Source`, `.SourceID` (subscription ID or Databricks
profile), `.Vault`, `.VaultURL`, `.Name`, `.Type`, `.Version` (from the
versions view), `.Link`, `.Reference`, `.URL` (web page), `.Note`,
`.Expires`, and `.Prompt.<name>`. They're shell-quoted when filled in, so a
note can't break or inject into the command; `{{raw .Note}}` opts out.

## The value

The secret value is never a field, so it can't end up on a command line
(where `ps` and shell history see it). `value: stdin` hands it to the
command's input, `value: env:NAME` puts it in that environment variable for
the command alone; without `value` it isn't fetched. Commands that get the
value ask first, showing the command (`confirm: false` skips that).

## Running

Output: `quiet` runs in the background with ✓ or ✗ in the footer, `popup`
shows what it printed, `terminal` hands it the terminal and waits for
enter. Commands run with `$SHELL -c` in the directory lazysecrets started
in, and come only from your config: a link can never run one. Their keys
are checked against the [keybindings](keys.md#rebinding-keys) at startup.
