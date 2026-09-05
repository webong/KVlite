#!/usr/bin/env bash
#
# Assemble third-party license notices for --bundle-runtime release builds.
#
# Usage: scripts/collect-runtime-notices.sh ROCKSDB_SOURCE_DIR OUTPUT_DIR
#
# Copies the RocksDB license text (required; the build fails without it) and
# records the exact platform compression/development library versions the
# bundle was linked against. Distro copyright texts are copied when present;
# anything missing is reported loudly so the release owner can complete the
# NOTICES set before publishing. Never ship a bundle whose NOTICES review is
# still open.
#
# The output directory receives one file per notice and prints its listing to
# stdout for CI logs.

set -euo pipefail

fail() {
  printf 'collect-runtime-notices: %s\n' "$*" >&2
  exit 2
}

[[ $# -eq 2 ]] || fail "usage: collect-runtime-notices.sh ROCKSDB_SOURCE_DIR OUTPUT_DIR"
rocksdb_source="$1"
output_dir="$2"

[[ -d "$rocksdb_source" ]] || fail "RocksDB source directory does not exist: $rocksdb_source"
license="$rocksdb_source/LICENSE"
[[ -f "$license" ]] || fail "RocksDB LICENSE not found at $license; refusing to assemble notices"
mkdir -p "$output_dir"
cp "$license" "$output_dir/NOTICE-rocksdb"

platform_file="$output_dir/NOTICE-platform-libraries"
{
  printf 'KVLite native runtime platform libraries.\n'
  printf 'Built on: %s\n' "$(uname -srm)"
  printf 'These development libraries were linked into the bundle; their\n'
  printf 'license texts ship alongside when the platform provides them.\n\n'
  if command -v dpkg-query >/dev/null 2>&1; then
    for package in libbz2-dev liblz4-dev libsnappy-dev libzstd-dev zlib1g-dev libstdc++-dev build-essential; do
      if dpkg-query -W -f='${Package} ${Version} ${Status}\n' "$package" 2>/dev/null | grep -q 'ok installed'; then
        dpkg-query -W -f='${Package} ${Version}\n' "$package" 2>/dev/null
      fi
    done
  elif command -v brew >/dev/null 2>&1; then
    for formula in bzip2 lz4 snappy zstd zlib; do
      if brew list --versions "$formula" 2>/dev/null | grep -q .; then
        brew list --versions "$formula" 2>/dev/null
      fi
    done
  else
    printf '(no dpkg-query or brew; platform library versions unrecorded)\n'
  fi
} > "$platform_file"

missing=0
if command -v dpkg-query >/dev/null 2>&1; then
  while IFS= read -r copyright; do
    cp "$copyright" "$output_dir/NOTICE-distro-$(basename "$(dirname "$copyright")")" 2>/dev/null || true
  done < <(for package in libbz2-1.0 liblz4-1 libsnappy1v5 libzstd1 zlib1g; do
    dpkg-query -L "$package" 2>/dev/null | grep '/copyright$' || true
  done)
  for package in libbz2-1.0 liblz4-1 libsnappy1v5 libzstd1 zlib1g; do
    if ! dpkg-query -L "$package" 2>/dev/null | grep -q '/copyright$'; then
      printf 'collect-runtime-notices: WARNING: no copyright text found for %s; review before publishing\n' "$package" >&2
      missing=1
    fi
  done
fi

printf 'collect-runtime-notices: assembled notices in %s:\n' "$output_dir" >&2
ls "$output_dir" >&2
if [[ "$missing" == "1" ]]; then
  printf 'collect-runtime-notices: WARNING: notices set is incomplete; the release owner must complete the review before publishing\n' >&2
fi
