# SOPS, .env, Spring and compose files in git repos

lazysecrets shows git repositories under **Repos**: their
[SOPS](https://github.com/getsops/sops)-encrypted files (YAML, JSON,
`.env`, INI), plain env files, [Spring Boot config](#spring-boot-config)
and [Docker Compose files](#docker-compose) as vaults, and the keys in them (`db.password`, `list[0]`) as secrets. A dot or bracket inside a key is
escaped, `app\.name`, so it doesn't read as nesting.

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
git-ignored (as they usually are), in the folders git tracks files in.
`.env.example`, `.sample`, `.template` and the like are listed hidden
(`H` shows them): someone may have put a real value in one. They're read
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

`include`/`exclude` apply to env, Spring and compose files too.

```yaml
providers:
  repos:
    roots: [~/git]                             # folders of repos (replaces the defaults; [] turns it off)
    paths: [~/work/infra]                      # single repos elsewhere
    include: ["secrets/**", "**/*.sops.yaml"]  # globs from the repo root; ** for any depth
    exclude: ["**/testdata/**"]
```

A repo's `.lazysecrets.yaml` can also name the secret its env files are
[shared](sharing.md#sharing-env-files) with:

```yaml
sharing:
  key: https://kv-team.vault.azure.net/secrets/env-share   # or its lazysecrets:// link
```

## Spring Boot config

`application.yml`, `application-prod.properties`, `bootstrap.yaml` and the
like, tracked by git (not those in a Helm chart's `templates/`). Most of
their keys aren't secret, so only those that look like one are listed; the
rest are hidden:

- **keys** named like a secret: ending in `password`, `pwd`, `secret`,
  `token`, `key`, `credentials`, `salt`, `connection-string` and the
  like (`spring.datasource.password`, `client-secret`, `checkoutKey`),
- **values** that look like one: Jasypt's `ENC(...)`, a URL with a
  password (`jdbc:postgresql://app:pw@db/app`), or a connection string
  with `AccountKey=`, `SharedAccessKey=` or `Password=`.

A file with none is hidden too, and a repo with only such files. `H` shows
what's hidden, dimmed, in the panes and in Go to (`lazysecrets list
--hidden` lists it); a pane's counter says how many it hides. A file's
details count its hidden keys, and `e` opens it whole. A `${DB_PASSWORD}`
value is marked as a reference: the secret is set elsewhere. A tracked
file is marked in red only when a listed key has its value in plain text.

The patterns are regexps, for all of a repo's config files and per type of
file; each list replaces the one it falls back to, and `keys: [".*"]`
lists every key:

```yaml
providers:
  repos:
    secretPatterns:                             # all config files
      keys: ["(?i)(password|secret|token|key)$"]  # matched against the key's name
      values: ["^ENC\\(.+\\)$"]                   # and against its value
    spring:
      secretPatterns:                           # Spring's: only what differs
        keys: ["(?i)(password|secret)$", "bearer$"]
    compose:
      secretPatterns:                           # and compose's
        values: []
```

A repo's `.lazysecrets.yaml` can set them too, the same way
(`secretPatterns:`, `spring: secretPatterns:`, `compose:
secretPatterns:`), for everyone. Each list
comes from the first that sets it: the repo's for the type, the repo's,
the config's for the type, the config's, the defaults.

Keys are named as Spring names them, `spring.datasource.password`; in a
file with several documents (`---`, or `#---` in `.properties`), a later
document's keys get its profile, `spring.datasource.password (prod)`.
Values Maven fills in at build time (`@project.version@`) are read as
they are.

## Docker Compose

`compose.yaml`, `docker-compose.yml` and their variants
(`docker-compose.test.yml`), tracked by git; `docker-compose.example.yml`
and the like are hidden. Their secrets are the services' `environment`
variables, as a map (`DB_PASSWORD: x`) or a list (`- DB_PASSWORD=x`),
named `service.VAR` (`api.DB_PASSWORD`); a `<<: *anchor` shared between
services is read too. The same [secret patterns](#spring-boot-config)
pick them, the rest hidden, with `compose: secretPatterns:` for compose
files' own.

`${DB_PASSWORD}`, `$DB_PASSWORD` and `${DB_PASSWORD:?…}`, and a variable
without a value (`DB_PASSWORD:`, `- DB_PASSWORD`), taken from where
compose runs, are marked as references; `${DB_PASSWORD:-x}` has its
default in the file.

## Decrypting

Listing never decrypts: SOPS and Jasypt encrypt values only, so keys and
metadata (encryption, last modified) are read straight from the files.
Revealing, copying or `lazysecrets get` decrypts one value; SOPS's with
your `sops` binary, so whatever sops uses works (age keys, Azure Key Vault
through az, AWS/GCP KMS, PGP). When sops can't decrypt, its whole error
message is shown.

On a file, the right pane shows how it's encrypted and the keys it's
encrypted for (public: age recipients, Key Vault keys, KMS ARNs, PGP
fingerprints), when it was last encrypted, and what git knows of it:
tracked, ignored or neither, and its last commit. A plain `.env` file that
git tracks, or that isn't ignored, is marked in red: its values are in the
repo's history, or easily could be.

### Jasypt

A value like `ENC(...)` in a plain `.env`, Spring or compose file is
[Jasypt](http://www.jasypt.org)'s, decrypted when it's revealed or copied
with jasypt-spring-boot's default algorithm (3.0+:
`PBEWITHHMACSHA512ANDAES_256`). The password is, as in Spring:

1. `$JASYPT_ENCRYPTOR_PASSWORD`,
2. the file's own: `jasypt.encryptor.password` in a Spring file, the
   service's `JASYPT_ENCRYPTOR_PASSWORD` in a compose file,
   `JASYPT_ENCRYPTOR_PASSWORD` in a `.env`; a `${VAR}` in it (with a
   default: `${VAR:default}` in Spring, `${VAR:-default}` elsewhere) is
   read from the environment.

Environment variables are lazysecrets' own: a link opened from the browser
starts it without your shell's. A `.env` whose values are all Jasypt's
isn't marked in red.

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

Repo links name the repo by its git remote, so they open in a colleague's
clone wherever it is ([sharing](sharing.md)).
