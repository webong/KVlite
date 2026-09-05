#!/usr/bin/env bash
#
# Copy non-system native runtime libraries into a staged release bundle and
# rewrite loader paths so the bundle runs without system-wide installs.
#
# Usage: scripts/bundle-native-deps.sh STAGING_DIR TARGET
#
# - STAGING_DIR is a release staging tree with bin/ and lib/ layout.
# - TARGET is OS-ARCH such as darwin-arm64 or linux-amd64.
#
# System libraries (/usr/lib, /System on darwin; linux-vdso, ld-linux,
# libc, libm, libpthread, libdl, librt, libgcc_s on linux) are never
# bundled. Everything else a staged Mach-O or ELF binary links against is
# copied into STAGING_DIR/lib and re-pointed at the bundle. Callers must
# re-checksum the tree afterwards (build-release.sh does).
#
# License notices for bundled third-party libraries are the bundle owner's
# responsibility; build-release.sh requires --notice-file entries when this
# script is enabled through --bundle-runtime.

set -euo pipefail

fail() {
  printf 'bundle-native-deps: %s\n' "$*" >&2
  exit 2
}

[[ $# -eq 2 ]] || fail "usage: bundle-native-deps.sh STAGING_DIR TARGET"
staging_dir="$1"
target="$2"
[[ -d "$staging_dir" ]] || fail "staging directory does not exist: $staging_dir"

os="${target%%-*}"

is_system_library_darwin() {
  case "$1" in
    /usr/lib/*|/System/*) return 0 ;;
  esac
  return 1
}

is_system_library_linux() {
  case "$1" in
    linux-vdso*|ld-linux*|*/libc.so*|*/libm.so*|*/libpthread.so*|*/libdl.so*|*/librt.so*|*/libgcc_s.so*) return 0 ;;
  esac
  return 1
}

case "$os" in
  darwin)
    command -v otool >/dev/null 2>&1 || fail "otool is required to bundle darwin runtime libraries"
    command -v install_name_tool >/dev/null 2>&1 || fail "install_name_tool is required to bundle darwin runtime libraries"
    ;;
  linux)
    command -v ldd >/dev/null 2>&1 || fail "ldd is required to bundle linux runtime libraries"
    command -v patchelf >/dev/null 2>&1 || fail "patchelf is required to bundle linux runtime libraries"
    ;;
  windows)
    fail "runtime bundling on windows-amd64 needs a pinned CI runner with the MSVC toolchain; see lib/README.md"
    ;;
  *) fail "unsupported target: $target" ;;
esac

mkdir -p "$staging_dir/lib"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  else
    shasum -a 256 "$1" | awk '{ print $1 }'
  fi
}

# Collect every staged artifact that can carry dependencies: all files under
# bin/ plus shared libraries under lib/. (No -perm filter: GNU and BSD find
# disagree on its spelling.)
staged_binaries=()
if [[ -d "$staging_dir/bin" ]]; then
  while IFS= read -r entry; do
    staged_binaries+=("$entry")
  done < <(find "$staging_dir/bin" -type f)
fi
if [[ -d "$staging_dir/lib" ]]; then
  while IFS= read -r entry; do
    staged_binaries+=("$entry")
  done < <(find "$staging_dir/lib" -type f \( -name '*.so' -o -name '*.dylib' -o -name '*.dll' \))
fi
if [[ "${#staged_binaries[@]}" -eq 0 ]]; then
  printf 'bundle-native-deps: no staged binaries found; nothing to bundle\n' >&2
  exit 0
fi

dependencies_for_darwin() {
  otool -L "$1" | awk 'NR > 1 { print $1 }'
}

dependencies_for_linux() {
  ldd "$1" 2>/dev/null | awk '/=>/ { print $3 } /^[^ ]+\.so/ { print $1 }' | grep -v '^$' || true
}

install_bundled_library() {
  local source="$1"
  local base
  base="$(basename "$source")"
  local destination="$staging_dir/lib/$base"
  if [[ -e "$destination" ]]; then
    local have want
    have="$(sha256_of "$destination")"
    want="$(sha256_of "$source")"
    [[ "$have" == "$want" ]] || fail "conflicting runtime library $base from $source"
    return 0
  fi
  cp "$source" "$destination"
  chmod 755 "$destination"
  printf 'bundle-native-deps: bundled %s\n' "$source" >&2
}

# Iterate to a fixpoint so transitive dependencies of bundled libraries are
# bundled too. Ten rounds bound pathological cycles.
for _ in $(seq 1 10); do
  added=0
  for binary in "${staged_binaries[@]}"; do
    case "$os" in
      darwin) deps="$(dependencies_for_darwin "$binary")" ;;
      linux) deps="$(dependencies_for_linux "$binary")" ;;
    esac
    while IFS= read -r dep; do
      [[ -n "$dep" ]] || continue
      case "$dep" in
        @loader_path*|@rpath*) continue ;;
      esac
      case "$os" in
        darwin) is_system_library_darwin "$dep" && continue ;;
        linux) is_system_library_linux "$dep" && continue ;;
      esac
      [[ -f "$dep" ]] || fail "dependency $dep of $binary is not a file on disk"
      before="$(ls "$staging_dir/lib" | wc -l)"
      install_bundled_library "$dep"
      after="$(ls "$staging_dir/lib" | wc -l)"
      if [[ "$after" != "$before" ]]; then
        added=1
        staged_binaries+=("$staging_dir/lib/$(basename "$dep")")
      fi
    done <<< "$deps"
  done
  [[ "$added" == "0" ]] && break
done

# Rewrite references at their use sites.
for binary in "${staged_binaries[@]}"; do
  case "$binary" in
    "$staging_dir"/bin/*) loader_prefix="@loader_path/../lib" ;;
    *) loader_prefix="@loader_path" ;;
  esac
  case "$os" in
    darwin)
      while IFS= read -r dep; do
        [[ -n "$dep" ]] || continue
        case "$dep" in
          @loader_path*|@rpath*) continue ;;
        esac
        is_system_library_darwin "$dep" && continue
        install_name_tool -change "$dep" "$loader_prefix/$(basename "$dep")" "$binary"
      done < <(dependencies_for_darwin "$binary")
      case "$binary" in
        "$staging_dir"/lib/*)
          # Bundled libraries (and libkvlite itself) address their peers
          # relative to their own directory.
          install_name_tool -id "$loader_prefix/$(basename "$binary")" "$binary" 2>/dev/null || true
          ;;
      esac
      ;;
    linux)
      rpath_set=0
      while IFS= read -r dep; do
        [[ -n "$dep" ]] || continue
        is_system_library_linux "$dep" && continue
        if [[ "$rpath_set" == "0" ]]; then
          case "$binary" in
            "$staging_dir"/bin/*) patchelf --set-rpath '$ORIGIN/../lib' "$binary" ;;
            *) patchelf --set-rpath '$ORIGIN' "$binary" ;;
          esac
          rpath_set=1
        fi
      done < <(dependencies_for_linux "$binary")
      ;;
  esac
done

printf 'bundle-native-deps: runtime bundling complete for %s\n' "$target" >&2

# Rewriting Mach-O load commands invalidates ad-hoc code signatures; re-sign
# so the bundled binaries still execute on hardened macOS runtimes.
if [[ "$os" == "darwin" ]] && command -v codesign >/dev/null 2>&1; then
  for binary in "${staged_binaries[@]}"; do
    codesign --force -s - "$binary" >/dev/null 2>&1 || true
  done
fi
