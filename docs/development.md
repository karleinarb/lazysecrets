# Development

```sh
go run ./cmd/lazysecrets
go run ./cmd/lazysecrets --demo                     # built-in sample data, no Azure needed
go test ./...
go test ./internal/ui/app -run TestGolden -update   # after intentional UI changes
```

A stripped binary is about 21 MB instead of 30 MB:

```sh
go build -trimpath -ldflags "-s -w" -o lazysecrets ./cmd/lazysecrets
```

Development builds show `dev-<commit>` as their version (`-dirty` with
uncommitted changes); `go install …@v0.2.0` shows the tag, releases the
version they were built for.

## Pull requests

Pull requests into `main` are squash-merged, so each is one commit titled
like the PR. The title is a [conventional commit](https://www.conventionalcommits.org)
subject, checked by `pr-title.yml`:

| Type | For | In the changelog |
|---|---|---|
| `feat` | a new feature | Added |
| `perf` | faster or smaller, same behaviour | Changed |
| `fix` | a bug fix | Fixed |
| `refactor`, `docs`, `test`, `ci`, `chore`, `build`, `style`, `revert` | everything users don't need to hear about | no |

`feat!:` marks a breaking change, `feat(scope):` names a part. Brackets
and a missing space work too: `[feat] x`, `[feat]: x` and `feat:x` all
become `feat: x` in the changelog. `[skip changelog]` in the description
leaves a pull request out.

CI (`ci.yml`) runs gofmt, vet, staticcheck, the tests and a build for each
platform on pull requests into `main`. `main` is protected: a pull request
merges only when it's up to date with `main` (Gitea's "Update branch"
button, or a rebase, then CI again), so what lands on `main` is exactly
what its pull request tested, and CI doesn't run again there.

## Releasing

Merge as many pull requests as you like, then release `main`:

```sh
scripts/release.sh --dry-run v0.2.0   # the notes the release would have
scripts/release.sh v0.2.0             # CHANGELOG.md, commit, tag, push
```

It refuses unless you're on a clean `main` that matches `origin/main`.
[git-cliff](https://git-cliff.org) (`cliff.toml`) writes `CHANGELOG.md`
from the pull request titles. The tag runs the release workflow
(`.gitea/workflows/release.yml`): it tests, then
[GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`) builds the
archives and publishes a Gitea release with them and the release's
changelog section. Then `scripts/github-release.mjs` copies that release,
notes and files, to the GitHub mirror, with the `GH_RELEASE_TOKEN`
secret (a fine-grained token that can write the mirror's contents);
without the secret it skips that. `goreleaser release --snapshot
--clean` builds the same archives locally, into `dist/`, without
publishing.

A suffix (`v0.2.0-rc.1`) makes a pre-release, which leaves `CHANGELOG.md`
alone.
