#!/usr/bin/env bash
set -euo pipefail

package_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$package_dir/../test-fixtures/mock_kvlite.c"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-php-test.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT

case "$(uname -s)" in
  Darwin)
    library="$temporary_dir/libkvlite.dylib"
    cc -dynamiclib -fPIC "$fixture" -o "$library"
    legacy_library="$temporary_dir/libkvlite-legacy.dylib"
    cc -dynamiclib -fPIC -DKVLITE_MOCK_LEGACY_ABI "$fixture" -o "$legacy_library"
    ;;
  Linux)
    library="$temporary_dir/libkvlite.so"
    cc -shared -fPIC "$fixture" -o "$library"
    legacy_library="$temporary_dir/libkvlite-legacy.so"
    cc -shared -fPIC -DKVLITE_MOCK_LEGACY_ABI "$fixture" -o "$legacy_library"
    ;;
  *)
    echo "PHP FFI mock-library test is supported on macOS and Linux only" >&2
    exit 0
    ;;
esac

php -d ffi.enable=1 "$package_dir/tests/run.php" "$library"
php -d ffi.enable=1 "$package_dir/tests/legacy_default.php" "$legacy_library"
