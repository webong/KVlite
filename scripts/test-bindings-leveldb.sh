#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-leveldb-bindings.XXXXXX")"
trap 'rm -rf "$temp_dir"' EXIT

case "$(go env GOOS)" in
  darwin) library_name="libkvlite.dylib" ;;
  windows) library_name="kvlite.dll" ;;
  *) library_name="libkvlite.so" ;;
esac

go build -tags kvlite_leveldb -buildmode=c-shared -o "$temp_dir/$library_name" ./capi
PYTHONPATH="$repo_root/lib/bindings/python/src" \
KVLITE_LIBRARY_PATH="$temp_dir/$library_name" \
python3 "$repo_root/lib/bindings/python/tests/real_leveldb_native.py"
