#!/usr/bin/env bash
#
# Pack distributable release tarballs from a built dist/ tree.
#
# Usage: scripts/make-release-tarballs.sh --version VERSION [--target OS-ARCH] [--out DIR]
#
# Produces, for the selected target (default: host):
#
#   kvlite-VERSION-TARGET.tar.gz        full tree: drivers/* + modules/*
#   kvlite-VERSION-TARGET.tar.gz.sha256 detached checksum of the tarball
#
# The tarball root is the target directory itself, so unpacking reproduces
# the dist layout the installer and scripts/install.sh expect. The installer
# script itself rides along so a release tarball is self-sufficient: unpack
# anywhere and run scripts/install.sh --from <dir>. Publish both files as
# release assets; the online installer fetches them by these exact names.
# Per-bundle SHA256SUMS files inside the tree still guard every file after
# unpacking.

set -euo pipefail

fail() {
  printf 'make-release-tarballs: %s\n' "$*" >&2
  exit 2
}

version=""
target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
out="dist"

while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || fail "--version requires a value"
      version="$2"
      shift 2
      ;;
    --target)
      (($# >= 2)) || fail "--target requires a value"
      target="$2"
      shift 2
      ;;
    --out)
      (($# >= 2)) || fail "--out requires a value"
      out="$2"
      shift 2
      ;;
    --help|-h)
      sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[[ -n "$version" ]] || fail "--version is required"
[[ "$version" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]] || fail "version may contain only letters, numbers, ., _, +, and -"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
tree="$repo_root/dist/$version/$target"
[[ -d "$tree" ]] || fail "no release tree at $tree; build it first"
if [[ -z "$(ls -A "$tree")" ]]; then
  fail "release tree $tree is empty"
fi

mkdir -p "$out"
base="kvlite-$version-$target"
tarball="$out/$base.tar.gz"

tar -czf "$tarball" -C "$repo_root/dist/$version" "$target" \
  -C "$repo_root" scripts/install.sh
(
  cd "$out"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$base.tar.gz" > "$base.tar.gz.sha256"
  else
    shasum -a 256 "$base.tar.gz" > "$base.tar.gz.sha256"
  fi
)
printf 'Packed %s (unpacks to %s/; install with scripts/install.sh)\n' "$tarball" "$target"
