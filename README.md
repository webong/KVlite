# KVLite

KVLite is a small, typed Go layer over RocksDB. It turns RocksDB's byte-oriented
C API into a developer-friendly embedded store with conservative defaults,
automatic JSON serialization, per-record TTLs, collections, and an optional
HTTP sharing endpoint. Go is the implementation language, not the required
application language: other languages use the JSON/HTTP protocol or the
embedded C ABI.

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
- A small C ABI for Python/Rust/Node/PHP FFI wrappers that need embedded mode.

## Install RocksDB

KVLite uses [`github.com/linxGnu/grocksdb`](https://github.com/linxGnu/grocksdb),
which calls RocksDB through cgo. Install RocksDB and its compression libraries
before building the native adapter.

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

### Embedded FFI mode

For a SQLite-like embedded integration, build the C-compatible shared library:

```bash
make build-c-shared
```

This produces `dist/libkvlite.so` and the generated Go header. The checked-in
[`capi/kvlite.h`](capi/kvlite.h) defines the stable ABI: open/close, put/get/
delete, arbitrary serialized byte payloads, TTL seconds, status codes, and one
`kvlite_free` allocator boundary. A Python `ctypes`, Rust `libloading`, PHP FFI,
or Node N-API wrapper can therefore be a thin adapter rather than another
database implementation.

The C ABI intentionally deals in bytes. Language bindings choose their own
serialization (JSON, MessagePack, protobuf, or a domain codec) and use
`kvlite_put`/`kvlite_get` without exposing Go values or Go pointers across the
boundary.

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
