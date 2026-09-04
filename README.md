# KVLite

KVLite is an engine-neutral, typed key-value core. Storage engines are normal
optional driver modules—not dependencies pulled into every application. The
core provides serialization, per-record TTLs, collections, metadata checks,
and migrations; a driver supplies the local storage engine. HTTP and Redis are
explicitly installed extensions, while the C ABI is an embedded boundary. Go
is the implementation language, not the required application language: other
languages use an optional server extension or a driver-specific C bundle.

This repository is an MVP: the storage format is versioned and tested, while
the public API is still free to evolve before a first stable release.

See [MODULES.md](MODULES.md) for the standalone module catalog used by native
bundles and optional transports. The normal Go blank-import path below remains
the in-process development workflow; applications and language bindings should
ultimately install prebuilt module artifacts rather than compile KVLite.

## What is included

- An extension registry: import only `extensions/rocksdb`,
  `extensions/leveldb`, or `extensions/berkeleydb`, then use `WithDriver`.
- Automatic JSON encoding for strings, numbers, structs, slices, and maps.
- A pluggable `Codec` interface with the codec name stored beside every value.
- Per-key and per-hash-field TTLs with exact read-time expiry.
- Hashes, string sets, and typed lists implemented with collision-safe key
  namespaces.
- An optional authenticated HTTP owner/client extension where clients select a
  server-exposed driver name, never a filesystem path.
- A RocksDB compaction filter that physically discards expired value envelopes;
  every backend still enforces TTL logically at read time.
- A persistent driver manifest that prevents one engine from accidentally
  opening a KVLite directory initialized for another engine.
- A versioned, language-neutral JSON/HTTP API and OpenAPI description provided
  by `extensions/http` and the server CLI.
- An optional single-node Redis RESP2-compatible server extension for existing
  Redis clients and CLI tools.
- First-class PHP, Python, Node.js, and Rust packages built on a small,
  versioned C ABI for embedded mode.

## Install one driver

The core module imports no storage engine. Pick one driver explicitly:

```go
import (
	"github.com/webong/kvlite"
	_ "github.com/webong/kvlite/extensions/rocksdb"
)

db, err := kvlite.Open("./app-data", kvlite.WithDriver("rocksdb"))
```

`extensions/leveldb` is pure Go. `extensions/rocksdb` needs the `rocksdb`
build tag and the native library. `extensions/berkeleydb` is CGo-only and
requires a separately chosen Berkeley DB distribution; the owner of a
Berkeley DB-enabled binary must comply with that distribution's terms. It
remains outside the core module and default bundles. See
[`extensions/`](extensions/) for the unified module layout; there are no
alternate Go driver import paths.

Released driver bundles include a checksummed `kvlite-module.json`. Set
`KVLITE_MODULE_PATH` or `KVLITE_HOME` and inspect artifacts without loading
them:

```bash
kvlite module list
./kvlite module run redis -- --path ./data --listen 127.0.0.1:6379
kvlite module verify rocksdb
```

### Embedded is the default

An ordinary `kvlite.Open` call opens only an embedded database. It starts no
network listener, and the core module does not depend on HTTP or Redis server
code. This is the normal SQLite-like path for Go and for language bindings
using the C ABI.

When an application explicitly needs cross-process access, install the
separate extension:

```bash
go get github.com/webong/kvlite/extensions/http
go get github.com/webong/kvlite/extensions/redis
```

The standalone `kvlite serve` CLI exposes HTTP/Redis as optional extensions.

`kvlite serve` defaults to `--extension-mode auto`, which keeps the linked
HTTP/Redis extensions when they are part of the binary and falls back to
standalone extension binaries when they are not linked.
- Add `--extension-mode standalone` to start module-discovered standalone extension
  binaries instead of linked packages.

```bash
kvlite serve --path ./data --extension-mode standalone --listen 127.0.0.1:8080
kvlite serve --path ./data --extension-mode standalone --redis-listen 127.0.0.1:6379
```

In standalone mode, only one protocol extension may own the database directory per
CLI instance; choose either HTTP (default) or Redis.

HTTP and Redis expose the same module metadata model as storage drivers. Standalone
binaries can be discovered through the catalog via `kvlite module list` and run as
extensions when needed.

### RocksDB driver

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
go test -tags rocksdb ./extensions/rocksdb/...
```

On Linux, install the RocksDB development package supplied by the distribution,
then run:

```bash
go test -tags rocksdb ./extensions/rocksdb/...
```

The `rocksdb` build tag is deliberate: consumers can run core, codec, and the
optional HTTP and Redis extensions without native headers. Calling `Open(path)` asks for the
compatibility default `rocksdb`; if no driver was imported it returns
`ErrDriverNotInstalled`, and if the imported RocksDB driver lacks native
support it returns `ErrRocksDBNotBuilt`.

### Native RocksDB tests in Docker

If RocksDB headers are not installed on the host, run the tagged suite in the
reproducible test container instead:

```bash
make test-rocksdb-docker
```

The helper builds `docker/rocksdb-test/Dockerfile`, compiles a pinned RocksDB
source release, installs the cgo linker dependencies, and runs the core,
RocksDB driver, HTTP and Redis extensions, CLI, C ABI, and example suites. The equivalent command is
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
	_ "github.com/webong/kvlite/extensions/rocksdb"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	db, err := kvlite.Open("./kvlite-data", kvlite.WithDriver("rocksdb"))
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

## Choose a storage driver

Choose the underlying engine when the local owner opens the directory. The
blank import is what installs the Go driver into this process:

```go
import _ "github.com/webong/kvlite/extensions/leveldb"

db, err := kvlite.Open("./kvlite-level-data", kvlite.WithDriver("leveldb"))
```

`kvlite.WithDriver("leveldb")` is the equivalent string-friendly form for
configuration-driven applications.

`rocksdb` remains the compatibility default for `Open(path)`, but it is not
linked unless `extensions/rocksdb` is imported. `leveldb` is a pure-Go optional
extension. `berkeleydb` is an opt-in native extension: without its explicit
import and `berkeleydb` build tag it returns `ErrDriverNotInstalled`; with the
import but without native support it returns `ErrBerkeleyDBNotBuilt`. If a matching
driver module is discoverable by `KVLITE_MODULE_PATH`/`KVLITE_HOME` but this process
does not link that driver, `Open(..., WithDriver(...))` now returns `ErrDriverNotLoaded`.

On first open, KVLite writes `KVLITE-MANIFEST.json` beside the database files
with the selected driver, implementation identity, and KVLite format version.
Later opens must select the same target. This is one KVLite API, not one shared
on-disk format: moving between engines requires a logical copy of records into
a new KVLite path. A legacy KVLite RocksDB path without a manifest can be
adopted by reopening it through the unchanged default `Open(path)` path.

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

Hashes and sets use one underlying-engine key per field/member. Lists use a compact,
length-delimited record and are atomically updated by the database owner,
including when the update comes through the HTTP client.

## Use KVLite from any language

KVLite remains embedded-first. When several processes or languages need the
same database, run the owner with the optional HTTP and/or Redis extension.
The server owns every local path; every other language uses a network protocol
and never needs native database headers or filesystem access.

Start the owner:

```bash
go build -tags 'rocksdb,kvlite_rocksdb' -o kvlite ./cmd/kvlite
./kvlite serve --driver rocksdb --path ./kvlite-data --listen 127.0.0.1:8089 --token "$KVLITE_TOKEN"
```

Inspect the compiled bundle before serving it:

```bash
./kvlite driver list
./kvlite module list
```

Write and read from any shell:

```bash
KEY=$(printf user:101 | base64 | tr '+/' '-_' | tr -d '=\n')
curl -fsS -X PUT "http://127.0.0.1:8089/v1/entries/$KEY?ttl_seconds=3600" \
  -H 'Content-Type: application/json' \
  -H 'X-KVLite-Driver: rocksdb' \
  -H "Authorization: Bearer $KVLITE_TOKEN" \
  -d '{"id":101,"name":"Ada"}'
curl -fsS "http://127.0.0.1:8089/v1/entries/$KEY" \
  -H 'X-KVLite-Driver: rocksdb' \
  -H "Authorization: Bearer $KVLITE_TOKEN"
```

The protocol is documented in [`protocol/openapi.yaml`](protocol/openapi.yaml).
The [`examples/python`](examples/python) and [`examples/node`](examples/node)
clients use only their languages' standard HTTP libraries; equivalent clients
can be generated from the OpenAPI document.

For a server that exposes more than one installed driver, the server owner
maps each driver to its own path. A client may select only one of those named
mappings; it cannot make the server open an arbitrary path:

```bash
go build -tags 'rocksdb,kvlite_rocksdb,kvlite_leveldb' -o kvlite ./cmd/kvlite
./kvlite serve --driver rocksdb --path ./rocks-data \
  --driver-path leveldb=./level-data --listen 127.0.0.1:8089
```

An unavailable request returns structured `driver_not_installed`,
`driver_unavailable`, or `driver_not_exposed` JSON rather than falling back.

### Redis-compatible server

KVLite can also expose the same database through a Redis-compatible RESP2
endpoint. This is useful when an application already speaks Redis or when you
want to inspect a local database with `redis-cli`:

```bash
go build -tags 'rocksdb,kvlite_rocksdb' -o kvlite ./cmd/kvlite
./kvlite serve \
  --path ./kvlite-data \
  --driver rocksdb \
  --listen 127.0.0.1:8089 \
  --redis-listen 127.0.0.1:6379 \
  --redis-password "$KVLITE_REDIS_PASSWORD"

redis-cli -h 127.0.0.1 -p 6379 -a "$KVLITE_REDIS_PASSWORD" SET user:101 '{"id":101,"name":"Ada"}'
redis-cli -h 127.0.0.1 -p 6379 -a "$KVLITE_REDIS_PASSWORD" GET user:101
```

The Go API is also available directly by importing only the Redis extension:

```go
import (
    "fmt"
    "log"
    "os"

    "github.com/webong/kvlite"
    kvliteredis "github.com/webong/kvlite/extensions/redis"
    _ "github.com/webong/kvlite/extensions/rocksdb"
)

db, err := kvlite.Open("./kvlite-data", kvlite.WithDriver("rocksdb"))
if err != nil {
    log.Fatal(err)
}
defer db.Close()

server, err := kvliteredis.Serve(db, kvliteredis.Options{
    ListenAddress: "127.0.0.1:6379",
    Password:      os.Getenv("KVLITE_REDIS_PASSWORD"),
})
if err != nil {
    log.Fatal(err)
}
defer server.Close()
fmt.Println(server.URL())
```

The current server covers the everyday Redis data model: `GET`/`SET`, TTL
commands, hashes, sets, lists, increments, key discovery, and common client
handshake commands (`AUTH`, `HELLO`, `CLIENT`, `SELECT`, and `COMMAND`).
Unsupported commands return a normal Redis `ERR` response. It is intentionally
a single-node protocol gateway: replication, Sentinel, cluster routing,
streams, sorted sets, Lua, and transactions are not part of this milestone.
Redis exposes the server's primary driver mapping. Standard Redis clients have
no request-header mechanism, so use KVLite's HTTP clients when one remote
process needs to select another driver mapping.
Keep the Redis listener on loopback or set `--redis-password` before binding it
to a non-local interface; this endpoint does not provide TLS.

### Embedded FFI mode

For a SQLite-like embedded integration, build the C-compatible shared library:

```bash
make build-c-shared DRIVER=leveldb
```

This produces a driver-specific `dist/libkvlite.so` bundle and the generated Go
header. The checked-in [`capi/kvlite.h`](capi/kvlite.h) defines ABI version 1:
`kvlite_open` for the bundle's default driver, additive
`kvlite_open_with_driver` (and the compatible `kvlite_open_with_backend`
alias), close, put/get/delete, arbitrary
serialized byte payloads, TTL seconds, status codes, and one `kvlite_free`
allocator boundary. Each binding checks `kvlite_abi_version()` before it opens
a database, so a mismatched native library fails cleanly.

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
`KVLITE_LIBRARY_PATH` to `libkvlite` before calling `open()`. Embedded wrappers
use their bundle's default driver unless given an optional driver name (for
example, `leveldb`); remote `connect()` clients may send a driver selection
that the server validates against its
server-owned mappings. The wrapper packages intentionally do not make a second
copy of RocksDB. They can already be tested
without RocksDB using an ABI-compatible mock; publishing self-contained package
installers waits for the native release bundle to include RocksDB and its
compression libraries with correct loader paths and notices.

Run the real native integration check (build `libkvlite` against the pinned
RocksDB container, then load it through Python `ctypes`) with:

```bash
make test-bindings-docker
```

The pure-Go LevelDB shared-library path is checked locally without Docker:

```bash
make test-bindings-leveldb
```

### Build release artifacts

The version-controlled release projects live in [`lib/`](lib/): the CLI, C
shared-library contract, and language packages are all defined there.
Generated artifacts are kept out of Git in `dist/`.

Build one separately installable driver bundle on each native target:

```bash
make release RELEASE_VERSION=v0.1.0 DRIVER=leveldb
make release RELEASE_VERSION=v0.1.0 DRIVER=rocksdb
```

For Berkeley DB distributions where the owner has accepted the license terms,
build a private bundle explicitly:

```bash
make release-berkeleydb RELEASE_VERSION=v0.1.0 \
  ALLOW_BERKELEYDB_BUNDLE=1 \
  CGO_CFLAGS="-I/opt/bdb/include" \
  CGO_LDFLAGS="-L/opt/bdb/lib -ldb"
```

This emits the CLI, the platform shared library, the reviewed `kvlite.h` ABI
header, and `SHA256SUMS` under
`dist/v0.1.0/<os>-<arch>/drivers/<driver>/`. Because RocksDB is a cgo/C++
dependency, the script intentionally refuses cross-compilation; the
included GitHub Actions workflow builds Linux and macOS artifacts on native
runners. `windows-amd64` is already part of the release contract and can be
built by the same script on a Windows runner once its RocksDB toolchain is
pinned in CI.

`drivers/<driver>` is retained as the initial release-layout path for language
binding compatibility. It does not reflect the source layout: all optional Go
modules—including RocksDB and LevelDB—live under [`extensions/`](extensions/).

The initial build produces native artifact candidates rather than a
self-contained installer: Berkeley DB and RocksDB runtime files, plus
compression libraries, are not bundled yet. The release packaging phase must
bundle those files, relocate their loader paths, and publish their license
notices before the binary downloads are suitable for clean machines.

## Optional multi-process sharing

The HTTP and Redis servers live in their respective extension modules, not the
core module. A server owns each selected driver/path pair and remains the only
process allowed to open those database directories. In an application, add
blank imports for the selected storage extensions before using this
configuration:

```go
import (
	"log"
	"os"

	"github.com/webong/kvlite"
	kvlitehttp "github.com/webong/kvlite/extensions/http"
	_ "github.com/webong/kvlite/extensions/leveldb"
	_ "github.com/webong/kvlite/extensions/rocksdb"
)

owner, err := kvlite.Open("./rocks-data", kvlite.WithDriver("rocksdb"))
if err != nil {
	log.Fatal(err)
}
defer owner.Close()

server, err := kvlitehttp.Serve(owner, kvlitehttp.Options{
	ListenAddress: "127.0.0.1:8089",
	BearerToken:   os.Getenv("KVLITE_TOKEN"),
	DriverPaths: map[kvlite.DriverName]string{
		kvlite.DriverLevelDB: "./level-data",
	},
})
if err != nil {
	log.Fatal(err)
}
defer server.Close()
```

Another process connects without opening the local database directory:

```go
db, err := kvlitehttp.Connect("http://127.0.0.1:8089", kvlitehttp.ClientOptions{
	BearerToken: os.Getenv("KVLITE_TOKEN"),
	Driver:      kvlite.DriverLevelDB,
})
```

When `ClientOptions.Driver` is supplied, `Connect` validates that selection
during connection and returns the server's structured driver error immediately.

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
budget instead of exposing RocksDB's full option surface. LevelDB maps the same
cache and write-buffer portions to its pure-Go options.

## TTL semantics

RocksDB's `DBWithTTL` accepts one TTL per database or column family. It does not
provide Redis-style `Put(..., ttl)` behavior, and physical deletion only occurs
during compaction. KVLite therefore stores an absolute expiry in each value
envelope and enforces it during reads. An expired value is never returned; a
read also deletes it opportunistically. RocksDB's compaction filter reclaims
expired bytes in the background; LevelDB keeps the same exact logical expiry
behavior and removes expired records lazily on read.

## Verification

```bash
make test
make test-race
make vet

# Requires native RocksDB headers and libraries:
make test-rocksdb
```
