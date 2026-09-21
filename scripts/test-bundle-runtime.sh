#!/usr/bin/env bash
#
# Functional check for scripts/bundle-native-deps.sh using a synthetic C
# library. It proves a non-system dependency is copied into the bundle,
# re-pointed at a loader-relative path, and executable from the bundle tree.
# Runs on the current native target; needs a system C compiler.

set -euo pipefail

fail() {
  printf 'bundle-runtime test: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

command -v cc >/dev/null 2>&1 || { printf 'bundle-runtime test: SKIP (no cc)\n' >&2; exit 0; }
target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
case "$target" in
  darwin-*) suffix="dylib"; shared_flag="-dynamiclib" ;;
  linux-*)
    suffix="so"; shared_flag="-shared"
    command -v patchelf >/dev/null 2>&1 || { printf 'bundle-runtime test: SKIP (no patchelf)\n' >&2; exit 0; }
    ;;
  *) printf 'bundle-runtime test: SKIP (unsupported target %s)\n' "$target" >&2; exit 0 ;;
esac

work_root="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-bundle-runtime-test.XXXXXX")"
cleanup() {
  rm -rf "$work_root"
}
trap cleanup EXIT

cat > "$work_root/dep.c" <<'EOF'
int kvlite_test_dep_answer(void) {
  return 42;
}
EOF
cat > "$work_root/main.c" <<'EOF'
#include <stdio.h>
int kvlite_test_dep_answer(void);
int main(void) {
  printf("%d\n", kvlite_test_dep_answer());
  return 0;
}
EOF

cc -shared -fPIC -O2 -o "$work_root/libkvlite_test_dep.$suffix" "$work_root/dep.c" 2>/dev/null || \
  cc $shared_flag -fPIC -O2 -o "$work_root/libkvlite_test_dep.$suffix" "$work_root/dep.c" || \
  fail "could not build synthetic dependency"
# Link with a real rpath, like release binaries carry for their native
# dependencies: ldd must resolve the library for the bundler to find it.
cc -O2 -o "$work_root/prog" "$work_root/main.c" -L"$work_root" -lkvlite_test_dep -Wl,-rpath,"$work_root" || fail "could not build synthetic binary"

staging="$work_root/staging"
mkdir -p "$staging/bin" "$staging/lib"
cp "$work_root/prog" "$staging/bin/prog"

before="$(otool -L "$staging/bin/prog" 2>/dev/null || ldd "$staging/bin/prog")"
echo "$before" | grep -q "libkvlite_test_dep" || fail "synthetic binary does not link the synthetic dependency"

bash "$repo_root/scripts/bundle-native-deps.sh" "$staging" "$target" >/dev/null || fail "bundling failed"

[[ -f "$staging/lib/libkvlite_test_dep.$suffix" ]] || fail "dependency was not copied into lib/"
after="$(otool -L "$staging/bin/prog" 2>/dev/null || ldd "$staging/bin/prog")"
echo "$after" | grep -q "libkvlite_test_dep" || fail "bundled binary lost its dependency reference"
if echo "$after" | grep -q "$work_root/libkvlite_test_dep"; then
  fail "bundled binary still references the absolute dependency path"
fi

# The bundle must execute from its own tree and from elsewhere.
[[ "$("$staging/bin/prog")" == "42" ]] || fail "bundled binary did not run from its own tree"
(cd "$repo_root" && [[ "$("$staging/bin/prog")" == "42" ]]) || fail "bundled binary did not run from another directory"

# Bundling twice must be a stable no-op, not a duplicate failure.
bash "$repo_root/scripts/bundle-native-deps.sh" "$staging" "$target" >/dev/null || fail "re-bundling failed"
[[ "$("$staging/bin/prog")" == "42" ]] || fail "re-bundled binary did not run"

# Bare dependency names (no directory part, as recorded for install-name
# basenames such as RocksDB's) must resolve through KVLITE_BUNDLE_LIB_PATH.
# The binary deliberately carries no rpath so only the variable saves it.
mkdir -p "$work_root/deplibs"
cp "$work_root/dep.c" "$work_root/bare.c"
if [[ "$target" == darwin-* ]]; then
  cc -dynamiclib -fPIC -O2 -o "$work_root/deplibs/libbaredep.dylib" "$work_root/bare.c" || fail "could not build bare dependency"
  install_name_tool -id "libbaredep.dylib" "$work_root/deplibs/libbaredep.dylib" || fail "could not set bare install name"
else
  cc -shared -fPIC -O2 -o "$work_root/deplibs/libbaredep.so" "$work_root/bare.c" || fail "could not build bare dependency"
fi
cc -O2 -o "$work_root/prog2" "$work_root/main.c" -L"$work_root/deplibs" -lbaredep || fail "could not build bare-linked binary"
bare_staging="$work_root/bare-staging"
mkdir -p "$bare_staging/bin"
cp "$work_root/prog2" "$bare_staging/bin/prog2"
KVLITE_BUNDLE_LIB_PATH="$work_root/deplibs" \
  bash "$repo_root/scripts/bundle-native-deps.sh" "$bare_staging" "$target" >/dev/null || fail "bare-name bundling failed"
case "$target" in
  darwin-*) bundled_bare="$bare_staging/lib/libbaredep.dylib" ;;
  *) bundled_bare="$bare_staging/lib/libbaredep.so" ;;
esac
[[ -f "$bundled_bare" ]] || fail "bare dependency was not copied into lib/"
[[ "$("$bare_staging/bin/prog2")" == "42" ]] || fail "bare-bundled binary did not run"

if [[ "$target" == linux-* ]]; then
  # An unresolvable dependency must fail loudly with the library name,
  # never silently produce an incomplete bundle.
  cc -O2 -o "$work_root/prog-unresolved" "$work_root/main.c" -L"$work_root" -lkvlite_test_dep || fail "could not build unresolved binary"
  bad_staging="$work_root/bad-staging"
  mkdir -p "$bad_staging/bin"
  cp "$work_root/prog-unresolved" "$bad_staging/bin/prog"
  if bash "$repo_root/scripts/bundle-native-deps.sh" "$bad_staging" "$target" >/dev/null 2>&1; then
    fail "unresolvable dependency was accepted"
  fi
fi

printf 'bundle-runtime test: ok (synthetic dependency bundled and executable)\n' >&2
