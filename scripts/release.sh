#!/usr/bin/env bash
# Releases main: writes CHANGELOG.md with git-cliff (cliff.toml), commits
# it, tags the commit and pushes both; the tag runs
# .gitea/workflows/release.yml, which builds and publishes the release.
#
#   scripts/release.sh minor           # or patch, major: after the latest release
#   scripts/release.sh v0.2.0          # or v0.2.0-rc.1 for a pre-release
#   scripts/release.sh --dry-run minor # print the tag and notes, change nothing
#
# A pre-release is tagged without touching CHANGELOG.md: its changes are
# listed under the release that follows.
set -euo pipefail

dry=false
if [ "${1:-}" = "--dry-run" ]; then
  dry=true
  shift
fi
tag="${1:?usage: $0 [--dry-run] <patch|minor|major, or a tag like v0.2.0>}"
command -v git-cliff >/dev/null || { echo "git-cliff is needed: brew install git-cliff" >&2; exit 1; }

cd "$(dirname "$0")/.."
git fetch -q origin main --tags

bumped=""
case "$tag" in
  patch | minor | major)
    # The latest release, pre-releases left out: v0.3.0-rc.1 isn't one.
    tags=$(git tag -l 'v*' --sort=-v:refname)
    last=$(grep -m1 -E '^v[0-9]+\.[0-9]+\.[0-9]+$' <<<"$tags" || true)
    [ -n "$last" ] || { echo "No vMAJOR.MINOR.PATCH tag to bump from; give the tag." >&2; exit 1; }
    IFS=. read -r major minor patch <<<"${last#v}"
    case "$tag" in
      major) next="v$((major + 1)).0.0" ;;
      minor) next="v$major.$((minor + 1)).0" ;;
      patch) next="v$major.$minor.$((patch + 1))" ;;
    esac
    bumped=" ($tag after $last)"
    tag=$next
    ;;
esac
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "$tag is not patch, minor, major or vMAJOR.MINOR.PATCH (optionally -suffix)" >&2
  exit 1
fi
echo "$tag$bumped"

notes=$(git cliff --unreleased --tag "$tag" --strip all)
if ! grep -q '^- ' <<<"$notes"; then
  echo "Nothing for the changelog since the last release (no feat/fix/perf pull requests)." >&2
  $dry || exit 1
fi
echo "$notes"
$dry && exit 0

if [ "$(git branch --show-current)" != main ]; then
  echo "Release from main." >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  echo "The working tree has changes; commit or stash them first." >&2
  exit 1
fi
if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
  echo "main isn't origin/main; pull (or push) first." >&2
  exit 1
fi
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  echo "$tag exists already." >&2
  exit 1
fi

echo
read -r -p "Release $tag$bumped with these notes? [y/N] " ok
[ "$ok" = y ] || [ "$ok" = Y ] || exit 1

if [[ "$tag" != *-* ]]; then
  git cliff --tag "$tag" -o CHANGELOG.md
  git add CHANGELOG.md
  git commit -q -m "chore(release): $tag"
fi
git tag -a "$tag" -m "$tag"
git push -q origin main "$tag"
echo "Pushed $tag: the release workflow builds and publishes it."
