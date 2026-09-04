#!/usr/bin/env bash
#
# Native LevelDB integration check for standalone extension bundles.
#
# Builds a LevelDB driver c-shared bundle plus HTTP/Redis protocol executables,
# assembles them under a temporary KVLITE_HOME, then proves discovery,
# checksum verification, sole-owner HTTP and Redis operation against separate
# database directories, and the missing-driver error path.
#
# Socket tests need loopback networking; run on a native CI runner, not in a
# network-restricted sandbox.

set -euo pipefail

fail() {
  printf 'standalone-modules test: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

version="${KVLITE_STANDALONE_TEST_VERSION:-standalone-test}"
target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
[[ "$(go env CGO_ENABLED)" == "1" ]] || fail "CGO_ENABLED must be 1 (driver loader uses the system dynamic loader)"

work_root="$(mktemp -d "${TMPDIR:-/tmp}/kvlite-standalone-test.XXXXXX")"
cleanup() {
  if [[ -n "${http_pid:-}" ]] && kill -0 "$http_pid" 2>/dev/null; then kill "$http_pid" 2>/dev/null || true; fi
  if [[ -n "${redis_pid:-}" ]] && kill -0 "$redis_pid" 2>/dev/null; then kill "$redis_pid" 2>/dev/null || true; fi
  if [[ -n "${cli_pid:-}" ]] && kill -0 "$cli_pid" 2>/dev/null; then kill "$cli_pid" 2>/dev/null || true; fi
  wait 2>/dev/null || true
  rm -rf "$work_root"
}
trap cleanup EXIT

echo "standalone-modules test: building LevelDB driver bundle" >&2
bash "$repo_root/scripts/build-release.sh" --version "$version" --target "$target" --driver leveldb >/dev/null
echo "standalone-modules test: building HTTP extension bundle" >&2
bash "$repo_root/scripts/build-release.sh" --version "$version" --target "$target" --extension http >/dev/null
echo "standalone-modules test: building Redis extension bundle" >&2
bash "$repo_root/scripts/build-release.sh" --version "$version" --target "$target" --extension redis >/dev/null

dist_root="$repo_root/dist/$version/$target"
driver_bundle="$dist_root/drivers/leveldb"
http_bundle="$dist_root/modules/http"
redis_bundle="$dist_root/modules/redis"
[[ -d "$driver_bundle" && -d "$http_bundle" && -d "$redis_bundle" ]] || fail "expected release bundles under $dist_root"

home="$work_root/home"
mkdir -p "$home/drivers" "$home/modules"
cp -R "$driver_bundle" "$home/drivers/leveldb"
cp -R "$http_bundle" "$home/modules/http"
cp -R "$redis_bundle" "$home/modules/redis"

export KVLITE_HOME="$home"
export KVLITE_MODULE_PATH=""

case "$target" in
  windows-*) cli_bin="$home/drivers/leveldb/bin/kvlite.exe"; http_bin="$home/modules/http/bin/kvlite-http.exe"; redis_bin="$home/modules/redis/bin/kvlite-redis.exe" ;;
  *) cli_bin="$home/drivers/leveldb/bin/kvlite"; http_bin="$home/modules/http/bin/kvlite-http"; redis_bin="$home/modules/redis/bin/kvlite-redis" ;;
esac
[[ -x "$cli_bin" && -x "$http_bin" && -x "$redis_bin" ]] || fail "release executables are not executable"

echo "standalone-modules test: module list discovers installed metadata" >&2
list_output="$("$cli_bin" module list)"
for name in leveldb http redis; do
  echo "$list_output" | grep -q "^$name[[:space:]]" || fail "module list missing $name (got: $list_output)"
done

echo "standalone-modules test: module verify checks checksums" >&2
"$cli_bin" module verify leveldb >/dev/null || fail "verify leveldb failed"
"$cli_bin" module verify http >/dev/null || fail "verify http failed"
"$cli_bin" module verify redis >/dev/null || fail "verify redis failed"

echo "standalone-modules test: tampered artifacts fail verification" >&2
tamper_root="$work_root/tamper"
mkdir -p "$tamper_root"
cp -R "$home/modules/http" "$tamper_root/http"
printf 'tamper' >> "$tamper_root/http/bin/$(basename "$http_bin")"
if KVLITE_HOME="" KVLITE_MODULE_PATH="$tamper_root/http" "$cli_bin" module verify http >/dev/null 2>&1; then
  fail "tampered HTTP executable passed verification"
fi
mkdir -p "$tamper_root/drv"
cp -R "$home/drivers/leveldb" "$tamper_root/drv/leveldb"
case "$target" in
  darwin-*) tamper_lib="$tamper_root/drv/leveldb/lib/libkvlite.dylib" ;;
  linux-*) tamper_lib="$tamper_root/drv/leveldb/lib/libkvlite.so" ;;
  windows-*) tamper_lib="$tamper_root/drv/leveldb/lib/kvlite.dll" ;;
esac
printf 'tamper' >> "$tamper_lib"
if KVLITE_HOME="" KVLITE_MODULE_PATH="$tamper_root/drv/leveldb" "$cli_bin" module verify leveldb >/dev/null 2>&1; then
  fail "tampered driver library passed verification"
fi

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'
}

http_port="$(free_port)"
redis_port="$(free_port)"
http_db="$work_root/http-data"
redis_db="$work_root/redis-data"

echo "standalone-modules test: HTTP owns $http_db on 127.0.0.1:$http_port" >&2
"$http_bin" --path "$http_db" --driver leveldb --listen "127.0.0.1:$http_port" >"$work_root/http.log" 2>&1 &
http_pid=$!
redis_pid=""
cli_pid=""
http_ready=0
for _ in $(seq 1 100); do
  if curl -fsS "http://127.0.0.1:$http_port/v1" >/dev/null 2>&1; then http_ready=1; break; fi
  sleep 0.2
done
[[ "$http_ready" == "1" ]] || { cat "$work_root/http.log" >&2; fail "standalone HTTP server did not start"; }

key="$(python3 -c 'import base64; print(base64.urlsafe_b64encode(b"standalone-key").decode().rstrip("="))')"
curl -fsS -X PUT "http://127.0.0.1:$http_port/v1/entries/$key" -H 'Content-Type: application/json' -d '{"hello":"standalone"}' >/dev/null || fail "HTTP PUT failed"
got="$(curl -fsS "http://127.0.0.1:$http_port/v1/entries/$key")" || fail "HTTP GET failed"
[[ "$got" == '{"hello":"standalone"}' ]] || fail "HTTP GET body = $got"

echo "standalone-modules test: Redis owns $redis_db on 127.0.0.1:$redis_port" >&2
"$redis_bin" --path "$redis_db" --driver leveldb --listen "127.0.0.1:$redis_port" >"$work_root/redis.log" 2>&1 &
redis_pid=$!
export KVLITE_REDIS_PORT="$redis_port"
# The v1 C embedding ABI exposes only put/get/delete, so a Redis process that
# opens its driver through an installed C-shared module can serve the
# connection/handshake plane (PING) but data commands that require key scans
# return a clear unsupported error. Full Redis data-plane coverage stays in
# linked mode (unit tests) until a scan-capable driver ABI exists.
python3 - "$work_root/redis.log" <<'PYEOF'
import socket, sys, time
port = int(__import__("os").environ["KVLITE_REDIS_PORT"])
def cmd(*args):
    parts = [("*%d\r\n" % len(args)).encode()]
    for a in args:
        b = a.encode() if isinstance(a, str) else a
        parts.append(("$%d\r\n" % len(b)).encode() + b + b"\r\n")
    return b"".join(parts)
def read_reply(f):
    line = f.readline().decode()
    if not line:
        raise RuntimeError("redis closed connection")
    kind, rest = line[0], line[1:].rstrip("\r\n")
    if kind == "+":
        return ("simple", rest)
    if kind == "-":
        return ("error", rest)
    if kind == ":":
        return ("int", int(rest))
    if kind == "$":
        n = int(rest)
        if n == -1:
            return ("nil", None)
        data = f.read(n + 2)[:-2].decode()
        return ("bulk", data)
    raise RuntimeError("unexpected redis reply: " + line)
for _ in range(100):
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=2)
        break
    except OSError:
        time.sleep(0.2)
else:
    print(open(sys.argv[1]).read(), file=sys.stderr)
    raise SystemExit("standalone Redis server did not start")
f = s.makefile("rb")
s.sendall(cmd("PING"))
kind, reply = read_reply(f)
assert (kind, reply) == ("simple", "PONG"), "PING failed: %r %r" % (kind, reply)
s.sendall(cmd("SET", "standalone-key", "standalone-value"))
kind, reply = read_reply(f)
assert kind == "error" and "scan" in reply.lower(), "SET over a scan-less module driver should fail clearly, got: %r %r" % (kind, reply)
s.close()
PYEOF
[[ -f "$redis_db/KVLITE-MANIFEST.json" ]] || fail "standalone Redis did not own $redis_db"
grep -q '"driver":"goleveldb"\|"backend":"leveldb"' "$redis_db/KVLITE-MANIFEST.json" || fail "redis database manifest does not record the leveldb driver"

echo "standalone-modules test: missing driver returns an actionable error" >&2
missing_output="$("$http_bin" --path "$work_root/missing-data" --driver rocksdb --listen "127.0.0.1:$(free_port)" 2>&1 || true)"
echo "$missing_output" | grep -qi "driver" || { printf '%s\n' "$missing_output" >&2; fail "missing-driver error did not mention a driver"; }
echo "$missing_output" | grep -qi -E "not installed|not loaded|unavailable|not built" || { printf '%s\n' "$missing_output" >&2; fail "missing-driver error is not actionable"; }

echo "standalone-modules test: extension-free CLI launches standalone HTTP" >&2
cli_port="$(free_port)"
cli_db="$work_root/cli-data"
"$cli_bin" serve --extension-mode=standalone --path "$cli_db" --driver leveldb --listen "127.0.0.1:$cli_port" >"$work_root/cli.log" 2>&1 &
cli_pid=$!
cli_ready=0
for _ in $(seq 1 100); do
  if curl -fsS "http://127.0.0.1:$cli_port/v1" >/dev/null 2>&1; then cli_ready=1; break; fi
  sleep 0.2
done
[[ "$cli_ready" == "1" ]] || { cat "$work_root/cli.log" >&2; fail "CLI standalone HTTP mode did not start"; }

echo "standalone-modules test: ok (leveldb + http put/get + redis ping/ownership + missing-driver + cli standalone)" >&2
