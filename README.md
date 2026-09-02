# KVLite

KVLite is a small, typed Go layer over RocksDB. It turns RocksDB's byte-oriented
C API into a developer-friendly embedded store with conservative defaults,
automatic JSON serialization, per-record TTLs, collections, and optional HTTP
and Redis-compatible endpoints. Go is the implementation language, not the
required application language: other languages use the JSON/HTTP protocol, a
normal Redis client, or the embedded C ABI.

This repository is an MVP: the storage format is versioned and tested, while
the public API is still free to evolve before a first stable release.

## What is included

- `Open(path)` with a bounded SSD-oriented RocksDB profile.
- Automatic JSON encoding for strings, numbers, structs, slices, and maps.
- A pluggable `Codec` interface with the codec name stored beside every value.
- Per-key and per-hash-field TTLs with exact read-time expiry.
- Hashes, string sets, and typed lists implemented with collision-safe key
  namespaces.
- An optional authenticated HTTP owner/client mode for multi-process access.
- A RocksDB compaction filter that physically discards expired value envelopes.
- A versioned, language-neutral JSON/HTTP API and OpenAPI description.
- An optional single-node Redis RESP2-compatible server for existing Redis
  clients and CLI tools.
- First-class PHP, Python, Node.js, and Rust packages built on a small,
  versioned C ABI for embedded mode.

## Install RocksDB

KVLite uses [`github.com/linxGnu/grocksdb`](https://github.com/linxGnu/grocksdb),
which calls RocksDB through cgo. KVLite deliberately uses the portable C API
surface shared by RocksDB `v10.8.3` through `v10.10.1`; newer RocksDB releases
are expected to work, but are not a substitute for testing your target build.
Install RocksDB and its compression development libraries before building the
native adapter.

On macOS with Homebrew:

```bash
brew install rocksdb

ROCKS_PREFIX="$(brew --prefix rocksdb)"
CGO_CFLAGS="-I${ROCKS_PREFIX}/include" \
CGO_LDFLAGS="-L${ROCKS_PREFIX}/lib" \
go test -tags rocksdb ./...
```

On Linux, install the RocksDB development package supplied by the distribution,
then run:

```bash
go test -tags rocksdb ./...
```

The `rocksdb` build tag is deliberate: consumers can run package-level tests,
codec tests, and remote-client builds without requiring native headers. Calling
`Open` without the tag returns `ErrRocksDBNotBuilt`; `OpenRemote` remains usable.

### Native RocksDB tests in Docker

If RocksDB headers are not installed on the host, run the tagged suite in the
reproducible test container instead:

```bash
make test-rocksdb-docker
```

The helper builds `docker/rocksdb-test/Dockerfile`, compiles a pinned RocksDB
source release, installs the cgo linker dependencies, and runs
`go test -tags rocksdb ./...`. The equivalent command is
`docker compose -f compose.rocksdb.yml run --rm --build rocksdb-test`.

KVLite's Docker compatibility check runs both ends of the supported range:

```bash
make test-rocksdb-compat
```

For ordinary consumer builds, prefer KVLite's released native library instead
of relying on a system RocksDB installation. System RocksDB is an advanced mode
and should stay within the tested range.

## Basic usage

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/webong/kvlite"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	db, err := kvlite.Open("./kvlite-data")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	err = db.Put(ctx, "user:101", User{ID: 101, Name: "Ada"},
		kvlite.TTL(time.Hour))
	if err != nil {
		log.Fatal(err)
	}

	user, err := kvlite.GetAs[User](ctx, db, "user:101")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %s", user.Name)
}
```

Build it with:

```bash
go run -tags rocksdb ./examples/basic
```

## Collections

```go
_ = db.HSet(ctx, "user:101", "profile", user)
_ = db.HGet(ctx, "user:101", "profile", &user)

_, _ = db.SAdd(ctx, "user:101:roles", "admin", "author")
roles, _ := db.SMembers(ctx, "user:101:roles")

_, _ = db.RPush(ctx, "jobs", Job{ID: 1}, Job{ID: 2})
var jobs []Job
_ = db.LRange(ctx, "jobs", 0, -1, &jobs)
```

Hashes and sets use one RocksDB key per field/member. Lists use a compact,
length-delimited record and are atomically updated by the database owner,
including when the update comes through the HTTP client.

## Use KVLite from any language

The easiest cross-language deployment is to run the owner binary and speak the
JSON API. The owner is the only process that opens the RocksDB directory; every
other language uses ordinary HTTP and never needs RocksDB headers.

Start the owner:

```bash
go build -tags rocksdb -o kvlite ./cmd/kvlite
./kvlite serve --path ./kvlite-data --listen 127.0.0.1:8089 --token "$KVLITE_TOKEN"
```

Write and read from any shell:

```bash
KEY=$(printf user:101 | base64 | tr '+/' '-_' | tr -d '=\n')
curl -fsS -X PUT "http://127.0.0.1:8089/v1/entries/$KEY?ttl_seconds=3600" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $KVLITE_TOKEN" \
  -d '{"id":101,"name":"Ada"}'
curl -fsS "http://127.0.0.1:8089/v1/entries/$KEY" \
  -H "Authorization: Bearer $KVLITE_TOKEN"
```

The protocol is documented in [`protocol/openapi.yaml`](protocol/openapi.yaml).
The [`examples/python`](examples/python) and [`examples/node`](examples/node)
clients use only their languages' standard HTTP libraries; equivalent clients
can be generated from the OpenAPI document.

### Redis-compatible server

KVLite can also expose the same database through a Redis-compatible RESP2
endpoint. This is useful when an application already speaks Redis or when you
want to inspect a local database with `redis-cli`:

```bash
go build -tags rocksdb -o kvlite ./cmd/kvlite
./kvlite serve \
  --path ./kvlite-data \
  --listen 127.0.0.1:8089 \
  --redis-listen 127.0.0.1:6379 \
  --redis-password "$KVLITE_REDIS_PASSWORD"

redis-cli -h 127.0.0.1 -p 6379 -a "$KVLITE_REDIS_PASSWORD" SET user:101 '{"id":101,"name":"Ada"}'
redis-cli -h 127.0.0.1 -p 6379 -a "$KVLITE_REDIS_PASSWORD" GET user:101
```

The Go API is also available directly:

```go
db, err := kvlite.Open("./kvlite-data", kvlite.WithRedis(kvlite.RedisOptions{
    ListenAddress: "127.0.0.1:6379",
    Password:      os.Getenv("KVLITE_REDIS_PASSWORD"),
}))
if err != nil {
    log.Fatal(err)
}
defer db.Close()
fmt.Println(db.RedisAddress())
```

The current server covers the everyday Redis data model: `GET`/`SET`, TTL
commands, hashes, sets, lists, increments, key discovery, and common client
handshake commands (`AUTH`, `HELLO`, `CLIENT`, `SELECT`, and `COMMAND`).
Unsupported commands return a normal Redis `ERR` response. It is intentionally
a single-node protocol gateway: replication, Sentinel, cluster routing,
streams, sorted sets, Lua, and transactions are not part of this milestone.
The process that opens the path remains the sole RocksDB owner.
Keep the Redis listener on loopback or set `--redis-password` before binding it
to a non-local interface; this endpoint does not provide TLS.

### Embedded FFI mode

For a SQLite-like embedded integration, build the C-compatible shared library:

```bash
make build-c-shared
```

This produces `dist/libkvlite.so` and the generated Go header. The checked-in
[`capi/kvlite.h`](capi/kvlite.h) defines ABI version 1: open/close, put/get/
delete, arbitrary serialized byte payloads, TTL seconds, status codes, and one
`kvlite_free` allocator boundary. Each binding checks `kvlite_abi_version()`
before it opens a database, so a mismatched native library fails cleanly.

The C ABI intentionally deals in bytes. Language bindings choose their own
serialization (JSON, MessagePack, protobuf, or a domain codec) and use
`kvlite_put`/`kvlite_get` without exposing Go values or Go pointers across the
boundary.

### First-class language packages

The source-controlled packages live in [`lib/bindings/`](lib/bindings/). They
all expose an `open()` path for local, SQLite-style use and use the same
versioned C library. PHP, Python, and Node also expose `connect()` for the
public JSON/HTTP protocol, which is the right choice when several processes
need the same database.

| Language | Package | Embedded implementation | Remote implementation |
| --- | --- | --- | --- |
| PHP | `webong/kvlite` | PHP FFI | JSON/HTTP streams |
| Python | `kvlite` | `ctypes` | `urllib` |
| Node.js | `@webong/kvlite` | N-API dynamic loader | `fetch` |
| Rust | `kvlite` | `libloading` | Use the OpenAPI or Redis client boundary |

For now, build/download the matching native release and set
`KVLITE_LIBRARY_PATH` to `libkvlite` before calling `open()`. The wrapper
packages intentionally do not make a second copy of RocksDB. They can already
be tested without RocksDB using an ABI-compatible mock; publishing self-
contained package installers waits for the native release bundle to include
RocksDB and its compression libraries with correct loader paths and notices.

Run the real native integration check (build `libkvlite` against the pinned
RocksDB container, then load it through Python `ctypes`) with:

```bash
make test-bindings-docker
```

### Build release artifacts

The version-controlled release projects live in [`lib/`](lib/): the CLI, C
shared-library contract, and language packages are all defined there.
Generated artifacts are kept out of Git in `dist/`.

On each native target with RocksDB installed, build a versioned release layout:

```bash
make release RELEASE_VERSION=v0.1.0
```

This emits the CLI, the platform shared library, the reviewed `kvlite.h` ABI
header, and `SHA256SUMS` under `dist/v0.1.0/<os>-<arch>/`. Because RocksDB is a
cgo/C++ dependency, the script intentionally refuses cross-compilation; the
included GitHub Actions workflow builds Linux and macOS artifacts on native
runners. `windows-amd64` is already part of the release contract and can be
built by the same script on a Windows runner once its RocksDB toolchain is
pinned in CI.

The initial build produces native artifact candidates rather than a
self-contained installer: RocksDB and its compression-library runtime files
are not bundled yet. The release packaging phase must bundle those files,
relocate their loader paths, and publish their license notices before the
binary downloads are suitable for clean machines.

## Multi-process sharing

The process that opens RocksDB remains the only owner of the database lock. It
can expose a loopback HTTP endpoint:

```go
owner, err := kvlite.Open("./kvlite-data", kvlite.WithSharing(kvlite.SharingOptions{
	ListenAddress: "127.0.0.1:8089",
	BearerToken:   os.Getenv("KVLITE_TOKEN"),
}))
```

Another process connects without opening the RocksDB directory:

```go
db, err := kvlite.OpenRemote("http://127.0.0.1:8089", kvlite.RemoteOptions{
	BearerToken: os.Getenv("KVLITE_TOKEN"),
})
```

The transport intentionally defaults to an ephemeral loopback listener when
no address is supplied. It does not terminate TLS; use a local-only socket or a
trusted TLS reverse proxy if traffic leaves the host.

## Default RocksDB profile

| Setting | Default | Intent |
|---|---:|---|
| Block cache | 64 MiB | Bound read-cache memory |
| Write buffer | 16 MiB | Keep memtables small |
| Write buffers | 2 | Bound aggregate write memory |
| Background jobs | 2 | Avoid monopolizing host CPU |
| Block size | 16 KiB | Favor SSD throughput without huge reads |
| Bloom filter | 10 bits/key | Reduce point-read disk work |
| Compression | LZ4 | Low-overhead compression |
| Periodic compaction | 1 hour | Give expired records cleanup opportunities |

Use `WithMemoryBudget(bytes)` to derive the cache and memtable sizes from one
budget instead of exposing RocksDB's full option surface.

## TTL semantics

RocksDB's `DBWithTTL` accepts one TTL per database or column family. It does not
provide Redis-style `Put(..., ttl)` behavior, and physical deletion only occurs
during compaction. KVLite therefore stores an absolute expiry in each value
envelope and enforces it during reads. An expired value is never returned; a
read also deletes it opportunistically. The compaction filter reclaims expired
bytes in the background.

## Verification

```bash
make test
make test-race
make vet

# Requires native RocksDB headers and libraries:
make test-rocksdb
```
