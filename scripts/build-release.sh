#!/usr/bin/env bash

# Build one driver-specific CLI and/or C shared library bundle into a
# reproducible release layout. RocksDB is linked through cgo, therefore its
# bundle must be built natively with matching headers, library, compiler, and
# linker available.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/build-release.sh [options]

Build KVLite artifacts for the current native platform.

Options:
  --version VERSION       Release version used in dist/VERSION (default: dev)
  --target OS-ARCH        Native target, such as darwin-arm64 (default: host)
  --driver NAME           Driver bundle: rocksdb or leveldb (default: rocksdb)
  --component NAME        Build cli or c-shared; repeat to choose both
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
components=()

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
      shift 2
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

case "$driver" in
  rocksdb)
    build_tags="rocksdb,kvlite_rocksdb"
    native_driver=1
    ;;
  leveldb)
    build_tags="kvlite_leveldb"
    native_driver=0
    ;;
  *) fail "unsupported driver: $driver (expected rocksdb or leveldb)" ;;
esac

case "$target" in
  linux-amd64|linux-arm64|darwin-amd64|darwin-arm64|windows-amd64) ;;
  *) fail "unsupported target: $target" ;;
esac

host_target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
go_target="$(go env GOOS)-$(go env GOARCH)"
[[ "$target" == "$host_target" ]] || fail "target $target is not native to this host ($host_target)"
[[ "$go_target" == "$host_target" ]] || fail "GOOS/GOARCH select $go_target; unset them for a native $host_target build"
if ((${#components[@]} == 0)); then
  components=(cli c-shared)
fi

for component in "${components[@]}"; do
  case "$component" in
    cli|c-shared) ;;
    *) fail "unsupported component: $component" ;;
  esac
done

needs_cgo="$native_driver"
for component in "${components[@]}"; do
  [[ "$component" == "c-shared" ]] && needs_cgo=1
done
if [[ "$needs_cgo" == "1" ]]; then
  [[ "$(go env CGO_ENABLED)" == "1" ]] || fail "CGO_ENABLED must be 1 for this driver bundle"
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
artifact_dir="$repo_root/dist/$version/$target/drivers/$driver"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-release.XXXXXX")"
staging_dir="$temporary_dir/artifact"
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT

cd "$repo_root"
mkdir -p "$staging_dir/bin" "$staging_dir/lib" "$staging_dir/include"
files=()

has_component() {
  local wanted="$1"
  local component
  for component in "${components[@]}"; do
    [[ "$component" == "$wanted" ]] && return 0
  done
  return 1
}

if has_component cli; then
  go build -tags "$build_tags" -trimpath -buildvcs=false \
    -o "$staging_dir/bin/$executable_name" \
    ./cmd/kvlite
  files+=("bin/$executable_name")
fi

if has_component c-shared; then
  # Go emits a generated header beside a c-shared output. Build in a temporary
  # directory so the release ships only the reviewed, checked-in ABI header.
  go build -tags "$build_tags" -trimpath -buildvcs=false -buildmode=c-shared \
    -o "$temporary_dir/$library_name" \
    ./capi
  cp "$temporary_dir/$library_name" "$staging_dir/lib/$library_name"
  cp "$repo_root/capi/kvlite.h" "$staging_dir/include/kvlite.h"
  files+=("lib/$library_name" "include/kvlite.h")
fi

(
  cd "$staging_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${files[@]}"
  else
    shasum -a 256 "${files[@]}"
  fi
) > "$staging_dir/SHA256SUMS"

# Only replace a version/target directory after every requested artifact was
# built and checksummed. This keeps failed native builds out of dist/.
[[ "$artifact_dir" == "$repo_root/dist/"* ]] || fail "refusing to replace an unsafe artifact directory"
mkdir -p "$(dirname "$artifact_dir")"
rm -rf "$artifact_dir"
mv "$staging_dir" "$artifact_dir"

printf 'Built KVLite %s driver bundle %s for %s in %s\n' "$version" "$driver" "$target" "$artifact_dir"
