#!/usr/bin/env bash

# Build one driver-specific CLI and/or C shared library bundle into a
# reproducible release layout. Every bundle receives a checked module manifest
# so language bindings and future hosts can discover it without compiling Go.
# RocksDB is linked through cgo, therefore its bundle must be built natively
# with matching headers, library, compiler, and linker available.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/build-release.sh [options]

Build KVLite artifacts for the current native platform.

Options:
  --version VERSION       Release version used in dist/VERSION (default: dev)
  --target OS-ARCH        Native target, such as darwin-arm64 (default: host)
  --driver NAME           Driver bundle: rocksdb, leveldb, or berkeleydb (default: rocksdb)
  --extension NAME        Protocol module bundle: http or redis (mutually exclusive with --driver)
  --allow-berkeleydb      Enable Berkeley DB release bundle build. This requires an explicit
                          license-reviewed decision from the bundle owner.
  --component NAME        Build cli or c-shared; repeat to choose both (driver bundles).
                          For --extension bundles the only component is the executable
                          (cli is accepted as an alias for it).
  --linked-extensions     Opt back into a CLI with linked HTTP/Redis extensions.
                          By default driver CLIs are built with kvlite_no_linked_extensions
                          so they launch verified standalone protocol executables.
  --bundle-runtime        Copy non-system native runtime libraries (RocksDB,
                          compression, C++ runtime) into the bundle and rewrite
                          loader paths to the bundle. Requires at least one
                          --notice-file for third-party license compliance.
                          Linux needs patchelf; Windows copies DLLs beside the
                          binaries (app-directory-first lookup, awaiting
                          runner proof).
  --notice-file PATH      Third-party license notice copied into NOTICES/;
                          repeat for each bundled runtime. Required with
                          --bundle-runtime.
  --help                  Show this help

Supported targets: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64,
windows-amd64. Cross-compilation is deliberately rejected because RocksDB is
a native dependency. Run this script on each target's native CI runner.
EOF
}

fail() {
  printf 'release build: %s\n' "$*" >&2
  exit 2
}

version="dev"
target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
driver="rocksdb"
driver_explicit=0
extension=""
allow_berkeleydb=0
linked_extensions=0
bundle_runtime=0
notice_files=()
components=()
if [[ "${ALLOW_BERKELEYDB_BUNDLE:-0}" == "1" ]]; then
  allow_berkeleydb=1
fi
if [[ "${KVLITE_LINKED_EXTENSIONS:-0}" == "1" ]]; then
  linked_extensions=1
fi
if [[ "${KVLITE_BUNDLE_RUNTIME:-0}" == "1" ]]; then
  bundle_runtime=1
fi

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
    --driver)
      (($# >= 2)) || fail "--driver requires a value"
      driver="$2"
      driver_explicit=1
      shift 2
      ;;
    --extension)
      (($# >= 2)) || fail "--extension requires a value"
      extension="$2"
      shift 2
      ;;
    --linked-extensions)
      linked_extensions=1
      shift 1
      ;;
    --bundle-runtime)
      bundle_runtime=1
      shift 1
      ;;
    --notice-file)
      (($# >= 2)) || fail "--notice-file requires a value"
      notice_files+=("$2")
      shift 2
      ;;
    --allow-berkeleydb)
      allow_berkeleydb=1
      shift 1
      ;;
    --component)
      (($# >= 2)) || fail "--component requires a value"
      components+=("$2")
      shift 2
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

[[ "$version" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]] || fail "version may contain only letters, numbers, ., _, +, and -"

if [[ -n "$extension" ]]; then
  [[ "$driver_explicit" == "0" ]] || fail "--extension and --driver are mutually exclusive; choose one release intent"
  case "$extension" in
    http|redis) ;;
    *) fail "unsupported extension: $extension (expected http or redis)" ;;
  esac
  is_extension_bundle=1
else
  is_extension_bundle=0
fi

case "$driver" in
  rocksdb)
    build_tags="rocksdb,kvlite_rocksdb"
    native_driver=1
    ;;
  leveldb)
    build_tags="kvlite_leveldb"
    native_driver=0
    ;;
  berkeleydb)
    if [[ "$allow_berkeleydb" != "1" ]]; then
      fail "Berkeley DB bundles are deliberately excluded from the standard release workflow; add --allow-berkeleydb and keep license obligations explicit"
    fi
    build_tags="berkeleydb,kvlite_berkeleydb"
    native_driver=1
    ;;
  *) fail "unsupported driver: $driver (expected rocksdb, leveldb, or berkeleydb)" ;;
esac

# Release driver CLIs are extension-free hosts: they launch verified standalone
# protocol executables instead of linking HTTP/Redis. Pass --linked-extensions
# only for an explicit development/convenience profile.
if [[ "$linked_extensions" == "0" ]]; then
  build_tags="$build_tags,kvlite_no_linked_extensions"
fi

case "$target" in
  linux-amd64|linux-arm64|darwin-amd64|darwin-arm64|windows-amd64) ;;
  *) fail "unsupported target: $target" ;;
esac

host_target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
go_target="$(go env GOOS)-$(go env GOARCH)"
[[ "$target" == "$host_target" ]] || fail "target $target is not native to this host ($host_target)"
[[ "$go_target" == "$host_target" ]] || fail "GOOS/GOARCH select $go_target; unset them for a native $host_target build"
if [[ "$is_extension_bundle" == "1" ]]; then
  if ((${#components[@]} == 0)); then
    components=(executable)
  fi
  for component in "${components[@]}"; do
    case "$component" in
      executable|cli) ;;
      *) fail "unsupported component for --extension: $component (expected executable)" ;;
    esac
  done
  # Normalize the cli alias so later stages emit one executable artifact.
  normalized=()
  for component in "${components[@]}"; do
    normalized+=("executable")
  done
  components=("${normalized[@]}")
else
  if ((${#components[@]} == 0)); then
    components=(cli c-shared)
  fi

  for component in "${components[@]}"; do
    case "$component" in
      cli|c-shared) ;;
      *) fail "unsupported component: $component" ;;
    esac
  done
fi

needs_cgo="$native_driver"
for component in "${components[@]}"; do
  [[ "$component" == "c-shared" ]] && needs_cgo=1
done
if [[ "$is_extension_bundle" == "1" ]]; then
  # Standalone protocol executables open installed driver C-shared modules
  # through driver_module_loader.go, which requires the system dynamic loader.
  needs_cgo=1
fi
if [[ "$needs_cgo" == "1" ]]; then
  if [[ "$is_extension_bundle" == "1" ]]; then
    [[ "$(go env CGO_ENABLED)" == "1" ]] || fail "CGO_ENABLED must be 1 for this extension bundle"
  else
    [[ "$(go env CGO_ENABLED)" == "1" ]] || fail "CGO_ENABLED must be 1 for this driver bundle"
  fi
fi

case "$target" in
  darwin-*)
    executable_name="kvlite"
    library_name="libkvlite.dylib"
    ;;
  linux-*)
    executable_name="kvlite"
    library_name="libkvlite.so"
    ;;
  windows-*)
    executable_name="kvlite.exe"
    library_name="kvlite.dll"
    ;;
esac

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
if [[ "$is_extension_bundle" == "1" ]]; then
  case "$target" in
    windows-*) extension_executable="kvlite-$extension.exe" ;;
    *) extension_executable="kvlite-$extension" ;;
  esac
  artifact_dir="$repo_root/dist/$version/$target/modules/$extension"
else
  artifact_dir="$repo_root/dist/$version/$target/drivers/$driver"
fi
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-release.XXXXXX")"
staging_dir="$temporary_dir/artifact"
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT

cd "$repo_root"
mkdir -p "$staging_dir/bin"
if [[ "$is_extension_bundle" == "0" ]]; then
  mkdir -p "$staging_dir/lib" "$staging_dir/include"
fi
files=()

has_component() {
  local wanted="$1"
  local component
  for component in "${components[@]}"; do
    [[ "$component" == "$wanted" ]] && return 0
  done
  return 1
}

if [[ "$is_extension_bundle" == "1" ]]; then
  # Protocol executables contain only core plus their protocol implementation.
  # They resolve storage through an installed driver C-shared module at
  # runtime and must not statically link any storage driver.
  extension_module_dir="$repo_root/extensions/$extension"
  [[ -d "$extension_module_dir/cmd/kvlite-$extension" ]] || fail "missing standalone entrypoint for extension $extension"
  (cd "$extension_module_dir" && go build -trimpath -buildvcs=false \
    -o "$staging_dir/bin/$extension_executable" \
    "./cmd/kvlite-$extension")
  files+=("bin/$extension_executable")
fi

if [[ "$is_extension_bundle" == "0" ]] && has_component cli; then
  go build -tags "$build_tags" -trimpath -buildvcs=false \
    -o "$staging_dir/bin/$executable_name" \
    ./cmd/kvlite
  files+=("bin/$executable_name")
fi

if [[ "$is_extension_bundle" == "0" ]] && has_component c-shared; then
  # Go emits a generated header beside a c-shared output. Build in a temporary
  # directory so the release ships only the reviewed, checked-in ABI header.
  go build -tags "$build_tags" -trimpath -buildvcs=false -buildmode=c-shared \
    -o "$temporary_dir/$library_name" \
    ./capi
  cp "$temporary_dir/$library_name" "$staging_dir/lib/$library_name"
  cp "$repo_root/capi/kvlite.h" "$staging_dir/include/kvlite.h"
  files+=("lib/$library_name" "include/kvlite.h")
fi

if [[ "$bundle_runtime" == "1" ]]; then
  if ((${#notice_files[@]} == 0)); then
    fail "--bundle-runtime requires at least one --notice-file with third-party license notices"
  fi
  mkdir -p "$staging_dir/NOTICES"
  for notice in "${notice_files[@]}"; do
    [[ -f "$notice" ]] || fail "notice file does not exist: $notice"
    cp "$notice" "$staging_dir/NOTICES/$(basename "$notice")"
    files+=("NOTICES/$(basename "$notice")")
  done
  bash "$script_dir/bundle-native-deps.sh" "$staging_dir" "$target"
  # Bundled runtime libraries join the checksummed closure. Entries already
  # listed (such as libkvlite itself) are not duplicated.
  if [[ -d "$staging_dir/lib" ]]; then
    while IFS= read -r bundled; do
      [[ -n "$bundled" ]] || continue
      already_listed=0
      for existing in "${files[@]}"; do
        if [[ "$existing" == "$bundled" ]]; then
          already_listed=1
          break
        fi
      done
      if [[ "$already_listed" == "0" ]]; then
        files+=("$bundled")
      fi
    done < <(cd "$staging_dir/lib" && find . -type f \( -name '*.so' -o -name '*.dylib' -o -name '*.dll' \) | sed 's|^\./|lib/|' | sort)
  fi
fi

(
  cd "$staging_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${files[@]}"
  else
    shasum -a 256 "${files[@]}"
  fi
) > "$staging_dir/SHA256SUMS"

hash_for() {
  local artifact_path="$1"
  awk -v artifact_path="$artifact_path" '$2 == artifact_path { print $1; exit }' "$staging_dir/SHA256SUMS"
}

write_module_manifest() {
  if [[ "$is_extension_bundle" == "1" ]]; then
    write_extension_manifest
    return
  fi
  local artifact_hash
  local needs_separator=0
  {
    printf '{\n'
    printf '  "schema_version": 1,\n'
    printf '  "name": "%s",\n' "$driver"
    printf '  "kind": "driver",\n'
    printf '  "version": "%s",\n' "$version"
    printf '  "module_abi": 1,\n'
    printf '  "driver": "%s",\n' "$driver"
    if [[ "$driver" == "rocksdb" ]]; then
      printf '  "capabilities": ["embedded-storage", "ttl-compaction"],\n'
      printf '  "license": "Apache-2.0",\n'
    else
      printf '  "capabilities": ["embedded-storage"],\n'
      if [[ "$driver" == "berkeleydb" ]]; then
        printf '  "license": "LicenseRef-Oracle-BerkeleyDB-separate-distribution",\n'
      else
        printf '  "license": "Apache-2.0",\n'
      fi
    fi
    printf '  "artifacts": [\n'
    if has_component cli; then
      artifact_hash="$(hash_for "bin/$executable_name")"
      [[ -n "$artifact_hash" ]] || fail "could not find CLI checksum"
      printf '    {"platform": "%s", "kind": "executable", "path": "bin/%s", "sha256": "%s"}' \
        "$target" "$executable_name" "$artifact_hash"
      needs_separator=1
    fi
    if has_component c-shared; then
      artifact_hash="$(hash_for "lib/$library_name")"
      [[ -n "$artifact_hash" ]] || fail "could not find C shared-library checksum"
      if [[ "$needs_separator" == "1" ]]; then
        printf ',\n'
      fi
      printf '    {"platform": "%s", "kind": "c-shared", "path": "lib/%s", "sha256": "%s", "symbol": "kvlite_abi_version"}' \
        "$target" "$library_name" "$artifact_hash"
    fi
    printf '\n  ]\n'
    printf '}\n'
  } > "$staging_dir/kvlite-module.json"
}

write_extension_manifest() {
  local artifact_hash capabilities_json license
  artifact_hash="$(hash_for "bin/$extension_executable")"
  [[ -n "$artifact_hash" ]] || fail "could not find extension executable checksum"
  # Keep installed capability/license metadata in sync with the source module
  # manifest; fall back to the reviewed contract if python3 is unavailable.
  capabilities_json=""
  license="Apache-2.0"
  if command -v python3 >/dev/null 2>&1; then
    capabilities_json="$(python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1])) .get("capabilities", []), separators=(",", ": ")))' "$repo_root/extensions/$extension/kvlite-module.json" 2>/dev/null || true)"
    license="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("license", "Apache-2.0"))' "$repo_root/extensions/$extension/kvlite-module.json" 2>/dev/null || true)"
  fi
  if [[ -z "$capabilities_json" ]]; then
    if [[ "$extension" == "http" ]]; then
      capabilities_json='["http-client", "http-server", "remote-driver-selection"]'
    else
      capabilities_json='["redis-resp2", "redis-server"]'
    fi
  fi
  [[ -n "$license" ]] || license="Apache-2.0"
  {
    printf '{\n'
    printf '  "schema_version": 1,\n'
    printf '  "name": "%s",\n' "$extension"
    printf '  "kind": "extension",\n'
    printf '  "version": "%s",\n' "$version"
    printf '  "module_abi": 1,\n'
    printf '  "capabilities": %s,\n' "$capabilities_json"
    printf '  "license": "%s",\n' "$license"
    printf '  "artifacts": [\n'
    printf '    {"platform": "%s", "kind": "executable", "path": "bin/%s", "sha256": "%s"}\n' \
      "$target" "$extension_executable" "$artifact_hash"
    printf '  ]\n'
    printf '}\n'
  } > "$staging_dir/kvlite-module.json"
}

write_module_manifest
(
  cd "$staging_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum kvlite-module.json
  else
    shasum -a 256 kvlite-module.json
  fi
) >> "$staging_dir/SHA256SUMS"

# Only replace a version/target directory after every requested artifact was
# built and checksummed. This keeps failed native builds out of dist/.
[[ "$artifact_dir" == "$repo_root/dist/"* ]] || fail "refusing to replace an unsafe artifact directory"
mkdir -p "$(dirname "$artifact_dir")"
rm -rf "$artifact_dir"
mv "$staging_dir" "$artifact_dir"

if [[ "$is_extension_bundle" == "1" ]]; then
  printf 'Built KVLite %s extension module %s for %s in %s\n' "$version" "$extension" "$target" "$artifact_dir"
else
  if [[ "$linked_extensions" == "0" ]]; then
    printf 'Built KVLite %s driver module %s for %s in %s (CLI without linked extensions)\n' "$version" "$driver" "$target" "$artifact_dir"
  else
    printf 'Built KVLite %s driver module %s for %s in %s (CLI with linked extensions)\n' "$version" "$driver" "$target" "$artifact_dir"
  fi
fi
