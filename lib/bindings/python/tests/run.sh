#!/usr/bin/env bash
set -euo pipefail

package_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$package_dir/../test-fixtures/mock_kvlite.c"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-python-test.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT

case "$(uname -s)" in
  Darwin)
    library="$temporary_dir/libkvlite.dylib"
    cc -dynamiclib -fPIC "$fixture" -o "$library"
    ;;
  Linux)
    library="$temporary_dir/libkvlite.so"
    cc -shared -fPIC "$fixture" -o "$library"
    ;;
  *)
    echo "Python ctypes mock-library test is supported on macOS and Linux only" >&2
    exit 0
    ;;
esac

PYTHONPATH="$package_dir/src" KVLITE_TEST_LIBRARY="$library" python3 -m unittest discover -s "$package_dir/tests" -p 'test_*.py'
