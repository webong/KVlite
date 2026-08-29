#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

exec docker compose -f "$repo_root/compose.rocksdb.yml" run --rm --build rocksdb-test
