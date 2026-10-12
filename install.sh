#!/bin/sh
# Installs lazysecrets from its releases: the archive for this OS and CPU,
# checked against the release's checksums.txt, into ~/.local/bin.
#
#   curl -fsSL https://git.kebcloud.no/keb/lazysecrets/raw/branch/main/install.sh | sh
#   curl -fsSL https://git.kebcloud.no/keb/lazysecrets/raw/branch/main/install.sh | sh -s -- v0.2.0
#
# LAZYSECRETS_INSTALL_DIR installs somewhere else. macOS and Linux, arm64
# and x86_64.
#
# Releases are on GitHub and git.kebcloud.no, tried in that order;
# LAZYSECRETS_RELEASES (repository addresses, space-separated) uses others.
set -eu

repos="${LAZYSECRETS_RELEASES:-https://github.com/karleinarb/lazysecrets https://git.kebcloud.no/keb/lazysecrets}"
dir="${LAZYSECRETS_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "lazysecrets install: $*" >&2
	exit 1
}

# Short timeouts, so a server that's down leaves time for the next.
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --connect-timeout 5 --max-time 15 "$1"; }
	download() { curl -fsSL --connect-timeout 5 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO- -T 15 "$1"; }
	download() { wget -qO "$2" -T 30 "$1"; }
else
	fail "needs curl or wget"
fi

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "no release for $(uname -s); build it with: go install git.kebcloud.no/keb/lazysecrets/cmd/lazysecrets@latest" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no release for $(uname -m)" ;;
esac

# api is a repository's API: GitHub's, or Gitea's (and Forgejo's).
api() {
	case "$1" in
	https://github.com/*) echo "https://api.github.com/repos/${1#https://github.com/}" ;;
	*)
		host=${1#*://}
		host=${host%%/*}
		echo "${1%%://*}://$host/api/v1/repos/${1#*://"$host"/}"
		;;
	esac
}

tag="${1:-}"
if [ -z "$tag" ]; then
	for repo in $repos; do
		tag=$(fetch "$(api "$repo")/releases/latest" 2>/dev/null |
			sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1) || tag=
		[ -n "$tag" ] && break
	done
	[ -n "$tag" ] || fail "couldn't look up the latest release on $repos"
fi
case "$tag" in
v*) ;;
*) tag="v$tag" ;;
esac
version="${tag#v}"

name="lazysecrets_${version}_${os}_${arch}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The archive and its checksums.txt from the first server that has both.
from=
for repo in $repos; do
	base="$repo/releases/download/$tag"
	if download "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
		echo "Downloading lazysecrets $tag for $os/$arch from $repo"
		if download "$base/$name.tar.gz" "$tmp/$name.tar.gz"; then
			from=$repo
			break
		fi
	fi
done
[ -n "$from" ] || fail "couldn't download $tag for $os/$arch from $repos"

want=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$name.tar.gz isn't in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $name.tar.gz: the download is damaged or not the release"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$dir"
# Replaced, not overwritten: a running lazysecrets keeps its binary.
cp "$tmp/$name/lazysecrets" "$dir/.lazysecrets.new"
chmod 755 "$dir/.lazysecrets.new"
mv -f "$dir/.lazysecrets.new" "$dir/lazysecrets"
echo "Installed $("$dir/lazysecrets" --version) in $dir"

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "$dir isn't on your PATH: add it to your shell's profile, e.g. export PATH=\"$dir:\$PATH\"" ;;
esac
