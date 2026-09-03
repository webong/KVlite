#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

# The image is built from the same pinned RocksDB source as the tagged Go
# suite. It creates a real libkvlite and proves the public Python ctypes
# package can open, write, read, and close it.
exec docker compose -f "$repo_root/compose.rocksdb.yml" run --rm --build rocksdb-test \
  sh -c 'go build -tags rocksdb,kvlite_rocksdb -buildmode=c-shared -o /tmp/libkvlite.so ./capi && PYTHONPATH=/workspace/lib/bindings/python/src KVLITE_LIBRARY_PATH=/tmp/libkvlite.so python3 /workspace/lib/bindings/python/tests/real_native.py'
