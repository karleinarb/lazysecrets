# Config

Optional, at `~/.config/lazysecrets/config.yaml` (or
`$XDG_CONFIG_HOME/lazysecrets/config.yaml`, or `--config`). Flags (noted
below) override it. Unknown keys are an error, so typos don't go
unnoticed. `e` on the status pane (`0`) opens it in your editor, as in
lazygit (making it if it isn't there yet); changes apply when lazysecrets
starts again.

```yaml
ui:
  icons: true            # Nerd Font icons (v3+); also --icons=false or LAZYSECRETS_ICONS=0
  recentIcon: false      # mark items used lately (Recent, R) with ◷, here and in lazysecrets list
  sourcesView: grouped   # or flat; ` toggles it
  collapseGroups: true   # start with the Sources pane's groups folded; enter or space unfolds one
  loadDelay: 150ms       # how long the cursor rests on a row before it loads; 0s loads at once
  expiryWarningDays: 30  # mark secrets, keys and certificates expiring this soon (⚠ 12d; ⚠ in lazysecrets list); expired ones get ✗
values:
  revealTimeout: 60s     # hide a revealed value after this long; 0s keeps it shown
  hideOnBlur: true       # hide a revealed value when the terminal loses focus (tmux: focus-events on)
  clipboardClear: 30s    # clear copied values from the clipboard after this long; 0s disables; also --clipboard-clear
storage:
  cache: true            # keep what was loaded (never values) for the next start
  history: true          # count uses of items for Recent (R)
updates:
  autoCheck: true          # look for a newer release at startup (U and lazysecrets update work either way)
  autoCheckInterval: 24h   # at most this often
  sources:                 # where updates come from, tried in order (GitHub, or a Gitea or Forgejo server)
    - https://github.com/karleinarb/lazysecrets
    - https://git.kebcloud.no/keb/lazysecrets
providers:
  timeout: 30s           # per backend request; a provider's own timeout overrides it; also --timeout
  azure:                 # likewise databricks and repos; azure covers every az account
    enabled: true        # default
    timeout: 30s
```

More, each on its own page: [Azure](azure.md#vaults-you-cant-discover)
(`providers.azure`), [Databricks](databricks.md) (`providers.databricks`),
[repos](repos.md#which-files) (`providers.repos`),
[sharing env files](sharing.md#sharing-env-files) (`sharing`), the
[link handler](sharing.md#from-the-browser-and-chat) (`linkHandler`),
[keybindings](keys.md#rebinding-keys) (`keybinding`) and
[custom commands](custom-commands.md) (`customCommands`).

## Clipboard

The clipboard is the system clipboard locally and OSC52 over SSH (your
terminal must allow OSC52 writes; in tmux, `set -g set-clipboard on`).

## Theme

Colours follow your terminal's 16-colour palette by default. A `theme:`
section overrides roles with hex colours, palette numbers (0-255) or names,
plus attributes (`bold`, `italic`, `underline`, `reverse`, `faint`). Role
names follow lazygit's `gui.theme` where they mean the same thing:

```yaml
theme:
  activeBorderColor: ["#ff966c", bold]   # focused pane
  inactiveBorderColor: ["#589ed7"]
  selectedLineBgColor: ["#2d3f76"]       # cursor row background (default: palette 8)
  inactiveViewSelectedLineBgColor: ["#2f334d"] # in the other panes (default: palette 8, not bold)
  defaultFgColor: ["#c8d3f5"]
  optionsTextColor: ["#82aaff"]          # keys and labels
  searchMatchColor: ["#ffc777", bold]
  errorColor: ["#c53b53"]                # ✗ failures
  noticeColor: ["#c3e88d"]
  successColor: ["#c3e88d", bold]        # the ✓ before confirmations
  warningColor: ["#ffc777"]              # ⚠ what can't be done here
  mutedColor: ["#636da6"]
  favouriteColor: ["#ffc777"]
  valueColor: ["#ffc777"]
  azureColor: ["#82aaff"]
  databricksColor: ["#ff757f"]
  repoColor: ["#c099ff"]
```

A role lazysecrets doesn't know, a typo or one from a newer version, is
skipped with a warning.

Icons need a [Nerd Font](https://www.nerdfonts.com) (v3+). If they render
as boxes, turn them off with `ui.icons: false`.

## Layering

Several config files can be layered, later ones winning (theme roles and
keybindings merge one by one), with a comma-separated `--config` or
`LAZYSECRETS_CONFIG_FILE`, like lazygit's `LG_CONFIG_FILE`; e.g. your
config plus an editor-generated theme:

```sh
LAZYSECRETS_CONFIG_FILE=~/.config/lazysecrets/config.yaml,~/.cache/nvim/lazysecrets-theme.yml lazysecrets
```

## State

Stars, notes, usage and the layout are kept in
`~/.local/state/lazysecrets/state.json` (`$XDG_STATE_HOME`), readable only
by you: one record per source, vault or secret, keyed by its
`lazysecrets://` link, never with values. A star belongs to the account you
starred the item under (a vault two of your accounts can see can be starred
for one); a note describes the item, so it shows under every account.
Several lazysecrets windows can save to it without losing each other's
changes. One written by a newer lazysecrets is read but not saved to,
with a warning, until you update.

Revealing, copying, opening in the browser, sharing and jumping to an item
count as using it: its record keeps when it was last used and how often,
for Recent (`R`). Records kept only for usage are dropped after 90 days
unused, and beyond the 100 most recent. `storage.history: false` stops counting.

It also keeps where you left lazysecrets in each folder (the selected
source, vault or secret), and goes back there the next time it starts in
that folder, unless it was started on a link.

## Cache

What lazysecrets has loaded is kept in
`~/.cache/lazysecrets/cache.json` (`$XDG_CACHE_HOME`), readable only by
you: the Sources lists, vaults and scopes, and secrets' names and metadata
(tags, dates, references), never values. On the next start they show at
once, and anything older than two minutes reloads in the background when
you go to it, or when Go to searches it. While it does, the pane's
bottom border says how old what it shows is (`cached 3h ago`). Revealing
and copying always fetch the value live. [`lazysecrets list`](integration.md#listing-for-fzf-and-pickers)
reads it and adds to it too.

Lists not loaded for 30 days are dropped, and so is everything from an
account you're no longer signed in to. Repos aren't cached: their files
are read from disk anyway. Deleting the file is always safe, and
`storage.cache: false` stops using it.

## Updates

At startup, in the background, lazysecrets asks for the latest release
(`updates.autoCheck`), once a day or every `updates.autoCheckInterval`;
until then it reuses the answer from the state file. Releases are
published on GitHub and on git.kebcloud.no, and it asks them in that
order, the next when one doesn't answer within 5 seconds;
`updates.sources` lists others, such as a mirror of your own. Offline, or with
every server down, it says nothing. A newer release shows in the status
pane, `↑ v0.2.0`; `U` (or `lazysecrets update --check`) shows what
changed since your version, across the last 10 releases.

Updating (`u` there, or `lazysecrets update`) downloads the release for
your OS and CPU from the first server that has it, checks it against
that server's `checksums.txt`, makes
sure it runs, and only then replaces the binary, atomically: a
lazysecrets that's running keeps working, and the next start is the new
version. A binary a package manager owns (Homebrew, Nix, a distro
package) is left to it. Development builds (`dev-…`) aren't offered
releases.
