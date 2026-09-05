#!/usr/bin/env bash
#
# KVLite online installer: fetch a release tarball and install it.
#
#   curl -fsSL https://github.com/webong/KVlite/releases/latest/download/kvlite-installer.sh | bash
#
# (Until release assets are published, fetch this file from the repository
# directly and pass --base-url pointing at a tarball host.)
#
# Usage: install-online.sh [options]
#
# Options:
#   --version VERSION   Release to install (default: latest)
#   --driver NAME       Driver CLI linked as bin/kvlite: leveldb or rocksdb
#                       (default: leveldb)
#   --prefix DIR        Install prefix (default: /usr/local when writable,
#                       otherwise $HOME/.local)
#   --no-http           Skip the HTTP protocol module
#   --no-redis          Skip the Redis protocol module
#   --base-url URL      Release asset base URL (default: GitHub releases).
#                       Anything serving kvlite-VERSION-TARGET.tar.gz plus
#                       .sha256 sidecars works, e.g. a local test server.
#   --shell-rc          Append the KVLITE_SYSTEM_MODULE_PATH export to your
#                       shell rc file (default: print instructions instead)
#   --yes               Assume defaults; never prompt
#   --help              Show this help
#
# Requires: curl or wget, tar, and sha256sum or shasum. Windows is not
# supported by this script; download the release tarball manually instead.
#
# The installer verifies the tarball checksum before unpacking, then the
# installed tree verifies every module before finishing. Checksums prove
# integrity, not provenance; inspect releases through your usual channels.

set -euo pipefail

fail() {
  printf 'install-online: %s\n' "$*" >&2
  exit 2
}

usage() {
  sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

version="latest"
driver="leveldb"
prefix=""
install_http=1
install_redis=1
base_url="https://github.com/webong/KVlite/releases/download"
configure_shell=0
assume_yes=0

# The only supported asset source layout. A versioned release uses
# .../releases/download/VERSION/...; "latest" uses .../releases/latest/download/...
latest_base_url="https://github.com/webong/KVlite/releases/latest/download"

while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || fail "--version requires a value"
      version="$2"
      shift 2
      ;;
    --driver)
      (($# >= 2)) || fail "--driver requires a value"
      driver="$2"
      shift 2
      ;;
    --prefix)
      (($# >= 2)) || fail "--prefix requires a value"
      prefix="$2"
      shift 2
      ;;
    --no-http)
      install_http=0
      shift 1
      ;;
    --no-redis)
      install_redis=0
      shift 1
      ;;
    --base-url)
      (($# >= 2)) || fail "--base-url requires a value"
      base_url="$2"
      shift 2
      ;;
    --shell-rc)
      configure_shell=1
      shift 1
      ;;
    --yes)
      assume_yes=1
      shift 1
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

case "$driver" in
  leveldb|rocksdb) ;;
  *) fail "unsupported driver: $driver (expected leveldb or rocksdb)" ;;
esac

os_name="$(uname -s)"
arch_name="$(uname -m)"
case "$os_name" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) fail "unsupported OS: $os_name (macOS and Linux only; Windows users should download the release tarball manually)" ;;
esac
case "$arch_name" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported architecture: $arch_name (amd64 and arm64 only)" ;;
esac
target="$os-$arch"

if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -q -O "$2" "$1"; }
else
  fail "curl or wget is required"
fi
command -v tar >/dev/null 2>&1 || fail "tar is required"
if command -v sha256sum >/dev/null 2>&1; then
  checksum_of() { sha256sum "$1" | awk '{ print $1 }'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum_of() { shasum -a 256 "$1" | awk '{ print $1 }'; }
else
  fail "sha256sum or shasum is required"
fi

if [[ -z "$prefix" ]]; then
  if [[ -w "/usr/local" || -w "/usr/local/bin" ]]; then
    prefix="/usr/local"
  else
    prefix="$HOME/.local"
  fi
fi
if [[ ! -w "$prefix" && ! -w "$(dirname "$prefix")" ]]; then
  if [[ "$assume_yes" == "1" ]]; then
    fail "prefix $prefix is not writable; rerun with --prefix or as root"
  fi
  printf 'install-online: prefix %s is not writable.\n' "$prefix" >&2
  printf 'Rerun with --prefix, or as root to install system-wide. ' >&2
  printf 'Continue with --prefix %s/.local instead? [y/N] ' "$HOME" >&2
  read -r answer
  case "$answer" in
    y|Y|yes|YES)
      prefix="$HOME/.local"
      ;;
    *)
      fail "installation cancelled"
      ;;
  esac
fi

if [[ "$version" == "latest" ]]; then
  asset_base="$latest_base_url"
  version_label="latest"
else
  asset_base="$base_url/$version"
  version_label="$version"
fi
# An explicit mirror or test server replaces the GitHub layout entirely.
if [[ "$base_url" != "https://github.com/webong/KVlite/releases/download" ]]; then
  asset_base="$base_url"
fi
base="kvlite-$version_label-$target"

work_root="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-install.XXXXXX")"
cleanup() {
  rm -rf "$work_root"
}
trap cleanup EXIT

printf 'install-online: downloading %s for %s\n' "$version_label" "$target" >&2
download "$asset_base/$base.tar.gz" "$work_root/$base.tar.gz" || \
  fail "could not download $asset_base/$base.tar.gz (check the version and target)"
download "$asset_base/$base.tar.gz.sha256" "$work_root/$base.tar.gz.sha256" || \
  fail "could not download the checksum sidecar for $base.tar.gz"

want="$(awk '{ print $1 }' "$work_root/$base.tar.gz.sha256")"
got="$(checksum_of "$work_root/$base.tar.gz")"
[[ -n "$want" ]] || fail "checksum file is empty"
if [[ "$want" != "$got" ]]; then
  fail "checksum mismatch for $base.tar.gz (want $want, got $got)"
fi
printf 'install-online: checksum verified\n' >&2

tar -xzf "$work_root/$base.tar.gz" -C "$work_root"
# Prune protocol modules the user declined before installing.
if [[ "$install_http" == "0" && -d "$work_root/$target/modules/http" ]]; then
  rm -rf "$work_root/$target/modules/http"
fi
if [[ "$install_redis" == "0" && -d "$work_root/$target/modules/redis" ]]; then
  rm -rf "$work_root/$target/modules/redis"
fi
[[ -x "$work_root/scripts/install.sh" ]] || fail "release tarball is missing scripts/install.sh"

bash "$work_root/scripts/install.sh" \
  --prefix "$prefix" \
  --from "$work_root" \
  --version "$version_label" \
  --target "$target" \
  --link-cli "$driver"

export_line="export KVLITE_SYSTEM_MODULE_PATH=\"$prefix/lib/kvlite\""
if [[ "$configure_shell" == "1" ]]; then
  rc=""
  case "${SHELL:-}" in
    *zsh) rc="$HOME/.zshrc" ;;
    *bash) rc="$HOME/.bashrc" ;;
    *fish)
      printf 'install-online: fish detected; add this to ~/.config/fish/config.fish instead:\n  set -x KVLITE_SYSTEM_MODULE_PATH "%s/lib/kvlite"\n' "$prefix" >&2
      rc=""
      ;;
  esac
  if [[ -n "$rc" ]]; then
    touch "$rc"
    if ! grep -q "KVLITE_SYSTEM_MODULE_PATH=" "$rc"; then
      printf '\n# KVLite installed modules (added by kvlite installer)\n%s\n' "$export_line" >> "$rc"
      printf 'install-online: added KVLITE_SYSTEM_MODULE_PATH to %s\n' "$rc" >&2
    else
      printf 'install-online: %s already sets KVLITE_SYSTEM_MODULE_PATH; leaving it alone\n' "$rc" >&2
    fi
  fi
else
  printf 'install-online: add this to your shell profile (or rerun with --shell-rc):\n  %s\n' "$export_line" >&2
fi

if [[ ":$PATH:" != *":$prefix/bin:"* ]]; then
  printf 'install-online: %s/bin is not on PATH; add it to use kvlite directly\n' "$prefix" >&2
fi

printf 'install-online: installed kvlite %s (%s) in %s\n' "$version_label" "$target" "$prefix" >&2
KVLITE_SYSTEM_MODULE_PATH="$prefix/lib/kvlite" KVLITE_MODULE_PATH="" KVLITE_HOME="" \
  "$prefix/bin/kvlite" module list 2>/dev/null | sed 's/^/install-online:   /' >&2 || true
