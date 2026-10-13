# Links

Subscriptions, workspaces, vaults, scopes, repos, files and secrets can
be shared as a `lazysecrets://` link, in chat or a ticket. Whoever opens
it lands on that item, reading it with their own account and
permissions: the link names the item, never its value. Env files can
also be [shared encrypted](#sharing-env-files), in the link itself.

```
lazysecrets://azure/kv-app-prod.vault.azure.net                              vault
lazysecrets://azure/kv-app-prod.vault.azure.net/keys                         vault, Keys tab
lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password          secret
lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password/91d3e0   one version
lazysecrets://databricks/adb-1111111111111111.11.azuredatabricks.net/app/storage-key
lazysecrets://repo/git.example.com%2Fteam%2Finfra/secrets%2Fapp.yaml/db.password
```

## Sharing

`s` copies a link to the selected item (from the versions view, pinned to
that version). `S` opens the share menu:

- secrets: the link (`o`), the plain Key Vault URL (`u`), the config
  reference (`y`) or the web page (`b`); in an env file, also the file as
  an [encrypted link](#sharing-env-files) (`f`)
- vaults and scopes: a link (`o`), a link that opens on the current tab
  (`t`), the plain vault URL (`u`) or the web page (`b`)
- subscriptions and workspaces: a link (`o`) or the web page (`b`)
- repos and env files: also their env files as an
  [encrypted link](#sharing-env-files) (`f`)

## Sharing env files

`.env` files usually live only on your machine. `S` → `f` shares them as
an encrypted link, on a repo (all its plain env files), an env file, or a
key in one (its file):

```
lazysecrets://share/AQ…
```

The files are encrypted with a secret's value, so whoever can read that
secret (in Key Vault, Databricks or a repo's SOPS file) can open the link,
and nobody else: who may open it is up to the vault's access, as for
everything else. Make a secret for it once, random:

```sh
az keyvault secret set --vault-name kv-team --name env-share --value "$(openssl rand -base64 32)"
```

and name it in the repo's `.lazysecrets.yaml` (committed: the whole team
uses it) or your config (for repos that don't name one), by its
`lazysecrets://` link, Key Vault URL or `@Microsoft.KeyVault(...)`
reference:

```yaml
share:
  key: https://kv-team.vault.azure.net/secrets/env-share
```

With neither, sharing asks which secret, in [Go to](keys.md#go-to): it
lists only secrets, and `enter` picks one (pasting a reference picks it
too).

Anyone with a link can try to guess the key, so sharing refuses one that
isn't long and varied enough, saying how (never showing it). By default
that's 32 characters of two kinds; your config can ask for more, or less:

```yaml
share:
  keyRules:
    minLength: 32    # characters; 16 at least
    minClasses: 2    # kinds of character: lowercase, uppercase, digits, symbols
```

Opening a link doesn't check them.

On the command line, `lazysecrets share [--key <secret>] <env file>...`
prints the link.

Opening a share link says which repo it's from, how many files and which
secret it's encrypted with, and decrypts only when asked: lazysecrets
reads the secret with your access, then shows where each file goes in
your clone of the repo (or asks for a folder, if it isn't here), which
ones would replace a file there and which ones git doesn't ignore. It
saves them all, only the new ones, or none. New files are readable by you
only.

A link holds the files' paths and contents encrypted ([age](https://age-encryption.org),
the secret's value as passphrase); the repo, the secret and the number of
files are in the clear. Links stay under 16 KB, which fits chat and
tickets.

A link stays readable as long as its secret is. On Azure it's pinned to
the secret's version, so adding a new version (rotating) doesn't break
old links, and disabling the old version, or taking away someone's
access, shuts them out; Databricks secrets have no versions, so changing
the value breaks every old link. Whoever has read the secret's value
before can decrypt links made with it without asking the vault, so rotate
it now and then: new links use the newest version.

## Opening

- Paste it into lazysecrets, or press `ctrl+o`, which reads the clipboard
  and fills in any reference it finds there. Key Vault URLs,
  `@Microsoft.KeyVault(...)` and `{{secrets/scope/key}}` work too.
- `lazysecrets <link>` (or `lazysecrets open <link>`) in a terminal.
- Click it anywhere, once the [link handler](#from-the-browser-and-chat) is
  set up.

If you copy a `lazysecrets://` link elsewhere and switch back, lazysecrets
offers to open it, once per link. It looks at the clipboard at startup,
when the terminal regains focus (in tmux: `set -g focus-events on`) and,
for terminals that don't report focus, on a key press at most every 3
seconds. It keeps nothing but `lazysecrets://` links, doesn't offer a link
to what's already selected (like one you just copied with `s`), and can't
read the clipboard over SSH.

lazysecrets finds the vault in your accounts (one Resource Graph query per
account) and jumps to it, asking which account to use when several can see
it, ending on a secret's details as `enter` on it would. Links say what,
not what to do: opening a link to a secret asks whether to just go to it,
also copy or show its value, or (`y`) copy its value without going there.

## In scripts

`lazysecrets get <link>` prints the value, for scripts and pipes:

```sh
export DB_PASSWORD=$(lazysecrets get lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password)
lazysecrets get lazysecrets://azure/kv-app-prod.vault.azure.net/secrets/db-password | kubectl create secret generic db --from-file=password=/dev/stdin
```

It reads the value directly, with your az accounts (the one that last read
it first), the Databricks profile for the workspace, or sops in your clone
of the repo; a version in the link reads that version. The value ends with
a newline only on a terminal.

## From the browser and chat

```sh
lazysecrets handler install     # run it in the terminal links should open in
lazysecrets handler uninstall
```

Use an installed binary (`go run`'s is temporary). lazysecrets offers to do
this at startup when links don't open it (or open a lazysecrets that no
longer exists): set it up, not now, or don't ask again, which is
remembered in the state file. `linkHandler.prompt: false` in the config
turns the question off too.

`handler install` registers `lazysecrets://` with the system: clicking a
link then opens a new window of the terminal you ran it in, with
lazysecrets on that link. It recognises kitty, Ghostty, WezTerm, iTerm2,
Terminal.app, Alacritty, GNOME Terminal, Konsole and foot, also inside
tmux; `--terminal NAME` picks another. It records your `PATH` and config
locations, since apps start without your shell's environment (and so
without `az`): run it again after moving lazysecrets or changing those.

- macOS: a small background app, `~/Applications/Lazysecrets Links.app`
- Linux: a desktop entry claiming `x-scheme-handler/lazysecrets`, set as
  the default with `xdg-mime` (desktops following freedesktop.org)
- Windows: not yet

The config can override the terminal, or run any other:

```yaml
linkHandler:
  terminal: ghostty                          # instead of the one install detected
  command: [foot, --title, secrets, "{cmd}"] # any terminal; {cmd} is the lazysecrets command
  reuse: true                                # hand links to a running lazysecrets first
  prompt: true                               # offer to set up the handler at startup
```

### Where a clicked link opens

- **In a terminal** (a link in a terminal app): a lazysecrets that
  terminal shows (or whose tmux session one of its tmux clients can switch
  to) gets it, never one elsewhere; otherwise a new window opens.
- **Anywhere else** (Slack, a browser): the lazysecrets you used last (key
  press or focus), preferring one already on screen; a tmux client is
  switched to its session if needed, its terminal comes to the front, and
  a hidden dropdown terminal (kitty's quick access terminal) is shown.
  With none running, a new window opens.

The link is then offered as if pasted. `linkHandler.reuse: false` always
opens a new window.

Running instances listen on a socket in `$XDG_RUNTIME_DIR/lazysecrets`, or
`run/` in the state directory, readable only by you. A link from a web page
can only make lazysecrets go somewhere: it asks before copying or showing
anything.
