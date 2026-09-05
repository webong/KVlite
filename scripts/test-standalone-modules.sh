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
  for pid_var in http_pid redis_pid cli_pid owner_pid attached_pid token_owner_pid cli_both_pid; do
    pid="${!pid_var:-}"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then kill "$pid" 2>/dev/null || true; fi
  done
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
python3 - <<PYEOF
import json, sys, urllib.request
with urllib.request.urlopen("http://127.0.0.1:$http_port/v1/scan?prefix=") as response:
    items = json.load(response)
assert isinstance(items, list) and len(items) >= 1, "HTTP /v1/scan returned no items"
PYEOF

echo "standalone-modules test: Redis owns $redis_db on 127.0.0.1:$redis_port" >&2
"$redis_bin" --path "$redis_db" --driver leveldb --listen "127.0.0.1:$redis_port" >"$work_root/redis.log" 2>&1 &
redis_pid=$!
export KVLITE_REDIS_PORT="$redis_port"
# The runtime driver loader speaks the engine keyspace through additive raw C
# ABI symbols, so the full Redis data plane works over an installed driver
# module: strings plus the hash type check that requires prefix scans.
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
        return rest
    if kind == "-":
        raise RuntimeError("redis error: " + rest)
    if kind == ":":
        return int(rest)
    if kind == "$":
        n = int(rest)
        if n == -1:
            return None
        data = f.read(n + 2)[:-2].decode()
        return data
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
assert read_reply(f) == "PONG", "PING failed"
s.sendall(cmd("SET", "standalone-key", "standalone-value"))
assert read_reply(f) == "OK", "SET failed"
s.sendall(cmd("GET", "standalone-key"))
assert read_reply(f) == "standalone-value", "GET failed"
s.sendall(cmd("HSET", "standalone-hash", "field", "value"))
assert read_reply(f) == 1, "HSET failed"
s.sendall(cmd("HGET", "standalone-hash", "field"))
assert read_reply(f) == "value", "HGET failed"
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

echo "standalone-modules test: HTTP owner with attached Redis share one directory" >&2
owner_port="$(free_port)"
attached_port="$(free_port)"
shared_db="$work_root/shared-data"
"$http_bin" --path "$shared_db" --driver leveldb --listen "127.0.0.1:$owner_port" >"$work_root/owner.log" 2>&1 &
owner_pid=$!
owner_ready=0
for _ in $(seq 1 100); do
  if curl -fsS "http://127.0.0.1:$owner_port/v1/health" >/dev/null 2>&1; then owner_ready=1; break; fi
  sleep 0.2
done
[[ "$owner_ready" == "1" ]] || { cat "$work_root/owner.log" >&2; fail "HTTP owner did not start"; }
"$redis_bin" --upstream "http://127.0.0.1:$owner_port" --upstream-driver leveldb --listen "127.0.0.1:$attached_port" >"$work_root/attached.log" 2>&1 &
attached_pid=$!
export KVLITE_ATTACHED_PORT="$attached_port"
python3 - <<'PYEOF'
import json, os, socket, time, urllib.request
port = int(os.environ["KVLITE_ATTACHED_PORT"])
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
        return rest
    if kind == "-":
        raise RuntimeError("redis error: " + rest)
    if kind == "$":
        n = int(rest)
        if n == -1:
            return None
        return f.read(n + 2)[:-2].decode()
    raise RuntimeError("unexpected redis reply: " + line)
for _ in range(100):
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=2)
        break
    except OSError:
        time.sleep(0.2)
else:
    raise SystemExit("attached Redis server did not start")
f = s.makefile("rb")
s.sendall(cmd("PING"))
assert read_reply(f) == "PONG", "attached PING failed"
s.sendall(cmd("GET", "shared-key"))
assert read_reply(f) is None, "attached GET before write should miss"
s.close()
PYEOF
shared_key="$(python3 -c 'import base64; print(base64.urlsafe_b64encode(b"shared-key").decode().rstrip("="))')"
curl -fsS -X PUT "http://127.0.0.1:$owner_port/v1/entries/$shared_key" -H 'Content-Type: application/json' -d '{"shared":"owner"}' >/dev/null || fail "owner HTTP PUT failed"
python3 - <<'PYEOF'
import os, socket
port = int(os.environ["KVLITE_ATTACHED_PORT"])
def cmd(*args):
    parts = [("*%d\r\n" % len(args)).encode()]
    for a in args:
        b = a.encode() if isinstance(a, str) else a
        parts.append(("$%d\r\n" % len(b)).encode() + b + b"\r\n")
    return b"".join(parts)
s = socket.create_connection(("127.0.0.1", port), timeout=5)
f = s.makefile("rb")
s.sendall(cmd("GET", "shared-key"))
line = f.readline().decode()
assert line.startswith("$"), "attached GET reply = " + line
body = f.read(int(line[1:].rstrip("\r\n")) + 2)[:-2].decode()
assert body == '{"shared":"owner"}', "attached GET body = " + body
s.sendall(cmd("SET", "attached-key", "attached-value"))
assert f.readline().decode().strip() == "+OK", "attached SET failed"
s.close()
PYEOF
# The attached process holds no directory of its own: one shared manifest.
[[ -f "$shared_db/KVLITE-MANIFEST.json" ]] || fail "shared database has no manifest"
grep -q "upstream=http" "$work_root/attached.log" || fail "attached redis did not report its upstream"

echo "standalone-modules test: attached Redis rejects a bad owner token" >&2
token_owner_port="$(free_port)"
"$http_bin" --path "$work_root/token-data" --driver leveldb --listen "127.0.0.1:$token_owner_port" --token secret >"$work_root/token-owner.log" 2>&1 &
token_owner_pid=$!
sleep 2
if "$redis_bin" --upstream "http://127.0.0.1:$token_owner_port" --listen "127.0.0.1:$(free_port)" --upstream-token wrong >"$work_root/token-redis.log" 2>&1; then
  kill "$token_owner_pid" 2>/dev/null || true
  fail "attached redis with a wrong token unexpectedly started"
fi
kill "$token_owner_pid" 2>/dev/null || true
wait "$token_owner_pid" 2>/dev/null || true

echo "standalone-modules test: CLI orchestrates owner plus attached Redis" >&2
both_port="$(free_port)"
both_redis_port="$(free_port)"
both_db="$work_root/both-data"
"$cli_bin" serve --extension-mode=standalone --path "$both_db" --driver leveldb --listen "127.0.0.1:$both_port" --redis-listen "127.0.0.1:$both_redis_port" >"$work_root/both.log" 2>&1 &
cli_both_pid=$!
export KVLITE_BOTH_PORT="$both_port" KVLITE_BOTH_REDIS_PORT="$both_redis_port"
python3 - <<'PYEOF'
import json, os, socket, time, urllib.request
http_port = int(os.environ["KVLITE_BOTH_PORT"])
redis_port = int(os.environ["KVLITE_BOTH_REDIS_PORT"])
for _ in range(100):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/v1/health" % http_port) as response:
            if response.status == 200:
                break
    except OSError:
        time.sleep(0.2)
else:
    raise SystemExit("orchestrated owner did not start")
def cmd(*args):
    parts = [("*%d\r\n" % len(args)).encode()]
    for a in args:
        b = a.encode() if isinstance(a, str) else a
        parts.append(("$%d\r\n" % len(b)).encode() + b + b"\r\n")
    return b"".join(parts)
for _ in range(100):
    try:
        s = socket.create_connection(("127.0.0.1", redis_port), timeout=2)
        break
    except OSError:
        time.sleep(0.2)
else:
    raise SystemExit("orchestrated redis did not start")
f = s.makefile("rb")
s.sendall(cmd("PING"))
assert f.readline().decode().strip() == "+PONG", "orchestrated PING failed"
key = __import__("base64").urlsafe_b64encode(b"both-key").decode().rstrip("=")
request = urllib.request.Request(
    "http://127.0.0.1:%d/v1/entries/%s" % (http_port, key),
    data=b'{"served":"both"}', headers={"Content-Type": "application/json"}, method="PUT")
with urllib.request.urlopen(request):
    pass
s.sendall(cmd("GET", "both-key"))
line = f.readline().decode()
assert line.startswith("$"), "orchestrated GET reply = " + line
body = f.read(int(line[1:].rstrip("\r\n")) + 2)[:-2].decode()
assert body == '{"served":"both"}', "orchestrated GET body = " + body
s.close()
PYEOF

echo "standalone-modules test: ok (leveldb + http put/get/scan + redis strings/hashes + shared owner/attached + cli orchestration + missing-driver + cli standalone)" >&2
