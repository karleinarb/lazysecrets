# Config

Optional, at `~/.config/lazysecrets/config.yaml` (or
`$XDG_CONFIG_HOME/lazysecrets/config.yaml`, or `--config`). Flags override
it. Unknown keys are an error, so typos don't go unnoticed.

```yaml
icons: true            # Nerd Font icons (v3+); also --icons=false or LAZYSECRETS_ICONS=0
clipboardClear: 30s    # clear copied values from the clipboard after this long; 0s disables
revealTimeout: 60s     # hide a revealed value after this long; 0s keeps it shown
hideOnBlur: true       # hide a revealed value when the terminal loses focus (tmux: focus-events on)
expiryWarningDays: 30  # mark secrets, keys and certificates expiring this soon (⚠ 12d); expired ones get ✗
timeout: 30s           # per backend request
loadDelay: 150ms       # how long the cursor rests on a row before it loads; 0s loads at once
sourcesView: grouped   # or flat; ` toggles it
collapseGroups: true   # start with the Sources pane's groups folded; enter or space unfolds one
updateCheck: true      # look for a newer release once a day (U and lazysecrets update work either way)
releases:              # where updates come from, tried in order (GitHub, or a Gitea or Forgejo server)
  - https://github.com/karleinarb/lazysecrets
  - https://git.kebcloud.no/keb/lazysecrets
history: true          # count uses of items for Recent (R)
cache: true            # keep what was loaded (never values) for the next start
providers:
  azure:                                 # applies to every az account
    enabled: true                        # default
    hideSubscriptions: [platform-mgmt]   # by name or ID
    vaults:                              # listed under "Pinned vaults", for vaults
      - https://kv-foo.vault.azure.net/  # you can read but not discover
  databricks:
    enabled: true
    hideProfiles: [test]
  repos:
    enabled: true
    roots: [~/git]
    paths: [~/work/infra]
    include: ["secrets/**"]
    exclude: ["**/testdata/**"]
```

More, each on its own page: [repos](repos.md) (`providers.repos`), the
[link handler](links.md#from-the-browser-and-chat) (`linkHandler`),
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

Icons need a [Nerd Font](https://www.nerdfonts.com) (v3+). If they render
as boxes, turn them off with `icons: false`.

## Layering

Several config files can be layered, later ones winning (theme roles and
keybindings merge one by one), with a comma-separated `--config` or
`LAZYSECRETS_CONFIG_FILE`, like lazygit's `LG_CONFIG_FILE`. An editor
plugin can so add a generated theme file to your own config, the way
snacks.nvim themes lazygit from Neovim's highlight groups:

```sh
LAZYSECRETS_CONFIG_FILE=~/.config/lazysecrets/config.yaml,~/.cache/nvim/lazysecrets-theme.yml lazysecrets
```

## State

Stars, notes, usage and the layout are kept in
`~/.local/state/lazysecrets/state.json` (`$XDG_STATE_HOME`), readable only
by you: one record per source, vault or secret, keyed by its
`lazysecrets://` link, never with values. A star belongs to the account you
starred the item under (a vault two of your accounts can see can be starred
for one); a note describes the item, so it shows under every account. It's
JSON so an editor plugin can read it with a built-in parser; several
lazysecrets windows can save to it without losing each other's changes.

Revealing, copying, opening in the browser, sharing and jumping to an item
count as using it: its record keeps when it was last used and how often,
for Recent (`R`). Records kept only for usage are dropped after 90 days
unused, and beyond the 100 most recent. `history: false` stops counting.

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
and copying always fetch the value live.

Lists not loaded for 30 days are dropped, and so is everything from an
account you're no longer signed in to. Repos aren't cached: their files
are read from disk anyway. Deleting the file is always safe, and
`cache: false` stops using it.

## Updates

Once a day (`updateCheck`), in the background, lazysecrets asks for the
latest release; within the day it reuses the answer from the state file.
Releases are published on GitHub and on git.kebcloud.no, and it asks
them in that order, the next when one doesn't answer within 5 seconds;
`releases` lists others, such as a mirror of your own. Offline, or with
every server down, it says nothing. A newer release shows in the footer, `v0.1.1 ↑ v0.2.0`; `U` (or
`lazysecrets update --check`) shows its changelog.

Updating (`u` there, or `lazysecrets update`) downloads the release for
your OS and CPU from the first server that has it, checks it against
that server's `checksums.txt`, makes
sure it runs, and only then replaces the binary, atomically: a
lazysecrets that's running keeps working, and the next start is the new
version. A binary a package manager owns (Homebrew, Nix, a distro
package) is left to it. Development builds (`dev-…`) aren't offered
releases.
