#!/usr/bin/env bash
set -euo pipefail

package_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$package_dir/../test-fixtures/mock_kvlite.c"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-node-test.XXXXXX")"
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
    echo "Node N-API mock-library test is supported on macOS and Linux only" >&2
    exit 0
    ;;
esac

node_root="$(cd "$(dirname "$(node -p 'process.execPath')")/.." && pwd)"
node_gyp="$node_root/lib/node_modules/npm/node_modules/node-gyp/bin/node-gyp.js"
if [[ ! -f "$node_gyp" ]]; then
  echo "node-gyp is required to test the KVLite N-API extension" >&2
  exit 1
fi
# node-gyp expands its include paths without shell escaping on some macOS
# installations. Herd's Node root contains a space, so give it a stable,
# space-free symlink while retaining the headers from the running Node version.
node_gyp_root="$temporary_dir/node-root"
ln -s "$node_root" "$node_gyp_root"
(
  cd "$package_dir"
  node "$node_gyp" rebuild --nodedir="$node_gyp_root"
)
KVLITE_TEST_LIBRARY="$library" node "$package_dir/tests/run.mjs"
