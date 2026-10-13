# SOPS and .env files in git repos

lazysecrets shows git repositories under **Repos**: their
[SOPS](https://github.com/getsops/sops)-encrypted files (YAML, JSON,
`.env`, INI) and plain env files as vaults, and the keys in them
(`db.password`, `list[0]`) as secrets.

## Which repos

**Repos** lists every repo with secret files under the `roots` (by
default whichever of `~/git`, `~/repo`, `~/repos`, `~/projects`, `~/code`,
`~/source` and `~/src` exist; one or two levels down, `repo` or
`org/repo`), any listed in `paths`, and any you starred something in
(the repo, a file or a key), wherever it is. They're checked in the background.

Started inside a repo, that repo gets a row of its own at the top,
`Repo · <name>`, and lazysecrets opens on it, with the files under the
directory you started in first. In the flat list (`` ` ``) it sorts first, marked
`· here`.

A repo without secret files isn't listed. `--dir <path>` starts somewhere
else, for example in one project of a monorepo.

Repos load themselves as they're found, files and keys, since they're
local and cheap: [Go to](keys.md#go-to) searches them without loading
anything by hand.

## Which files

**Env files**: `.env`, `.env.local`, `prod.env` and the like, also when
git-ignored (as they usually are), in the folders git tracks files in;
not `.env.example`, `.sample`, `.template` and the like. They're read
directly; a file with SOPS metadata is always treated as SOPS, whatever
its name.

Folders of dependencies and caches are never looked in, tracked or not:
`node_modules`, `vendor`, `.venv`, `venv`, `__pycache__`, `.terraform`,
`.gradle`, `.yarn`, `.next` and the like.

**SOPS files**: those tracked by git, then the first of

1. `include`/`exclude` in the repo's own `.lazysecrets.yaml` (committed,
   so everyone sees the same files),
2. `include`/`exclude` in the config,
3. where the repo's `.sops.yaml` `creation_rules` say SOPS files go,
4. the whole repo.

`include`/`exclude` apply to env files too.

```yaml
providers:
  repos:
    roots: [~/git]                             # folders of repos (replaces the defaults; [] turns it off)
    paths: [~/work/infra]                      # single repos elsewhere
    include: ["secrets/**", "**/*.sops.yaml"]  # globs from the repo root; ** for any depth
    exclude: ["**/testdata/**"]
```

A repo's `.lazysecrets.yaml` can also name the secret its env files are
[shared](links.md#sharing-env-files) with:

```yaml
share:
  key: https://kv-team.vault.azure.net/secrets/env-share   # or its lazysecrets:// link
```

## Decrypting

Listing never decrypts: SOPS encrypts values only, so keys and metadata
(encryption, last modified) are read straight from the files. Revealing,
copying or `lazysecrets get` decrypts one value with your `sops` binary, so
whatever sops uses works (age keys, Azure Key Vault through az, AWS/GCP
KMS, PGP). When sops can't decrypt, its whole error message is shown.

On a file, the right pane shows how it's encrypted and the keys it's
encrypted for (public: age recipients, Key Vault keys, KMS ARNs, PGP
fingerprints), when it was last encrypted, and what git knows of it:
tracked, ignored or neither, and its last commit. A plain `.env` file that
git tracks, or that isn't ignored, is marked in red: its values are in the
repo's history, or easily could be.

## Opening files

`e` opens the selected file, or on a secret, its file at that key's line:

- **SOPS files** open with `sops edit`, which decrypts them into your
  editor (`$SOPS_EDITOR` or `$EDITOR`) and encrypts them again when you
  save. lazysecrets comes back after, with the file's keys reloaded.
- **Other files**, when lazysecrets runs inside nvim (its plugin, or
  `:terminal`): lazysecrets quits and the file opens in that nvim, in the
  window you came from.
- Otherwise in `$VISUAL` or `$EDITOR` in the terminal, coming back after;
  without either, in the system's default app for the file.

Editors that take a line (vim, nvim, nano, emacs, micro, helix, VS Code,
Zed, Sublime and others) start on the key. With `sops edit` that works for
editors taking `+N` (vim, nvim, nano, emacs, micro and others), and needs
sops 3.8 or later.

## Links

`S` → `f` shares env files as an [encrypted link](links.md#sharing-env-files).

Links name the repo by its git remote, so they open in a colleague's clone
wherever it is:

```
lazysecrets://repo/git.example.com%2Fteam%2Finfra/secrets%2Fapp.yaml/db.password
```

See [links](links.md).
