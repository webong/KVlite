#!/usr/bin/env bash
#
# End-to-end check for scripts/install-online.sh. Builds real release
# bundles, packs tarballs, serves them over loopback HTTP as a stand-in for
# GitHub release assets, and runs the installer against that server. Also
# proves a tampered tarball is rejected before anything executes.

set -euo pipefail

fail() {
  printf 'install-online test: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
case "$target" in
  darwin-*|linux-*) ;;
  *) printf 'install-online test: SKIP (unsupported target %s)\n' "$target" >&2; exit 0 ;;
esac

work_root="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-install-online-test.XXXXXX")"
cleanup() {
  if [[ -n "${server_pid:-}" ]] && kill -0 "$server_pid" 2>/dev/null; then kill "$server_pid" 2>/dev/null || true; fi
  if [[ -n "${bad_server_pid:-}" ]] && kill -0 "$bad_server_pid" 2>/dev/null; then kill "$bad_server_pid" 2>/dev/null || true; fi
  if [[ -n "${app_pid:-}" ]] && kill -0 "$app_pid" 2>/dev/null; then kill "$app_pid" 2>/dev/null || true; fi
  wait 2>/dev/null || true
  rm -rf "$work_root"
}
trap cleanup EXIT

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'
}

echo "install-online test: building release bundles" >&2
bash "$repo_root/scripts/build-release.sh" --version latest --driver leveldb >/dev/null
bash "$repo_root/scripts/build-release.sh" --version latest --extension http >/dev/null
bash "$repo_root/scripts/build-release.sh" --version latest --extension redis >/dev/null

assets="$work_root/assets"
bash "$repo_root/scripts/make-release-tarballs.sh" --version latest --out "$assets" >/dev/null
base="kvlite-latest-$target"
[[ -f "$assets/$base.tar.gz" && -f "$assets/$base.tar.gz.sha256" ]] || fail "tarballs missing"

port="$(free_port)"
(cd "$assets" && python3 -m http.server "$port" >/dev/null 2>&1) &
server_pid=$!
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:$port/$base.tar.gz.sha256" >/dev/null 2>&1; then break; fi
  sleep 0.2
done
curl -fsS "http://127.0.0.1:$port/$base.tar.gz.sha256" >/dev/null 2>&1 || fail "asset server did not start"

prefix="$work_root/prefix"
echo "install-online test: installing from asset server" >&2
bash "$repo_root/scripts/install-online.sh" \
  --base-url "http://127.0.0.1:$port" \
  --prefix "$prefix" \
  --yes >/dev/null || fail "online install failed"
[[ -x "$prefix/bin/kvlite" && -x "$prefix/bin/kvlite-http" && -x "$prefix/bin/kvlite-redis" ]] || fail "installed binaries missing"

export KVLITE_SYSTEM_MODULE_PATH="$prefix/lib/kvlite"
export KVLITE_MODULE_PATH=""
export KVLITE_HOME=""
"$prefix/bin/kvlite" module verify leveldb >/dev/null || fail "verify leveldb failed"
"$prefix/bin/kvlite" module verify http >/dev/null || fail "verify http failed"
"$prefix/bin/kvlite" module verify redis >/dev/null || fail "verify redis failed"

echo "install-online test: serving from the installed tree" >&2
data="$work_root/data"
http_port="$(free_port)"
"$prefix/bin/kvlite" serve --extension-mode=standalone --path "$data" --driver leveldb --listen "127.0.0.1:$http_port" >"$work_root/serve.log" 2>&1 &
app_pid=$!
ready=0
for _ in $(seq 1 100); do
  if curl -fsS "http://127.0.0.1:$http_port/v1" >/dev/null 2>&1; then ready=1; break; fi
  sleep 0.2
done
[[ "$ready" == "1" ]] || { cat "$work_root/serve.log" >&2; fail "installed server did not start"; }
key="$(python3 -c 'import base64; print(base64.urlsafe_b64encode(b"online-key").decode().rstrip("="))')"
curl -fsS -X PUT "http://127.0.0.1:$http_port/v1/entries/$key" -H 'Content-Type: application/json' -d '{"online":true}' >/dev/null || fail "installed PUT failed"
got="$(curl -fsS "http://127.0.0.1:$http_port/v1/entries/$key")" || fail "installed GET failed"
[[ "$got" == '{"online":true}' ]] || fail "installed GET body = $got"
kill "$app_pid" 2>/dev/null || true
wait "$app_pid" 2>/dev/null || true
app_pid=""
export KVLITE_SYSTEM_MODULE_PATH=""

echo "install-online test: tampered tarball is rejected" >&2
bad="$work_root/bad"
mkdir -p "$bad"
cp "$assets/$base.tar.gz" "$bad/$base.tar.gz"
cp "$assets/$base.tar.gz.sha256" "$bad/$base.tar.gz.sha256"
printf 'tamper' >> "$bad/$base.tar.gz"
bad_port="$(free_port)"
(cd "$bad" && python3 -m http.server "$bad_port" >/dev/null 2>&1) &
bad_server_pid=$!
sleep 1
if bash "$repo_root/scripts/install-online.sh" --base-url "http://127.0.0.1:$bad_port" --prefix "$work_root/bad-prefix" --yes >/dev/null 2>&1; then
  fail "tampered tarball was accepted"
fi

echo "install-online test: ok (install, verify, serve, tamper rejection)" >&2
