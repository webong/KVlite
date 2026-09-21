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

# Windows resolves DLLs from the loading application's own directory first,
# so bundling there means copying dependencies next to each executable. No
# path rewriting exists or is needed. DLLs backing the C shared library
# itself resolve through the host process directory or PATH; shipping every
# bundled DLL in bin/ means a single PATH entry covers the whole tree.
is_system_library_windows() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    kernel32.dll|user32.dll|gdi32.dll|advapi32.dll|shell32.dll|ole32.dll|oleaut32.dll|\
uuid.dll|comdlg32.dll|ws2_32.dll|wsock32.dll|wininet.dll|winhttp.dll|\
crypt32.dll|bcrypt.dll|ncrypt.dll|secur32.dll|credui.dll|mswsock.dll|\
comctl32.dll|shlwapi.dll|version.dll|winmm.dll|winspool.dll|imm32.dll|\
usp10.dll|msimg32.dll|dwmapi.dll|uxtheme.dll|ntdll.dll|kernelbase.dll|\
msvcrt.dll|ucrtbase.dll|api-ms-*|ext-ms-*) return 0 ;;
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
    # Untested on a Windows runner so far; the copy-beside-exe mechanism is
    # standard loader behavior, but treat the first runner proof as required.
    printf 'bundle-native-deps: WARNING: Windows bundling awaits runner proof; verify with dumpbin -dependents\n' >&2
    if command -v dumpbin >/dev/null 2>&1; then
      dep_tool="dumpbin"
    elif command -v objdump >/dev/null 2>&1; then
      dep_tool="objdump"
    else
      fail "dumpbin (MSVC) or objdump (MinGW) is required to bundle windows runtime libraries"
    fi
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
  # NB: ldd renders an unresolvable library as "libfoo.so => not found".
  # The bare word "not" must never be treated as a path; unresolvable
  # non-system libraries are rejected explicitly below instead. The second
  # rule only fires on lines without "=>" (linux-vdso and the loader
  # itself), so resolved names are never emitted twice, bare.
  #
  # KVLITE_BUNDLE_LIB_PATH is honored through LD_LIBRARY_PATH so link-time
  # directories resolve exactly like the build saw them.
  LD_LIBRARY_PATH="${KVLITE_BUNDLE_LIB_PATH:-}${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" \
    ldd "$1" 2>/dev/null | awk '/=>/ { if ($3 != "" && $3 != "not") print $3; next } /^[^ ]+\.so/ { print $1 }' | grep -v '^$' || true
}

unresolved_for_linux() {
  LD_LIBRARY_PATH="${KVLITE_BUNDLE_LIB_PATH:-}${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" \
    ldd "$1" 2>/dev/null | awk '/=> not found/ { print $1 }' || true
}

dependencies_for_windows() {
  if [[ "$dep_tool" == "dumpbin" ]]; then
    dumpbin /dependents "$1" 2>/dev/null | awk '/\.dll/ { for (i = 1; i <= NF; i++) if ($i ~ /\.dll$/i) print $i }' | sort -u
  else
    objdump -p "$1" 2>/dev/null | awk '/DLL Name:/ { print $NF }' | sort -u
  fi
}

# Resolve a bare library name (no directory part, as recorded by linkers
# that use -install_name filenames or -l names) to a file: beside the
# binary first, then KVLITE_BUNDLE_LIB_PATH, a path-list of link-time
# library directories the caller provides (for example the RocksDB prefix
# lib). Callers must set it whenever dependencies are not absolute.
resolve_bare_dependency() {
  local name="$1"
  local beside="$2"
  if [[ -f "$beside/$name" ]]; then
    printf '%s/%s\n' "$beside" "$name"
    return 0
  fi
  local search="${KVLITE_BUNDLE_LIB_PATH:-}"
  local separator=":"
  [[ "$os" == "windows" ]] && separator=";"
  local dir
  local saved_ifs="$IFS"
  IFS="$separator"
  for dir in $search; do
    if [[ -n "$dir" && -f "$dir/$name" ]]; then
      printf '%s/%s\n' "$dir" "$name"
      IFS="$saved_ifs"
      return 0
    fi
  done
  IFS="$saved_ifs"
  return 1
}

install_bundled_library() {
  local source="$1"
  local base
  base="$(basename "$source")"
  local destination="$bundle_destination_dir/$base"
  if [[ -e "$destination" ]]; then
    local have want
    have="$(sha256_of "$destination")"
    want="$(sha256_of "$source")"
    [[ "$have" == "$want" ]] || fail "conflicting runtime library $base from $source"
    return 0
  fi
  # Dereference: a resolved dependency is often itself a version symlink
  # (librocksdb.10.8.dylib -> librocksdb.10.8.3.dylib). The bundle needs the
  # real bytes under the requested name, not a link dangling out of the tree.
  cp -L "$source" "$destination"
  chmod 755 "$destination"
  printf 'bundle-native-deps: bundled %s\n' "$source" >&2
  post_copy_fixup "$destination"
}

# On Linux a library's SONAME can differ from its filename while DT_NEEDED
# records the SONAME (libfoo.so.2 requested, libfoo.so.2.1.0 on disk). Link
# the SONAME beside the copy so the bundle resolves exactly like the system.
post_copy_fixup() {
  [[ "$os" == "linux" ]] || return 0
  local soname
  soname="$(patchelf --print-soname "$1" 2>/dev/null || true)"
  if [[ -n "$soname" && "$soname" != "$(basename "$1")" ]]; then
    ln -sf "$(basename "$1")" "$(dirname "$1")/$soname"
    printf 'bundle-native-deps: linked SONAME %s\n' "$soname" >&2
  fi
}

# Non-system dependencies land beside the binaries that load them: lib/ on
# Unix (re-pointed with loader paths below), bin/ on Windows (found through
# the application-directory-first search order).
bundle_destination_dir="$staging_dir/lib"
[[ "$os" == "windows" ]] && bundle_destination_dir="$staging_dir/bin"
mkdir -p "$bundle_destination_dir"

list_dependencies() {
  case "$os" in
    darwin) dependencies_for_darwin "$1" ;;
    linux) dependencies_for_linux "$1" ;;
    windows) dependencies_for_windows "$1" ;;
  esac
}

is_system_dependency() {
  case "$os" in
    darwin) is_system_library_darwin "$1" ;;
    linux) is_system_library_linux "$1" ;;
    windows) is_system_library_windows "$1" ;;
  esac
}

# Iterate to a fixpoint so transitive dependencies of bundled libraries are
# bundled too. Ten rounds bound pathological cycles.
for _ in $(seq 1 10); do
  added=0
  for binary in "${staged_binaries[@]}"; do
    if [[ "$os" == "linux" ]]; then
      # A missing dependency would otherwise bundle silently incomplete:
      # fail here with the library name and the remedy. (Entries resolvable
      # through KVLITE_BUNDLE_LIB_PATH never reach this list: ldd resolves
      # them first, honoring the same variable.)
      while IFS= read -r missing; do
        [[ -n "$missing" ]] || continue
        is_system_dependency "$missing" && continue
        fail "dependency $missing of $binary is not resolvable (ldd reports 'not found'); link with an rpath, set LD_LIBRARY_PATH so it resolves, or point KVLITE_BUNDLE_LIB_PATH at its directory"
      done < <(unresolved_for_linux "$binary")
    fi
    deps="$(list_dependencies "$binary")"
    while IFS= read -r dep; do
      [[ -n "$dep" ]] || continue
      case "$dep" in
        @loader_path*|@rpath*) continue ;;
      esac
      # Windows reports bare DLL names; other linkers may record bare
      # filenames too (RocksDB uses its -install_name basename). Resolve
      # beside the binary, through KVLITE_BUNDLE_LIB_PATH, and on Windows
      # through PATH as a last resort.
      if [[ "$dep" != *[/\\]* ]]; then
        resolved="$(resolve_bare_dependency "$dep" "$(dirname "$binary")")"
        if [[ -z "$resolved" && "$os" == "windows" ]]; then
          saved_path_ifs="$IFS"
          IFS=';'
          for path_dir in $PATH; do
            if [[ -f "$path_dir/$dep" ]]; then
              resolved="$path_dir/$dep"
              break
            fi
          done
          IFS="$saved_path_ifs"
        fi
        [[ -n "${resolved:-}" ]] || fail "dependency $dep of $binary was not found beside it, on KVLITE_BUNDLE_LIB_PATH, or on PATH"
        dep="$resolved"
      fi
      is_system_dependency "$dep" && continue
      [[ -f "$dep" ]] || fail "dependency $dep of $binary is not a file on disk"
      before="$(ls "$bundle_destination_dir" | wc -l)"
      install_bundled_library "$dep"
      after="$(ls "$bundle_destination_dir" | wc -l)"
      if [[ "$after" != "$before" ]]; then
        added=1
        staged_binaries+=("$bundle_destination_dir/$(basename "$dep")")
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
