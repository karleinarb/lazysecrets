# Keys

`?` lists every key with what it does, the focused pane's first; type to
filter, `enter` runs one. The help bar at the bottom shows the most useful
keys for where you are. Every key can be [rebound](#rebinding-keys).

The rule: a letter in the lists, ctrl+ that letter where you type a filter
(`f` stars, `ctrl+f` stars while filtering).

## Moving around

| Key | Action |
|---|---|
| `j`/`k`, `↑`/`↓` | move |
| `g`/`G` | top / bottom |
| `tab`, `shift+tab`/`h` | next / previous pane |
| `l` | the details of what's selected, on the right (`h` goes back) |
| `1`–`4` | jump to pane |
| `enter` / `esc`, `⌫` | drill in / back (also clears a filter or leaves the versions view) |
| `[` / `]` | previous / next tab of the focused pane: Sources and Favourites in Sources; Secrets, Keys, Certs in Secrets |
| `/` | filter the focused list by name or note ([how it matches](#searching)); `enter` keeps it, `esc` clears it. While typing: `ctrl+j`/`ctrl+k` move, `ctrl+y` copies, `ctrl+r` reveals, `ctrl+f` stars |
| `` ` `` | switch the Sources pane between grouped by provider and a flat list; `enter`/`space` on a provider folds it |
| `H` | show / hide what's hidden: [Spring keys](repos.md#spring-boot-config) that don't look secret, and files and repos with only those; shown dimmed |
| `r` | refresh the focused pane |
| `q`, `ctrl+c` | quit |

## Searching

`/` in a pane, Go to, `?` and other popups with a text field all search
the same way, with [fzf](https://github.com/junegunn/fzf)'s scoring (the
same as snacks.nvim's picker): letters at the start of a word or in one
run count far more than letters scattered through a name, matches in an
item's own name more than in its path or note, and equally good matches
put the shorter name first.

| Query | Finds |
|---|---|
| `dbpw` | fuzzy: `db-password` |
| `pass prod` | both terms, in any order |
| `smtp \| key` | either |
| `'prod` | exactly `prod`, not fuzzy |
| `^kv-` / `-dev$` | names starting / ending with it |
| `!test` | without `test` |

Lowercase ignores case; typing an uppercase letter makes that term
match case exactly.

## Secrets

| Key | Action |
|---|---|
| `space` | reveal / hide the selected value (public material for keys and certificates) |
| `y` | copy the value without revealing it. On an Azure account's row: its object ID; in its pane, the selected field |
| `Y` | copy the reference (`@Microsoft.KeyVault(...)`, key/certificate URL, `{{secrets/scope/key}}`) |
| `v` | versions of the selected item (Azure); `space`/`y`/`Y` then act on that version; `esc`, `⌫` or `v` to go back |
| `o` | open the selected item in the browser (over SSH: copy the link) |
| `e` | open the selected SOPS, `.env` or Spring file, or a secret's file at its line ([how](repos.md#opening-files)) |
| `s` / `S` | copy a [link](sharing.md) to the selected item / share menu (also env files, [encrypted](sharing.md#sharing-env-files)) |
| `ctrl+o` | open a link or reference (or just paste one) |
| `x` | [custom commands](custom-commands.md) |
| `L` | sign in again (shown when a login has expired) |
| `U` | a newer lazysecrets: its changelog, and update in place (restart to use it); or look for one |

A revealed value is shown as it reads best: JSON indented, and PEM
certificates as their subject, issuer, dates, names and thumbprint, with
any other PEM block (a private key) named but not shown. `y` still copies
it exactly as stored. It hides again after a while, and when the terminal
loses focus ([config](config.md)).

## Stars and notes

| Key | Action |
|---|---|
| `f` | star the selected source, vault or secret: it gets a ★ and sorts first in its list |
| `n` | write a short note on it, shown dimmed on its row and in the detail pane |

Both are kept in the [state file](config.md#state).

## Go to

`ctrl+s` opens Go to on its Search tab, `F` on Favourites (everything you
starred) and `R` on Recent (what you used last). `enter` goes to the
selected item.

Search has the scope on the left and what's in it on the right. The
scopes are All, then grouped like the Sources pane: each account (or
Databricks, or Repos) with its sources under it, with `○ ◐ ●` for how
much of each is loaded. Repos searches every repo without asking Azure
or Databricks for anything. Go to opens on All; `ctrl+s` again narrows
it to the source or group selected in the Sources pane, and once more
goes back to All. Typing a search loads what it covers: everything on
All, else the account or source picked; results appear as lists
arrive. That's once each time Go to opens, and only what isn't loaded
yet; `ctrl+a` loads again, lists that failed included.

Like a pane, Go to is either browsing or typing a filter:

| Browsing | Typing | Action |
|---|---|---|
| `j`/`k` | `ctrl+j`/`ctrl+k` | move |
| `g`/`G` | | top / bottom |
| `ctrl+s` | `ctrl+s` | only the source or group you're in / All |
| `h`/`l`, `tab` | `ctrl+h`/`ctrl+l`, `tab` | scope list / results |
| `[` / `]` | | previous / next tab |
| `f` | `ctrl+f` | star |
| `r` | `ctrl+r` | load the selected item, or the scope when nothing is selected |
| `a` | `ctrl+a` | load everything in the scope (again) |
| `/` | `esc` | start typing / clear it and go back to browsing |
| `esc`, `q` | | close |

Favourites and Recent come from the state file, so they work before
anything loads.

## Layout

The pane on the right describes what's selected in the pane you're in: the
account or subscription in Sources, the vault in Vaults, the secret in
Secrets. Going into it (`l`, or `4`) keeps it on that; there `j`/`k`,
`/` and `y` work on its rows, and `h` goes back. A value you reveal stays in view whichever pane you're in, until
it's hidden.

On top, the status pane (`0`) shows the version, a newer release and how
your accounts are doing; `e` there opens your [config](config.md) in your
editor, as in lazygit. Focused, the right pane lists more under the
logo; `y` copies any row:

- the version and updates (`U`)
- each account (`L` on one that needs it signs in)
- what's expiring (`enter` goes to it)
- this session's errors, newest first (not kept after quitting)
- the config, state and cache files (`e` opens one in your editor)
- links to the docs, source, issues, releases and changelog (`o` opens one)
- the changelog of every release up to yours, built in: `/` filters it
  (`/fixed`, `/search`)

| Key | Action |
|---|---|
| `ctrl+←`/`ctrl+→` or `<`/`>` | move the divider between the left panes and the detail pane |
| `ctrl+↓`/`ctrl+↑` or `}`/`{` | make the focused left pane taller / shorter |
| `+` / `_` | zoom the focused pane: normal → half → full screen, and back |
| `=` | reset the layout (divider, heights, zoom) |

The divider and pane heights are remembered (zoom isn't). `<>{}` are for
macOS, where ctrl+arrows switch desktops.

## Rebinding keys

Every action can be rebound in the [config](config.md), per context:

```yaml
keybinding:
  lists:                 # the panes, when not typing
    star: [x]            # replaces f
    versions: []         # unbound
  filter:                # typing a filter in a pane
    star: [ctrl+x]
  popup:                 # popups with a text field: help, notes, open link, Go to while typing
    down: [ctrl+j, down]
  menu:                  # popups without a text field
  search:                # Go to's own actions while typing (on top of popup)
  goto:                  # Go to while browsing
```

An action you set gets exactly the keys you list, replacing its defaults;
`[]` unbinds it. Actions you don't set keep their defaults, also across
layered config files. `lazysecrets keybindings` prints every action with
its keys (your config applied), ready to copy and edit. A key bound to two
actions that apply together is an error at startup, naming both. The help
bar, `?` and every hint show the keys you bound. Fixed: the letters inside
menus (they're part of each entry), and `ctrl+c`, which always quits.

Without the kitty keyboard protocol (or tmux `extended-keys`), some
combinations never reach a terminal app, such as `ctrl+shift+x`, `ctrl+1`
or `ctrl+enter`.
