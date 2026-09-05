#!/usr/bin/env bash
#
# Install prebuilt KVLite release bundles into an FHS-style prefix.
#
# Usage: scripts/install.sh [options]
#
# Options:
#   --prefix DIR        Installation prefix (default: /usr/local)
#   --destdir DIR       Staging root prepended to all paths (for packagers)
#   --version VERSION   Release version under dist/ (default: dev)
#   --from DIR          Use an unpacked release tree instead of dist/
#                       (the online installer sets this; DIR must contain
#                       <target>/{drivers,modules} like a tarball root)
#   --target OS-ARCH    Native target under dist/ (default: host)
#   --component NAME    Install drivers or modules; repeat for both
#                       (default: both when present)
#   --link-cli DRIVER   Driver bundle whose CLI becomes bin/kvlite
#                       (default: leveldb when installed, else the only
#                       installed driver; required when several are present)
#   --skip-verify       Skip the post-install module verification
#   --help              Show this help
#
# Layout (all bundle-internal paths stay relative, so DESTDIR packages and
# relocated prefixes keep working):
#
#   <prefix>/bin/kvlite                 -> ../lib/kvlite/drivers/<driver>/bin/kvlite
#   <prefix>/bin/kvlite-http[.exe]     -> ../lib/kvlite/modules/http/bin/...
#   <prefix>/bin/kvlite-redis[.exe]    -> ../lib/kvlite/modules/redis/bin/...
#   <prefix>/lib/kvlite/drivers/*/     full driver bundles, intact
#   <prefix>/lib/kvlite/modules/*/     full protocol bundles, intact
#   <prefix>/include/kvlite.h          reviewed C ABI header
#   <prefix>/share/doc/kvlite/<bundle>/ third-party notices per bundle
#
# Only one CLI can own bin/kvlite, so additional installed drivers are
# reached through `kvlite module run` (or a packager's alternatives system).
# After installing, point discovery at the catalog root:
#
#   export KVLITE_SYSTEM_MODULE_PATH="<prefix>/lib/kvlite"
#
# Package recipes should prefer that variable (or KVLITE_HOME for a user
# install) over patching binaries. See packaging/README.md.

set -euo pipefail

fail() {
  printf 'install: %s\n' "$*" >&2
  exit 2
}

usage() {
  sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

prefix="/usr/local"
destdir=""
version="dev"
from=""
target=""
components=()
link_cli=""
verify=1

while (($# > 0)); do
  case "$1" in
    --prefix)
      (($# >= 2)) || fail "--prefix requires a value"
      prefix="$2"
      shift 2
      ;;
    --destdir)
      (($# >= 2)) || fail "--destdir requires a value"
      destdir="$2"
      shift 2
      ;;
    --version)
      (($# >= 2)) || fail "--version requires a value"
      version="$2"
      shift 2
      ;;
    --from)
      (($# >= 2)) || fail "--from requires a value"
      from="$2"
      shift 2
      ;;
    --target)
      (($# >= 2)) || fail "--target requires a value"
      target="$2"
      shift 2
      ;;
    --component)
      (($# >= 2)) || fail "--component requires a value"
      components+=("$2")
      shift 2
      ;;
    --link-cli)
      (($# >= 2)) || fail "--link-cli requires a value"
      link_cli="$2"
      shift 2
      ;;
    --skip-verify)
      verify=0
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

# ${#components[@]} is safe under set -u even when empty; expanding an empty
# array with "${components[@]}" is not (pre-4.4 bash), so the loop only runs
# when the array is known non-empty.
if ((${#components[@]} > 0)); then
  for component in "${components[@]}"; do
    case "$component" in
      drivers|modules) ;;
      *) fail "unsupported component: $component (expected drivers or modules)" ;;
    esac
  done
else
  components=(drivers modules)
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
if [[ -z "$target" ]]; then
  if command -v go >/dev/null 2>&1; then
    target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
  else
    fail "go is not installed; pass --target explicitly (for example --target linux-amd64)"
  fi
fi
dist_root="$repo_root/dist/$version/$target"
if [[ -n "$from" ]]; then
  dist_root="$from/$target"
fi
[[ -d "$dist_root" ]] || fail "no release tree at $dist_root; build it first (make release ...)"

want_component() {
  local wanted="$1"
  local component
  for component in "${components[@]}"; do
    [[ "$component" == "$wanted" ]] && return 0
  done
  return 1
}

prefix="${prefix%/}"
dest_prefix="$destdir$prefix"
catalog="$dest_prefix/lib/kvlite"
installed_drivers=()
installed_modules=()

if want_component drivers && [[ -d "$dist_root/drivers" ]]; then
  for bundle in "$dist_root"/drivers/*/; do
    [[ -d "$bundle" ]] || continue
    driver="$(basename "$bundle")"
    mkdir -p "$catalog/drivers"
    cp -R "$bundle" "$catalog/drivers/$driver"
    chmod -R u+w "$catalog/drivers/$driver"
    installed_drivers+=("$driver")
    if [[ -d "$catalog/drivers/$driver/NOTICES" ]]; then
      mkdir -p "$dest_prefix/share/doc/kvlite/$driver"
      cp "$catalog/drivers/$driver"/NOTICES/* "$dest_prefix/share/doc/kvlite/$driver/"
    fi
  done
fi

if want_component modules && [[ -d "$dist_root/modules" ]]; then
  for bundle in "$dist_root"/modules/*/; do
    [[ -d "$bundle" ]] || continue
    extension="$(basename "$bundle")"
    mkdir -p "$catalog/modules"
    cp -R "$bundle" "$catalog/modules/$extension"
    chmod -R u+w "$catalog/modules/$extension"
    installed_modules+=("$extension")
  done
fi

if ((${#installed_drivers[@]} == 0)) && ((${#installed_modules[@]} == 0)); then
  fail "nothing to install from $dist_root for components (${components[*]})"
fi

# The reviewed ABI header is identical across driver bundles; ship one copy.
if ((${#installed_drivers[@]} > 0)); then
  for driver in "${installed_drivers[@]}"; do
    header="$catalog/drivers/$driver/include/kvlite.h"
    if [[ -f "$header" ]]; then
      mkdir -p "$dest_prefix/include"
      cp "$header" "$dest_prefix/include/kvlite.h"
      chmod 644 "$dest_prefix/include/kvlite.h"
      break
    fi
  done
fi

# One CLI owns bin/kvlite; every other binary links under its own name.
mkdir -p "$dest_prefix/bin"
link_target_driver="$link_cli"
if [[ -z "$link_target_driver" ]] && ((${#installed_drivers[@]} > 0)); then
  for driver in "${installed_drivers[@]}"; do
    if [[ "$driver" == "leveldb" ]]; then
      link_target_driver="leveldb"
      break
    fi
  done
  if [[ -z "$link_target_driver" && "${#installed_drivers[@]}" -eq 1 ]]; then
    link_target_driver="${installed_drivers[0]}"
  fi
fi
if [[ -n "$link_target_driver" ]]; then
  found=0
  if ((${#installed_drivers[@]} > 0)); then
    for driver in "${installed_drivers[@]}"; do
      [[ "$driver" == "$link_target_driver" ]] && found=1
    done
  fi
  [[ "$found" == "1" ]] || fail "--link-cli $link_target_driver is not among the installed drivers"
else
  if ((${#installed_drivers[@]} > 1)); then
    fail "several drivers installed; choose one with --link-cli for bin/kvlite"
  fi
fi

link_binary() {
  local link_name="$1"
  local relative_target="$2"
  local link_path="$dest_prefix/bin/$link_name"
  rm -f "$link_path"
  if ln -s "$relative_target" "$link_path" 2>/dev/null; then
    return 0
  fi
  printf 'install: symlink failed; copying %s instead\n' "$link_name" >&2
  cp "$dest_prefix/$relative_target" "$link_path"
}

if [[ -n "${link_target_driver:-}" ]]; then
  cli_name="kvlite"
  cli_link="kvlite"
  if [[ "$target" == windows-* ]]; then
    cli_name="kvlite.exe"
    cli_link="kvlite.exe"
  fi
  link_binary "$cli_link" "../lib/kvlite/drivers/$link_target_driver/bin/$cli_name"
fi
if ((${#installed_modules[@]} > 0)); then
  for extension in "${installed_modules[@]}"; do
    exe_suffix=""
    [[ "$target" == windows-* ]] && exe_suffix=".exe"
    link_binary "kvlite-$extension$exe_suffix" "../lib/kvlite/modules/$extension/bin/kvlite-$extension$exe_suffix"
  done
fi

if [[ "$verify" == "1" ]]; then
  cli="$dest_prefix/bin/kvlite"
  [[ -x "$cli" ]] || cli=""
  if [[ -z "$cli" ]]; then
    # A modules-only install (the driver package lands separately) has no
    # CLI to verify through yet; the system integrator verifies the
    # assembled tree instead of failing this package here.
    printf 'install: no bin/kvlite installed; skipping post-install verification\n' >&2
  else
    if ((${#installed_drivers[@]} > 0)); then
      for driver in "${installed_drivers[@]}"; do
        KVLITE_SYSTEM_MODULE_PATH="$catalog" KVLITE_MODULE_PATH="" KVLITE_HOME="" "$cli" module verify "$driver" >/dev/null \
          || fail "post-install verification failed for driver $driver"
      done
    fi
    if ((${#installed_modules[@]} > 0)); then
      for extension in "${installed_modules[@]}"; do
        KVLITE_SYSTEM_MODULE_PATH="$catalog" KVLITE_MODULE_PATH="" KVLITE_HOME="" "$cli" module verify "$extension" >/dev/null \
          || fail "post-install verification failed for extension $extension"
      done
    fi
  fi
fi

printf 'Installed KVLite %s for %s in %s\n' "$version" "$target" "$dest_prefix"
printf 'Catalog root: %s/lib/kvlite\n' "$prefix"
printf 'To use it, export KVLITE_SYSTEM_MODULE_PATH="%s/lib/kvlite"\n' "$prefix"
